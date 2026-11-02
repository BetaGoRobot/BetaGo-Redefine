package lark

import (
	"context"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
)

// MusicCardActionSubmission contains authenticated transport identity. The
// presentation service validates the opaque action against its persisted card.
type MusicCardActionSubmission struct {
	Scope                       domain.Scope
	ActorID, EventID, MessageID string
	Token                       string `json:"-"`
}

func isMusicCardAction(e *callback.CardActionTriggerEvent) bool {
	if e == nil || e.Event == nil || e.Event.Action == nil {
		return false
	}
	a := e.Event.Action
	kind, _ := a.Value["kind"].(string)
	action, _ := a.Value["action"].(string)
	return kind == "music" || strings.HasPrefix(action, "music.") || strings.HasPrefix(a.Name, "agent_music_")
}

func (i *Ingress) handleMusicCardAction(ctx context.Context, e *callback.CardActionTriggerEvent) (*callback.CardActionTriggerResponse, error) {
	failed := func() (*callback.CardActionTriggerResponse, error) {
		return &callback.CardActionTriggerResponse{Toast: &callback.Toast{Type: "error", Content: "卡片操作未能确认，请刷新卡片后重试。"}}, nil
	}
	if e == nil || e.Event == nil || e.Event.Action == nil || e.Event.Operator == nil || e.Event.Context == nil || e.Event.Host != "im_message" || i.cfg.MusicCardAction == nil {
		return failed()
	}
	scope, err := i.n.scope(e.EventV2Base, e.Event.Context.OpenChatID)
	if err != nil || e.EventV2Base == nil || e.EventV2Base.Header == nil {
		return failed()
	}
	operator := e.Event.Operator
	if value(operator.TenantKey) != scope.TenantID || !validBindingID(operator.OpenID, 1024) || !validBindingID(e.Event.Context.OpenMessageID, 1024) || !validBindingID(e.EventV2Base.Header.EventID, 1024) {
		return failed()
	}
	if _, _, err = providerTimestamp(e.EventV2Base.Header.CreateTime, time.UnixMicro); err != nil {
		return failed()
	}
	a := e.Event.Action
	kind, _ := a.Value["kind"].(string)
	token, _ := a.Value["token"].(string)
	if a.Tag != "button" || kind != "music" || !validBindingID(token, 8192) {
		return failed()
	}
	ctx, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
	defer cancel()
	if err = i.cfg.MusicCardAction(ctx, MusicCardActionSubmission{Scope: scope, ActorID: operator.OpenID, EventID: e.EventV2Base.Header.EventID, MessageID: e.Event.Context.OpenMessageID, Token: token}); err != nil {
		return failed()
	}
	return &callback.CardActionTriggerResponse{Toast: &callback.Toast{Type: "info", Content: "已收到，正在更新音乐卡片。"}}, nil
}
