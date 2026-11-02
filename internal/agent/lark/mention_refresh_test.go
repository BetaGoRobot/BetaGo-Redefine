package lark

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

func TestSDKRefreshKeepsMessageLocalMentionBindingsWithoutNewInvitation(t *testing.T) {
	client, err := NewClient(ClientConfig{AppID: "app", AppSecret: "fixture", BotOpenID: "bot", HTTPClient: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/auth/") {
			return response(`{"code":0,"tenant_access_token":"fixture","expire":7200}`), nil
		}
		if r.Method != http.MethodGet || r.URL.Query().Get("user_id_type") != "open_id" {
			t.Fatal("unexpected provider request")
		}
		id := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		target, name, kind := "bob", "乙", "open_id"
		if id == "second" {
			target, name, kind = "bot", "群助手", ""
		}
		body, _ := json.Marshal(map[string]any{"code": 0, "data": map[string]any{"items": []any{map[string]any{
			"message_id": id, "chat_id": "group", "msg_type": "text", "create_time": "1792738800000", "update_time": "1792738801000",
			"sender":   map[string]any{"id": "alice", "id_type": "open_id", "sender_type": "user", "tenant_key": "tenant"},
			"body":     map[string]any{"content": `{"text":"@_user_1 你看下"}`},
			"mentions": []any{map[string]any{"key": "@_user_1", "id": target, "id_type": kind, "name": name}, map[string]any{"key": "@_user_2", "id": "non-open-id", "id_type": "user_id", "name": "不可错绑"}},
		}}}})
		return response(string(body)), nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ id, target, name string }{{"first", "bob", "乙"}, {"second", "bot", "群助手"}} {
		message, err := client.ReadMessage(context.Background(), clientScope(), tc.id)
		if err != nil {
			t.Fatal(err)
		}
		var meta domain.IngressMetadata
		if json.Unmarshal(message.Raw, &meta) != nil {
			t.Fatal("invalid refresh metadata")
		}
		if len(meta.Mentions) != 1 || meta.Mentions[0].Key != "@_user_1" || meta.Mentions[0].OpenID != tc.target || meta.Mentions[0].Name != tc.name {
			t.Fatalf("refresh lost or mixed local mention binding for %s: %+v", tc.id, meta.Mentions)
		}
		if meta.Kind != "refresh" || meta.Addressed || message.Source.AuthorID != "alice" {
			t.Fatal("display mention became a new invitation or changed actual author")
		}
	}
}
