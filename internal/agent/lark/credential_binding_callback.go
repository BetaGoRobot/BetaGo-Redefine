package lark

import (
	"context"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
)

// CredentialBindingSubmission is transient callback data. The hook must verify
// this exact principal, nonce, provider message and expiry against persisted
// issuance before atomically encrypting or revoking the personal credential.
// Token is deliberately excluded from JSON and must never be logged.
type CredentialBindingSubmission struct {
	Scope     domain.Scope
	ActorID   string
	BindingID string
	MessageID string
	Token     string `json:"-"`
	Revoke    bool
}

// Reserve the entire private namespace and sensitive form markers, including
// malformed submissions. They must never reach normal card-fact persistence.
func isLuckinBindingCallback(e *callback.CardActionTriggerEvent) bool {
	if e == nil || e.Event == nil || e.Event.Action == nil {
		return false
	}
	a := e.Event.Action
	action, _ := a.Value["action"].(string)
	_, hasBinding := a.Value["binding_id"]
	_, hasToken := a.FormValue[luckinBindingTokenField]
	return hasBinding || hasToken || strings.HasPrefix(action, luckinBindingActionPrefix) || strings.HasPrefix(a.Name, luckinBindingNamePrefix)
}

func (i *Ingress) handleLuckinBinding(ctx context.Context, e *callback.CardActionTriggerEvent) (*callback.CardActionTriggerResponse, error) {
	submission, err := i.luckinBindingSubmission(e)
	// Drop parsed form input immediately. Neither the raw event nor provider
	// token is passed into normal event persistence, telemetry or tool execution.
	if e != nil && e.Event != nil && e.Event.Action != nil {
		e.Event.Action.FormValue = nil
		e.Event.Action.InputValue = ""
	}
	if err != nil {
		return nil, errCredentialBinding
	}
	failed := func() (*callback.CardActionTriggerResponse, error) {
		return bindingCallbackResponse("error", "个人凭证操作未能确认完成，请重新发起绑定后重试。"), nil
	}
	if i.cfg.LuckinBinding == nil {
		return failed()
	}
	ctx, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
	defer cancel()
	if err := i.cfg.LuckinBinding(ctx, submission); err != nil {
		return failed()
	}
	content := "你的个人凭证已加密保存。有效性将在下次只读查询时确认。"
	if submission.Revoke {
		content = "你的个人凭证已撤销。后续请求将无法使用该凭证。"
	}
	return bindingCallbackResponse("success", content), nil
}

func (i *Ingress) luckinBindingSubmission(e *callback.CardActionTriggerEvent) (CredentialBindingSubmission, error) {
	var s CredentialBindingSubmission
	if e == nil || e.Event == nil || e.Event.Operator == nil || e.Event.Context == nil || e.Event.Action == nil || e.Event.Host != "im_message" {
		return s, errCredentialBinding
	}
	scope, err := i.n.scope(e.EventV2Base, e.Event.Context.OpenChatID)
	if err != nil || !validBindingID(e.EventV2Base.Header.EventID, 1024) {
		return s, errCredentialBinding
	}
	if value(e.Event.Operator.TenantKey) != scope.TenantID || !validBindingID(e.Event.Operator.OpenID, 1024) || !validBindingID(e.Event.Context.OpenMessageID, 1024) {
		return s, errCredentialBinding
	}
	if _, _, err := providerTimestamp(e.EventV2Base.Header.CreateTime, time.UnixMicro); err != nil {
		return s, errCredentialBinding
	}
	a := e.Event.Action
	action, _ := a.Value["action"].(string)
	bindingID, _ := a.Value["binding_id"].(string)
	if a.Tag != "button" || !validBindingID(bindingID, 256) || (action != luckinBindingBindAction && action != luckinBindingRevokeAction) {
		return s, errCredentialBinding
	}
	s = CredentialBindingSubmission{Scope: scope, ActorID: e.Event.Operator.OpenID, MessageID: e.Event.Context.OpenMessageID, BindingID: bindingID, Revoke: action == luckinBindingRevokeAction}
	if s.Revoke {
		return s, nil
	}
	token, ok := a.FormValue[luckinBindingTokenField].(string)
	// Limit before normalization as well as after, avoiding whitespace padding
	// or a huge Bearer prefix turning oversized input into an accepted token.
	if !ok || len(token) > luckinBindingMaxToken+32 {
		return CredentialBindingSubmission{}, errCredentialBinding
	}
	token = strings.TrimSpace(token)
	if len(token) >= 7 && strings.EqualFold(token[:7], "bearer ") {
		token = strings.TrimSpace(token[7:])
	}
	if !validBindingID(token, luckinBindingMaxToken) {
		return CredentialBindingSubmission{}, errCredentialBinding
	}
	s.Token = token
	return s, nil
}

func bindingCallbackResponse(toastType, content string) *callback.CardActionTriggerResponse {
	return &callback.CardActionTriggerResponse{
		Toast: &callback.Toast{Type: toastType, Content: content},
		Card: &callback.Card{Type: "raw", Data: map[string]any{
			"schema": "2.0",
			"header": map[string]any{"title": bindingPlainText("瑞幸个人凭证 · 仅对你可见"), "template": "blue"},
			"body":   map[string]any{"elements": []any{map[string]any{"tag": "markdown", "content": content}}},
		}},
	}
}
