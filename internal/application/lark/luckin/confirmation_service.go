package luckin

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/mcpclient"
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
	if !confirmRequestMatches(order, req, now) {
		return nil, ErrPendingOrderNotConfirmable
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
	if strings.TrimSpace(order.PayloadHash) != strings.TrimSpace(req.PayloadHash) ||
		strings.TrimSpace(order.ChatID) != strings.TrimSpace(req.ChatID) ||
		strings.TrimSpace(order.RequesterOpenID) != strings.TrimSpace(req.OperatorOpenID) ||
		order.Status != PendingStatusPending ||
		!order.ExpiresAt.After(now) {
		return ErrPendingOrderNotConfirmable
	}
	return s.store.MarkCancelled(ctx, order.ID, order.PayloadHash, req.OperatorOpenID, req.ChatID, now)
}

func (s confirmationService) remoteURL() string {
	if s.serverURL != "" {
		return s.serverURL
	}
	return ServerURL
}

func confirmRequestMatches(order PendingOrder, req ConfirmRequest, now time.Time) bool {
	return strings.TrimSpace(order.ID) == strings.TrimSpace(req.PendingOrderID) &&
		strings.TrimSpace(order.PayloadHash) == strings.TrimSpace(req.PayloadHash) &&
		strings.TrimSpace(order.ChatID) == strings.TrimSpace(req.ChatID) &&
		strings.TrimSpace(order.RequesterOpenID) == strings.TrimSpace(req.OperatorOpenID) &&
		order.Status == PendingStatusPending &&
		order.ExpiresAt.After(now)
}
