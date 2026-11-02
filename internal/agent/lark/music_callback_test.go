package lark

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
)

func musicCallbackPayload(t *testing.T, mutate func(map[string]any, map[string]any)) []byte {
	t.Helper()
	header := map[string]any{"event_type": "card.action.trigger", "event_id": "music-event", "app_id": "app", "tenant_key": "tenant", "create_time": "1792650000000000", "token": "verify"}
	event := map[string]any{"host": "im_message", "operator": map[string]any{"open_id": "alice", "tenant_key": "tenant"}, "context": map[string]any{"open_chat_id": "group", "open_message_id": "music-card"}, "action": map[string]any{"tag": "button", "value": map[string]any{"kind": "music", "token": "opaque-signed-token", "actor_id": "mallory", "chat_id": "other"}}}
	if mutate != nil {
		mutate(header, event)
	}
	b, err := json.Marshal(map[string]any{"schema": "2.0", "header": header, "event": event})
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func TestMusicCallbackUsesSDKIdentityAndOnlyQueuesTypedAction(t *testing.T) {
	persisted, calls := 0, 0
	var got MusicCardActionSubmission
	cfg := bindingIngressConfig(&persisted, nil)
	cfg.MusicCardAction = func(ctx context.Context, in MusicCardActionSubmission) error {
		calls++
		got = in
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 3*time.Second {
			t.Fatal("music callback lost its short persistence deadline")
		}
		return nil
	}
	ing, err := NewIngress(cfg)
	if err != nil {
		t.Fatal(err)
	}
	response, err := ing.Dispatcher().Do(context.Background(), musicCallbackPayload(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || persisted != 0 || got.Scope != clientScope() || got.ActorID != "alice" || got.MessageID != "music-card" || got.EventID != "music-event" || got.Token != "opaque-signed-token" {
		t.Fatal("music callback trusted payload identity or entered ordinary persistence")
	}
	r, ok := response.(*callback.CardActionTriggerResponse)
	if !ok || r.Toast == nil || r.Toast.Type != "info" || r.Card != nil {
		t.Fatal("music callback must acknowledge queued work without replacing its card")
	}
}
func TestMalformedOrUnauthorizedMusicCallbackNeverBecomesTaskControl(t *testing.T) {
	for name, mutate := range map[string]func(map[string]any, map[string]any){
		"other_app":             func(h, e map[string]any) { h["app_id"] = "other" },
		"other_tenant":          func(h, e map[string]any) { h["tenant_key"] = "other" },
		"other_chat":            func(h, e map[string]any) { e["context"].(map[string]any)["open_chat_id"] = "other" },
		"other_operator_tenant": func(h, e map[string]any) { e["operator"].(map[string]any)["tenant_key"] = "other" },
		"missing_token":         func(h, e map[string]any) { delete(e["action"].(map[string]any)["value"].(map[string]any), "token") },
		"oversized_token": func(h, e map[string]any) {
			e["action"].(map[string]any)["value"].(map[string]any)["token"] = string(make([]byte, 8193))
		},
		"missing_message": func(h, e map[string]any) { delete(e["context"].(map[string]any), "open_message_id") },
		"wrong_host":      func(h, e map[string]any) { e["host"] = "url_preview" },
	} {
		t.Run(name, func(t *testing.T) {
			persisted, calls := 0, 0
			cfg := bindingIngressConfig(&persisted, nil)
			cfg.MusicCardAction = func(context.Context, MusicCardActionSubmission) error { calls++; return nil }
			ing, err := NewIngress(cfg)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = ing.Dispatcher().Do(context.Background(), musicCallbackPayload(t, mutate))
			if calls != 0 || persisted != 0 {
				t.Fatal("untrusted music callback reached handler or ordinary controls")
			}
		})
	}
}
func TestMusicCallbackFailureAcknowledgesWithFixedToastWithoutEchoingToken(t *testing.T) {
	persisted := 0
	cfg := bindingIngressConfig(&persisted, nil)
	cfg.MusicCardAction = func(context.Context, MusicCardActionSubmission) error {
		return errors.New("provider-secret-error opaque-signed-token")
	}
	ing, err := NewIngress(cfg)
	if err != nil {
		t.Fatal(err)
	}
	response, err := ing.Dispatcher().Do(context.Background(), musicCallbackPayload(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	r, ok := response.(*callback.CardActionTriggerResponse)
	if !ok || r.Toast == nil || r.Toast.Type != "error" || persisted != 0 {
		t.Fatal("callback failure must return a bounded local toast")
	}
	b, _ := json.Marshal(response)
	var text string = string(b)
	if strings.Contains(text, "provider-secret-error") || strings.Contains(text, "opaque-signed-token") {
		t.Fatal("callback error leaked internal material")
	}
}
