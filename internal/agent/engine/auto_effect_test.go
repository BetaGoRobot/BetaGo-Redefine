package engine

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/atoms"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/modelapi"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/pgstore"
	_ "github.com/lib/pq"
)

// The scripted provider exercises the real native Eino loop. Only the model
// and external business service are local fixtures; authority lives in real PG.
type autoEffectModel struct {
	args         []string
	mu           sync.Mutex
	observations []string
}

func (m *autoEffectModel) Generate(_ context.Context, r modelapi.ChatRequest) (modelapi.ChatResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var observations []string
	for _, msg := range r.Messages {
		if msg.Role == "tool" {
			content, ok := msg.Content.(string)
			if !ok {
				return modelapi.ChatResponse{}, errors.New("tool observation is not text")
			}
			observations = append(observations, content)
		}
	}
	m.observations = observations
	msg := modelapi.ChatMessage{Role: "assistant", Content: "已收到工具的执行结果。"}
	if len(observations) < len(m.args) {
		msg.Content = ""
		msg.ToolCalls = []modelapi.ToolCall{{ID: fmt.Sprintf("action_%d", len(observations)), Type: "function", Function: modelapi.ToolFunction{Name: "ordinary_action", Arguments: m.args[len(observations)]}}}
	}
	return modelapi.ChatResponse{Message: msg}, nil
}

type autoEffectFixture struct {
	store      *pgstore.Store
	db         *sql.DB
	schema     string
	task       domain.Task
	job        domain.Job
	engine     *Engine
	definition atoms.Definition
	model      *autoEffectModel
	calls      atomic.Int32
	actors     chan string
}

func newAutoEffectFixture(t *testing.T, auto, fail bool, args ...string) *autoEffectFixture {
	t.Helper()
	dsn := os.Getenv("AGENT_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("real isolated PostgreSQL required")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal("isolated PostgreSQL fixture unavailable")
	}
	var nonce [8]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	f := &autoEffectFixture{db: db, schema: "auto_effect_" + hex.EncodeToString(nonce[:]), actors: make(chan string, 16), model: &autoEffectModel{args: args}}
	f.store, err = pgstore.New(db, f.schema)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Exec(`DROP SCHEMA IF EXISTS "` + f.schema + `" CASCADE`); db.Close() })
	if err = f.store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.calls.Add(1)
		var body struct {
			Context struct {
				Actor string `json:"actor_id"`
			} `json:"agent_context"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		f.actors <- body.Context.Actor
		if r.Header.Get("Idempotency-Key") == "" {
			t.Error("missing effect idempotency key")
		}
		if fail {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"receipt":"completed"}`))
	}))
	t.Cleanup(api.Close)
	// JSON keeps this regression runnable against the pre-classification code:
	// the old registry ignores AutoExecute and incorrectly freezes an approval.
	var config atoms.APIConfig
	raw, _ := json.Marshal(map[string]any{"Name": "ordinary_action", "URL": api.URL, "Method": "POST", "SideEffect": true, "AutoExecute": auto, "Parameters": map[string]any{"type": "object", "properties": map[string]any{"amount": map[string]string{"type": "integer"}}, "required": []string{"amount"}, "additionalProperties": false}})
	if err = json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	registry, err := atoms.New(atoms.Options{APIs: []atoms.APIConfig{config}})
	if err != nil {
		t.Fatal(err)
	}
	for _, definition := range registry.Definitions() {
		if definition.Name == "ordinary_action" {
			f.definition = definition
		}
	}
	f.engine, err = New(Options{Store: f.store, Model: f.model, Registry: registry})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(-time.Second)
	scope := domain.Scope{TenantID: "tenant", AppID: "app", ChatID: "chat", Visibility: "group"}
	m := domain.Message{Source: domain.SourceRef{Scope: scope, MessageID: "request", AuthorID: "backend-actor", Version: 1, OccurredAt: now}, EventID: "request", Text: "do the ordinary action", UpdatedAt: now}
	if _, err = f.store.Accept(context.Background(), domain.Event{ID: m.EventID, Type: domain.EventMessage, Message: m, OccurredAt: now}); err != nil {
		t.Fatal(err)
	}
	f.task, err = f.store.CreateTask(context.Background(), m, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	f.job, err = f.store.ClaimJob(context.Background(), []domain.JobKind{domain.JobAgentTurn}, "auto-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *autoEffectFixture) execute(ctx context.Context) error {
	return f.engine.Execute(ctx, f.job, domain.TurnRequest{TaskID: f.task.ID, Revision: f.task.Revision})
}

func TestNativeAutomaticEffectExecutesWithoutApprovalAndReusesCompletedReceipt(t *testing.T) {
	f := newAutoEffectFixture(t, true, false, `{"amount":1}`, `{"amount":1}`)
	if err := f.execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	approvals, err := f.store.Approvals(context.Background(), f.task.Scope, f.task.ID)
	if err != nil || len(approvals) != 0 || f.calls.Load() != 1 {
		t.Fatalf("automatic effect did not execute once without approval: calls=%d approvals=%d error=%v", f.calls.Load(), len(approvals), err)
	}
	x, err := f.store.ToolExecution(context.Background(), f.task.Scope, f.task.ID, domain.StableID(f.task.ID, "ordinary_action", `{"amount":1}`))
	if err != nil || x.Status != "completed" || x.ApprovalID != "" || x.ActorID != "backend-actor" || x.Result != `{"receipt":"completed"}` {
		t.Fatalf("incorrect effect receipt: %+v %v", x, err)
	}
	if actor := <-f.actors; actor != "backend-actor" {
		t.Fatalf("external actor=%s", actor)
	}
	if len(f.model.observations) != 2 || f.model.observations[0] != f.model.observations[1] {
		t.Fatal("native tool loop did not reuse the completed receipt")
	}
}

func TestNativeAutomaticUnknownEffectReturnsStaticObservationAndNeverReplays(t *testing.T) {
	f := newAutoEffectFixture(t, true, true, `{"amount":1}`, `{"amount":1}`, `{"amount":2}`)
	if err := f.execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.calls.Load() != 1 || len(f.model.observations) != 3 {
		t.Fatalf("uncertain action replayed or aborted model explanation: calls=%d observations=%v", f.calls.Load(), f.model.observations)
	}
	for _, observation := range f.model.observations {
		if observation != `{"status":"unknown","replay_blocked":true}` {
			t.Fatalf("unknown observation must be static and structured: %s", observation)
		}
	}
	x, err := f.store.ToolExecution(context.Background(), f.task.Scope, f.task.ID, domain.StableID(f.task.ID, "ordinary_action", `{"amount":1}`))
	if err != nil || x.Status != "unknown" {
		t.Fatalf("uncertain receipt missing: %+v %v", x, err)
	}
	approvals, err := f.store.Approvals(context.Background(), f.task.Scope, f.task.ID)
	if err != nil || len(approvals) != 0 {
		t.Fatalf("unknown automatic action froze approval: %v %v", approvals, err)
	}
}

func TestNativeDefaultEffectStillInterruptsForExactApproval(t *testing.T) {
	f := newAutoEffectFixture(t, false, false, `{"amount":1}`)
	if err := f.execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	approvals, err := f.store.Approvals(context.Background(), f.task.Scope, f.task.ID)
	if err != nil || len(approvals) != 1 || approvals[0].InterruptID == "" || approvals[0].Status != "pending" || f.calls.Load() != 0 {
		t.Fatalf("high/unknown action bypassed native approval: %v %v calls=%d", approvals, err, f.calls.Load())
	}
}

func TestNativeAutomaticPolicyRevocationAndMismatchRejectBeforeCall(t *testing.T) {
	for _, mutation := range []string{"enabled=false", "policy_digest='changed'", "side_effect=false", "auto_execute=false"} {
		t.Run(mutation, func(t *testing.T) {
			f := newAutoEffectFixture(t, true, false, `{"amount":1}`)
			if _, err := f.db.Exec(`UPDATE "` + f.schema + `".tool_policies SET ` + mutation); err != nil {
				t.Fatal(err)
			}
			if err := f.execute(context.Background()); !errors.Is(err, domain.ErrUnauthorized) {
				t.Fatalf("changed policy did not reject: %v", err)
			}
			if f.calls.Load() != 0 {
				t.Fatal("revoked/mismatched policy executed")
			}
		})
	}
}

func TestAutomaticCallFailuresKeepGuardAndCancellationErrors(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded, domain.ErrInvalid, domain.ErrUnauthorized, domain.ErrStaleSource, domain.ErrStaleClaim, domain.ErrConflict, domain.ErrTaskBusy} {
		t.Run(cause.Error(), func(t *testing.T) {
			f := newAutoEffectFixture(t, true, false)
			d := f.definition
			d.Run = func(context.Context, atoms.Invocation, string) (string, error) { return "", cause }
			b := &boundTool{definition: d, store: f.store, job: f.job, task: f.task}
			if _, err := b.InvokableRun(context.Background(), `{"amount":1}`); !errors.Is(err, cause) {
				t.Fatalf("guard failure swallowed as observation: %v", err)
			}
		})
	}
}
