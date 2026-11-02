package lark

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

const bindingCanary = "private-binding-token-canary"

func bindingCallbackPayload(t *testing.T, mutate func(map[string]any, map[string]any)) []byte {
	t.Helper()
	header := map[string]any{"event_type": "card.action.trigger", "event_id": "binding-event", "app_id": "app", "tenant_key": "tenant", "create_time": "1792650000000000", "token": "verify"}
	event := map[string]any{
		"operator": map[string]any{"open_id": "alice", "tenant_key": "tenant"},
		"host":     "im_message", "context": map[string]any{"open_chat_id": "group", "open_message_id": "personal-card"},
		"action": map[string]any{
			"tag": "button", "name": "luckin_personal_bind",
			"value":      map[string]any{"action": "luckin.personal.bind", "binding_id": "opaque-binding", "actor_id": "mallory", "chat_id": "other", "tenant_id": "other", "app_id": "other"},
			"form_value": map[string]any{"luckin_personal_token": "  bEaReR " + bindingCanary + "  "},
		},
	}
	if mutate != nil {
		mutate(header, event)
	}
	b, err := json.Marshal(map[string]any{"schema": "2.0", "header": header, "event": event})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func bindingIngressConfig(persisted *int, hook func(context.Context, CredentialBindingSubmission) error) IngressConfig {
	return IngressConfig{
		Normalize: NormalizeConfig{AppID: "app", BotOpenID: "bot", TenantID: "tenant", AllowedChatIDs: []string{"group"}},
		AppSecret: "secret", VerificationToken: "verify", EncryptionKey: "encrypt", LuckinBinding: hook,
		Persist: func(context.Context, domain.Event) error { *persisted++; return nil },
	}
}

func TestLuckinBindingCallbackUsesVerifiedIdentityWithoutPersistenceOrEcho(t *testing.T) {
	var persisted, hooks int
	var got CredentialBindingSubmission
	ing, err := NewIngress(bindingIngressConfig(&persisted, func(ctx context.Context, submission CredentialBindingSubmission) error {
		hooks++
		got = submission
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 3*time.Second {
			t.Error("binding hook is missing callback deadline")
		}
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	response, err := ing.Dispatcher().Do(context.Background(), bindingCallbackPayload(t, nil))
	if err != nil || hooks != 1 || persisted != 0 {
		t.Fatalf("private callback was not handled exclusively: hooks=%d persisted=%d err=%v", hooks, persisted, err)
	}
	if got.Scope != clientScope() || got.ActorID != "alice" || got.BindingID != "opaque-binding" || got.MessageID != "personal-card" || got.Token != bindingCanary || got.Revoke {
		t.Fatal("binding trusted callback values instead of SDK event/operator/context or failed to normalize Bearer")
	}
	r, ok := response.(*callback.CardActionTriggerResponse)
	if !ok || r.Toast == nil || r.Toast.Type != "success" || r.Card == nil || r.Card.Type != "raw" {
		t.Fatal("private callback lacks success toast and clearing replacement card")
	}
	encoded, _ := json.Marshal(response)
	if strings.Contains(string(encoded), bindingCanary) || strings.Contains(string(encoded), "opaque-binding") || strings.Contains(string(encoded), `"tag":"input"`) || !strings.Contains(string(encoded), "加密保存") || !strings.Contains(string(encoded), "只读查询") {
		t.Fatal("binding success response echoes input or falsely promises provider validation")
	}
	submissionJSON, _ := json.Marshal(got)
	if strings.Contains(string(submissionJSON), bindingCanary) {
		t.Fatal("transient submission exposes credential to JSON serialization")
	}
}

func TestLuckinBindingCallbackRejectsMalformedAndCrossScopeWithoutFallback(t *testing.T) {
	cases := map[string]func(map[string]any, map[string]any){
		"other_app":               func(h, e map[string]any) { h["app_id"] = "other" },
		"other_tenant":            func(h, e map[string]any) { h["tenant_key"] = "other" },
		"other_chat":              func(h, e map[string]any) { e["context"].(map[string]any)["open_chat_id"] = "other" },
		"operator_tenant":         func(h, e map[string]any) { e["operator"].(map[string]any)["tenant_key"] = "other" },
		"missing_operator_tenant": func(h, e map[string]any) { delete(e["operator"].(map[string]any), "tenant_key") },
		"missing_actor":           func(h, e map[string]any) { e["operator"].(map[string]any)["open_id"] = "" },
		"actor_control":           func(h, e map[string]any) { e["operator"].(map[string]any)["open_id"] = "ali\tce" },
		"missing_context":         func(h, e map[string]any) { delete(e, "context") },
		"missing_operator":        func(h, e map[string]any) { delete(e, "operator") },
		"missing_message":         func(h, e map[string]any) { e["context"].(map[string]any)["open_message_id"] = "" },
		"missing_event_id":        func(h, e map[string]any) { delete(h, "event_id") },
		"wrong_host":              func(h, e map[string]any) { e["host"] = "url_preview" },
		"wrong_tag":               func(h, e map[string]any) { e["action"].(map[string]any)["tag"] = "input" },
		"unknown_action": func(h, e map[string]any) {
			e["action"].(map[string]any)["value"].(map[string]any)["action"] = "luckin.personal.unknown"
		},
		"missing_action":   func(h, e map[string]any) { delete(e["action"].(map[string]any)["value"].(map[string]any), "action") },
		"nonstring_action": func(h, e map[string]any) { e["action"].(map[string]any)["value"].(map[string]any)["action"] = 42 },
		"missing_binding": func(h, e map[string]any) {
			delete(e["action"].(map[string]any)["value"].(map[string]any), "binding_id")
		},
		"long_binding": func(h, e map[string]any) {
			e["action"].(map[string]any)["value"].(map[string]any)["binding_id"] = strings.Repeat("b", 257)
		},
		"missing_token": func(h, e map[string]any) { delete(e["action"].(map[string]any), "form_value") },
		"nonstring_token": func(h, e map[string]any) {
			e["action"].(map[string]any)["form_value"].(map[string]any)["luckin_personal_token"] = map[string]any{"token": bindingCanary}
		},
		"long_token": func(h, e map[string]any) {
			e["action"].(map[string]any)["form_value"].(map[string]any)["luckin_personal_token"] = strings.Repeat("t", 8193)
		},
		"whitespace_token": func(h, e map[string]any) {
			e["action"].(map[string]any)["form_value"].(map[string]any)["luckin_personal_token"] = "a b"
		},
		"control_token": func(h, e map[string]any) {
			e["action"].(map[string]any)["form_value"].(map[string]any)["luckin_personal_token"] = "a\tb"
		},
		"other_field_fallback": func(h, e map[string]any) {
			action := e["action"].(map[string]any)
			action["name"] = "approval"
			action["value"] = map[string]any{"action": "approve", "task_id": "task", "approval_id": "approval", "revision": "1"}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			var persisted, hooks int
			ing, err := NewIngress(bindingIngressConfig(&persisted, func(context.Context, CredentialBindingSubmission) error { hooks++; return nil }))
			if err != nil {
				t.Fatal(err)
			}
			_, err = ing.Dispatcher().Do(context.Background(), bindingCallbackPayload(t, mutate))
			if err == nil || hooks != 0 || persisted != 0 || strings.Contains(err.Error(), bindingCanary) {
				t.Fatalf("invalid private callback accepted, persisted, or leaked: hooks=%d persisted=%d err=%v", hooks, persisted, err)
			}
		})
	}
}

func TestLuckinBindingCallbackHookFailureAndNilHookRemainPrivate(t *testing.T) {
	oldLog, oldProvider := slog.Default(), otel.GetTracerProvider()
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	var output bytes.Buffer
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	otel.SetTracerProvider(provider)
	defer func() {
		slog.SetDefault(oldLog)
		otel.SetTracerProvider(oldProvider)
		provider.Shutdown(context.Background())
	}()
	for _, enabled := range []bool{false, true} {
		var persisted, hooks int
		var hook func(context.Context, CredentialBindingSubmission) error
		if enabled {
			hook = func(context.Context, CredentialBindingSubmission) error { hooks++; return errors.New(bindingCanary) }
		}
		ing, err := NewIngress(bindingIngressConfig(&persisted, hook))
		if err != nil {
			t.Fatal(err)
		}
		r, err := ing.Dispatcher().Do(context.Background(), bindingCallbackPayload(t, nil))
		encoded, _ := json.Marshal(r)
		response, ok := r.(*callback.CardActionTriggerResponse)
		if err != nil || !ok || response.Toast == nil || response.Toast.Type != "error" || response.Card == nil || strings.Contains(string(encoded), bindingCanary) || strings.Contains(string(encoded), `"tag":"input"`) || persisted != 0 || (enabled && hooks != 1) {
			t.Fatal("private hook failure fell through, acknowledged or leaked")
		}
	}
	spans, _ := json.Marshal(exporter.GetSpans())
	if strings.Contains(output.String(), bindingCanary) || strings.Contains(string(spans), bindingCanary) || strings.Contains(output.String(), "agent.message.received") {
		t.Fatal("private callback entered normal ingress telemetry")
	}
}

func TestLuckinBindingRevokeDiscardsFormToken(t *testing.T) {
	var persisted, hooks int
	ing, err := NewIngress(bindingIngressConfig(&persisted, func(ctx context.Context, s CredentialBindingSubmission) error {
		hooks++
		if !s.Revoke || s.Token != "" || s.ActorID != "alice" || s.MessageID != "personal-card" {
			t.Error("revoke retained form input or lost verified identity")
		}
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	b := bindingCallbackPayload(t, func(h, e map[string]any) {
		a := e["action"].(map[string]any)
		a["name"] = "luckin_personal_revoke"
		a["value"].(map[string]any)["action"] = "luckin.personal.revoke"
	})
	r, err := ing.Dispatcher().Do(context.Background(), b)
	encoded, _ := json.Marshal(r)
	if err != nil || hooks != 1 || persisted != 0 || strings.Contains(string(encoded), bindingCanary) || !strings.Contains(string(encoded), "撤销") || strings.Contains(string(encoded), `"tag":"input"`) {
		t.Fatal("revoke failed to clear the personal form")
	}
}

func TestLuckinBindingHTTPCallbackRetainsSDKSignatureVerification(t *testing.T) {
	var persisted, hooks int
	ing, err := NewIngress(bindingIngressConfig(&persisted, func(context.Context, CredentialBindingSubmission) error { hooks++; return nil }))
	if err != nil {
		t.Fatal(err)
	}
	b := bindingCallbackPayload(t, nil)
	bad := encryptedCallback(t, "encrypt", b)
	bad.Header.Set("X-Lark-Signature", "invalid")
	recorder := httptest.NewRecorder()
	ing.Handler().ServeHTTP(recorder, bad)
	if recorder.Code < 400 || hooks != 0 || persisted != 0 || strings.Contains(recorder.Body.String(), bindingCanary) {
		t.Fatal("unsigned personal callback bypassed SDK verification")
	}
	recorder = httptest.NewRecorder()
	ing.Handler().ServeHTTP(recorder, encryptedCallback(t, "encrypt", b))
	if recorder.Code != 200 || hooks != 1 || persisted != 0 || strings.Contains(recorder.Body.String(), bindingCanary) {
		t.Fatal("signed personal callback not exclusively handled")
	}
}
