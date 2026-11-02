package lark

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(body string) *http.Response {
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), ContentLength: -1}
}
func clientScope() domain.Scope {
	return domain.Scope{TenantID: "tenant", AppID: "app", ChatID: "group", Visibility: "group"}
}

type countingBody struct {
	read   int
	closed bool
}

func (b *countingBody) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}
	b.read += len(p)
	return len(p), nil
}
func (b *countingBody) Close() error { b.closed = true; return nil }

func TestSDKResourceDownloadStopsBeforeUnboundedBody(t *testing.T) {
	body := &countingBody{}
	c, err := NewClient(ClientConfig{AppID: "app", AppSecret: "secret", BotOpenID: "bot", MaxDownloadBytes: 64, HTTPClient: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/resources/") {
			resp := response("")
			resp.Body = body
			return resp, nil
		}
		return response(`{"code":0,"tenant_access_token":"test-token","expire":7200}`), nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	media := domain.Media{Type: domain.MediaImage, Source: domain.MediaSourceLark, ResourceID: "img", Sequence: 0}
	m := domain.Message{Source: domain.SourceRef{Scope: clientScope(), MessageID: "message", AuthorID: "alice", Version: 1, OccurredAt: time.Now()}, Media: []domain.Media{media}}
	if _, _, err := c.Download(context.Background(), m, media); err == nil {
		t.Fatal("unbounded resource accepted")
	}
	if body.read > 65 || !body.closed {
		t.Fatal("resource budget did not stop and close upstream")
	}
}

func TestDownloadCannotSubstituteAnotherResource(t *testing.T) {
	var calls atomic.Int32
	c, _ := NewClient(ClientConfig{AppID: "app", AppSecret: "secret", BotOpenID: "bot", HTTPClient: &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) { calls.Add(1); return response(`{}`), nil })}})
	m := domain.Message{Source: domain.SourceRef{Scope: clientScope(), MessageID: "message", AuthorID: "alice", Version: 1, OccurredAt: time.Now()}, Media: []domain.Media{{Type: domain.MediaImage, Source: domain.MediaSourceLark, ResourceID: "original"}}}
	if _, _, err := c.Download(context.Background(), m, domain.Media{Type: domain.MediaImage, Source: domain.MediaSourceLark, ResourceID: "different"}); err == nil || calls.Load() != 0 {
		t.Fatal("substituted resource reached SDK")
	}
}

func TestReplyRejectsParentFromAnotherGroupAndKeepsDeliveryUUID(t *testing.T) {
	var posts atomic.Int32
	wrongGroup := true
	c, err := NewClient(ClientConfig{AppID: "app", AppSecret: "secret", BotOpenID: "bot", HTTPClient: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/auth/") {
			return response(`{"code":0,"tenant_access_token":"test-token","expire":7200}`), nil
		}
		if r.Method == "GET" {
			group := "group"
			if wrongGroup {
				group = "other"
			}
			b, _ := json.Marshal(map[string]any{"code": 0, "data": map[string]any{"items": []any{map[string]any{"message_id": "parent", "chat_id": group}}}})
			return response(string(b)), nil
		}
		posts.Add(1)
		var b map[string]any
		json.NewDecoder(r.Body).Decode(&b)
		if b["uuid"] != "frozen-delivery-uuid" {
			t.Error("delivery UUID lost")
		}
		return response(`{"code":0,"data":{"message_id":"sent"}}`), nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Send(context.Background(), clientScope(), "parent", "text", `{"text":"result"}`, "frozen-delivery-uuid"); err == nil || posts.Load() != 0 {
		t.Fatal("cross-group reply sent")
	}
	wrongGroup = false
	id, err := c.Send(context.Background(), clientScope(), "parent", "text", `{"text":"result"}`, "frozen-delivery-uuid")
	if err != nil || id != "sent" || posts.Load() != 1 {
		t.Fatal("scoped reply failed")
	}
}

func TestReadMessageUsesOriginalAuthorAndProviderVersion(t *testing.T) {
	c, _ := NewClient(ClientConfig{AppID: "app", AppSecret: "secret", BotOpenID: "bot", HTTPClient: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/auth/") {
			return response(`{"code":0,"tenant_access_token":"test-token","expire":7200}`), nil
		}
		return response(`{"code":0,"data":{"items":[{"message_id":"message","chat_id":"group","msg_type":"text","create_time":"1792649990000","update_time":"1792650000000","sender":{"id":"alice","id_type":"open_id","sender_type":"user","tenant_key":"tenant"},"body":{"content":"{\"text\":\"最新正文\"}"}}]}}`), nil
	})}})
	m, err := c.ReadMessage(context.Background(), clientScope(), "message")
	if err != nil {
		t.Fatal(err)
	}
	if m.Source.AuthorID != "alice" || m.Source.Version != 1792650000000*2 || m.Text != "最新正文" || m.Source.OccurredAt.UnixMilli() != 1792649990000 {
		t.Fatal("read substituted processing identity/time")
	}
	var meta domain.IngressMetadata
	json.Unmarshal(m.Raw, &meta)
	if meta.Kind != "refresh" || meta.Addressed {
		t.Fatal("historical read became new participation request")
	}
}
