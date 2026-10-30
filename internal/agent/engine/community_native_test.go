package engine

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/atoms"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/lark"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/modelapi"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/pgstore"
)

type communityNativeTransport func(*http.Request) (*http.Response, error)

func (f communityNativeTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type communityNativeModel struct{ calls int }

func (m *communityNativeModel) Generate(_ context.Context, r modelapi.ChatRequest) (modelapi.ChatResponse, error) {
	m.calls++
	observations := 0
	for _, v := range r.Messages {
		if v.Role == "tool" {
			observations++
			raw, ok := v.Content.(string)
			if !ok || !json.Valid([]byte(raw)) {
				panic("reaction receipt not JSON")
			}
		}
	}
	if observations == 0 {
		return modelapi.ChatResponse{Message: modelapi.ChatMessage{Role: "assistant", ToolCalls: []modelapi.ToolCall{{ID: "reaction", Type: "function", Function: modelapi.ToolFunction{Name: "feishu_add_reaction", Arguments: `{"message_id":"request","emoji":"THUMBSUP"}`}}}}}, nil
	}
	return modelapi.ChatResponse{Message: modelapi.ChatMessage{Role: "assistant", Content: `{"choice":"observe","summary":"","messages":[]}`}}, nil
}
func TestNativeReactionThenObserveNeedsNoApprovalOrText(t *testing.T) {
	f := newAutoEffectFixture(t, true, false)
	var writes atomic.Int32
	client, err := lark.NewClient(lark.ClientConfig{AppID: "app", AppSecret: "fixture-secret", BotOpenID: "bot", HTTPClient: &http.Client{Transport: communityNativeTransport(func(r *http.Request) (*http.Response, error) {
		body := `{"code":0,"tenant_access_token":"fixture","expire":7200}`
		if !strings.Contains(r.URL.Path, "/auth/") {
			if r.Method == "GET" {
				body = `{"code":0,"data":{"items":[{"message_id":"request","chat_id":"chat","msg_type":"text","create_time":"1792649990000","update_time":"1792650000000","sender":{"id":"backend-actor","id_type":"open_id","sender_type":"user","tenant_key":"tenant"},"body":{"content":"{\"text\":\"please react\"}"}}]}}`
			} else {
				writes.Add(1)
				body = `{"code":0,"data":{"reaction_id":"own","operator":{"operator_id":"app","operator_type":"app"},"reaction_type":{"emoji_type":"THUMBSUP"}}}`
			}
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	reg, err := atoms.New(atoms.Options{Sources: f.store, Tasks: f.store, Group: client})
	if err != nil {
		t.Fatal(err)
	}
	model := &communityNativeModel{}
	f.engine, err = New(Options{Store: f.store, Model: model, Registry: reg, ConversationOutput: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = f.execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	approvals, err := f.store.Approvals(context.Background(), f.task.Scope, f.task.ID)
	if err != nil || len(approvals) != 0 || writes.Load() != 1 || model.calls != 2 {
		t.Fatalf("reaction loop changed: approvals=%d writes=%d turns=%d err=%v", len(approvals), writes.Load(), model.calls, err)
	}
	var deliveries int
	if err = f.db.QueryRow(`SELECT count(*) FROM "` + f.schema + `".deliveries`).Scan(&deliveries); err != nil || deliveries != 0 {
		t.Fatalf("reaction forced text/receipt delivery: %d %v", deliveries, err)
	}
	key := domain.StableID(f.task.ID, "feishu_add_reaction", `{"emoji":"THUMBSUP","message_id":"request"}`)
	x, err := f.store.ToolExecution(context.Background(), f.task.Scope, f.task.ID, key)
	if err != nil || x.Status != "completed" || x.ApprovalID != "" {
		t.Fatalf("missing automatic effect ledger: %+v %v", x, err)
	}
}

type communitySourceGroup struct {
	*lark.Client
	store *pgstore.Store
}

func (g communitySourceGroup) ReadMessage(ctx context.Context, s domain.Scope, id string) (domain.Message, error) {
	return g.store.Message(ctx, s, id)
}
func (g communitySourceGroup) ListReactions(context.Context, domain.Scope, string, int, string) (domain.ReactionPage, error) {
	return domain.ReactionPage{Reactions: []domain.Reaction{{ID: "own", Own: true}}}, nil
}

type communityReadModel struct {
	tool, args string
	prepare    func() error
	seen       bool
}

func (m *communityReadModel) Generate(_ context.Context, r modelapi.ChatRequest) (modelapi.ChatResponse, error) {
	for _, v := range r.Messages {
		if v.Role == "tool" {
			m.seen = true
			return modelapi.ChatResponse{Message: modelapi.ChatMessage{Role: "assistant", Content: `{"choice":"observe","summary":"","messages":[]}`}}, nil
		}
	}
	if err := m.prepare(); err != nil {
		return modelapi.ChatResponse{}, err
	}
	return modelapi.ChatResponse{Message: modelapi.ChatMessage{Role: "assistant", ToolCalls: []modelapi.ToolCall{{ID: "read", Type: "function", Function: modelapi.ToolFunction{Name: m.tool, Arguments: m.args}}}}}, nil
}
func TestNativeCommunityReadsPinActualSourceDependencies(t *testing.T) {
	for _, tool := range []string{"feishu_message_history", "feishu_message_context", "feishu_read_attachment", "feishu_list_reactions"} {
		t.Run(tool, func(t *testing.T) {
			f := newAutoEffectFixture(t, true, false)
			ctx := context.Background()
			now := time.Now().UTC().Add(-2 * time.Second)
			ref := domain.SourceRef{Scope: f.task.Scope, MessageID: "newly-read", Version: 1, AuthorID: "actual-author", OccurredAt: now}
			m := domain.Message{Source: ref, EventID: "newly-read", Text: "actual source text", UpdatedAt: now, Media: []domain.Media{{Type: domain.MediaImage, Source: domain.MediaSourceLark, ResourceID: "actual-image", Sequence: 0}}}
			model := &communityReadModel{tool: tool, args: `{"message_id":"newly-read"}`, prepare: func() error {
				if _, err := f.store.Accept(ctx, domain.Event{ID: m.EventID, Type: domain.EventMessage, Message: m, OccurredAt: now}); err != nil {
					return err
				}
				job, err := f.store.ClaimJob(ctx, []domain.JobKind{domain.JobMedia}, "media-worker", time.Minute)
				if err != nil {
					return err
				}
				return f.store.PutMedia(ctx, job, domain.MediaArtifact{Source: ref, Parts: []domain.MediaPart{{Object: domain.Object{Source: ref, ResourceID: "actual-image", Sequence: 0, Key: "never-show-secret-object-path"}, Text: "parsed actual image", Page: 1}}})
			}}
			if tool == "feishu_message_history" {
				model.args = `{}`
			}
			registry, err := atoms.New(atoms.Options{Sources: f.store, Tasks: f.store, Group: communitySourceGroup{store: f.store}})
			if err != nil {
				t.Fatal(err)
			}
			f.engine, err = New(Options{Store: f.store, Model: model, Registry: registry, ConversationOutput: true})
			if err != nil {
				t.Fatal(err)
			}
			if err = f.execute(ctx); err != nil {
				t.Fatal(err)
			}
			current, err := f.store.Task(ctx, f.task.Scope, f.task.ID)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, source := range current.ContextSources {
				found = found || source == ref
			}
			if !model.seen || !found {
				t.Fatalf("read source bypassed AddTurnSources: seen=%v refs=%+v", model.seen, current.ContextSources)
			}
		})
	}
}

type communityImageRefreshGroup struct {
	*lark.Client
	next   domain.Message
	writes int
}

type communityLocalImageEdit struct {
	*pgstore.Store
	next   domain.Message
	edited bool
}

func (s *communityLocalImageEdit) Message(ctx context.Context, scope domain.Scope, id string) (domain.Message, error) {
	if !s.edited && id == s.next.Source.MessageID {
		s.edited = true
		if _, err := s.Store.Accept(ctx, domain.Event{ID: s.next.EventID, Type: domain.EventMessage, OccurredAt: s.next.UpdatedAt, Message: s.next}); err != nil {
			return domain.Message{}, err
		}
	}
	return s.Store.Message(ctx, scope, id)
}

func (g *communityImageRefreshGroup) ReadMessage(context.Context, domain.Scope, string) (domain.Message, error) {
	return g.next, nil
}
func (g *communityImageRefreshGroup) SendImage(context.Context, domain.Scope, domain.Message, domain.Media, string, string) (string, error) {
	g.writes++
	return "sent", nil
}
func TestImageRefreshCannotWriteAfterInvalidatingTaskSource(t *testing.T) {
	f := newAutoEffectFixture(t, true, false)
	ctx := context.Background()
	task, err := f.store.StartTurn(ctx, f.job, domain.TurnRequest{TaskID: f.task.ID, Revision: f.task.Revision})
	if err != nil {
		t.Fatal(err)
	}
	m, err := f.store.Message(ctx, task.Scope, task.Root.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	m.Source.Version++
	m.EventID = "actual-root-edit"
	m.UpdatedAt = m.UpdatedAt.Add(time.Millisecond)
	m.Media = []domain.Media{{Type: domain.MediaImage, Source: domain.MediaSourceLark, ResourceID: "image", Sequence: 0}}
	group := &communityImageRefreshGroup{next: m}
	// A local source can be edited while the tool is reading. The local-first
	// policy deliberately does not refresh an already captured message remotely.
	local := &communityLocalImageEdit{Store: f.store, next: m}
	reg, err := atoms.New(atoms.Options{Sources: local, Tasks: f.store, Group: group})
	if err != nil {
		t.Fatal(err)
	}
	var image atoms.Definition
	for _, d := range reg.Definitions() {
		if d.Name == "feishu_send_image" {
			image = d
		}
	}
	_, err = image.Run(ctx, atoms.Invocation{Task: task, Job: f.job, ExecutionKey: "image"}, `{"message_id":"request","sequence":0}`)
	if !errors.Is(err, domain.ErrStaleSource) || group.writes != 0 {
		t.Fatalf("refresh invalidated task but image write occurred: writes=%d error=%v", group.writes, err)
	}
}
