package lark

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

type localMessageFunc func(context.Context, domain.Scope, string) (domain.Message, error)

func (f localMessageFunc) Message(ctx context.Context, s domain.Scope, id string) (domain.Message, error) {
	return f(ctx, s, id)
}
func cachedMessage() domain.Message {
	at := time.Unix(1792650000, 0).UTC()
	return domain.Message{Source: domain.SourceRef{Scope: clientScope(), MessageID: "message", AuthorID: "alice", Version: 1, OccurredAt: at}, Text: "cached original", UpdatedAt: at}
}
func TestReadMessageLocalFirstNeverReadsProviderForOriginalOrTombstone(t *testing.T) {
	for _, deleted := range []bool{false, true} {
		m := cachedMessage()
		m.Deleted = deleted
		if deleted {
			m.Source.AuthorID = ""
			m.Source.OccurredAt = time.Time{}
		}
		var calls atomic.Int32
		c, err := NewClient(ClientConfig{AppID: "app", AppSecret: "secret", BotOpenID: "bot", LocalMessages: localMessageFunc(func(context.Context, domain.Scope, string) (domain.Message, error) { return m, nil }), HTTPClient: &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
			calls.Add(1)
			return nil, errors.New("provider unavailable")
		})}})
		if err != nil {
			t.Fatal(err)
		}
		got, err := c.ReadMessage(context.Background(), clientScope(), "message")
		if err != nil || got.Source != m.Source || got.Text != m.Text || got.Deleted != deleted || calls.Load() != 0 {
			t.Fatalf("deleted=%v got=%+v err=%v provider calls=%d", deleted, got, err, calls.Load())
		}
	}
}
func TestReadMessageLocalErrorsAndInvalidIdentityNeverFallBack(t *testing.T) {
	databaseErr := errors.New("database unavailable")
	for _, mode := range []string{"database", "scope", "id", "invalid", "canceled"} {
		m := cachedMessage()
		var localErr error
		want := domain.ErrUnauthorized
		ctx := context.Background()
		switch mode {
		case "database":
			localErr = databaseErr
			want = databaseErr
		case "scope":
			m.Source.Scope.ChatID = "other"
		case "id":
			m.Source.MessageID = "other"
		case "invalid":
			m.Source.AuthorID = ""
			want = domain.ErrInvalid
		case "canceled":
			cc, cancel := context.WithCancel(ctx)
			cancel()
			ctx = cc
			want = context.Canceled
		}
		var calls atomic.Int32
		c, _ := NewClient(ClientConfig{AppID: "app", AppSecret: "secret", BotOpenID: "bot", LocalMessages: localMessageFunc(func(context.Context, domain.Scope, string) (domain.Message, error) { return m, localErr }), HTTPClient: &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
			calls.Add(1)
			return nil, errors.New("provider unavailable")
		})}})
		if _, err := c.ReadMessage(ctx, clientScope(), "message"); !errors.Is(err, want) || calls.Load() != 0 {
			t.Fatalf("%s err=%v provider calls=%d", mode, err, calls.Load())
		}
	}
}
func TestReadMessageExplicitMissingLocalFetchesProviderOnce(t *testing.T) {
	var gets atomic.Int32
	c, _ := NewClient(ClientConfig{AppID: "app", AppSecret: "secret", BotOpenID: "bot", LocalMessages: localMessageFunc(func(context.Context, domain.Scope, string) (domain.Message, error) {
		return domain.Message{}, domain.ErrNotFound
	}), HTTPClient: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/auth/") {
			return response(`{"code":0,"tenant_access_token":"test-token","expire":7200}`), nil
		}
		gets.Add(1)
		return response(`{"code":0,"data":{"items":[{"message_id":"message","chat_id":"group","msg_type":"text","create_time":"1792649990000","update_time":"1792650000000","sender":{"id":"alice","id_type":"open_id","sender_type":"user","tenant_key":"tenant"},"body":{"content":"{\"text\":\"fetched original\"}"}}]}}`), nil
	})}})
	m, err := c.ReadMessage(context.Background(), clientScope(), "message")
	if err != nil || m.Text != "fetched original" || gets.Load() != 1 {
		t.Fatalf("missing fetch err=%v gets=%d message=%+v", err, gets.Load(), m)
	}
}
func TestSendUsesLocalTargetValidationWithoutProviderRead(t *testing.T) {
	for _, mode := range []string{"known", "missing", "foreign", "deleted", "database", "unauthorized_scope", "canceled"} {
		var gets, posts, validations atomic.Int32
		ctx := context.Background()
		scope := clientScope()
		var targetErr error
		switch mode {
		case "missing":
			targetErr = domain.ErrNotFound
		case "foreign":
			targetErr = domain.ErrUnauthorized
		case "deleted":
			targetErr = domain.ErrStaleSource
		case "database":
			targetErr = errors.New("database unavailable")
		case "unauthorized_scope":
			scope.AppID = "foreign"
		case "canceled":
			cc, cancel := context.WithCancel(ctx)
			cancel()
			ctx = cc
		}
		c, _ := NewClient(ClientConfig{AppID: "app", AppSecret: "secret", BotOpenID: "bot", ValidateTarget: func(_ context.Context, s domain.Scope, id string) error {
			validations.Add(1)
			if s != clientScope() || id != "message" {
				t.Error("target identity changed")
			}
			return targetErr
		}, HTTPClient: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
			if strings.Contains(r.URL.Path, "/auth/") {
				return response(`{"code":0,"tenant_access_token":"test-token","expire":7200}`), nil
			}
			if r.Method == "GET" {
				gets.Add(1)
				return response(`{"code":0,"data":{"items":[{"message_id":"message","chat_id":"group"}]}}`), nil
			}
			posts.Add(1)
			return response(`{"code":0,"data":{"message_id":"sent"}}`), nil
		})}})
		id, err := c.Send(ctx, scope, "message", "text", `{"text":"reply"}`, "frozen-uuid")
		if gets.Load() != 0 {
			t.Fatalf("%s caused target GET", mode)
		}
		if mode == "known" {
			if err != nil || id != "sent" || posts.Load() != 1 || validations.Load() != 1 {
				t.Fatalf("known reply err=%v id=%s posts=%d validations=%d", err, id, posts.Load(), validations.Load())
			}
		} else if err == nil || posts.Load() != 0 {
			t.Fatalf("%s write reached provider err=%v posts=%d", mode, err, posts.Load())
		}
		if (mode == "unauthorized_scope" || mode == "canceled") && validations.Load() != 0 {
			t.Fatal("outer guard bypassed")
		}
	}
}
func TestCurrentTargetAndImageReuseLocalRowsAndRejectTombstones(t *testing.T) {
	m := cachedMessage()
	media := domain.Media{Type: domain.MediaImage, Source: domain.MediaSourceLark, ResourceID: "image", Sequence: 0}
	m.Media = []domain.Media{media}
	var calls atomic.Int32
	c, _ := NewClient(ClientConfig{AppID: "app", AppSecret: "secret", BotOpenID: "bot", LocalMessages: localMessageFunc(func(context.Context, domain.Scope, string) (domain.Message, error) { return m, nil }), HTTPClient: &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, errors.New("provider unavailable")
	})}})
	if _, err := c.currentTarget(context.Background(), clientScope(), "message"); err != nil {
		t.Fatal(err)
	}
	if err := c.checkImage(context.Background(), clientScope(), m, media); err != nil {
		t.Fatal(err)
	}
	old := m.Clone()
	m.Source.Version++
	if err := c.checkImage(context.Background(), clientScope(), old, media); !errors.Is(err, domain.ErrStaleSource) {
		t.Fatal("stale version accepted", err)
	}
	m.Deleted = true
	if _, err := c.currentTarget(context.Background(), clientScope(), "message"); !errors.Is(err, domain.ErrStaleSource) {
		t.Fatal("tombstone accepted", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("cached target/image caused %d provider calls", calls.Load())
	}
}

func TestReactionsReuseLocalTargetWithoutProviderMessageReads(t *testing.T) {
	m := cachedMessage()
	var messageGets, reactionGets, writes atomic.Int32
	c, _ := NewClient(ClientConfig{AppID: "app", AppSecret: "secret", BotOpenID: "bot", LocalMessages: localMessageFunc(func(context.Context, domain.Scope, string) (domain.Message, error) { return m, nil }), HTTPClient: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/auth/") {
			return response(`{"code":0,"tenant_access_token":"test-token","expire":7200}`), nil
		}
		if strings.Contains(r.URL.Path, "/reactions") {
			if r.Method == "GET" {
				reactionGets.Add(1)
				return response(`{"code":0,"data":{"items":[{"reaction_id":"own","operator":{"operator_id":"app","operator_type":"app"},"reaction_type":{"emoji_type":"THUMBSUP"}}],"has_more":false}}`), nil
			}
			writes.Add(1)
			return response(`{"code":0,"data":{"reaction_id":"own","operator":{"operator_id":"app","operator_type":"app"},"reaction_type":{"emoji_type":"THUMBSUP"}}}`), nil
		}
		messageGets.Add(1)
		return nil, errors.New("message provider unavailable")
	})}})
	if _, err := c.ListReactions(context.Background(), clientScope(), "message", 100, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := c.AddReaction(context.Background(), clientScope(), "message", "THUMBSUP", "add-uuid"); err != nil {
		t.Fatal(err)
	}
	if err := c.RemoveReaction(context.Background(), clientScope(), "message", "own", "remove-uuid"); err != nil {
		t.Fatal(err)
	}
	if messageGets.Load() != 0 || reactionGets.Load() != 2 || writes.Load() != 2 {
		t.Fatalf("message reads=%d reaction reads=%d writes=%d", messageGets.Load(), reactionGets.Load(), writes.Load())
	}
	m.Deleted = true
	if _, err := c.AddReaction(context.Background(), clientScope(), "message", "THUMBSUP", "deleted-uuid"); !errors.Is(err, domain.ErrStaleSource) || writes.Load() != 2 {
		t.Fatal("deleted target reached write", err)
	}
}
