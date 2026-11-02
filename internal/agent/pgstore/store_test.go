package pgstore_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/pgstore"
	_ "github.com/lib/pq"
)

type fixture struct {
	*pgstore.Store
	db     *sql.DB
	schema string
}

// The fixture never loads application configuration or uses its database.
func setup(t *testing.T) fixture {
	t.Helper()
	dsn := os.Getenv("AGENT_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("real isolated PostgreSQL required: set AGENT_TEST_POSTGRES_DSN")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(16)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err = db.PingContext(ctx); err != nil {
		t.Fatal("isolated PostgreSQL fixture unavailable")
	}
	var b [8]byte
	if _, err = rand.Read(b[:]); err != nil {
		t.Fatal(err)
	}
	schema := "agent_test_" + hex.EncodeToString(b[:])
	s, err := pgstore.New(db, schema)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, dropErr := db.Exec(`DROP SCHEMA IF EXISTS "` + schema + `" CASCADE`)
		if dropErr != nil {
			t.Error(dropErr)
		}
		db.Close()
	})
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatalf("migration not idempotent: %v", err)
	}
	return fixture{s, db, schema}
}

func testScope() domain.Scope {
	return domain.Scope{TenantID: "tenant", AppID: "app", ChatID: "chat", Visibility: "group"}
}

var baseTime = time.Date(2026, 10, 1, 1, 2, 3, 123456789, time.UTC)

func event(id string, version int64) domain.Event {
	now := baseTime.Add(time.Duration(version) * time.Second)
	return domain.Event{ID: id, Type: domain.EventMessage, OccurredAt: now, Message: domain.Message{EventID: id, Source: domain.SourceRef{Scope: testScope(), MessageID: "message", Version: version, AuthorID: "author", OccurredAt: baseTime}, Text: "原文", UpdatedAt: now, Raw: json.RawMessage(`{ "exact": "audit" }`)}}
}
func accept(t *testing.T, f fixture, e domain.Event) pgstore.AcceptResult {
	t.Helper()
	r, err := f.Accept(context.Background(), e)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestSchemaIdentifiersRejectSQLInjection(t *testing.T) {
	for _, schema := range []string{`bad.schema`, `x"; DROP SCHEMA public;--`, `UPPER`, `a b`, string(make([]byte, 64))} {
		if _, err := pgstore.New(&sql.DB{}, schema); !errors.Is(err, domain.ErrInvalid) {
			t.Fatalf("accepted schema %q: %v", schema, err)
		}
	}
}

func TestAcceptDeduplicatesWithoutJobsAndRejectsConflict(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	e := event("event", 1)
	first := accept(t, f, e)
	if !first.Current || first.Duplicate || len(first.JobIDs) < 4 {
		t.Fatalf("missing atomic work: %+v", first)
	}
	second := accept(t, f, e)
	if !second.Duplicate || len(second.JobIDs) != 0 {
		t.Fatalf("duplicate produced work: %+v", second)
	}
	e.Message.Text = "conflicting payload"
	if _, err := f.Accept(ctx, e); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("conflicting identity accepted: %v", err)
	}
	m, err := f.Message(ctx, testScope(), "message")
	if err != nil || m.Text != "原文" || string(m.Raw) != `{ "exact": "audit" }` {
		t.Fatalf("original audit lost: %+v %v", m, err)
	}
	m.Raw[2] = 'x'
	m.Text = "caller mutation"
	again, _ := f.Message(ctx, testScope(), "message")
	if again.Text != "原文" || string(again.Raw) != `{ "exact": "audit" }` {
		t.Fatal("mutable content escaped")
	}
	versions, err := f.Versions(ctx, testScope(), "message")
	if err != nil || len(versions) != 1 {
		t.Fatalf("versions %+v: %v", versions, err)
	}
	e = event("other-event", 1)
	e.Message.Text = "conflicting source version"
	if _, err = f.Accept(ctx, e); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("source version not immutable: %v", err)
	}
}

func TestOutboxFailureRollsBackEventsCurrentAndHistory(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	_, err := f.db.Exec(`ALTER TABLE "` + f.schema + `".jobs ADD CONSTRAINT fixture_reject_jobs CHECK (kind = 'impossible')`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Accept(ctx, event("rollback", 1)); err == nil {
		t.Fatal("expected injected outbox constraint failure")
	}
	if _, err = f.Message(ctx, testScope(), "message"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("current escaped rollback: %v", err)
	}
	versions, err := f.Versions(ctx, testScope(), "message")
	if err != nil || len(versions) != 0 {
		t.Fatalf("history escaped rollback: %v %v", versions, err)
	}
	var count int
	if err = f.db.QueryRow(`SELECT count(*) FROM "` + f.schema + `".events`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("event escaped rollback: %d %v", count, err)
	}
	if _, err = f.db.Exec(`ALTER TABLE "` + f.schema + `".jobs DROP CONSTRAINT fixture_reject_jobs`); err != nil {
		t.Fatal(err)
	}
	if r := accept(t, f, event("rollback", 1)); r.Duplicate {
		t.Fatal("rolled-back dedupe poisoned retry")
	}
}

func TestScopeIsolationStaleUpdatesAndImmutableHistory(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	newer := event("newer", 3)
	newer.Message.Text = "new"
	accept(t, f, newer)
	old := event("older", 1)
	old.Message.Text = "old"
	if r := accept(t, f, old); r.Current {
		t.Fatal("late old event became current")
	}
	other := old
	other.Message.Source.Scope.ChatID = "other"
	other.Message.Text = "other chat"
	accept(t, f, other)
	m, _ := f.Message(ctx, testScope(), "message")
	if m.Text != "new" || m.Source.Version != 3 {
		t.Fatal("late event overwrote current")
	}
	m, _ = f.Message(ctx, other.Message.Source.Scope, "message")
	if m.Text != "other chat" {
		t.Fatal("cross-chat key collision")
	}
	v, err := f.Versions(ctx, testScope(), "message")
	if err != nil || len(v) != 2 || v[0].Source.Version != 1 || v[1].Text != "new" {
		t.Fatalf("history not immutable: %+v %v", v, err)
	}
	if !v[1].UpdatedAt.Equal(newer.Message.UpdatedAt) {
		t.Fatal("exact provider timestamp truncated")
	}
}

func TestDeleteBeforeCreationAndLaterOriginalCannotResurrect(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	deleted := event("delete", 2)
	deleted.Type = domain.EventDelete
	deleted.Message.Deleted = true
	deleted.Message.Source.AuthorID = ""
	deleted.Message.Source.OccurredAt = time.Time{}
	accept(t, f, deleted)
	accept(t, f, event("late-original", 1))
	// A provider's later event sequence must also never turn deletion into creation.
	accept(t, f, event("late-delivery-with-new-sequence", 3))
	m, err := f.Message(ctx, testScope(), "message")
	if err != nil || !m.Deleted || m.Source.Version != 2 || m.Source.AuthorID != "" {
		t.Fatalf("tombstone resurrected or author fabricated: %+v %v", m, err)
	}
	recent, err := f.Recent(ctx, testScope(), baseTime.Add(time.Hour), 10)
	if err != nil || len(recent) != 0 {
		t.Fatalf("deleted recent message leaked: %+v %v", recent, err)
	}
}

func TestDeletePreservesKnownAuthorAndReplyIdentity(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	original := event("original", 1)
	original.Message.ParentID = "parent"
	original.Message.ThreadID = "thread"
	accept(t, f, original)
	d := event("delete", 2)
	d.Type = domain.EventDelete
	d.Message.Deleted = true
	d.Message.Source.AuthorID = ""
	d.Message.Source.OccurredAt = time.Time{}
	accept(t, f, d)
	m, err := f.Message(ctx, testScope(), "message")
	if err != nil || m.Source.AuthorID != "author" || !m.Source.OccurredAt.Equal(baseTime) || m.ParentID != "parent" || m.ThreadID != "thread" {
		t.Fatalf("delete lost original identity: %+v %v", m, err)
	}
}

func TestSourceValidationUsesFullScopeVersionAuthorAndExactAnchor(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	e := event("event", 1)
	accept(t, f, e)
	q := domain.Query{Scope: testScope(), Anchor: e.Message.UpdatedAt, Mode: domain.QueryLiteral, Limit: 10}
	valid := func(refs []domain.SourceRef, want bool) {
		t.Helper()
		got, err := f.ValidSources(ctx, q, refs)
		if err != nil || got != want {
			t.Fatalf("valid sources=%v want=%v: %v", got, want, err)
		}
	}
	valid([]domain.SourceRef{e.Message.Source}, true)
	valid(nil, false)
	for _, mutate := range []func(*domain.SourceRef){func(r *domain.SourceRef) { r.Scope.ChatID = "other" }, func(r *domain.SourceRef) { r.Scope.Visibility = "private" }, func(r *domain.SourceRef) { r.AuthorID = "other-author" }, func(r *domain.SourceRef) { r.Version++ }, func(r *domain.SourceRef) { r.OccurredAt = r.OccurredAt.Add(time.Nanosecond) }} {
		ref := e.Message.Source
		mutate(&ref)
		valid([]domain.SourceRef{ref}, false)
	}
	q.Anchor = e.Message.UpdatedAt.Add(-time.Nanosecond)
	valid([]domain.SourceRef{e.Message.Source}, false)
	q.Anchor = e.Message.UpdatedAt
	q.AuthorID = "someone-else"
	valid([]domain.SourceRef{e.Message.Source}, false)
	q.AuthorID = ""
	q.Anchor = baseTime.Add(time.Hour)
	edited := event("edit", 2)
	accept(t, f, edited)
	valid([]domain.SourceRef{e.Message.Source}, false)
	valid([]domain.SourceRef{edited.Message.Source}, true)
	q.Anchor = edited.Message.UpdatedAt.Add(-time.Nanosecond)
	valid([]domain.SourceRef{edited.Message.Source}, false)
	q.Anchor = baseTime.Add(time.Hour)
	deleted := event("delete", 3)
	deleted.Type = domain.EventDelete
	deleted.Message.Deleted = true
	accept(t, f, deleted)
	valid([]domain.SourceRef{deleted.Message.Source}, false)
}

func TestRecentReadsCurrentRawWithAnchorAndScope(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	e := event("first", 1)
	accept(t, f, e)
	future := event("future", 2)
	future.Message.Source.MessageID = "future"
	accept(t, f, future)
	msgs, err := f.Recent(ctx, testScope(), e.Message.UpdatedAt, 1)
	if err != nil || len(msgs) != 1 || msgs[0].EventID != "first" {
		t.Fatalf("recent anchor: %+v %v", msgs, err)
	}
	msgs, err = f.Recent(ctx, testScope(), e.Message.UpdatedAt.Add(-time.Nanosecond), 10)
	if err != nil || len(msgs) != 0 {
		t.Fatalf("submicrosecond future leaked: %+v %v", msgs, err)
	}
	other := testScope()
	other.TenantID = "other"
	msgs, err = f.Recent(ctx, other, baseTime.Add(time.Hour), 10)
	if err != nil || len(msgs) != 0 {
		t.Fatalf("cross-scope recent: %+v %v", msgs, err)
	}
}

func TestRecentKeepsExactNanosecondOrderBeforeApplyingLimit(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	for i, id := range []string{"a", "z"} {
		e := event(id, 1)
		e.Message.Source.MessageID = id
		e.Message.Source.OccurredAt = baseTime.Add(time.Duration(i) * time.Nanosecond)
		e.Message.UpdatedAt = e.Message.Source.OccurredAt
		accept(t, f, e)
	}
	msgs, err := f.Recent(ctx, testScope(), baseTime.Add(time.Second), 1)
	if err != nil || len(msgs) != 1 || msgs[0].Source.MessageID != "z" {
		t.Fatalf("exact recent order lost: %+v %v", msgs, err)
	}
}

func TestEditCannotChangeOriginalAuthorOrCreationTimestamp(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	accept(t, f, event("one", 1))
	for _, mutate := range []func(*domain.Event){func(e *domain.Event) { e.Message.Source.AuthorID = "someone-else" }, func(e *domain.Event) { e.Message.Source.OccurredAt = baseTime.Add(time.Nanosecond) }} {
		e := event("edit", 2)
		mutate(&e)
		if _, err := f.Accept(ctx, e); !errors.Is(err, domain.ErrConflict) {
			t.Fatalf("source identity rewritten: %v", err)
		}
	}
}

func TestContextsTrackAllSourcesAndInvalidateImmediatelyOnMutation(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	e := event("one", 1)
	accept(t, f, e)
	second := event("two", 1)
	second.Message.Source.MessageID = "second"
	accept(t, f, second)
	c := domain.Context{ID: "stable-context", Scope: testScope(), Version: 1, Text: "discussion", Sources: []domain.SourceRef{e.Message.Source, second.Message.Source}, CreatedAt: baseTime, UpdatedAt: baseTime.Add(time.Minute)}
	if err := f.PutContext(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := f.PutContext(ctx, c); err != nil {
		t.Fatalf("context retry not idempotent: %v", err)
	}
	stored, err := f.Context(ctx, testScope(), c.ID)
	if err != nil || len(stored.Sources) != 2 || stored.Invalidated {
		t.Fatalf("sources lost: %+v %v", stored, err)
	}
	affected, err := f.AffectedContexts(ctx, testScope(), "second")
	if err != nil || len(affected) != 1 {
		t.Fatalf("reverse lookup: %+v %v", affected, err)
	}
	edit := second
	edit.ID = "two-edit"
	edit.Message.EventID = edit.ID
	edit.Message.Source.Version = 2
	edit.Message.UpdatedAt = baseTime.Add(2 * time.Second)
	edit.Message.Text = "edited"
	accept(t, f, edit)
	stored, err = f.Context(ctx, testScope(), c.ID)
	if err != nil || !stored.Invalidated || stored.InvalidatedAt == nil {
		t.Fatalf("context retained stale validity: %+v %v", stored, err)
	}
	if err = f.PutContext(ctx, c); !errors.Is(err, domain.ErrStaleSource) {
		t.Fatalf("stale derived write accepted: %v", err)
	}
	job, err := f.ClaimJob(ctx, []domain.JobKind{domain.JobCleanup}, "cleanup", time.Minute)
	if err != nil || job.Source.MessageID != "second" {
		t.Fatalf("missing durable invalidation: %+v %v", job, err)
	}
	var payload struct {
		ContextIDs []string `json:"context_ids"`
	}
	if err = json.Unmarshal(job.Payload, &payload); err != nil || len(payload.ContextIDs) != 1 || payload.ContextIDs[0] != c.ID {
		t.Fatalf("cleanup misses derived context: %s %v", job.Payload, err)
	}
	newContext := c
	newContext.Version = 2
	newContext.Sources[1] = edit.Message.Source
	newContext.UpdatedAt = baseTime.Add(2 * time.Minute)
	if err = f.PutContext(ctx, newContext); err != nil {
		t.Fatal(err)
	}
	stored, _ = f.Context(ctx, testScope(), c.ID)
	if stored.Invalidated || stored.Version != 2 {
		t.Fatal("rebuilt context not active")
	}
}

func TestJobsRawPriorityConcurrentClaimsAndFence(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	accept(t, f, event("one", 1))
	job, err := f.ClaimJob(ctx, nil, "worker", time.Minute)
	if err != nil || job.Kind != domain.JobIndexMessage {
		t.Fatalf("raw indexing not prioritized: %+v %v", job, err)
	}
	if err = f.CompleteJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err = f.CompleteJob(ctx, job); !errors.Is(err, domain.ErrStaleClaim) {
		t.Fatalf("completed claim reused: %v", err)
	}
	const n = 8
	for i := 0; i < n; i++ {
		if _, err = f.Enqueue(ctx, domain.JobSpec{Kind: domain.JobDelivery, Scope: testScope(), Payload: json.RawMessage(`{"delivery":true}`)}); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	results := make(chan domain.Job, n)
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Go(func() {
			j, e := f.ClaimJob(ctx, []domain.JobKind{domain.JobDelivery}, "parallel", time.Minute)
			results <- j
			errs <- e
		})
	}
	wg.Wait()
	close(results)
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	seen := map[int64]bool{}
	for j := range results {
		if seen[j.ID] {
			t.Fatal("job claimed concurrently twice")
		}
		seen[j.ID] = true
	}
	if len(seen) != n {
		t.Fatal("jobs lost")
	}
}

func TestExpiredLeaseRejectsStaleCompletionAndRecoversWithNewFence(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	id, err := f.Enqueue(ctx, domain.JobSpec{Kind: domain.JobDelivery, Scope: testScope()})
	if err != nil {
		t.Fatal(err)
	}
	old, err := f.ClaimJob(ctx, []domain.JobKind{domain.JobDelivery}, "old", 25*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(40 * time.Millisecond)
	if err = f.CompleteJob(ctx, old); !errors.Is(err, domain.ErrStaleClaim) {
		t.Fatalf("expired lease accepted: %v", err)
	}
	// The same named worker can restart; fencing, not owner naming, must reject
	// its old in-flight completion/retry.
	next, err := f.ClaimJob(ctx, []domain.JobKind{domain.JobDelivery}, "old", time.Minute)
	if err != nil || next.ID != id || next.Fence <= old.Fence || next.Attempt != 2 {
		t.Fatalf("lease not recovered: %+v %v", next, err)
	}
	if err = f.RetryJob(ctx, old, errors.New("late error")); !errors.Is(err, domain.ErrStaleClaim) {
		t.Fatalf("old worker retried new claim: %v", err)
	}
	if err = f.CompleteJob(ctx, next); err != nil {
		t.Fatal(err)
	}
}

func TestRetriesHaveBoundedBackoffExpiryAndInspectableFailure(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	id, err := f.Enqueue(ctx, domain.JobSpec{Kind: domain.JobMemory, Scope: testScope(), MaxAttempts: 2})
	if err != nil {
		t.Fatal(err)
	}
	j, err := f.ClaimJob(ctx, []domain.JobKind{domain.JobMemory}, "worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	before := time.Now()
	if err = f.RetryJob(ctx, j, errors.New("model unavailable")); err != nil {
		t.Fatal(err)
	}
	state, err := f.Job(ctx, id)
	if err != nil || state.Status != domain.JobPending || state.LastError != "model unavailable" || !state.AvailableAt.After(before) || state.AvailableAt.After(before.Add(time.Hour)) {
		t.Fatalf("retry state: %+v %v", state, err)
	}
	if _, err = f.ClaimJob(ctx, []domain.JobKind{domain.JobMemory}, "too-soon", time.Minute); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("backoff bypassed: %v", err)
	}
	if _, err = f.db.Exec(`UPDATE "`+f.schema+`".jobs SET available_at=clock_timestamp() WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	j, err = f.ClaimJob(ctx, []domain.JobKind{domain.JobMemory}, "last", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.RetryJob(ctx, j, errors.New("permanent failure")); err != nil {
		t.Fatal(err)
	}
	state, err = f.Job(ctx, id)
	if err != nil || state.Status != domain.JobFailed || state.Attempt != 2 {
		t.Fatalf("unbounded retries: %+v %v", state, err)
	}
	expiredID, err := f.Enqueue(ctx, domain.JobSpec{Kind: domain.JobDelivery, Scope: testScope(), ExpiresAt: time.Now().Add(20 * time.Millisecond)})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)
	if _, err = f.ClaimJob(ctx, []domain.JobKind{domain.JobDelivery}, "expired", time.Minute); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expired job claimed: %v", err)
	}
	state, err = f.Job(ctx, expiredID)
	if err != nil || state.Status != domain.JobFailed {
		t.Fatalf("expired job not terminal/inspectable: %+v %v", state, err)
	}
}

func waitForPGCondition(t *testing.T, ctx context.Context, db *sql.DB, query string, args ...any) {
	t.Helper()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		var ready bool
		if err := db.QueryRowContext(ctx, query, args...).Scan(&ready); err != nil {
			t.Fatalf("PostgreSQL condition: %v", err)
		}
		if ready {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("PostgreSQL condition deadline: %v", ctx.Err())
		case <-ticker.C:
		}
	}
}

func TestJobMutationRejectsExpiryDuringRowLockWait(t *testing.T) {
	for _, operation := range []string{"complete", "retry"} {
		for _, expiry := range []string{"lease_until", "expires_at"} {
			t.Run(operation+"/"+expiry, func(t *testing.T) {
				f := setup(t)
				ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
				defer cancel()
				id, err := f.Enqueue(ctx, domain.JobSpec{Kind: domain.JobDelivery, Scope: testScope()})
				if err != nil {
					t.Fatal(err)
				}
				job, err := f.ClaimJob(ctx, []domain.JobKind{domain.JobDelivery}, "worker", time.Minute)
				if err != nil {
					t.Fatal(err)
				}
				// Commit the short boundary before taking the blocking lock. Merely
				// locking the row must not change its tuple and trigger re-evaluation.
				if _, err = f.db.ExecContext(ctx, `UPDATE "`+f.schema+`".jobs SET `+expiry+`=clock_timestamp()+interval '1 second' WHERE id=$1`, id); err != nil {
					t.Fatal(err)
				}
				before, err := f.Job(ctx, id)
				if err != nil {
					t.Fatal(err)
				}
				locker, err := f.db.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer locker.Rollback()
				var pid int
				if err = locker.QueryRowContext(ctx, `SELECT pg_backend_pid() FROM "`+f.schema+`".jobs WHERE id=$1 FOR UPDATE`, id).Scan(&pid); err != nil {
					t.Fatal(err)
				}
				result := make(chan error, 1)
				go func() {
					if operation == "complete" {
						result <- f.CompleteJob(ctx, job)
					} else {
						result <- f.RetryJob(ctx, job, errors.New("must not mutate expired claim"))
					}
				}()
				// Observe the actual PostgreSQL lock wait, rather than assuming the
				// goroutine reached its statement after an arbitrary sleep.
				waitForPGCondition(t, ctx, f.db, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))`, pid)
				var stillValid bool
				if err = f.db.QueryRowContext(ctx, `SELECT `+expiry+`>clock_timestamp() FROM "`+f.schema+`".jobs WHERE id=$1`, id).Scan(&stillValid); err != nil || !stillValid {
					t.Fatalf("operation did not begin waiting before expiry: %v", err)
				}
				waitForPGCondition(t, ctx, f.db, `SELECT clock_timestamp()>=`+expiry+` FROM "`+f.schema+`".jobs WHERE id=$1`, id)
				// The other deadline must remain live, so each predicate is tested
				// independently (ClaimJob normally clamps lease to global expiry).
				other := "expires_at"
				if expiry == "expires_at" {
					other = "lease_until"
				}
				if err = f.db.QueryRowContext(ctx, `SELECT `+other+`>clock_timestamp() FROM "`+f.schema+`".jobs WHERE id=$1`, id).Scan(&stillValid); err != nil || !stillValid {
					t.Fatalf("other deadline expired: %v", err)
				}
				if err = locker.Commit(); err != nil {
					t.Fatal(err)
				}
				select {
				case err = <-result:
					if !errors.Is(err, domain.ErrStaleClaim) {
						t.Errorf("%s accepted %s expiry while waiting for row lock: %v", operation, expiry, err)
					}
				case <-ctx.Done():
					t.Fatal("job mutation did not return before test deadline")
				}
				after, err := f.Job(ctx, id)
				if err != nil {
					t.Fatal(err)
				}
				if after.Status != before.Status || after.Owner != before.Owner || after.Fence != before.Fence || after.LastError != before.LastError || !after.AvailableAt.Equal(before.AvailableAt) || !after.LeaseUntil.Equal(before.LeaseUntil) || !after.ExpiresAt.Equal(before.ExpiresAt) {
					t.Fatalf("stale claim mutated persisted state: before=%+v after=%+v", before, after)
				}
			})
		}
	}
}

func TestLockedExpiredJobsDoNotBlockRawClaims(t *testing.T) {
	for _, tc := range []struct {
		name        string
		expiredKind domain.JobKind
		kinds       []domain.JobKind
	}{
		{"other_kind", domain.JobMedia, []domain.JobKind{domain.JobIndexMessage}},
		{"all_kinds", domain.JobMedia, nil},
		{"same_kind", domain.JobIndexMessage, []domain.JobKind{domain.JobIndexMessage}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := setup(t)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			expiredID, err := f.Enqueue(ctx, domain.JobSpec{Kind: tc.expiredKind, Scope: testScope()})
			if err != nil {
				t.Fatal(err)
			}
			rawID, err := f.Enqueue(ctx, domain.JobSpec{Kind: domain.JobIndexMessage, Scope: testScope()})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.db.ExecContext(ctx, `UPDATE "`+f.schema+`".jobs SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, expiredID); err != nil {
				t.Fatal(err)
			}
			locker, err := f.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer locker.Rollback()
			var lockedID int64
			if err = locker.QueryRowContext(ctx, `SELECT id FROM "`+f.schema+`".jobs WHERE id=$1 FOR UPDATE`, expiredID).Scan(&lockedID); err != nil {
				t.Fatal(err)
			}
			claimCtx, claimCancel := context.WithTimeout(ctx, 350*time.Millisecond)
			defer claimCancel()
			job, err := f.ClaimJob(claimCtx, tc.kinds, "raw-worker", time.Minute)
			if err != nil || job.ID != rawID || job.Kind != domain.JobIndexMessage {
				t.Fatalf("locked expired %s prevented available raw claim: id=%d want=%d err=%v", tc.expiredKind, job.ID, rawID, err)
			}
		})
	}
}

func TestTombstonePreservesExplicitRootMessageLocator(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	e := event("root-provenance", 1)
	e.Message.ParentID = "om-parent"
	e.Message.RootID = "om-root"
	e.Message.ThreadID = "omt_topic"
	accept(t, f, e)
	deleted := event("root-provenance-delete", 2)
	deleted.Type = domain.EventDelete
	deleted.Message.Deleted = true
	deleted.Message.Source.AuthorID = ""
	accept(t, f, deleted)
	m, err := f.Message(ctx, testScope(), e.Message.Source.MessageID)
	if err != nil || m.RootID != e.Message.RootID || m.ThreadID != e.Message.ThreadID || m.ParentID != e.Message.ParentID || m.Source.AuthorID != e.Message.Source.AuthorID {
		t.Fatalf("tombstone discarded root message provenance: %+v %v", m, err)
	}
}
