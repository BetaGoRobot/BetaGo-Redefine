package luckinaction

import (
	"context"
	"sync"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/luckin"
	infraDB "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/mcpclient"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/mcpstore"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"go.uber.org/zap"
)

const (
	orderPollTick      = 30 * time.Second
	orderPollLease     = 2 * time.Minute
	orderPollBatch     = 50
	orderMaxFailCount  = 5
)

// OrderPoller 后台轮询瑞幸订单生命周期，按状态机推进并通知/更新卡片。
type OrderPoller struct {
	repo   *mcpstore.OrderRepository
	draft  luckin.DraftService
	tokens luckin.CredentialStore
	cfg    luckin.OrderPollConfig

	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	mu      sync.Mutex
	running bool
}

func NewOrderPoller() *OrderPoller {
	db := infraDB.DB()
	if db == nil {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &OrderPoller{
		repo:   mcpstore.NewOrderRepository(db),
		draft:  luckin.NewDraftService(mcpclient.New(mcpclient.ClientOptions{}), luckinServerURL()),
		tokens: credentialStore{},
		cfg:    luckinOrderPollConfig(),
		ctx:    ctx,
		cancel: cancel,
	}
}

func (p *OrderPoller) Start() {
	if p == nil {
		return
	}
	p.mu.Lock()
	if p.running {
		p.mu.Unlock()
		return
	}
	p.running = true
	p.mu.Unlock()

	p.wg.Add(1)
	go p.run()
	logs.L().Info("luckin order poller started")
}

func (p *OrderPoller) Stop() {
	if p == nil {
		return
	}
	p.mu.Lock()
	if !p.running {
		p.mu.Unlock()
		return
	}
	p.running = false
	p.mu.Unlock()
	p.cancel()
	p.wg.Wait()
	logs.L().Info("luckin order poller stopped")
}

func (p *OrderPoller) run() {
	defer p.wg.Done()
	ticker := time.NewTicker(orderPollTick)
	defer ticker.Stop()

	p.tick()
	for {
		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
			p.tick()
		}
	}
}

func (p *OrderPoller) tick() {
	now := time.Now()
	records, err := p.repo.ClaimDueOrders(p.ctx, now, orderPollLease, orderPollBatch)
	if err != nil {
		logs.L().Ctx(p.ctx).Warn("luckin claim due orders failed", zap.Error(err))
		return
	}
	for _, record := range records {
		p.process(record, time.Now())
	}
}

func (p *OrderPoller) process(record luckin.OrderRecord, now time.Time) {
	rowID, ok, err := p.repo.FindRowID(p.ctx, record.AppID, record.BotOpenID, record.OrderID)
	if err != nil || !ok {
		return
	}

	cred, err := resolveCredential(p.ctx, p.tokens, luckin.CredentialRequest{
		AppID:     record.AppID,
		BotOpenID: record.BotOpenID,
		ChatID:    record.ChatID,
		OpenID:    record.RequesterOpenID,
		ChatType:  scopeChatType(record.CredentialScope),
	})
	if err != nil {
		// token 失效：停止轮询，避免无意义重试。
		p.stop(rowID, luckin.OrderRecordFailed, "credential unavailable", now)
		return
	}

	detail, err := p.draft.OrderDetail(p.ctx, cred, record.OrderID)
	if err != nil {
		p.handleFailure(record, rowID, now)
		return
	}

	decision := luckin.EvaluatePoll(record, detail, p.cfg, now)
	p.apply(record, rowID, detail, decision, now)
}

func (p *OrderPoller) handleFailure(record luckin.OrderRecord, rowID int64, now time.Time) {
	failCount := record.FailCount + 1
	if failCount >= orderMaxFailCount {
		p.stop(rowID, luckin.OrderRecordFailed, "exceeded max poll failures", now)
		return
	}
	next := now.Add(p.cfg.PollInterval)
	_ = p.repo.ApplyUpdate(p.ctx, rowID, mcpstore.OrderUpdate{
		FailCount:  &failCount,
		NextPollAt: &next,
	}, now)
}

func (p *OrderPoller) apply(record luckin.OrderRecord, rowID int64, detail luckin.OrderDetail, decision luckin.PollDecision, now time.Time) {
	update := mcpstore.OrderUpdate{
		Status:           decision.Status,
		LastRemoteStatus: decision.LastRemoteStatus,
		UnpaidReminded:   decision.UnpaidReminded,
		NextPollAt:       decision.NextPollAt,
		StoppedReason:    decision.StoppedReason,
		Timestamps:       decision.StatusTimestamps,
	}
	if err := p.repo.ApplyUpdate(p.ctx, rowID, update, now); err != nil {
		logs.L().Ctx(p.ctx).Warn("luckin order update failed", zap.String("order_id", record.OrderID), zap.Error(err))
	}

	if record.MessageID == "" {
		return
	}
	switch {
	case decision.SendUnpaidReminder:
		_ = larkmsg.PatchCardJSON(p.ctx, record.MessageID, luckin.BuildUnpaidReminderCard(record.OrderID, record.PayURL))
	case decision.NoticeText != "":
		_ = larkmsg.PatchCardJSON(p.ctx, record.MessageID, luckin.BuildOrderNoticeCard(decision.NoticeText, detail))
	case decision.PatchStatusCard:
		_ = larkmsg.PatchCardJSON(p.ctx, record.MessageID, luckin.BuildOrderStatusCard(detail))
	}
}

func (p *OrderPoller) stop(rowID int64, status luckin.OrderRecordStatus, reason string, now time.Time) {
	_ = p.repo.ApplyUpdate(p.ctx, rowID, mcpstore.OrderUpdate{
		Status:        status,
		StoppedReason: reason,
	}, now)
}

func scopeChatType(scope luckin.CredentialScope) luckin.ChatType {
	if scope.Type == luckin.ScopeChat {
		return luckin.ChatTypeGroup
	}
	return luckin.ChatTypePrivate
}

var (
	globalOrderPoller   *OrderPoller
	globalOrderPollerMu sync.Mutex
)

// StartOrderPoller 启动全局订单轮询 worker（单实例）。
func StartOrderPoller() {
	_ = botidentity.Current()
	poller := NewOrderPoller()
	if poller == nil {
		logs.L().Warn("luckin order poller not started: db unavailable")
		return
	}
	globalOrderPollerMu.Lock()
	prev := globalOrderPoller
	globalOrderPoller = poller
	globalOrderPollerMu.Unlock()
	if prev != nil {
		prev.Stop()
	}
	poller.Start()
}

// StopOrderPoller 停止全局订单轮询 worker。
func StopOrderPoller() {
	globalOrderPollerMu.Lock()
	poller := globalOrderPoller
	globalOrderPoller = nil
	globalOrderPollerMu.Unlock()
	if poller != nil {
		poller.Stop()
	}
}
