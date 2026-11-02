package lark

import (
	"context"
	"encoding/json"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"net/http"
	"strings"
	"testing"
)

func TestSDKRetractRequiresExactOwnMessageInCurrentGroup(t *testing.T) {
	for _, mode := range []string{"own-app", "own-bot", "foreign-app", "foreign-bot", "user", "other-chat", "deleted", "error"} {
		t.Run(mode, func(t *testing.T) {
			deletes := 0
			c, err := NewClient(ClientConfig{AppID: "app", AppSecret: "secret", BotOpenID: "bot", HTTPClient: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				if strings.Contains(r.URL.Path, "/auth/") {
					return response(`{"code":0,"tenant_access_token":"test-token","expire":7200}`), nil
				}
				if r.Method == "GET" {
					sender := map[string]any{"id": "app", "id_type": "app_id", "sender_type": "app", "open_bot_id": "bot"}
					chat := "group"
					switch mode {
					case "own-bot":
						sender = map[string]any{"id": "bot", "id_type": "open_id", "sender_type": "app"}
					case "foreign-app":
						sender["id"] = "foreign"
					case "foreign-bot":
						sender["open_bot_id"] = "foreign"
					case "user":
						sender = map[string]any{"id": "alice", "id_type": "open_id", "sender_type": "user"}
					case "other-chat":
						chat = "foreign"
					}
					data, _ := json.Marshal(map[string]any{"code": 0, "data": map[string]any{"items": []any{map[string]any{"message_id": "own", "chat_id": chat, "sender": sender, "deleted": mode == "deleted"}}}})
					return response(string(data)), nil
				}
				if r.Method != "DELETE" || r.URL.Path != "/open-apis/im/v1/messages/own" {
					t.Errorf("wrong mutation %s %s", r.Method, r.URL.Path)
				}
				deletes++
				if mode == "error" {
					return response(`{"code":230026}`), nil
				}
				return response(`{"code":0}`), nil
			})}})
			if err != nil {
				t.Fatal(err)
			}
			retractor, ok := any(c).(interface {
				Retract(context.Context, domain.Scope, string) error
			})
			if !ok {
				t.Fatal("own-message retraction not implemented")
			}
			err = retractor.Retract(context.Background(), clientScope(), "own")
			allowed := mode == "own-app" || mode == "own-bot" || mode == "deleted"
			if allowed && err != nil {
				t.Fatal(err)
			}
			if !allowed && err == nil {
				t.Fatal("unsafe retraction accepted")
			}
			if !allowed && mode != "error" && deletes != 0 {
				t.Fatal("unsafe target mutated")
			}
			if mode == "deleted" && deletes != 0 {
				t.Fatal("deleted own message mutated twice")
			}
		})
	}
}
