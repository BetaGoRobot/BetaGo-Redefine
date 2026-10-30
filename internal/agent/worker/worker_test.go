package worker

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/pgstore"
	_ "github.com/lib/pq"
)

type testIndex struct {
	mu             sync.Mutex
	messages       map[string]domain.Message
	contexts       map[string]domain.Context
	block, release chan struct{}
}

func (i *testIndex) UpsertMessage(ctx context.Context, m domain.Message) error {
	if i.block != nil {
		close(i.block)
		select {
		case <-i.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	i.messages[m.Source.MessageID] = m
	return nil
}
func (i *testIndex) DeleteMessage(_ context.Context, _ domain.Scope, id string) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	delete(i.messages, id)
	return nil
}
func (i *testIndex) UpsertContext(_ context.Context, c domain.Context, _ []float32) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.contexts[c.ID] = c
	return nil
}
func (i *testIndex) DeleteContext(_ context.Context, _ domain.Scope, id string) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	delete(i.contexts, id)
	return nil
}

type embedding struct{ err error }

func (e embedding) Embed(context.Context, []string) ([][]float32, error) {
	if e.err != nil {
		return nil, e.err
	}
	return [][]float32{{1, 0}}, nil
}

type memory struct {
	calls      int
	deletes    int
	deleteFail bool
	fail       bool
	wait       time.Duration
}

func (m *memory) Retain(ctx context.Context, c domain.Context) error {
	if m.wait > 0 {
		select {
		case <-time.After(m.wait):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	m.calls++
	if m.fail {
		return errors.New("memory unavailable")
	}
	return nil
}
func (m *memory) DeleteDocument(context.Context, domain.Scope, string) error {
	m.deletes++
	if m.deleteFail {
		m.deleteFail = false
		return errors.New("cleanup unavailable")
	}
	return nil
}
func fixture(t *testing.T) (*pgstore.Store, *testIndex, *sql.DB, string) {
	t.Helper()
	dsn := os.Getenv("AGENT_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("isolated PG required")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("agent_worker_%d", time.Now().UnixNano())
	s, err := pgstore.New(db, schema)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Exec(`DROP SCHEMA "` + schema + `" CASCADE`); db.Close() })
	return s, &testIndex{messages: map[string]domain.Message{}, contexts: map[string]domain.Context{}}, db, schema
}

var scope = domain.Scope{TenantID: "t", AppID: "a", ChatID: "c", Visibility: "group"}

func event(id string, version int64) domain.Event {
	at := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	eid := fmt.Sprintf("%s-%d", id, version)
	return domain.Event{ID: eid, Type: domain.EventMessage, OccurredAt: at.Add(time.Duration(version) * time.Second), Message: domain.Message{Source: domain.SourceRef{Scope: scope, MessageID: id, Version: version, AuthorID: "author-" + id, OccurredAt: at}, EventID: eid, Text: "canonical " + id, UpdatedAt: at.Add(time.Duration(version) * time.Second), Raw: json.RawMessage(`{}`)}}
}
func TestRawIndexSurvivesModelFailureAndMemoryWaitsForContexts(t *testing.T) {
	s, index, _, _ := fixture(t)
	ctx := context.Background()
	s.Accept(ctx, event("one", 1))
	mem := &memory{}
	w, err := New(Config{Store: s, Index: index, Memory: mem, Embedder: embedding{errors.New("model unavailable")}, Owner: "test", Lease: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if err = w.Step(ctx, domain.JobIndexMessage); err != nil {
		t.Fatal(err)
	}
	if len(index.messages) != 1 {
		t.Fatal("literal indexing required model")
	}
	if err = w.Step(ctx, domain.JobMemory); err != nil {
		t.Fatal(err)
	}
	if mem.calls != 0 {
		t.Fatal("memory bypassed durable context readiness")
	}
	if err = w.Step(ctx, domain.JobContexts); err != nil {
		t.Fatal(err)
	}
	if err = w.Step(ctx, domain.JobEmbedContext); err == nil {
		t.Fatal("model failure hidden")
	}
	if len(index.messages) != 1 {
		t.Fatal("model failure removed canonical original")
	}
}
func TestContextOnlyExplicitAncestorsSourceTimesAndDuplicateClaim(t *testing.T) {
	s, index, _, _ := fixture(t)
	ctx := context.Background()
	root := event("root", 1)
	reply := event("reply", 1)
	reply.Message.ParentID = "root"
	other := event("unrelated", 1)
	for _, e := range []domain.Event{root, reply, other} {
		s.Accept(ctx, e)
	}
	mem := &memory{}
	w, _ := New(Config{Store: s, Index: index, Memory: mem, Embedder: embedding{}, Owner: "test", Lease: time.Second})
	for range 3 {
		if err := w.Step(ctx, domain.JobContexts); err != nil {
			t.Fatal(err)
		}
	}
	c, err := s.Context(ctx, scope, ContextID(scope, "reply"))
	if err != nil || len(c.Sources) != 2 || c.Sources[0].MessageID != "root" || !c.UpdatedAt.Equal(reply.Message.UpdatedAt) {
		t.Fatalf("mixed context or processing timestamp %+v %v", c, err)
	}
	j, err := s.ClaimJob(ctx, []domain.JobKind{domain.JobMemory}, "manual", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err = w.handle(ctx, j); err != nil {
		t.Fatal(err)
	}
	if err = w.handle(ctx, j); err != nil {
		t.Fatal(err)
	}
	if mem.calls != 1 {
		t.Fatalf("duplicate memory %d", mem.calls)
	}
}
func TestEditDuringExternalWriteCannotLeaveOldFinalDocument(t *testing.T) {
	s, index, _, _ := fixture(t)
	ctx := context.Background()
	s.Accept(ctx, event("one", 1))
	index.block = make(chan struct{})
	index.release = make(chan struct{})
	w, _ := New(Config{Store: s, Index: index, Owner: "test", Lease: time.Second})
	done := make(chan error, 1)
	go func() { done <- w.Step(ctx, domain.JobIndexMessage) }()
	<-index.block
	edit := event("one", 2)
	edit.Message.Text = "latest"
	if _, err := s.Accept(ctx, edit); err != nil {
		t.Fatal(err)
	}
	close(index.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	index.block = nil
	if err := w.Step(ctx, domain.JobIndexMessage); err != nil {
		t.Fatal(err)
	}
	if index.messages["one"].Text != "latest" {
		t.Fatal("stale final overwrite")
	}
}
func TestHeartbeatKeepsLongMemoryClaimAlive(t *testing.T) {
	s, index, _, _ := fixture(t)
	ctx := context.Background()
	s.Accept(ctx, event("one", 1))
	mem := &memory{wait: 150 * time.Millisecond}
	w, _ := New(Config{Store: s, Index: index, Memory: mem, Embedder: embedding{}, Owner: "test", Lease: 60 * time.Millisecond})
	if err := w.Step(ctx, domain.JobContexts); err != nil {
		t.Fatal(err)
	}
	if err := w.Step(ctx, domain.JobMemory); err != nil {
		t.Fatalf("long retain lost lease: %v", err)
	}
}
func TestMediaReadinessBlocksContextAndMemoryUntilArtifactCommitted(t *testing.T) {
	s, index, _, _ := fixture(t)
	ctx := context.Background()
	e := event("image", 1)
	e.Message.Media = []domain.Media{{Type: domain.MediaImage, ResourceID: "picture", MIME: "image/png"}}
	s.Accept(ctx, e)
	mem := &memory{}
	w, _ := New(Config{Store: s, Index: index, Memory: mem, Embedder: embedding{}, Owner: "worker", Lease: time.Second})
	if err := w.Step(ctx, domain.JobContexts); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Context(ctx, scope, ContextID(scope, "image")); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("context consumed media before ready")
	}
	if err := w.Step(ctx, domain.JobMemory); err != nil || mem.calls != 0 {
		t.Fatal("memory consumed incomplete media")
	}
	j, err := s.ClaimJob(ctx, []domain.JobKind{domain.JobMedia}, "manual", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.PutMedia(ctx, j, domain.MediaArtifact{Source: e.Message.Source, Parts: []domain.MediaPart{{Text: "derived image text", Page: 2, Object: domain.Object{ResourceID: "picture"}}}}); err != nil {
		t.Fatal(err)
	}
	ctxJob, err := s.ClaimJob(ctx, []domain.JobKind{domain.JobContexts}, "manual", time.Second)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("prerequisite backoff bypassed", ctxJob, err)
	}
	id, err := s.Enqueue(ctx, domain.JobSpec{Kind: domain.JobContexts, Scope: scope, Source: e.Message.Source, DedupeKey: "artifact-ready"})
	if err != nil || id < 1 {
		t.Fatal(err)
	}
	if err = w.Step(ctx, domain.JobContexts); err != nil {
		t.Fatal(err)
	}
	c, err := s.Context(ctx, scope, ContextID(scope, "image"))
	if err != nil || !strings.Contains(c.Text, "derived image text") || !strings.Contains(c.Text, "page=2") {
		t.Fatal("media provenance missing", err)
	}
}
func TestDeleteCleanupRetriesAndTombstoneDoesNotBlockCleanup(t *testing.T) {
	s, index, db, schema := fixture(t)
	ctx := context.Background()
	e := event("one", 1)
	s.Accept(ctx, e)
	mem := &memory{}
	w, _ := New(Config{Store: s, Index: index, Memory: mem, Embedder: embedding{}, Owner: "worker", Lease: time.Second})
	for _, kind := range []domain.JobKind{domain.JobIndexMessage, domain.JobContexts, domain.JobEmbedContext, domain.JobMemory} {
		if err := w.Step(ctx, kind); err != nil {
			t.Fatal(err)
		}
	}
	e = event("one", 2)
	e.ID = "delete"
	e.Message.EventID = e.ID
	e.Type = domain.EventDelete
	e.Message.Deleted = true
	e.Message.Source.AuthorID = ""
	s.Accept(ctx, e)
	mem.deleteFail = true
	if err := w.Step(ctx, domain.JobCleanup); err == nil {
		t.Fatal("cleanup failure hidden")
	}
	if mem.deletes != 1 {
		t.Fatal("cleanup failed before document deletion")
	}
	if _, err := db.Exec(`UPDATE "` + schema + `".jobs SET available_at=clock_timestamp() WHERE kind='cleanup'`); err != nil {
		t.Fatal(err)
	}
	if err := w.Step(ctx, domain.JobCleanup); err != nil {
		t.Fatal(err)
	}
	if mem.deletes != 2 {
		t.Fatal("external cleanup not retried")
	}
	if len(index.contexts) != 0 || len(index.messages) != 0 {
		t.Fatal("deleted source retained external artifacts")
	}
}
func TestMissingMediaProcessorIsExplicitFailure(t *testing.T) {
	s, index, _, _ := fixture(t)
	ctx := context.Background()
	e := event("image", 1)
	e.Message.Media = []domain.Media{{Type: domain.MediaImage, ResourceID: "image"}}
	s.Accept(ctx, e)
	w, _ := New(Config{Store: s, Index: index, Owner: "missing", Lease: time.Second})
	if err := w.Step(ctx, domain.JobMedia); err == nil {
		t.Fatal("missing media processor reported prerequisite wait instead of explicit failure")
	}
}

type blockingMemory struct {
	entered, release chan struct{}
	retains, deletes int
}

func (m *blockingMemory) Retain(ctx context.Context, c domain.Context) error {
	close(m.entered)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-m.release:
		m.retains++
		return nil
	}
}
func (m *blockingMemory) DeleteDocument(context.Context, domain.Scope, string) error {
	m.deletes++
	return nil
}
func TestEditDuringRetainRechecksSourcesAndCleansStalePublication(t *testing.T) {
	s, index, _, _ := fixture(t)
	ctx := context.Background()
	s.Accept(ctx, event("one", 1))
	mem := &blockingMemory{entered: make(chan struct{}), release: make(chan struct{})}
	w, _ := New(Config{Store: s, Index: index, Memory: mem, Embedder: embedding{}, Owner: "memory-worker", Lease: time.Second})
	if err := w.Step(ctx, domain.JobContexts); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- w.Step(ctx, domain.JobMemory) }()
	<-mem.entered
	ingress, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	if _, err := s.Accept(ingress, event("one", 2)); err != nil {
		t.Fatal("retain blocked ingress", err)
	}
	close(mem.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if mem.retains != 1 || mem.deletes != 1 {
		t.Fatal("stale retain not cleaned")
	}
	c, err := s.Context(ctx, scope, ContextID(scope, "one"))
	if err != nil || !c.Invalidated {
		t.Fatal("concurrent edit did not invalidate derived source")
	}
}
func TestOldCleanupCannotDeleteNewlyRebuiltContext(t *testing.T) {
	s, index, _, _ := fixture(t)
	ctx := context.Background()
	s.Accept(ctx, event("one", 1))
	mem := &memory{}
	w, _ := New(Config{Store: s, Index: index, Memory: mem, Embedder: embedding{}, Owner: "worker", Lease: time.Second})
	for _, kind := range []domain.JobKind{domain.JobContexts, domain.JobEmbedContext, domain.JobMemory} {
		if err := w.Step(ctx, kind); err != nil {
			t.Fatal(err)
		}
	}
	s.Accept(ctx, event("one", 2))
	if err := w.Step(ctx, domain.JobContexts); err != nil {
		t.Fatal(err)
	}
	if err := w.Step(ctx, domain.JobEmbedContext); err != nil {
		t.Fatal(err)
	}
	// Publish both new external channels before claiming that old cleanup must skip them.
	for range 3 {
		if err := w.Step(ctx, domain.JobMemory); err != nil {
			t.Fatal(err)
		}
		stage, err := s.Stage(ctx, event("one", 2).Message.Source, stageName("memory", domain.Context{ID: ContextID(scope, "one"), Version: 2}))
		if err == nil && stage.ContextVersion == 2 {
			break
		}
	}
	if err := w.Step(ctx, domain.JobCleanup); err != nil {
		t.Fatal(err)
	}
	c, ok := index.contexts[ContextID(scope, "one")]
	if !ok || c.Version != 2 || mem.deletes != 0 {
		t.Fatal("old cleanup deleted new context")
	}
}
func TestFutureMutationWaitsInsteadOfLosingDurableProcessing(t *testing.T) {
	s, index, _, _ := fixture(t)
	ctx := context.Background()
	e := event("future", 1)
	e.Message.UpdatedAt = time.Now().Add(time.Hour)
	e.OccurredAt = e.Message.UpdatedAt
	r, err := s.Accept(ctx, e)
	if err != nil {
		t.Fatal(err)
	}
	w, _ := New(Config{Store: s, Index: index, Owner: "worker", Lease: time.Second})
	if err = w.Step(ctx, domain.JobContexts); err != nil {
		t.Fatal(err)
	}
	for _, id := range r.JobIDs {
		j, err := s.Job(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if j.Kind == domain.JobContexts && j.Status != domain.JobPending {
			t.Fatalf("future mutation permanently swallowed %s", j.Status)
		}
	}
}

func TestCleanupFailureStillRetriesAfterPGContextRebuilt(t *testing.T) {
	s, index, db, schema := fixture(t)
	ctx := context.Background()
	if _, err := s.Accept(ctx, event("one", 1)); err != nil {
		t.Fatal(err)
	}
	mem := &memory{}
	w, err := New(Config{Store: s, Index: index, Memory: mem, Embedder: embedding{}, Owner: "review", Lease: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []domain.JobKind{domain.JobContexts, domain.JobEmbedContext, domain.JobMemory} {
		if err = w.Step(ctx, kind); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.Accept(ctx, event("one", 2)); err != nil {
		t.Fatal(err)
	}
	mem.deleteFail = true
	if err = w.Step(ctx, domain.JobCleanup); err == nil {
		t.Fatal("expected transient memory cleanup failure")
	}
	if err = w.Step(ctx, domain.JobContexts); err != nil {
		t.Fatal(err)
	}
	if err = w.Step(ctx, domain.JobEmbedContext); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE "` + schema + `".jobs SET available_at=clock_timestamp() WHERE kind='cleanup'`); err != nil {
		t.Fatal(err)
	}
	if err = w.Step(ctx, domain.JobCleanup); err != nil {
		t.Fatal(err)
	}
	if c, ok := index.contexts[ContextID(scope, "one")]; !ok || c.Version != 2 {
		t.Fatal("fresh index deleted while retrying stale memory cleanup")
	}
	if mem.deletes != 2 {
		t.Fatalf("cleanup marked completed after PG-only rebuild without retrying failed external memory deletion: attempts=%d", mem.deletes)
	}
}
func TestMissingExplicitAncestorWaitsAndResumesWithoutTruncatedMemory(t *testing.T) {
	for _, relation := range []string{"parent", "root"} {
		t.Run(relation, func(t *testing.T) {
			s, index, db, schema := fixture(t)
			ctx := context.Background()
			reply := event("late-reply", 1)
			if relation == "parent" {
				reply.Message.ParentID = "late-root"
			} else {
				reply.Message.RootID = "late-root"
				reply.Message.ThreadID = "omt_late_topic"
			}
			accepted, err := s.Accept(ctx, reply)
			if err != nil {
				t.Fatal(err)
			}
			mem := &memory{}
			w, _ := New(Config{Store: s, Index: index, Memory: mem, Embedder: embedding{}, Owner: "late-parent", Lease: time.Second})
			if err = w.Step(ctx, domain.JobIndexMessage); err != nil {
				t.Fatal(err)
			}
			if _, ok := index.messages[reply.Message.Source.MessageID]; !ok {
				t.Fatal("missing parent blocked canonical indexing")
			}
			if err = w.Step(ctx, domain.JobContexts); err != nil {
				t.Fatal(err)
			}
			if c, err := s.Context(ctx, scope, ContextID(scope, "late-reply")); !errors.Is(err, domain.ErrNotFound) {
				t.Fatalf("truncated context published before explicit ancestor: %+v %v", c, err)
			}
			for _, id := range accepted.JobIDs {
				j, e := s.Job(ctx, id)
				if e != nil {
					t.Fatal(e)
				}
				if j.Kind == domain.JobContexts && (j.Status != domain.JobPending || j.Attempt != 0) {
					t.Fatalf("missing prerequisite consumed claim: %s attempt%d", j.Status, j.Attempt)
				}
			}
			if err = w.Step(ctx, domain.JobMemory); err != nil || mem.calls != 0 {
				t.Fatal("memory consumed unresolved ancestor")
			}
			root := event("late-root", 1)
			root.Message.Text = "required original ancestor"
			if _, err = s.Accept(ctx, root); err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec(`UPDATE "` + schema + `".jobs SET available_at=clock_timestamp() WHERE kind='contexts' AND status='pending'`); err != nil {
				t.Fatal(err)
			}
			// Both the newly arrived root and deferred reply have ready jobs.
			for range 2 {
				if err = w.Step(ctx, domain.JobContexts); err != nil {
					t.Fatal(err)
				}
			}
			c, err := s.Context(ctx, scope, ContextID(scope, "late-reply"))
			if err != nil || len(c.Sources) != 2 || c.Sources[0].MessageID != "late-root" || !strings.Contains(c.Text, root.Message.Text) {
				t.Fatalf("late ancestor not resumed %+v %v", c, err)
			}
		})
	}
}
func TestDeletedExplicitAncestorIsMarkedBoundaryWithoutBlockingLiveDiscussion(t *testing.T) {
	s, index, _, _ := fixture(t)
	ctx := context.Background()
	deleted := event("deleted-root", 2)
	deleted.Type = domain.EventDelete
	deleted.Message.Deleted = true
	deleted.Message.Text = "deleted secret content"
	if _, err := s.Accept(ctx, deleted); err != nil {
		t.Fatal(err)
	}
	reply := event("live-reply", 1)
	reply.Message.ParentID = "deleted-root"
	accepted, err := s.Accept(ctx, reply)
	if err != nil {
		t.Fatal(err)
	}
	mem := &memory{}
	w, _ := New(Config{Store: s, Index: index, Memory: mem, Embedder: embedding{}, Owner: "deleted-boundary", Lease: time.Second})
	if err = w.Step(ctx, domain.JobContexts); err != nil {
		t.Fatal(err)
	}
	c, err := s.Context(ctx, scope, ContextID(scope, "live-reply"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(c.Text, "discussion boundary") || !strings.Contains(c.Text, "deleted") || !strings.Contains(c.Text, "omitted") {
		t.Fatal("deleted ancestor silently truncated discussion")
	}
	if strings.Contains(c.Text, deleted.Message.Text) || strings.Contains(c.Text, deleted.Message.Source.AuthorID) || len(c.Sources) != 1 || c.Sources[0] != reply.Message.Source {
		t.Fatal("deleted content or invented source included")
	}
	for _, id := range accepted.JobIDs {
		j, e := s.Job(ctx, id)
		if e != nil {
			t.Fatal(e)
		}
		if j.Kind == domain.JobContexts && j.Status != domain.JobCompleted {
			t.Fatalf("live discussion stuck after tombstone: %s", j.Status)
		}
	}
	if err = w.Step(ctx, domain.JobMemory); err != nil || mem.calls != 1 {
		t.Fatal("remaining live discussion cannot reach memory", err)
	}
}
func TestTopicThreadIDNeverBecomesMessageAncestor(t *testing.T) {
	s, index, _, _ := fixture(t)
	ctx := context.Background()
	root := event("om-root", 1)
	root.Message.ThreadID = "omt_real_topic"
	other := event("om-independent", 1)
	other.Message.ThreadID = root.Message.ThreadID
	for _, e := range []domain.Event{root, other} {
		if _, err := s.Accept(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	w, _ := New(Config{Store: s, Index: index, Memory: &memory{}, Embedder: embedding{}, Owner: "topic-metadata", Lease: time.Second})
	for range 2 {
		if err := w.Step(ctx, domain.JobContexts); err != nil {
			t.Fatal(err)
		}
	}
	for _, e := range []domain.Event{root, other} {
		c, err := s.Context(ctx, scope, ContextID(scope, e.Message.Source.MessageID))
		if err != nil || len(c.Sources) != 1 || c.Sources[0].MessageID != e.Message.Source.MessageID {
			t.Fatalf("topic metadata treated as message dependency: %+v %v", c, err)
		}
	}
}

func TestExplicitParentAndRootJoinMessagesWhileTopicRemainsMetadata(t *testing.T) {
	for _, relation := range []string{"parent", "root"} {
		t.Run(relation, func(t *testing.T) {
			s, index, _, _ := fixture(t)
			ctx := context.Background()
			root := event("om-root", 1)
			root.Message.ThreadID = "omt_distinct_topic_id"
			root.Message.RootID = root.Message.Source.MessageID
			child := event("om-child", 1)
			child.Message.ThreadID = root.Message.ThreadID
			if relation == "parent" {
				child.Message.ParentID = root.Message.Source.MessageID
				child.Message.RootID = "om-unrelated-not-yet-observed"
			} else {
				child.Message.RootID = root.Message.Source.MessageID
			}
			for _, e := range []domain.Event{root, child} {
				if _, err := s.Accept(ctx, e); err != nil {
					t.Fatal(err)
				}
			}
			w, _ := New(Config{Store: s, Index: index, Memory: &memory{}, Embedder: embedding{}, Owner: "explicit-root", Lease: time.Second})
			for range 2 {
				if err := w.Step(ctx, domain.JobContexts); err != nil {
					t.Fatal(err)
				}
			}
			c, err := s.Context(ctx, scope, ContextID(scope, "om-child"))
			if err != nil || len(c.Sources) != 2 || c.Sources[0].MessageID != "om-root" || c.Sources[1].MessageID != "om-child" {
				t.Fatalf("explicit message relationship lost: %+v %v", c, err)
			}
		})
	}
}
