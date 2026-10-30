package lark

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

func TestSDKUpdateCardVerifiesExactOwnInteractiveMessageBeforeMutation(t *testing.T) {
	for _, mode := range []string{"app", "bot", "foreign_app", "foreign_bot", "group", "text", "deleted", "missing_sender", "no_update_multi", "false_update_multi", "empty_uuid", "zero_sequence", "negative_sequence", "overflow_sequence"} {
		t.Run(mode, func(t *testing.T) {
			patches := 0
			uuid, sequence := "frame-one", 1
			switch mode {
			case "empty_uuid":
				uuid = ""
			case "zero_sequence":
				sequence = 0
			case "negative_sequence":
				sequence = -1
			case "overflow_sequence":
				sequence = 2147483648
			}
			content := `{"schema":"2.0","config":{"update_multi":true},"body":{"elements":[{"tag":"markdown","content":"full body"}]}}`
			if mode == "false_update_multi" {
				content = `{"schema":"2.0","config":{"update_multi":false},"body":{"elements":[]}}`
			}
			if mode == "no_update_multi" {
				content = `{"schema":"2.0","body":{"elements":[]}}`
			}
			c, err := NewClient(ClientConfig{AppID: "app", AppSecret: "secret", BotOpenID: "bot", HTTPClient: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				if strings.Contains(r.URL.Path, "/auth/") {
					return response(`{"code":0,"tenant_access_token":"test-token","expire":7200}`), nil
				}
				if r.Method == "GET" {
					m := map[string]any{"message_id": "card", "chat_id": "group", "msg_type": "interactive", "deleted": false, "sender": map[string]any{"id": "app", "id_type": "app_id", "sender_type": "app", "open_bot_id": "bot"}}
					// Real GET returns elements/title, without the original config.
					m["body"] = map[string]any{"content": `{"elements":[{"tag":"markdown","content":"prior detail"}],"title":""}`}
					switch mode {
					case "bot":
						m["sender"] = map[string]any{"id": "bot", "id_type": "open_id", "sender_type": "app"}
					case "foreign_app":
						m["sender"] = map[string]any{"id": "foreign", "id_type": "app_id", "sender_type": "app", "open_bot_id": "other"}
					case "foreign_bot":
						m["sender"] = map[string]any{"id": "other", "id_type": "open_id", "sender_type": "app"}
					case "group":
						m["chat_id"] = "other"
					case "text":
						m["msg_type"] = "text"
					case "deleted":
						m["deleted"] = true
					case "missing_sender":
						delete(m, "sender")
					}
					b, _ := json.Marshal(map[string]any{"code": 0, "data": map[string]any{"items": []any{m}}})
					return response(string(b)), nil
				}

				if r.URL.Path == "/open-apis/cardkit/v1/cards/id_convert" {
					var body struct {
						MessageID string `json:"message_id"`
					}
					if r.Method != "POST" || json.NewDecoder(r.Body).Decode(&body) != nil || body.MessageID != "card" {
						t.Error("card conversion lost confirmed message ID")
					}
					return response(`{"code":0,"data":{"card_id":"entity"}}`), nil
				}
				patches++

				var body struct {
					Card     struct{ Type, Data string }
					Uuid     string
					Sequence int
				}
				if r.Method != "PUT" || r.URL.Path != "/open-apis/cardkit/v1/cards/entity" || json.NewDecoder(r.Body).Decode(&body) != nil || body.Card.Type != "card_json" || body.Card.Data != content || body.Uuid != "frame-one" || body.Sequence != 1 {
					t.Error("invalid atomic card update wire method/path/content/uuid/sequence")
				}

				return response(`{"code":0}`), nil
			})}})
			if err != nil {
				t.Fatal(err)
			}
			updater, ok := any(c).(interface {
				UpdateCard(context.Context, domain.Scope, string, string, string, int) error
			})
			if !ok {
				t.Fatal("SDK client has no card updater")
			}
			err = updater.UpdateCard(context.Background(), clientScope(), "card", content, uuid, sequence)
			allowed := mode == "app" || mode == "bot"
			if allowed && (err != nil || patches != 1) {
				t.Fatalf("own card update failed: patches=%d err=%v", patches, err)
			}
			if !allowed && (err == nil || patches != 0) {
				t.Fatalf("unauthorized card update reached PATCH: patches=%d err=%v", patches, err)
			}
		})
	}
}

func TestSDKCardKitSequenceAndUUIDPreventLateOldFrameFromOverwritingFinal(t *testing.T) {
	var mu sync.Mutex
	started, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	calls, highest := 0, 0
	visible := ""
	type operation struct {
		uuid, content string
		sequence      int
	}
	var received []operation
	seen := map[string]operation{}
	c, err := NewClient(ClientConfig{AppID: "app", AppSecret: "secret", BotOpenID: "bot", HTTPClient: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/auth/") {
			return response(`{"code":0,"tenant_access_token":"test-token","expire":7200}`), nil
		}
		if r.Method == "GET" {
			return response(`{"code":0,"data":{"items":[{"message_id":"card","chat_id":"group","msg_type":"interactive","deleted":false,"sender":{"id":"app","id_type":"app_id","sender_type":"app","open_bot_id":"bot"},"body":{"content":"{\"elements\":[],\"title\":\"\"}"}}]}}`), nil
		}
		if r.URL.Path == "/open-apis/cardkit/v1/cards/id_convert" {
			var body struct {
				MessageID string `json:"message_id"`
			}
			if r.Method != "POST" || json.NewDecoder(r.Body).Decode(&body) != nil || body.MessageID != "card" {
				t.Error("invalid message-to-card conversion")
			}
			return response(`{"code":0,"data":{"card_id":"entity"}}`), nil
		}
		var body struct {
			Card     struct{ Type, Data string }
			Uuid     string
			Sequence int
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Error("invalid card update body")
		}
		if r.Method != "PUT" || r.URL.Path != "/open-apis/cardkit/v1/cards/entity" || body.Card.Type != "card_json" || body.Uuid == "" || body.Sequence < 1 {
			t.Error("update missing provider atomic ordering contract")
		}
		op := operation{body.Uuid, body.Card.Data, body.Sequence}
		mu.Lock()
		calls++
		call := calls
		received = append(received, op)
		mu.Unlock()
		// Request has left the worker; ignoring cancellation models late arrival.
		if call == 1 {
			close(started)
			<-release
		}
		mu.Lock()
		defer mu.Unlock()
		if old, ok := seen[op.uuid]; ok {
			if old != op {
				t.Error("same UUID changed operation")
			}
			return response(`{"code":0}`), nil
		}
		if op.sequence <= highest {
			return response(`{"code":12345,"msg":"sequence is stale"}`), nil
		}
		highest = op.sequence
		visible = op.content
		seen[op.uuid] = op
		return response(`{"code":0}`), nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	updater, ok := any(c).(interface {
		UpdateCard(context.Context, domain.Scope, string, string, string, int) error
	})
	if !ok {
		t.Fatal("SDK updater lacks provider sequence/uuid fencing")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	first := `{"schema":"2.0","config":{"update_multi":true},"body":{"elements":[{"tag":"markdown","content":"first"}]}}`
	final := strings.Replace(first, "first", "first plus final", 1)
	done := make(chan error, 1)
	go func() { done <- updater.UpdateCard(ctx, clientScope(), "card", first, "stable-first-update", 1) }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("old card update did not reach provider")
	}
	if err = updater.UpdateCard(ctx, clientScope(), "card", first, "stable-first-update", 1); err != nil {
		t.Fatal(err)
	}
	if err = updater.UpdateCard(ctx, clientScope(), "card", final, "stable-final-update", 2); err != nil {
		t.Fatal(err)
	}
	if err = updater.UpdateCard(ctx, clientScope(), "card", first, "different-stale-request", 1); err == nil {
		t.Fatal("provider stale-sequence rejection was hidden")
	}
	releaseOnce.Do(func() { close(release) })
	<-done
	mu.Lock()
	defer mu.Unlock()
	if visible != final || highest != 2 || len(received) != 4 || received[0] != received[1] || received[2].uuid == received[0].uuid {
		t.Fatal("late old frame regressed provider or retry/frame identity changed")
	}
}

// The service binds this tuple in immutable delivery storage before the first
// call. This models a provider-confirmed update whose receipt was lost, followed
// by an exact replay of the same frozen target, content, UUID and sequence.
func TestSDKCardKitConsumedUUIDConfirmsExactPreviouslyAcceptedOperation(t *testing.T) {
	type operation struct {
		Path     string
		Card     struct{ Type, Data string }
		Uuid     string
		Sequence int
	}
	var accepted *operation
	calls := 0
	c, err := NewClient(ClientConfig{AppID: "app", AppSecret: "secret", BotOpenID: "bot", HTTPClient: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/auth/") {
			return response(`{"code":0,"tenant_access_token":"test-token","expire":7200}`), nil
		}
		if r.Method == "GET" {
			return response(`{"code":0,"data":{"items":[{"message_id":"card","chat_id":"group","msg_type":"interactive","deleted":false,"sender":{"id":"app","id_type":"app_id","sender_type":"app","open_bot_id":"bot"}}]}}`), nil
		}
		if r.URL.Path == "/open-apis/cardkit/v1/cards/id_convert" {
			return response(`{"code":0,"data":{"card_id":"entity"}}`), nil
		}
		var op operation
		op.Path = r.URL.Path
		if json.NewDecoder(r.Body).Decode(&op) != nil {
			t.Fatal("invalid update request")
		}
		calls++
		if accepted == nil {
			accepted = &op
			return response(`{"code":0}`), nil
		}
		if *accepted != op {
			t.Fatal("retry changed immutable operation tuple")
		}
		return response(`{"code":200770,"msg":"ErrMsg: this UUID has been recently consumed; "}`), nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	content := `{"schema":"2.0","config":{"update_multi":true},"body":{"elements":[{"tag":"markdown","content":"frozen answer"}]}}`
	if err = c.UpdateCard(context.Background(), clientScope(), "card", content, "same-frozen-operation", 1); err != nil {
		t.Fatal(err)
	}
	if err = c.UpdateCard(context.Background(), clientScope(), "card", content, "same-frozen-operation", 1); err != nil {
		t.Fatalf("exact accepted UUID replay could not confirm receipt: %v", err)
	}
	if calls != 2 {
		t.Fatal("exact replay performed unexpected provider writes")
	}
}

func TestSDKCardKitOtherErrorsAndConversionUUIDErrorRemainFailures(t *testing.T) {
	for _, code := range []int{10002, 200771, 200769, 300317} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			for _, stage := range []string{"update", "id_convert", "get"} {
				c, err := NewClient(ClientConfig{AppID: "app", AppSecret: "secret", BotOpenID: "bot", HTTPClient: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
					if strings.Contains(r.URL.Path, "/auth/") {
						return response(`{"code":0,"tenant_access_token":"test-token","expire":7200}`), nil
					}
					if r.Method == "GET" {
						if stage == "get" {
							return response(`{"code":200770,"msg":"UUID consumed outside Update"}`), nil
						}
						return response(`{"code":0,"data":{"items":[{"message_id":"card","chat_id":"group","msg_type":"interactive","sender":{"id":"app","id_type":"app_id","sender_type":"app","open_bot_id":"bot"}}]}}`), nil
					}
					if r.URL.Path == "/open-apis/cardkit/v1/cards/id_convert" {
						if stage == "id_convert" {
							return response(`{"code":200770,"msg":"UUID consumed outside Update"}`), nil
						}
						return response(`{"code":0,"data":{"card_id":"entity"}}`), nil
					}
					return response(fmt.Sprintf(`{"code":%d,"msg":"provider rejected"}`, code)), nil
				})}})
				if err != nil {
					t.Fatal(err)
				}
				if err = c.UpdateCard(context.Background(), clientScope(), "card", `{"schema":"2.0","config":{"update_multi":true},"body":{"elements":[]}}`, "frozen-operation", 1); err == nil {
					t.Fatalf("unrelated provider error accepted: code=%d stage=%s", code, stage)
				}
			}
		})
	}
}
