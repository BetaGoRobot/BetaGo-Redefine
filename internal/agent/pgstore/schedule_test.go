package pgstore_test

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/pgstore"
	"testing"
	"time"
)

func newSchedule(t *testing.T, f fixture, name string) (domain.Schedule, domain.Job) {
	t.Helper()
	in := domain.ScheduleInput{Text: "recurring water", StartAt: time.Now().UTC().Add(time.Second), IntervalSeconds: 60, Runs: 3}
	j, task, x := workTurn(t, f, name, "alice", "schedule_create", in)
	out, err := f.CreateSchedule(context.Background(), j, task, x.Key, "work-policy", in)
	if err != nil {
		t.Fatal(err)
	}
	again, err := f.CreateSchedule(context.Background(), j, task, x.Key, "work-policy", in)
	if err != nil || again.ID != out.ID {
		t.Fatalf("duplicate %+v %v", again, err)
	}
	if _, err = f.CreateSchedule(context.Background(), j, task, "forged", "work-policy", in); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("unguarded %v", err)
	}
	if err = f.CompleteJob(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	return out, j
}
func TestScheduleCreatesDurableIndependentRunAndRetryUUID(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	plan, _ := newSchedule(t, f, "schedule-origin")
	if plan.CreatedBy != "alice" || plan.Status != "active" {
		t.Fatalf("plan %+v", plan)
	}
	if _, err := f.db.Exec(`UPDATE "` + f.schema + `".tasks SET expires_at=clock_timestamp()-interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Until(plan.Plan.StartAt) + 10*time.Millisecond)
	j, err := f.ClaimJob(ctx, []domain.JobKind{domain.JobSchedule}, "schedule-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	a, err := f.PrepareScheduleRun(ctx, j)
	if err != nil || a.Status != "sending" || a.UUID == "" {
		t.Fatalf("prepare %+v %v", a, err)
	}
	b, err := f.PrepareScheduleRun(ctx, j)
	if err != nil || a.UUID != b.UUID {
		t.Fatalf("retry %+v %v", b, err)
	}
	after, err := f.QuerySchedule(ctx, plan.Scope, plan.ID)
	if err != nil || after.NextIndex != 1 {
		t.Fatalf("recurrence %+v %v", after, err)
	}
	if err = f.CheckScheduleRun(ctx, j, a); err != nil {
		t.Fatal(err)
	}
	if err = f.FinishScheduleRun(ctx, j, a, "real-receipt"); err != nil {
		t.Fatal(err)
	}
	runs, err := f.ScheduleRuns(ctx, plan.Scope, plan.ID, 20)
	if err != nil || len(runs) != 2 || runs[0].MessageID != "real-receipt" {
		t.Fatalf("runs %+v %v", runs, err)
	}
}
func TestScheduleOwnerStatusControlAndLateReceipt(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	plan, _ := newSchedule(t, f, "schedule-owner")
	time.Sleep(time.Until(plan.Plan.StartAt) + 10*time.Millisecond)
	job, err := f.ClaimJob(ctx, []domain.JobKind{domain.JobSchedule}, "schedule-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	run, err := f.PrepareScheduleRun(ctx, job)
	if err != nil {
		t.Fatal(err)
	}
	change := domain.ScheduleUpdateInput{ID: plan.ID, Action: "pause"}
	j, task, x := workTurn(t, f, "bob-pause", "bob", "schedule_update", change)
	if _, err = f.UpdateSchedule(ctx, j, task, x.Key, "work-policy", change); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("other owner %v", err)
	}
	if err = f.CompleteJob(ctx, j); err != nil {
		t.Fatal(err)
	}
	j, task, x = workTurn(t, f, "alice-pause", "alice", "schedule_update", change)
	paused, err := f.UpdateSchedule(ctx, j, task, x.Key, "work-policy", change)
	if err != nil || paused.Status != "paused" {
		t.Fatalf("pause %+v %v", paused, err)
	}
	if err = f.CheckScheduleRun(ctx, job, run); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("paused send permitted %v", err)
	}
	if err = f.FinishScheduleRun(ctx, job, run, "late-receipt"); err != nil {
		t.Fatal(err)
	}
	runs, err := f.ScheduleRuns(ctx, plan.Scope, plan.ID, 20)
	if err != nil || runs[0].Status != "canceled" || runs[0].MessageID != "late-receipt" {
		t.Fatalf("late receipt %+v %v", runs, err)
	}
	if err = f.CompleteJob(ctx, j); err != nil {
		t.Fatal(err)
	}
	change.Action = "resume"
	j, task, x = workTurn(t, f, "alice-resume", "alice", "schedule_update", change)
	resumed, err := f.UpdateSchedule(ctx, j, task, x.Key, "work-policy", change)
	if err != nil || resumed.Status != "active" || resumed.Revision <= paused.Revision {
		t.Fatalf("resume %+v %v", resumed, err)
	}
	foreign := plan.Scope
	foreign.ChatID = "foreign"
	if _, err = f.QuerySchedule(ctx, foreign, plan.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("scope leak %v", err)
	}
}
func TestScheduleSkipsDowntimeInsteadOfCatchupStorm(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	plan, _ := newSchedule(t, f, "schedule-downtime")
	runs, err := f.ScheduleRuns(ctx, plan.Scope, plan.ID, 20)
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs %v %v", runs, err)
	}
	plan.Plan.StartAt = time.Now().UTC().Add(-time.Hour)
	plan.Plan.Runs = 100
	run := runs[0]
	run.DueAt = plan.Plan.StartAt
	run.NotifyUntil = run.DueAt.Add(30 * time.Minute)
	pb, _ := json.Marshal(plan)
	rb, _ := json.Marshal(run)
	if _, err = f.db.Exec(`UPDATE "`+f.schema+`".schedules SET body=$1 WHERE id=$2`, pb, plan.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.Exec(`UPDATE "`+f.schema+`".schedule_runs SET body=$1 WHERE schedule_id=$2`, rb, plan.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.Exec(`UPDATE "` + f.schema + `".jobs SET available_at=clock_timestamp() WHERE kind='schedule'`); err != nil {
		t.Fatal(err)
	}
	j, err := f.ClaimJob(ctx, []domain.JobKind{domain.JobSchedule}, "schedule-recovery", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	expired, err := f.PrepareScheduleRun(ctx, j)
	if err != nil || expired.Status != "expired" {
		t.Fatalf("expired %+v %v", expired, err)
	}
	current, err := f.QuerySchedule(ctx, plan.Scope, plan.ID)
	if err != nil || current.NextIndex < 60 {
		t.Fatalf("catchup %+v %v", current, err)
	}
	runs, err = f.ScheduleRuns(ctx, plan.Scope, plan.ID, 20)
	if err != nil || len(runs) != 2 {
		t.Fatalf("catchup fanout %d %v", len(runs), err)
	}
}
func TestScheduleQueryFreezesRenderedPayloadBeforeAmbiguousSend(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	in := domain.ScheduleInput{StartAt: time.Now().UTC().Add(time.Second), IntervalSeconds: 60, Runs: 2, Query: &domain.ScheduleQuery{Name: "gold_price_get", Arguments: json.RawMessage(`{"days":1}`)}}
	j, task, x := workTurn(t, f, "query-schedule", "alice", "schedule_create", in)
	if err := f.SyncToolPolicies(ctx, []pgstore.ToolPolicy{{Name: "schedule_create", SideEffect: true, AutoExecute: true, Digest: "work-policy"}, {Name: "gold_price_get", Digest: "read-policy"}}); err != nil {
		t.Fatal(err)
	}
	plan, err := f.CreateSchedule(ctx, j, task, x.Key, "work-policy", in)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.CompleteJob(ctx, j); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Until(plan.Plan.StartAt) + 10*time.Millisecond)
	runJob, err := f.ClaimJob(ctx, []domain.JobKind{domain.JobSchedule}, "query-renderer", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := f.PrepareScheduleRun(ctx, runJob)
	if err != nil || pending.Status != "rendering" || !pending.SendStartedAt.IsZero() {
		t.Fatalf("unrendered run claimed send %+v %v", pending, err)
	}
	frozen, err := f.FreezeScheduleRun(ctx, runJob, pending, "interactive", `{"body":{"elements":[]}}`)
	if err != nil || frozen.Content == "" || frozen.MessageType != "interactive" || frozen.SendStartedAt.IsZero() {
		t.Fatalf("freeze %+v %v", frozen, err)
	}
	again, err := f.PrepareScheduleRun(ctx, runJob)
	if err != nil || again.Content != frozen.Content || again.UUID != frozen.UUID || again.Status != "sending" {
		t.Fatalf("ambiguous retry rerenders %+v %v", again, err)
	}
	if _, err = f.FreezeScheduleRun(ctx, runJob, pending, "text", `{"text":"changed after send"}`); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("overwrite frozen ambiguous payload %v", err)
	}
}
func TestScheduleEditKeepsRunningAnchorAndMutationReplay(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	plan, _ := newSchedule(t, f, "edit-running-plan")
	time.Sleep(time.Until(plan.Plan.StartAt) + 10*time.Millisecond)
	job, err := f.ClaimJob(ctx, []domain.JobKind{domain.JobSchedule}, "running-edit", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.PrepareScheduleRun(ctx, job); err != nil {
		t.Fatal(err)
	}
	text := "new text"
	change := domain.ScheduleUpdateInput{ID: plan.ID, Action: "edit", Text: &text}
	j, task, x := workTurn(t, f, "edit-plan-request", "alice", "schedule_update", change)
	edited, err := f.UpdateSchedule(ctx, j, task, x.Key, "work-policy", change)
	if err != nil || edited.Plan.Text != text || edited.NextIndex != 1 {
		t.Fatalf("edit running %+v %v", edited, err)
	}
	replay, err := f.UpdateSchedule(ctx, j, task, x.Key, "work-policy", change)
	if err != nil || replay.Revision != edited.Revision {
		t.Fatalf("replayed edit %+v %v", replay, err)
	}
	stale, err := f.PrepareScheduleRun(ctx, job)
	if err != nil || stale.Status != "canceled" {
		t.Fatalf("old job survived edit %+v %v", stale, err)
	}
}
func TestScheduleFutureControlsAreEnforcedAtExecution(t *testing.T) {
	for _, control := range []string{"feature", "mute", "history", "source", "policy"} {
		t.Run(control, func(t *testing.T) {
			f := setup(t)
			ctx := context.Background()
			plan, _ := newSchedule(t, f, "control-"+control)
			if control == "policy" {
				if _, err := f.db.Exec(`UPDATE "` + f.schema + `".tool_policies SET enabled=false WHERE tool_name='schedule_create'`); err != nil {
					t.Fatal(err)
				}
			} else if control == "source" {
				e := event("edited-source", 2)
				e.Message.Source.MessageID = plan.Origin.MessageID
				e.Message.Source.AuthorID = "alice"
				accept(t, f, e)
			} else {
				entry := domain.CatalogEntry{Kind: domain.CatalogGroupConfig, Name: "mute_until", Revision: 1, Origin: plan.Origin, CatalogValue: domain.CatalogValue{Config: &domain.CatalogConfigValue{Value: time.Now().Add(time.Hour).Format(time.RFC3339Nano)}}}
				if control == "feature" {
					entry.Kind = domain.CatalogFeature
					entry.Name = "reminders"
					entry.Config = nil
					entry.Feature = &domain.CatalogFeatureValue{Enabled: false}
				}
				if control == "history" {
					entry.Name = "history_since"
					entry.Config.Value = time.Now().Format(time.RFC3339Nano)
				}
				b, _ := json.Marshal(entry)
				if _, err := f.db.Exec(`INSERT INTO "`+f.schema+`".catalog_entries(scope_key,kind,name,revision,deleted,creator,body,updated_at)VALUES($1,$2,$3,1,false,'alice',$4,clock_timestamp())`, plan.Scope.Key(), entry.Kind, entry.Name, b); err != nil {
					t.Fatal(err)
				}
			}
			time.Sleep(time.Until(plan.Plan.StartAt) + 10*time.Millisecond)
			j, err := f.ClaimJob(ctx, []domain.JobKind{domain.JobSchedule}, "control-worker", time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			run, err := f.PrepareScheduleRun(ctx, j)
			if err != nil || run.Status == "sending" || run.Status == "rendering" {
				t.Fatalf("control %s allowed send %+v %v", control, run, err)
			}
			after, err := f.QuerySchedule(ctx, plan.Scope, plan.ID)
			if err != nil {
				t.Fatal(err)
			}
			if (control == "source" || control == "history") && after.Status != "invalidated" {
				t.Fatalf("source survived %+v", after)
			}
			if control == "policy" && after.Status != "paused" {
				t.Fatalf("policy survived %+v", after)
			}
		})
	}
}
func TestScheduleListDerivesElapsedPlansBeforeStatusLimit(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	plan, _ := newSchedule(t, f, "elapsed-schedule")
	plan.Plan.StartAt = time.Now().UTC().Add(-time.Hour)
	plan.Plan.Runs = 1
	body, _ := json.Marshal(plan)
	if _, err := f.db.Exec(`UPDATE "`+f.schema+`".schedules SET body=$1 WHERE id=$2`, body, plan.ID); err != nil {
		t.Fatal(err)
	}
	rows, err := f.ListSchedules(ctx, plan.Scope, "completed", 1)
	if err != nil || len(rows) != 1 || rows[0].Status != "completed" {
		t.Fatalf("elapsed plan still active %+v %v", rows, err)
	}
	rows, err = f.ListSchedules(ctx, plan.Scope, "active", 1)
	if err != nil || len(rows) != 0 {
		t.Fatalf("status after limit %+v %v", rows, err)
	}
}
func TestScheduleSelectedReadPolicyIsFrozenAndRevocable(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	in := domain.ScheduleInput{StartAt: time.Now().UTC().Add(time.Second), IntervalSeconds: 60, Runs: 2, Query: &domain.ScheduleQuery{Name: "gold_price_get", Arguments: json.RawMessage(`{"days":1}`)}}
	j, task, x := workTurn(t, f, "query-policy-schedule", "alice", "schedule_create", in)
	if err := f.SyncToolPolicies(ctx, []pgstore.ToolPolicy{{Name: "schedule_create", SideEffect: true, AutoExecute: true, Digest: "work-policy"}, {Name: "gold_price_get", Digest: "read-policy"}}); err != nil {
		t.Fatal(err)
	}
	plan, err := f.CreateSchedule(ctx, j, task, x.Key, "work-policy", in)
	if err != nil || plan.QueryPolicyDigest != "read-policy" {
		t.Fatalf("unbound read grant %+v %v", plan, err)
	}
	if err = f.CompleteJob(ctx, j); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Until(plan.Plan.StartAt) + 10*time.Millisecond)
	job, err := f.ClaimJob(ctx, []domain.JobKind{domain.JobSchedule}, "policy-check", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	run, err := f.PrepareScheduleRun(ctx, job)
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := f.FreezeScheduleRun(ctx, job, run, "text", `{"text":"bound quote"}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.Exec(`UPDATE "` + f.schema + `".tool_policies SET enabled=false WHERE tool_name='gold_price_get'`); err != nil {
		t.Fatal(err)
	}
	if err = f.CheckScheduleRun(ctx, job, frozen); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("revoked leaf replayed %v", err)
	}
	after, err := f.PrepareScheduleRun(ctx, job)
	if err != nil || after.Status != "canceled" {
		t.Fatalf("revoked leaf prepared %+v %v", after, err)
	}
	plan, err = f.QuerySchedule(ctx, plan.Scope, plan.ID)
	if err != nil || plan.Status != "paused" {
		t.Fatalf("revoked leaf parent %+v %v", plan, err)
	}
}
func TestScheduleQueryFeatureStopsFrozenPayloadRetry(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	in := domain.ScheduleInput{StartAt: time.Now().UTC().Add(time.Second), IntervalSeconds: 60, Runs: 2, Query: &domain.ScheduleQuery{Name: "gold_price_get", Arguments: json.RawMessage(`{"days":1}`)}}
	j, task, x := workTurn(t, f, "query-feature-schedule", "alice", "schedule_create", in)
	if err := f.SyncToolPolicies(ctx, []pgstore.ToolPolicy{{Name: "schedule_create", SideEffect: true, AutoExecute: true, Digest: "work-policy"}, {Name: "gold_price_get", Digest: "read-policy"}}); err != nil {
		t.Fatal(err)
	}
	plan, err := f.CreateSchedule(ctx, j, task, x.Key, "work-policy", in)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.CompleteJob(ctx, j); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Until(plan.Plan.StartAt) + 10*time.Millisecond)
	job, err := f.ClaimJob(ctx, []domain.JobKind{domain.JobSchedule}, "feature-check", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	run, err := f.PrepareScheduleRun(ctx, job)
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := f.FreezeScheduleRun(ctx, job, run, "text", `{"text":"bound quote"}`)
	if err != nil {
		t.Fatal(err)
	}
	entry := domain.CatalogEntry{Kind: domain.CatalogFeature, Name: "public_data", Revision: 1, Origin: plan.Origin, CatalogValue: domain.CatalogValue{Feature: &domain.CatalogFeatureValue{Enabled: false}}}
	b, _ := json.Marshal(entry)
	if _, err = f.db.Exec(`INSERT INTO "`+f.schema+`".catalog_entries(scope_key,kind,name,revision,deleted,creator,body,updated_at)VALUES($1,'feature','public_data',1,false,'alice',$2,clock_timestamp())`, plan.Scope.Key(), b); err != nil {
		t.Fatal(err)
	}
	if err = f.CheckScheduleRun(ctx, job, frozen); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("disabled query feature replayed %v", err)
	}
	after, err := f.PrepareScheduleRun(ctx, job)
	if err != nil || after.Status != "expired" || after.Content != frozen.Content || after.SendStartedAt.IsZero() {
		t.Fatalf("feature skip erased ambiguous payload %+v %v", after, err)
	}
}
func TestScheduleLateLeaseReceiptIsPreservedWithoutAuthorityToReplay(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	plan, _ := newSchedule(t, f, "late-lease-schedule")
	time.Sleep(time.Until(plan.Plan.StartAt) + 10*time.Millisecond)
	job, err := f.ClaimJob(ctx, []domain.JobKind{domain.JobSchedule}, "lost-owner", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	run, err := f.PrepareScheduleRun(ctx, job)
	if err != nil {
		t.Fatal(err)
	}
	forged := job
	forged.Payload = json.RawMessage(`{"schedule_id":"forged","revision":1,"index":0}`)
	if _, err = f.PrepareScheduleRun(ctx, forged); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("forged persisted job %v", err)
	}
	if _, err = f.db.Exec(`UPDATE "`+f.schema+`".jobs SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, job.ID); err != nil {
		t.Fatal(err)
	}
	if err = f.FinishScheduleRun(ctx, job, run, "late-confirmed-message"); !errors.Is(err, domain.ErrStaleClaim) {
		t.Fatalf("lost lease authority %v", err)
	}
	reclaimed, err := f.ClaimJob(ctx, []domain.JobKind{domain.JobSchedule}, "new-owner", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	observed, err := f.PrepareScheduleRun(ctx, reclaimed)
	if err != nil || observed.Status != "sent" || observed.MessageID != "late-confirmed-message" {
		t.Fatalf("lost receipt replay %+v %v", observed, err)
	}
}
func TestScheduleFinalLeaseCrashReconcilesUnsentRendering(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	in := domain.ScheduleInput{StartAt: time.Now().UTC().Add(time.Second), IntervalSeconds: 60, Runs: 1, Query: &domain.ScheduleQuery{Name: "gold_price_get", Arguments: json.RawMessage(`{"days":1}`)}}
	j, task, x := workTurn(t, f, "query-crash-schedule", "alice", "schedule_create", in)
	if err := f.SyncToolPolicies(ctx, []pgstore.ToolPolicy{{Name: "schedule_create", SideEffect: true, AutoExecute: true, Digest: "work-policy"}, {Name: "gold_price_get", Digest: "read-policy"}}); err != nil {
		t.Fatal(err)
	}
	plan, err := f.CreateSchedule(ctx, j, task, x.Key, "work-policy", in)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.CompleteJob(ctx, j); err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.Exec(`UPDATE "` + f.schema + `".jobs SET max_attempts=1 WHERE kind='schedule'`); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Until(plan.Plan.StartAt) + 10*time.Millisecond)
	job, err := f.ClaimJob(ctx, []domain.JobKind{domain.JobSchedule}, "crashing-renderer", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	run, err := f.PrepareScheduleRun(ctx, job)
	if err != nil || run.Status != "rendering" {
		t.Fatalf("prepare %+v %v", run, err)
	}
	if _, err = f.db.Exec(`UPDATE "`+f.schema+`".jobs SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.ClaimJob(ctx, []domain.JobKind{domain.JobSchedule}, "restarted-worker", time.Minute); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("exhausted job revived %v", err)
	}
	runs, err := f.ScheduleRuns(ctx, plan.Scope, plan.ID, 20)
	if err != nil || len(runs) != 1 || runs[0].Status != "failed" || runs[0].FailureReason != "render_attempts_exhausted" || !runs[0].SendStartedAt.IsZero() {
		t.Fatalf("orphan after restart %+v %v", runs, err)
	}
}
