package lark

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

type bindingSender interface {
	SendLuckinBinding(context.Context, domain.Scope, string, string) (string, error)
}

type bindingResponseBody struct {
	io.Reader
	closed bool
}

func (b *bindingResponseBody) Close() error { b.closed = true; return nil }

func TestLuckinBindingUsesPrivateEphemeralRecipientAndConfirmedReceipt(t *testing.T) {
	var posts int
	body := &bindingResponseBody{Reader: strings.NewReader(`{"code":0,"data":{"message_id":"personal-card"}}`)}
	c, err := NewClient(ClientConfig{AppID: "app", AppSecret: "secret", BotOpenID: "bot", TenantID: "tenant", AllowedChatIDs: []string{"group"}, HTTPClient: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/auth/") {
			return response(`{"code":0,"tenant_access_token":"test-token","expire":7200}`), nil
		}
		posts++
		if r.Method != http.MethodPost || r.URL.Path != "/open-apis/ephemeral/v1/send" || r.URL.RawQuery != "" || r.Header.Get("Authorization") == "" {
			t.Error("binding did not use authenticated ephemeral endpoint")
		}
		var request struct {
			ChatID  string          `json:"chat_id"`
			OpenID  string          `json:"open_id"`
			MsgType string          `json:"msg_type"`
			Card    json.RawMessage `json:"card"`
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil || request.ChatID != "group" || request.OpenID != "alice" || request.MsgType != "interactive" {
			t.Error("binding recipient lost verified personal identity or group context")
		}
		var card map[string]any
		if json.Unmarshal(request.Card, &card) != nil || card["schema"] != "2.0" || card["body"] == nil {
			t.Error("binding card is not a schema 2.0 object")
		}
		text := string(request.Card)
		for _, expected := range []string{`"tag":"form"`, `"tag":"input"`, `"form_action_type":"submit"`, "luckin.personal.bind", "luckin.personal.revoke", "opaque-binding", "https://open.lkcoffee.com", "仅对你可见", "本人", "不要"} {
			if !strings.Contains(text, expected) {
				t.Errorf("binding card missing required element %s", expected)
			}
		}
		for _, forbidden := range []string{`"initial_value"`, `"actor_id"`, `"chat_id"`, `"token":"`, `"input_value"`} {
			if strings.Contains(text, forbidden) {
				t.Errorf("binding card exposed credential/default or trusted identity: %s", forbidden)
			}
		}
		resp := response("")
		resp.Body = body
		return resp, nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	sender, ok := any(c).(bindingSender)
	if !ok {
		t.Fatal("SDK client is missing the private Luckin binding transport")
	}
	id, err := sender.SendLuckinBinding(context.Background(), clientScope(), "alice", "opaque-binding")
	if err != nil || id != "personal-card" || posts != 1 || !body.closed {
		t.Fatalf("binding receipt/body lifecycle failed: id=%q posts=%d closed=%v err=%v", id, posts, body.closed, err)
	}
}

func TestLuckinBindingRejectsUntrustedScopeAndSecretProviderErrors(t *testing.T) {
	for _, mode := range []string{"actor_missing", "actor_control", "binding_missing", "binding_control", "other_chat", "other_tenant", "other_app", "bad_receipt", "error_code", "http_error", "invalid_json"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			c, err := NewClient(ClientConfig{AppID: "app", AppSecret: "secret", BotOpenID: "bot", TenantID: "tenant", AllowedChatIDs: []string{"group"}, HTTPClient: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				if strings.Contains(r.URL.Path, "/auth/") {
					return response(`{"code":0,"tenant_access_token":"test-token","expire":7200}`), nil
				}
				calls++
				resp := response(`{"code":0,"data":{"message_id":"card"}}`)
				switch mode {
				case "bad_receipt":
					resp = response(`{"code":0,"data":{}}`)
				case "error_code":
					resp = response(`{"code":99999,"msg":"private-provider-secret"}`)
				case "http_error":
					resp.StatusCode = 503
				case "invalid_json":
					resp = response(`private-provider-secret`)
				}
				return resp, nil
			})}})
			if err != nil {
				t.Fatal(err)
			}
			sender, ok := any(c).(bindingSender)
			if !ok {
				t.Fatal("SDK client is missing the private Luckin binding transport")
			}
			scope, actor, binding := clientScope(), "alice", "binding"
			switch mode {
			case "actor_missing":
				actor = ""
			case "actor_control":
				actor = "ali\tce"
			case "binding_missing":
				binding = ""
			case "binding_control":
				binding = "bi\tnding"
			case "other_chat":
				scope.ChatID = "other"
			case "other_tenant":
				scope.TenantID = "other"
			case "other_app":
				scope.AppID = "other"
			}
			id, err := sender.SendLuckinBinding(context.Background(), scope, actor, binding)
			if err == nil || id != "" || strings.Contains(err.Error(), "private-provider-secret") {
				t.Fatal("invalid scope/response accepted or provider error leaked")
			}
			if strings.HasPrefix(mode, "other_") || strings.HasPrefix(mode, "actor_") || strings.HasPrefix(mode, "binding_") {
				if calls != 0 {
					t.Fatal("untrusted identity reached provider")
				}
			}
		})
	}
}
