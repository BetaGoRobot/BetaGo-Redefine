package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/lark"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/pgstore"
)

func TestServiceGroupCancelsAndJoinsEveryStartedService(t *testing.T) {
	started := make(chan struct{})
	joined := make(chan struct{})
	want := errors.New("service failed")
	err := runServices(context.Background(),
		func(ctx context.Context) error { close(started); <-ctx.Done(); close(joined); return ctx.Err() },
		func(context.Context) error { <-started; return want },
	)
	if !errors.Is(err, want) {
		t.Fatal("first failure was lost")
	}
	select {
	case <-joined:
	default:
		t.Fatal("service returned before its sibling stopped")
	}
}

func TestAssembledAgentPersistsGroupSceneAndDeliversStructuredReplies(t *testing.T) {
	dsn := os.Getenv("AGENT_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("isolated PostgreSQL fixture required")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	var nonce [8]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	schema := "entry_test_" + hex.EncodeToString(nonce[:])
	store, err := pgstore.New(db, schema)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Exec(`DROP SCHEMA IF EXISTS "` + schema + `" CASCADE`); db.Close() })
	if err = store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	var models, admissions, answers, sends, messageReads atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		models.Add(1)
		body, _ := io.ReadAll(r.Body)
		var input struct {
			ResponseFormat json.RawMessage `json:"response_format"`
			Messages       []struct {
				Role    string
				Content string
			} `json:"messages"`
			Tools []struct {
				Function struct{ Name string } `json:"function"`
			} `json:"tools"`
		}
		if err := json.Unmarshal(body, &input); err != nil {
			t.Error("invalid model request")
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(string(input.ResponseFormat), `"participation"`) {
			admissions.Add(1)
			t.Error("group dialogue must not be discarded by a separate participation model")
			w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"{\"respond\":false}"},"finish_reason":"stop"}]}`))
			return
		}
		answers.Add(1)
		seenInvitation := false
		currentID := ""
		for _, msg := range input.Messages {
			if msg.Role != "user" {
				continue
			}
			var scene struct {
				InvocationOrigin string
				CurrentUtterance struct {
					Addressed              bool
					MessageID, SpeakerName string
				}
			}
			if json.Unmarshal([]byte(msg.Content), &scene) == nil {
				currentID = scene.CurrentUtterance.MessageID
				if scene.CurrentUtterance.SpeakerName != "Alice" {
					t.Errorf("current author lacks resolved group name: %q", scene.CurrentUtterance.SpeakerName)
				}
				seenInvitation = scene.CurrentUtterance.Addressed && scene.InvocationOrigin == "direct"
			}
		}
		if currentID == "silent" {
			w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"{\"choice\":\"observe\",\"messages\":[]}"},"finish_reason":"stop"}]}`))
			return
		}
		if currentID == "resume" {
			if seenInvitation || !strings.Contains(string(body), "已查看本群请求") {
				t.Error("unmentioned follow-up lost the actual earlier bot reply")
			}
		} else if !seenInvitation {
			t.Error("SDK root refresh erased the verified direct social invitation")
		}

		names := map[string]bool{}
		for _, tool := range input.Tools {
			names[tool.Function.Name] = true
			if tool.Function.Name == "feishu_send_message" {
				t.Error("ordinary replies must use final delivery, not an approval-gated send tool")
			}
		}
		if !names["web_search"] || !names["read_webpage"] {
			t.Error("answer lacks configured scoped external read tools")
		}
		if !strings.Contains(string(input.ResponseFormat), `"group_conversation"`) {
			t.Error("production answer lacks structured conversation schema")
		}
		if !strings.Contains(string(body), "bob") || !strings.Contains(string(body), "上文是预算讨论") {
			t.Error("resolved reply material or original author missing from native prompt")
		}
		w.Header().Set("Content-Type", "application/json")
		answer := fmt.Sprintf(`{"choice":"reply","messages":[{"text":"已查看本群请求","reply_to_message_id":%q}]}`, currentID)
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"role": "assistant", "content": answer}, "finish_reason": "stop"}}})
	}))
	defer provider.Close()
	created := fmt.Sprint(time.Now().Add(-time.Minute).UnixMilli())
	oldCreated := fmt.Sprint(time.Now().Add(-3 * time.Hour).UnixMilli())
	editTime := fmt.Sprint(time.Now().Add(-30 * time.Second).UnixMilli())
	var edited atomic.Bool
	larkHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "tenant_access_token"):
			w.Write([]byte(`{"code":0,"tenant_access_token":"fixture","expire":7200}`))
		case r.Method == "GET" && r.URL.Path == "/open-apis/im/v1/chats/group/members":
			if r.URL.Query().Get("member_id_type") != "open_id" {
				t.Error("member display request lost exact open ID type")
			}
			w.Write([]byte(`{"code":0,"data":{"items":[{"member_id":"alice","member_id_type":"open_id","name":"Alice"},{"member_id":"bob","member_id_type":"open_id","name":"Bob"}],"has_more":false,"member_total":2}}`))
		case r.Method == "GET" && strings.Contains(r.URL.Path, "/messages/"):
			messageReads.Add(1)
			id := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			text := "群友互聊"
			author := "alice"
			if id == "historical" {
				text = "上文是预算讨论"
				author = "bob"
			}
			sdkParent := ""
			updated := created
			messageCreated := created
			if id == "silent" && edited.Load() {
				text = "群友编辑后的原话"
				updated = editTime
			}
			if id == "request" {
				text = "@_user_1 查预算（补充）"
				sdkParent = "historical"
				messageCreated = oldCreated
				updated = editTime
			}
			if id == "resume" {
				text = "你觉得呢。。"
			}
			content, _ := json.Marshal(map[string]string{"text": text})
			json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"items": []any{map[string]any{"message_id": id, "chat_id": "group", "parent_id": sdkParent, "msg_type": "text", "create_time": messageCreated, "update_time": updated, "sender": map[string]string{"id": author, "id_type": "open_id", "sender_type": "user", "tenant_key": "tenant"}, "body": map[string]string{"content": string(content)}}}}})
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/reply"):
			sends.Add(1)
			var body struct{ UUID, Content string }
			if json.NewDecoder(r.Body).Decode(&body) != nil || body.UUID == "" || !strings.Contains(body.Content, "已查看本群请求") {
				t.Error("frozen delivery not wired to SDK")
			}
			w.Write([]byte(`{"code":0,"data":{"message_id":"delivered"}}`))
		default:
			t.Errorf("unexpected SDK operation %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer larkHTTP.Close()
	leaf, err := lark.NewClient(lark.ClientConfig{AppID: "app", AppSecret: "secret", BotOpenID: "bot", BaseURL: larkHTTP.URL})
	if err != nil {
		t.Fatal(err)
	}
	c := config.Config{App: config.App{Visibility: "group", APIKey: "management-fixture"}, Lark: config.Lark{AppID: "app", AppSecret: "secret", BotOpenID: "bot", Transport: "callback"}, Runtime: config.Runtime{MaxConcurrentTasks: 2, MaxTurns: 3, MaxContextBytes: 131072, TurnLeaseSeconds: 30, TaskTimeoutSeconds: 60}, Worker: config.Worker{PollMilliseconds: 10}}
	c.Models.WebSearch = config.Endpoint{BaseURL: provider.URL, APIKey: "fixture", Model: "search-scripted", Protocol: "ark_responses", TimeoutSeconds: 5}
	c.Models.Answer = config.Endpoint{BaseURL: provider.URL, APIKey: "fixture", Model: "scripted", Protocol: "openai", TimeoutSeconds: 5}
	rt, err := assembleAgent(c, &components{db: db, store: store, lark: leaf})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- rt.run(ctx, "127.0.0.1:0") }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(3 * time.Second):
			t.Error("assembled service did not stop")
		}
	})
	post := func(id, text, parent string) {
		b, _ := json.Marshal(map[string]any{"schema": "2.0", "header": map[string]string{"event_id": "event_" + id, "event_type": "im.message.receive_v1", "app_id": "app", "tenant_key": "tenant", "create_time": created}, "event": map[string]any{"sender": map[string]any{"sender_id": map[string]string{"open_id": "alice"}, "sender_type": "user", "tenant_key": "tenant"}, "message": map[string]any{"message_id": id, "chat_id": "group", "chat_type": "group", "parent_id": parent, "message_type": "text", "content": fmt.Sprintf(`{"text":%q}`, text), "create_time": created, "update_time": created}}})
		if id == "request" {
			var payload map[string]any
			if err := json.Unmarshal(b, &payload); err != nil {
				t.Fatal(err)
			}
			payload["event"].(map[string]any)["message"].(map[string]any)["mentions"] = []any{map[string]any{"key": "@_user_1", "id": map[string]string{"open_id": "bot"}}}
			payload["event"].(map[string]any)["message"].(map[string]any)["create_time"] = oldCreated
			b, _ = json.Marshal(payload)
		}
		if _, err := rt.ingress.Dispatcher().Do(ctx, b); err != nil {
			t.Fatal(err)
		}
	}
	post("silent", "群友互聊", "")
	scope := domain.Scope{TenantID: "tenant", AppID: "app", ChatID: "group", Visibility: "group"}
	if m, err := store.Message(ctx, scope, "silent"); err != nil || m.Source.AuthorID != "alice" {
		t.Fatal("SDK acknowledged before persistence")
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		var n int
		err = db.QueryRow(`SELECT count(*) FROM "` + schema + `".jobs WHERE kind='route' AND status='completed'`).Scan(&n)
		if err != nil {
			t.Fatal(err)
		}
		var observed int
		if err = db.QueryRow(`SELECT count(*) FROM "` + schema + `".turns WHERE status='observed'`).Scan(&observed); err != nil {
			t.Fatal(err)
		}
		if n > 0 && observed == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("group route never ran")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if admissions.Load() != 0 || answers.Load() != 1 || sends.Load() != 0 {
		t.Fatal("ordinary group chatter must reach full conversation and may quietly observe")
	}
	if got := messageReads.Load(); got != 0 {
		t.Fatalf("ordinary cached group dialogue queried Feishu messages %d times", got)
	}
	refresher := sourceRefresher(&components{store: store, lark: leaf})
	original, _ := store.Message(ctx, scope, "silent")
	if err := refresher(ctx, original.Source); err != nil {
		t.Fatal(err)
	}
	unchanged, _ := store.Message(ctx, scope, "silent")
	if unchanged.EventID != original.EventID {
		t.Fatal("unchanged read minted a new event")
	}
	edited.Store(true)
	if err := refresher(ctx, original.Source); err != nil {
		t.Fatal(err)
	}
	newer, _ := store.Message(ctx, scope, "silent")
	var meta domain.IngressMetadata
	json.Unmarshal(newer.Raw, &meta)
	if newer.Source.Version <= original.Source.Version || newer.Source.AuthorID != "alice" || meta.Kind != "refresh" || meta.Addressed {
		t.Fatal("provider edit failed to preserve refresh/source boundary")
	}
	post("request", "@_user_1 查预算", "historical")
	deadline = time.Now().Add(5 * time.Second)
	for sends.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if models.Load() != 2 || admissions.Load() != 0 || answers.Load() != 2 || sends.Load() != 1 {
		t.Fatalf("request did not traverse native agent and SDK delivery: models=%d sends=%d", models.Load(), sends.Load())
	}
	if m, err := store.Message(ctx, scope, "historical"); err != nil || m.Source.AuthorID != "bob" {
		t.Fatal("missing explicit reply ancestor was not safely backfilled")
	}
	// Model the production pause without sleeping or reusing a live group:
	// the actual confirmed reply is two hours old; the new question is fresh.
	deadline = time.Now().Add(3 * time.Second)
	for {
		result, err := db.Exec(`UPDATE "`+schema+`".deliveries SET body=convert_to(jsonb_set(convert_from(body,'UTF8')::jsonb,'{UpdatedAt}',to_jsonb($1::text))::text,'UTF8') WHERE status='delivered' AND message_id='delivered'`, time.Now().UTC().Add(-2*time.Hour).Format(time.RFC3339Nano))
		if err != nil {
			t.Fatal(err)
		}
		if n, _ := result.RowsAffected(); n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("first reply receipt was never persisted")
		}
		time.Sleep(10 * time.Millisecond)
	}
	post("resume", "你觉得呢。。", "")
	deadline = time.Now().Add(5 * time.Second)
	for sends.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if models.Load() != 3 || admissions.Load() != 0 || answers.Load() != 3 || sends.Load() != 2 {
		t.Fatalf("fresh follow-up after pause was dropped: models=%d admissions=%d answers=%d sends=%d", models.Load(), admissions.Load(), answers.Load(), sends.Load())
	}
}
