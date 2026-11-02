package service_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/atoms"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/engine"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/modelapi"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/pgstore"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/service"
	_ "github.com/lib/pq"
	"go.opentelemetry.io/otel/codes"
)

type sent struct{ kind, content, uuid string }
type messenger struct {
	mu        sync.Mutex
	sends     []sent
	failFinal bool
}

func (m *messenger) Send(_ context.Context, _ domain.Scope, _ string, kind, content, uuid string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sends = append(m.sends, sent{kind, content, uuid})
	if kind == "text" && m.failFinal {
		m.failFinal = false
		return "", errors.New("receipt transport failed")
	}
	return "out_" + uuid, nil
}

type fixture struct {
	store                 *pgstore.Store
	db                    *sql.DB
	schema                string
	svc                   *service.Service
	messenger             *messenger
	modelCalls, toolCalls *atomic.Int32
	scope                 domain.Scope
	modelPrompts          chan string
	eng                   *engine.Engine
}

func setup(t *testing.T) fixture { return setupWithHook(t, nil) }
func setupWithHook(t *testing.T, hook func(), refresh ...func(context.Context, domain.SourceRef) error) fixture {
	var sourceRefresh func(context.Context, domain.SourceRef) error
	if len(refresh) > 0 {
		sourceRefresh = refresh[0]
	}
	return setupRuntime(t, hook, sourceRefresh, nil)
}
func setupRuntime(t *testing.T, hook func(), sourceRefresh func(context.Context, domain.SourceRef) error, resolve func(context.Context, domain.Scope, string) error) fixture {
	t.Helper()
	dsn := os.Getenv("AGENT_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("isolated PostgreSQL fixture required")
	}
	db, e := sql.Open("postgres", dsn)
	if e != nil {
		t.Fatal(e)
	}
	var nonce [8]byte
	rand.Read(nonce[:])
	schema := "runtime_test_" + hex.EncodeToString(nonce[:])
	store, e := pgstore.New(db, schema)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Exec(`DROP SCHEMA IF EXISTS "` + schema + `" CASCADE`); db.Close() })
	if e = store.Migrate(context.Background()); e != nil {
		t.Fatal(e)
	}
	prompts := make(chan string, 128)
	modelCalls := &atomic.Int32{}
	toolCalls := &atomic.Int32{}
	modelHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		modelCalls.Add(1)
		var input struct {
			Messages []modelapi.ChatMessage    `json:"messages"`
			Tools    []modelapi.ToolDefinition `json:"tools"`
		}
		if json.NewDecoder(r.Body).Decode(&input) != nil {
			t.Error("invalid provider request")
			w.WriteHeader(400)
			return
		}
		encoded, _ := json.Marshal(input)
		prompts <- string(encoded)
		hasTool := false
		for _, m := range input.Messages {
			hasTool = hasTool || m.Role == "tool"
		}
		message := modelapi.ChatMessage{Role: "assistant", Content: "执行完成"}
		if !hasTool {
			message = modelapi.ChatMessage{Role: "assistant", ToolCalls: []modelapi.ToolCall{{ID: "call_write", Type: "function", Function: modelapi.ToolFunction{Name: "business_write", Arguments: `{"amount":1}`}}}}
		}
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": message, "finish_reason": "stop"}}})
	}))
	t.Cleanup(modelHTTP.Close)
	apiHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		toolCalls.Add(1)
		if hook != nil {
			hook()
		}
		var body map[string]json.RawMessage
		if json.NewDecoder(r.Body).Decode(&body) != nil || body["agent_context"] == nil || body["arguments"] == nil {
			t.Error("missing server identity envelope")
		}
		w.Write([]byte(`{"receipt":"ok"}`))
	}))
	t.Cleanup(apiHTTP.Close)
	chat, e := modelapi.NewChat(modelapi.Endpoint{BaseURL: modelHTTP.URL, APIKey: "fixture", Model: "scripted", Timeout: 5 * time.Second})
	if e != nil {
		t.Fatal(e)
	}
	messenger := &messenger{}
	reg, e := atoms.New(atoms.Options{Tasks: store, Sources: store, Messenger: messenger, APIs: []atoms.APIConfig{{Name: "business_write", URL: apiHTTP.URL, Method: "POST", Parameters: json.RawMessage(`{"type":"object","properties":{"amount":{"type":"integer"}},"required":["amount"],"additionalProperties":false}`)}}})
	if e != nil {
		t.Fatal(e)
	}
	opts := engine.Options{Store: store, Model: chat, Registry: reg, RefreshSource: sourceRefresh, ResolveMessage: resolve}
	eng, e := engine.New(opts)
	if e != nil {
		t.Fatal(e)
	}
	svc, e := service.New(service.Options{Store: store, Engine: eng, Messenger: messenger, Owner: "fixture", Lease: time.Minute, Poll: time.Millisecond, TaskTimeout: time.Hour, Concurrency: 2, MaxConcurrentTasks: 2})
	if e != nil {
		t.Fatal(e)
	}
	return fixture{store: store, db: db, schema: schema, svc: svc, messenger: messenger, modelCalls: modelCalls, toolCalls: toolCalls, scope: domain.Scope{TenantID: "t", AppID: "a", ChatID: "c", Visibility: "group"}, modelPrompts: prompts, eng: eng}
}
func (f fixture) event(t *testing.T, id, actor, text, parent string, meta domain.IngressMetadata) domain.Message {
	t.Helper()
	now := time.Now().UTC().Add(-time.Second)
	raw, _ := json.Marshal(meta)
	m := domain.Message{Source: domain.SourceRef{Scope: f.scope, MessageID: id, AuthorID: actor, Version: 1, OccurredAt: now}, EventID: id, Text: text, ParentID: parent, Raw: raw, UpdatedAt: now}
	if _, e := f.store.Accept(context.Background(), domain.Event{ID: id, Message: m, Type: domain.EventMessage, OccurredAt: now}); e != nil {
		t.Fatal(e)
	}
	return m
}
func step(t *testing.T, f fixture, kind domain.JobKind) {
	t.Helper()
	if e := f.svc.Step(context.Background(), kind); e != nil {
		t.Fatal(e)
	}
}
func TestNativeRunnerCheckpointApprovalResumeAndIndependentDeliveryRetry(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	f.event(t, "ordinary", "other", "普通成员聊天", "", domain.IngressMetadata{Kind: "message", SenderType: "user"})
	step(t, f, domain.JobRoute)
	if f.modelCalls.Load() != 0 {
		t.Fatal("ordinary group chat called model")
	}
	m := f.event(t, "request", "owner", "@bot 执行", "", domain.IngressMetadata{Kind: "message", SenderType: "user", Addressed: true})
	duplicate, duplicateErr := f.store.Accept(ctx, domain.Event{ID: m.EventID, Type: domain.EventMessage, Message: m, OccurredAt: m.Source.OccurredAt})
	if duplicateErr != nil || !duplicate.Duplicate || len(duplicate.JobIDs) != 0 {
		t.Fatalf("duplicate event produced runtime work: %+v %v", duplicate, duplicateErr)
	}
	step(t, f, domain.JobRoute)
	task, _, e := f.store.TaskForMessage(ctx, f.scope, m.Source.MessageID)
	if e != nil {
		t.Fatal(e)
	}
	step(t, f, domain.JobAgentTurn)
	if f.toolCalls.Load() != 0 {
		t.Fatal("effect ran before approval")
	}
	as, e := f.store.Approvals(ctx, f.scope, task.ID)
	if e != nil || len(as) != 1 || as[0].InterruptID == "" {
		t.Fatalf("no native interrupt: %+v %v", as, e)
	}
	var checkpoints int
	if e = f.db.QueryRow(`SELECT count(*) FROM "` + f.schema + `".checkpoints`).Scan(&checkpoints); e != nil || checkpoints < 1 {
		t.Fatalf("native checkpoints missing: %d %v", checkpoints, e)
	}
	step(t, f, domain.JobDelivery)
	c := domain.ControlAction{Action: "approve", TaskID: task.ID, Revision: task.Revision, ApprovalID: as[0].ID}
	f.event(t, "unauthorized", "other", "", "", domain.IngressMetadata{Kind: "control", SenderType: "user", Control: &c})
	step(t, f, domain.JobRoute)
	if f.toolCalls.Load() != 0 {
		t.Fatal("nonowner approved")
	}
	f.event(t, "approved", "owner", "", "", domain.IngressMetadata{Kind: "control", SenderType: "user", Control: &c})
	step(t, f, domain.JobRoute)
	step(t, f, domain.JobAgentTurn)
	if f.toolCalls.Load() != 1 || f.modelCalls.Load() != 2 {
		t.Fatalf("native resume reran planning or effect: tools=%d model=%d", f.toolCalls.Load(), f.modelCalls.Load())
	}
	f.messenger.failFinal = true
	if e = f.svc.Step(ctx, domain.JobDelivery); e == nil {
		t.Fatal("receipt failure not returned")
	}
	if _, e = f.db.Exec(`UPDATE "` + f.schema + `".jobs SET available_at=clock_timestamp() WHERE kind='delivery' AND status='pending'`); e != nil {
		t.Fatal(e)
	}
	step(t, f, domain.JobDelivery)
	if f.toolCalls.Load() != 1 || f.modelCalls.Load() != 2 {
		t.Fatal("delivery retry reran agent")
	}
	f.messenger.mu.Lock()
	sends := append([]sent(nil), f.messenger.sends...)
	f.messenger.mu.Unlock()
	if len(sends) != 3 || sends[1].uuid != sends[2].uuid || sends[1].content != sends[2].content {
		t.Fatalf("delivery not frozen: %+v", sends)
	}
	task, e = f.store.Task(ctx, f.scope, task.ID)
	if e != nil || task.Status != "completed" {
		t.Fatalf("task not completed: %+v %v", task, e)
	}
}

func TestCompletedToolReceiptReusedWhenFinalModelCallFails(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	m := f.event(t, "reuse_request", "owner", "@bot do it", "", domain.IngressMetadata{Kind: "message", SenderType: "user", Addressed: true})
	step(t, f, domain.JobRoute)
	task, _, _ := f.store.TaskForMessage(ctx, f.scope, m.Source.MessageID)
	step(t, f, domain.JobAgentTurn)
	as, _ := f.store.Approvals(ctx, f.scope, task.ID)
	_, e := f.store.ControlTask(ctx, f.scope, "owner", domain.ControlAction{Action: "approve", TaskID: task.ID, Revision: 1, ApprovalID: as[0].ID}, domain.SourceRef{}, "")
	if e != nil {
		t.Fatal(e)
	}
	// Force final publication to fail after native resume has persisted the tool
	// result. The retry must consume the existing native checkpoint and ledger.
	if _, e = f.db.Exec(`ALTER TABLE "` + f.schema + `".deliveries ADD CONSTRAINT reject_final CHECK (delivery_id='never')`); e == nil {
		t.Fatal("constraint should reject existing proposal row")
	}
	if _, e = f.db.Exec(`ALTER TABLE "` + f.schema + `".deliveries ADD CONSTRAINT reject_final CHECK (status<>'pending' OR convert_from(body,'UTF8') NOT LIKE '%"Kind":"final"%') NOT VALID`); e != nil {
		t.Fatal(e)
	}
	if e = f.svc.Step(ctx, domain.JobAgentTurn); e == nil {
		t.Fatal("injected final outbox failure missing")
	}
	if f.toolCalls.Load() != 1 {
		t.Fatal("tool did not complete")
	}
	if _, e = f.db.Exec(`ALTER TABLE "` + f.schema + `".deliveries DROP CONSTRAINT reject_final`); e != nil {
		t.Fatal(e)
	}
	if _, e = f.db.Exec(`UPDATE "` + f.schema + `".jobs SET available_at=clock_timestamp() WHERE kind='agent_turn' AND status='pending'`); e != nil {
		t.Fatal(e)
	}
	step(t, f, domain.JobAgentTurn)
	if f.toolCalls.Load() != 1 {
		t.Fatal("completed write replayed")
	}
	task, e = f.store.Task(ctx, f.scope, task.ID)
	if e != nil || task.Status != "ready_delivery" {
		t.Fatalf("completed ledger could not resume: %+v %v", task, e)
	}
}

func TestOwnerFollowupRouteRetryDoesNotIncrementRevisionTwice(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	m := f.event(t, "root", "owner", "@bot request", "", domain.IngressMetadata{Kind: "message", SenderType: "user", Addressed: true})
	step(t, f, domain.JobRoute)
	task, _, _ := f.store.TaskForMessage(ctx, f.scope, m.Source.MessageID)
	f.event(t, "followup", "owner", "modified request", m.Source.MessageID, domain.IngressMetadata{Kind: "message", SenderType: "user"})
	step(t, f, domain.JobRoute)
	if _, e := f.db.Exec(`UPDATE "` + f.schema + `".jobs SET status='pending',available_at=clock_timestamp() WHERE kind='route' AND convert_from(payload,'UTF8') LIKE '%followup%'`); e != nil {
		t.Fatal(e)
	}
	step(t, f, domain.JobRoute)
	task, e := f.store.Task(ctx, f.scope, task.ID)
	if e != nil || task.Revision != 2 {
		t.Fatalf("route crash retry changed revision twice: %+v %v", task, e)
	}
}

func TestCancelDuringNativeRPCPersistsLateReceiptWithoutFinalDelivery(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	f := setupWithHook(t, func() { close(started); <-release })
	ctx := context.Background()
	m := f.event(t, "cancel_rpc", "owner", "@bot do it", "", domain.IngressMetadata{Kind: "message", SenderType: "user", Addressed: true})
	step(t, f, domain.JobRoute)
	task, _, _ := f.store.TaskForMessage(ctx, f.scope, m.Source.MessageID)
	step(t, f, domain.JobAgentTurn)
	as, _ := f.store.Approvals(ctx, f.scope, task.ID)
	if _, e := f.store.ControlTask(ctx, f.scope, "owner", domain.ControlAction{Action: "approve", TaskID: task.ID, Revision: 1, ApprovalID: as[0].ID}, domain.SourceRef{}, ""); e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { done <- f.svc.Step(ctx, domain.JobAgentTurn) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("RPC did not start")
	}
	if _, e := f.store.ControlTask(ctx, f.scope, "owner", domain.ControlAction{Action: "cancel", TaskID: task.ID, Revision: 1}, domain.SourceRef{}, ""); e != nil {
		t.Fatal(e)
	}
	close(release)
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	task, e := f.store.Task(ctx, f.scope, task.ID)
	if e != nil || task.Status != "canceled" {
		t.Fatalf("canceled task revived: %+v %v", task, e)
	}
	x, e := f.store.ToolExecution(ctx, f.scope, task.ID, domain.StableID(task.ID, "business_write", `{"amount":1}`))
	if e != nil || x.Status != "completed_late" || x.Result == "" {
		t.Fatalf("late receipt not retained: %+v %v", x, e)
	}
	var finals int
	if e = f.db.QueryRow(`SELECT count(*) FROM "` + f.schema + `".deliveries WHERE convert_from(body,'UTF8') LIKE '%"Kind":"final"%'`).Scan(&finals); e != nil || finals != 0 {
		t.Fatalf("late final outbox: %d %v", finals, e)
	}
}

func TestSameGroupTaskIsolationContributorAndDelegateAuthority(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	a := f.event(t, "task_a", "owner", "@bot A", "", domain.IngressMetadata{Kind: "message", SenderType: "user", Addressed: true})
	b := f.event(t, "task_b", "second", "@bot B", "", domain.IngressMetadata{Kind: "message", SenderType: "user", Addressed: true})
	step(t, f, domain.JobRoute)
	step(t, f, domain.JobRoute)
	ta, _, _ := f.store.TaskForMessage(ctx, f.scope, a.Source.MessageID)
	tb, _, _ := f.store.TaskForMessage(ctx, f.scope, b.Source.MessageID)
	if ta.ID == tb.ID {
		t.Fatal("tasks collapsed by chat")
	}
	f.event(t, "material", "contributor", "资料作者是我", a.Source.MessageID, domain.IngressMetadata{Kind: "message", SenderType: "user"})
	step(t, f, domain.JobRoute)
	ta, _ = f.store.Task(ctx, f.scope, ta.ID)
	if ta.OwnerID != "owner" || ta.RequesterID != "owner" || len(ta.Facts) != 1 || ta.Facts[0].Source.AuthorID != "contributor" {
		t.Fatalf("provenance or ownership collapsed: %+v", ta)
	}
	j, e := f.store.ClaimJob(ctx, []domain.JobKind{domain.JobAgentTurn}, "isolation", time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	f.store.CompleteJob(ctx, j) // obsolete A revision 1
	j, e = f.store.ClaimJob(ctx, []domain.JobKind{domain.JobAgentTurn}, "isolation", time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	f.store.CompleteJob(ctx, j) // independent B
	j, e = f.store.ClaimJob(ctx, []domain.JobKind{domain.JobAgentTurn}, "isolation", time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	approval, e := f.store.FreezeApproval(ctx, j, ta, "business_write", `{"amount":2}`)
	if e != nil {
		t.Fatal(e)
	}
	if e = f.store.BindInterrupt(ctx, j, ta, approval.ID, "native-bound"); e != nil {
		t.Fatal(e)
	}
	control := domain.ControlAction{Action: "approve", TaskID: ta.ID, Revision: ta.Revision, ApprovalID: approval.ID}
	if _, e = f.store.ControlTask(ctx, f.scope, "contributor", control, domain.SourceRef{}, ""); !errors.Is(e, domain.ErrUnauthorized) {
		t.Fatalf("material author approved: %v", e)
	}
	if _, e = f.store.ControlTask(ctx, f.scope, "owner", domain.ControlAction{Action: "delegate", TaskID: ta.ID, Revision: ta.Revision}, domain.SourceRef{}, "contributor"); e != nil {
		t.Fatal(e)
	}
	if _, e = f.store.ControlTask(ctx, f.scope, "contributor", control, domain.SourceRef{}, ""); e != nil {
		t.Fatalf("explicit delegate not authorized: %v", e)
	}
	if _, e = f.store.ControlTask(ctx, f.scope, "owner", domain.ControlAction{Action: "cancel", TaskID: ta.ID, Revision: ta.Revision}, domain.SourceRef{}, ""); e != nil {
		t.Fatal(e)
	}
	tb, e = f.store.Task(ctx, f.scope, tb.ID)
	if e != nil || tb.Status != "ready" || tb.Revision != 1 {
		t.Fatalf("cancel affected other task: %+v %v", tb, e)
	}
}

func TestTaskMediaWaitsDurablyForCurrentVersionArtifact(t *testing.T) {
	f := setup(t)
	recorder, logs := observeRuntime(t)
	ctx := context.Background()
	now := time.Now().UTC().Add(-time.Second)
	raw, _ := json.Marshal(domain.IngressMetadata{Kind: "message", SenderType: "user", Addressed: true})
	m := domain.Message{Source: domain.SourceRef{Scope: f.scope, MessageID: "image_request", AuthorID: "owner", Version: 1, OccurredAt: now}, EventID: "image_request", Raw: raw, UpdatedAt: now, Media: []domain.Media{{Type: domain.MediaImage, Source: domain.MediaSourceLark, ResourceID: "synthetic-image", Sequence: 0}}}
	if _, e := f.store.Accept(ctx, domain.Event{ID: m.EventID, Type: domain.EventMessage, Message: m, OccurredAt: now}); e != nil {
		t.Fatal(e)
	}
	step(t, f, domain.JobRoute)
	logs.Reset()
	for i := 0; i < 3; i++ {
		if _, err := f.db.Exec(`UPDATE "` + f.schema + `".jobs SET available_at=clock_timestamp() WHERE kind='agent_turn'`); err != nil {
			t.Fatal(err)
		}
		step(t, f, domain.JobAgentTurn)
	}
	if f.modelCalls.Load() != 0 {
		t.Fatal("model answered before image interpretation was ready")
	}
	if logs.Len() != 0 {
		t.Fatalf("repeated media polls produced INFO/ERROR noise: %s", logs.String())
	}
	deferred := 0
	for _, span := range recorder.Ended() {
		if span.Status().Code == codes.Error {
			t.Fatalf("expected media wait marked %s as failed", span.Name())
		}
		for _, attr := range span.Attributes() {
			if string(attr.Key) == "defer.reason" && attr.Value.AsString() == "media_not_ready" {
				deferred++
			}
		}
	}
	if deferred < 3 {
		t.Fatal("quiet media waits lost trace-level deferral evidence")
	}
	var attempts int
	var status string
	if e := f.db.QueryRow(`SELECT attempt,status FROM "`+f.schema+`".jobs WHERE kind='agent_turn'`).Scan(&attempts, &status); e != nil || attempts != 0 || status != "pending" {
		t.Fatalf("media wait burned attempt: %d %s %v", attempts, status, e)
	}
	mediaJob, e := f.store.ClaimJob(ctx, []domain.JobKind{domain.JobMedia}, "media", time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	if e = f.store.PutMedia(ctx, mediaJob, domain.MediaArtifact{Source: m.Source, Parts: []domain.MediaPart{{Object: domain.Object{Source: m.Source, ResourceID: "synthetic-image", Sequence: 0, Key: "private-fixture-object"}, Text: "DERIVED OCR synthetic invoice 427", Page: 3, Start: time.Second, End: 2 * time.Second}}}); e != nil {
		t.Fatal(e)
	}
	f.store.CompleteJob(ctx, mediaJob)
	f.db.Exec(`UPDATE "` + f.schema + `".jobs SET available_at=clock_timestamp() WHERE kind='agent_turn'`)
	step(t, f, domain.JobAgentTurn)
	if f.modelCalls.Load() != 1 {
		t.Fatal("ready artifact did not release durable turn")
	}
	step(t, f, domain.JobDelivery)
	for _, event := range []string{"agent.model.request", "agent.tool.invoke.started", "agent.delivery.queued", "agent.delivery.receipt"} {
		if !strings.Contains(logs.String(), event) {
			t.Fatalf("quiet lifecycle suppressed actual business event %s", event)
		}
	}
	prompt := <-f.modelPrompts
	var request struct{ Messages []modelapi.ChatMessage }
	if err := json.Unmarshal([]byte(prompt), &request); err != nil {
		t.Fatal(err)
	}
	var scene struct {
		CurrentUtterance struct{ MessageID, SpeakerRef, SpeakerName string }
		Participants     []struct{ ID, MemberRef, Name, NameOrigin string }
		DerivedMedia     []struct {
			MessageID, Interpretation string
			Parts                     []struct {
				ResourceID, Text string
				Sequence, Page   int
				Start, End       time.Duration
			}
		}
	}
	foundScene := false
	for _, message := range request.Messages {
		if message.Role != "user" {
			continue
		}
		content, ok := message.Content.(string)
		if !ok {
			t.Fatal("media scene is not readable text")
		}
		if err := json.Unmarshal([]byte(content), &scene); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{`"Source"`, `"Scope"`, `"Version"`, "private-fixture-object"} {
			if strings.Contains(content, key) {
				t.Fatalf("private media proof leaked into reading scene: %s", key)
			}
		}
		foundScene = true
	}
	current := scene.CurrentUtterance
	if !foundScene || current.MessageID != m.Source.MessageID || current.SpeakerRef == "" || current.SpeakerName == "" {
		t.Fatalf("media scene lost actual current speaker: %+v", current)
	}
	authorFound := false
	for _, person := range scene.Participants {
		if person.ID == m.Source.AuthorID && person.MemberRef == current.SpeakerRef && person.Name == current.SpeakerName && person.NameOrigin == "unknown" {
			authorFound = true
		}
	}
	if !authorFound {
		t.Fatal("media author was not mapped by exact ID to stable unknown display")
	}
	if len(scene.DerivedMedia) != 1 || scene.DerivedMedia[0].MessageID != m.Source.MessageID || !strings.Contains(scene.DerivedMedia[0].Interpretation, "DERIVED media interpretation") || len(scene.DerivedMedia[0].Parts) != 1 {
		t.Fatalf("media interpretation lost message relationship: %+v", scene.DerivedMedia)
	}
	part := scene.DerivedMedia[0].Parts[0]
	if part.ResourceID != "synthetic-image" || part.Sequence != 0 || part.Text != "DERIVED OCR synthetic invoice 427" || part.Page != 3 || part.Start != time.Second || part.End != 2*time.Second {
		t.Fatalf("media reading view lost source locator/text/segments: %+v", part)
	}
	task, _, err := f.store.TaskForMessage(ctx, f.scope, m.Source.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	pinned := false
	for _, ref := range task.ContextSources {
		pinned = pinned || ref == m.Source
	}
	if !pinned || task.Root != m.Source {
		t.Fatal("media display separation dropped exact backend source proof")
	}
	artifact, err := f.store.Media(ctx, m.Source)
	if err != nil || artifact.Source != m.Source || len(artifact.Parts) != 1 || artifact.Parts[0].Object.Source != m.Source {
		t.Fatal("media artifact no longer retains exact private source proof")
	}

}

func TestProviderRefreshBeforeResumeRevokesOldRevisionAndReplansExistingTask(t *testing.T) {
	var f fixture
	var refresh atomic.Bool
	f = setupWithHook(t, nil, func(ctx context.Context, ref domain.SourceRef) error {
		if !refresh.Load() || ref.MessageID != "refresh_request" {
			return nil
		}
		m, e := f.store.Message(ctx, ref.Scope, ref.MessageID)
		if e != nil {
			return e
		}
		if m.Source.Version > 1 {
			return nil
		}
		m.Source.Version = 2
		m.Text = "edited provider request"
		m.UpdatedAt = m.UpdatedAt.Add(time.Second)
		m.EventID = "provider_refresh"
		m.Raw, _ = json.Marshal(domain.IngressMetadata{Kind: "refresh", SenderType: "user"})
		_, e = f.store.Accept(ctx, domain.Event{ID: m.EventID, Type: domain.EventMessage, Message: m, OccurredAt: time.Now().UTC()})
		return e
	})
	ctx := context.Background()
	m := f.event(t, "refresh_request", "owner", "@bot original", "", domain.IngressMetadata{Kind: "message", SenderType: "user", Addressed: true})
	step(t, f, domain.JobRoute)
	task, _, _ := f.store.TaskForMessage(ctx, f.scope, m.Source.MessageID)
	step(t, f, domain.JobAgentTurn)
	as, _ := f.store.Approvals(ctx, f.scope, task.ID)
	if _, e := f.store.ControlTask(ctx, f.scope, "owner", domain.ControlAction{Action: "approve", TaskID: task.ID, Revision: 1, ApprovalID: as[0].ID}, domain.SourceRef{}, ""); e != nil {
		t.Fatal(e)
	}
	refresh.Store(true)
	step(t, f, domain.JobAgentTurn)
	if f.toolCalls.Load() != 0 {
		t.Fatal("provider edited request executed stale action")
	}
	step(t, f, domain.JobRoute)
	task, e := f.store.Task(ctx, f.scope, task.ID)
	if e != nil || task.Revision != 2 || task.Request != "edited provider request" {
		t.Fatalf("refresh did not replan existing task: %+v %v", task, e)
	}
	if _, e = f.store.ControlTask(ctx, f.scope, "owner", domain.ControlAction{Action: "cancel", TaskID: task.ID, Revision: 1}, domain.SourceRef{}, ""); !errors.Is(e, domain.ErrConflict) {
		t.Fatalf("stale cancel executed: %v", e)
	}
}

func TestNativeResumeNewFenceBlocksInflightEffectAndReusesLateKnownReceipt(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	f := setupWithHook(t, func() { close(started); <-release })
	ctx := context.Background()
	m := f.event(t, "lease_request", "owner", "@bot do it", "", domain.IngressMetadata{Kind: "message", SenderType: "user", Addressed: true})
	step(t, f, domain.JobRoute)
	task, _, _ := f.store.TaskForMessage(ctx, f.scope, m.Source.MessageID)
	step(t, f, domain.JobAgentTurn)
	as, _ := f.store.Approvals(ctx, f.scope, task.ID)
	control := domain.ControlAction{Action: "approve", TaskID: task.ID, Revision: 1, ApprovalID: as[0].ID}
	if _, e := f.store.ControlTask(ctx, f.scope, "owner", control, domain.SourceRef{}, ""); e != nil {
		t.Fatal(e)
	}
	j, e := f.store.ClaimJob(ctx, []domain.JobKind{domain.JobAgentTurn}, "old", time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	r := domain.TurnRequest{TaskID: task.ID, Revision: 1, ApprovalID: as[0].ID}
	done := make(chan error, 1)
	go func() { done <- f.eng.Execute(ctx, j, r) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("RPC not started")
	}
	if _, e = f.db.Exec(`UPDATE "`+f.schema+`".jobs SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, j.ID); e != nil {
		t.Fatal(e)
	}
	newClaim, e := f.store.ClaimJob(ctx, []domain.JobKind{domain.JobAgentTurn}, "new", time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	if newClaim.Fence <= j.Fence {
		t.Fatal("fence did not advance")
	}
	if e = f.eng.Execute(ctx, newClaim, r); !errors.Is(e, domain.ErrEffectUnknown) {
		t.Fatalf("new fence replayed inflight write: %v", e)
	}
	if f.toolCalls.Load() != 1 {
		t.Fatal("effect repeated under new fence")
	}
	close(release)
	if e = <-done; !errors.Is(e, domain.ErrStaleClaim) {
		t.Fatalf("old fence advanced after RPC: %v", e)
	}
	if e = f.eng.Execute(ctx, newClaim, r); e != nil {
		t.Fatalf("known persisted receipt could not resume: %v", e)
	}
	if f.toolCalls.Load() != 1 {
		t.Fatal("known receipt replayed effect")
	}
}

func TestFailedTaskMediaProducesControlledNoticeWithoutModel(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	now := time.Now().UTC().Add(-time.Second)
	raw, _ := json.Marshal(domain.IngressMetadata{Kind: "message", SenderType: "user", Addressed: true})
	m := domain.Message{Source: domain.SourceRef{Scope: f.scope, MessageID: "failed_media", AuthorID: "owner", Version: 1, OccurredAt: now}, EventID: "failed_media", Raw: raw, UpdatedAt: now, Media: []domain.Media{{Type: domain.MediaAudio, ResourceID: "synthetic-audio"}}}
	if _, e := f.store.Accept(ctx, domain.Event{ID: m.EventID, Type: domain.EventMessage, Message: m, OccurredAt: now}); e != nil {
		t.Fatal(e)
	}
	if _, e := f.db.Exec(`UPDATE "` + f.schema + `".jobs SET status='failed' WHERE kind='media'`); e != nil {
		t.Fatal(e)
	}
	step(t, f, domain.JobRoute)
	step(t, f, domain.JobAgentTurn)
	if f.modelCalls.Load() != 0 || f.toolCalls.Load() != 0 {
		t.Fatal("failed modality called model/effect")
	}
	step(t, f, domain.JobDelivery)
	f.messenger.mu.Lock()
	defer f.messenger.mu.Unlock()
	if len(f.messenger.sends) != 1 || !strings.Contains(f.messenger.sends[0].content, "媒体解析失败") {
		t.Fatalf("failed media hidden: %+v", f.messenger.sends)
	}
}

func TestReplyToCompletedBotDeliveryCreatesNewIndependentTask(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	root := f.event(t, "completed_root", "owner", "@bot initial", "", domain.IngressMetadata{Kind: "message", SenderType: "user", Addressed: true})
	step(t, f, domain.JobRoute)
	task, _, _ := f.store.TaskForMessage(ctx, f.scope, root.Source.MessageID)
	j, e := f.store.ClaimJob(ctx, []domain.JobKind{domain.JobAgentTurn}, "complete", time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	if e = f.store.QueueFinalDelivery(ctx, j, task, "finished"); e != nil {
		t.Fatal(e)
	}
	f.store.CompleteJob(ctx, j)
	step(t, f, domain.JobDelivery)
	f.messenger.mu.Lock()
	parent := "out_" + f.messenger.sends[0].uuid
	f.messenger.mu.Unlock()
	follow := f.event(t, "follow_new", "new-requester", "next question", parent, domain.IngressMetadata{Kind: "message", SenderType: "user"})
	step(t, f, domain.JobRoute)
	next, _, e := f.store.TaskForMessage(ctx, f.scope, follow.Source.MessageID)
	if e != nil || next.ID == task.ID || next.OwnerID != "new-requester" || next.RequesterID != "new-requester" {
		t.Fatalf("completed task swallowed new reply: %+v %v", next, e)
	}
}
