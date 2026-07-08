package luckin

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/mcpclient"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"go.uber.org/zap"
)

type ConfirmationService interface {
	Confirm(context.Context, ConfirmRequest) (map[string]any, error)
	Cancel(context.Context, CancelRequest) error
}

type ConfirmRequest struct {
	PendingOrderID string
	PayloadHash    string
	OperatorOpenID string
	ChatID         string
	MessageID      string
	Now            time.Time
}

type CancelRequest struct {
	PendingOrderID string
	PayloadHash    string
	OperatorOpenID string
	ChatID         string
	Now            time.Time
}

type PendingOrderStore interface {
	FindPendingOrder(context.Context, string) (PendingOrder, error)
	MarkConfirmed(context.Context, string, string, string, json.RawMessage, time.Time) error
	MarkCancelled(context.Context, string, string, string, string, time.Time) error
	UpdateDraft(context.Context, PendingOrder, time.Time) error
}

type ToolCaller interface {
	CallTool(context.Context, mcpclient.CallRequest) (mcpclient.CallResult, error)
}

type confirmationService struct {
	store     PendingOrderStore
	tokens    CredentialStore
	caller    ToolCaller
	serverURL string
	tracker   OrderTracker
	images    ImageUploader
	pollCfg   OrderPollConfig
}

// OrderPollConfig 订单轮询相关时间阈值。
type OrderPollConfig struct {
	PollInterval   time.Duration
	PollMax        time.Duration
	UnpaidTimeout  time.Duration
	UnpaidRemindAt time.Duration
}

func DefaultOrderPollConfig() OrderPollConfig {
	return OrderPollConfig{
		PollInterval:   5 * time.Second,
		PollMax:        2 * time.Hour,
		UnpaidTimeout:  15 * time.Minute,
		UnpaidRemindAt: 10 * time.Minute,
	}
}

func NewConfirmationService(store PendingOrderStore, tokens CredentialStore, caller ToolCaller, serverURL string) ConfirmationService {
	return confirmationService{store: store, tokens: tokens, caller: caller, serverURL: strings.TrimSpace(serverURL), pollCfg: DefaultOrderPollConfig()}
}

// NewConfirmationServiceWithTracking 额外接入订单跟踪与二维码上传。
func NewConfirmationServiceWithTracking(store PendingOrderStore, tokens CredentialStore, caller ToolCaller, serverURL string, tracker OrderTracker, images ImageUploader, pollCfg OrderPollConfig) ConfirmationService {
	if pollCfg.PollInterval == 0 {
		pollCfg = DefaultOrderPollConfig()
	}
	return confirmationService{
		store:     store,
		tokens:    tokens,
		caller:    caller,
		serverURL: strings.TrimSpace(serverURL),
		tracker:   tracker,
		images:    images,
		pollCfg:   pollCfg,
	}
}

func (s confirmationService) Confirm(ctx context.Context, req ConfirmRequest) (map[string]any, error) {
	now := req.Now
	if now.IsZero() {
		now = time.Now()
	}
	order, err := s.store.FindPendingOrder(ctx, req.PendingOrderID)
	if err != nil {
		return nil, err
	}
	if err := confirmRequestMatch(order, req, now); err != nil {
		logs.L().Ctx(ctx).Warn("luckin confirm pending order mismatch",
			zap.String("pending_id", req.PendingOrderID),
			zap.String("order_status", string(order.Status)),
			zap.Bool("chat_match", strings.TrimSpace(order.ChatID) == strings.TrimSpace(req.ChatID)),
			zap.Bool("hash_match", strings.TrimSpace(order.PayloadHash) == strings.TrimSpace(req.PayloadHash)),
			zap.Bool("requester_match", strings.TrimSpace(order.RequesterOpenID) == strings.TrimSpace(req.OperatorOpenID)),
			zap.Bool("expired", !order.ExpiresAt.After(now)),
			zap.Error(err),
		)
		return nil, err
	}
	cred, err := s.tokens.FindToken(ctx, CredentialLookup{
		Provider:  ProviderLuckin,
		AppID:     order.AppID,
		BotOpenID: order.BotOpenID,
		Scope:     order.CredentialScope,
	})
	if err != nil {
		return nil, err
	}
	result, err := s.caller.CallTool(ctx, mcpclient.CallRequest{
		Server: mcpclient.ServerConfig{
			Name:    ServerName,
			URL:     s.remoteURL(),
			Headers: map[string]string{"Authorization": "Bearer " + cred.Token},
			Timeout: DefaultTimeout(),
		},
		ToolName:  "createOrder",
		Arguments: order.CreateOrderPayload,
	})
	if err != nil {
		return nil, err
	}
	if err := s.store.MarkConfirmed(ctx, order.ID, order.PayloadHash, req.OperatorOpenID, result.Content, now); err != nil {
		return nil, err
	}

	created := OrderCreatedFromResult(result.Content)
	qrImgKey := ""
	if s.images != nil && created.QRCodeURL != "" {
		qrImgKey = s.images.UploadByURL(ctx, created.QRCodeURL)
	}
	// 记录订单以便后台轮询生命周期；失败不阻断下单成功反馈。
	if s.tracker != nil && created.OrderID != "" {
		record := OrderRecord{
			OrderID:          created.OrderID,
			AppID:            order.AppID,
			BotOpenID:        order.BotOpenID,
			ChatID:           order.ChatID,
			RequesterOpenID:  order.RequesterOpenID,
			InitiatorOpenID:  order.InitiatorOpenID,
			CartSnapshot:     order.CartSnapshot,
			CredentialScope:  order.CredentialScope,
			MessageID:        req.MessageID,
			Status:           OrderRecordActive,
			LastRemoteStatus: OrderStatusUnpaid,
			NeedPay:          created.NeedPay,
			PayURL:           created.PayURL,
			QRCodeURL:        created.QRCodeURL,
			DiscountPrice:    created.DiscountPrice,
			NextPollAt:       now.Add(s.pollCfg.PollInterval),
			PollDeadline:     now.Add(s.pollCfg.PollMax),
			CreatedAt:        now,
		}
		if err := s.tracker.CreateOrder(ctx, record); err != nil {
			// 仅记录，不影响用户拿到下单卡片。
			_ = err
		}
	}
	return BuildOrderCreatedCard(result.Content, qrImgKey), nil
}

func (s confirmationService) Cancel(ctx context.Context, req CancelRequest) error {
	now := req.Now
	if now.IsZero() {
		now = time.Now()
	}
	order, err := s.store.FindPendingOrder(ctx, req.PendingOrderID)
	if err != nil {
		return err
	}
	if err := cancelRequestMatch(order, req, now); err != nil {
		logs.L().Ctx(ctx).Warn("luckin cancel pending order mismatch",
			zap.String("pending_id", req.PendingOrderID),
			zap.String("order_status", string(order.Status)),
			zap.Bool("chat_match", strings.TrimSpace(order.ChatID) == strings.TrimSpace(req.ChatID)),
			zap.Bool("hash_match", strings.TrimSpace(order.PayloadHash) == strings.TrimSpace(req.PayloadHash)),
			zap.Bool("requester_match", strings.TrimSpace(order.RequesterOpenID) == strings.TrimSpace(req.OperatorOpenID)),
			zap.Bool("expired", !order.ExpiresAt.After(now)),
			zap.Error(err),
		)
		return err
	}
	return s.store.MarkCancelled(ctx, order.ID, order.PayloadHash, req.OperatorOpenID, req.ChatID, now)
}

func (s confirmationService) remoteURL() string {
	if s.serverURL != "" {
		return s.serverURL
	}
	return ServerURL
}

// confirmRequestMatch 把 6 条前置校验拆成不同 sentinel error，便于上层给用户友好文案与日志定位。
// 顺序上先校验强身份字段（ID / hash / chat），再校验业务状态（status / expires）与归属人。
func confirmRequestMatch(order PendingOrder, req ConfirmRequest, now time.Time) error {
	if strings.TrimSpace(order.ID) != strings.TrimSpace(req.PendingOrderID) ||
		strings.TrimSpace(order.PayloadHash) != strings.TrimSpace(req.PayloadHash) {
		return ErrPendingOrderPayloadMismatch
	}
	if strings.TrimSpace(order.ChatID) != strings.TrimSpace(req.ChatID) {
		return ErrPendingOrderChatMismatch
	}
	switch order.Status {
	case PendingStatusPending:
	case PendingStatusConfirmed, PendingStatusCancelled, PendingStatusFailed, PendingStatusExpired:
		return ErrPendingOrderAlreadyDone
	default:
		return ErrPendingOrderNotConfirmable
	}
	if !order.ExpiresAt.After(now) {
		return ErrPendingOrderExpired
	}
	if strings.TrimSpace(order.RequesterOpenID) != strings.TrimSpace(req.OperatorOpenID) {
		return ErrPendingOrderNotOwnedByOperator
	}
	return nil
}

// cancelRequestMatch Cancel 的前置校验，语义同 confirmRequestMatch。
func cancelRequestMatch(order PendingOrder, req CancelRequest, now time.Time) error {
	if strings.TrimSpace(order.ID) != strings.TrimSpace(req.PendingOrderID) ||
		strings.TrimSpace(order.PayloadHash) != strings.TrimSpace(req.PayloadHash) {
		return ErrPendingOrderPayloadMismatch
	}
	if strings.TrimSpace(order.ChatID) != strings.TrimSpace(req.ChatID) {
		return ErrPendingOrderChatMismatch
	}
	switch order.Status {
	case PendingStatusPending:
	case PendingStatusConfirmed, PendingStatusCancelled, PendingStatusFailed, PendingStatusExpired:
		return ErrPendingOrderAlreadyDone
	default:
		return ErrPendingOrderNotConfirmable
	}
	if !order.ExpiresAt.After(now) {
		return ErrPendingOrderExpired
	}
	if strings.TrimSpace(order.RequesterOpenID) != strings.TrimSpace(req.OperatorOpenID) {
		return ErrPendingOrderNotOwnedByOperator
	}
	return nil
}
