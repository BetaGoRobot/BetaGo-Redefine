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
}

type ToolCaller interface {
	CallTool(context.Context, mcpclient.CallRequest) (mcpclient.CallResult, error)
}

type confirmationService struct {
	store     PendingOrderStore
	tokens    CredentialStore
	caller    ToolCaller
	serverURL string
}

func NewConfirmationService(store PendingOrderStore, tokens CredentialStore, caller ToolCaller, serverURL string) ConfirmationService {
	return confirmationService{store: store, tokens: tokens, caller: caller, serverURL: strings.TrimSpace(serverURL)}
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
	return BuildOrderCreatedCard(result.Content), nil
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

func BuildOrderCreatedCard(result json.RawMessage) map[string]any {
	content := strings.TrimSpace(string(result))
	if content == "" {
		content = "{}"
	}
	return map[string]any{
		"schema": "2.0",
		"config": map[string]any{"wide_screen_mode": true},
		"body": map[string]any{
			"elements": []any{
				map[string]any{"tag": "markdown", "content": "**瑞幸订单已创建**"},
				map[string]any{"tag": "markdown", "content": "订单结果：" + content},
			},
		},
	}
}
