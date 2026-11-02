package pgstore_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/pgstore"
)

func TestTaskOwnerRevisionAndUnknownEffectSurviveNewFence(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	e := event("request", 1)
	accept(t, f, e)
	task, err := f.CreateTask(ctx, e.Message, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	j, err := f.ClaimJob(ctx, []domain.JobKind{domain.JobAgentTurn}, "worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	a, err := f.FreezeApproval(ctx, j, task, "business_write", `{"amount":1}`)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.BindInterrupt(ctx, j, task, a.ID, "native-interrupt"); err != nil {
		t.Fatal(err)
	}
	if _, err = f.ControlTask(ctx, e.Message.Source.Scope, "other", domain.ControlAction{Action: "approve", TaskID: task.ID, Revision: 1, ApprovalID: a.ID}, domain.SourceRef{}, ""); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("other author approved: %v", err)
	}
	if _, err = f.ControlTask(ctx, e.Message.Source.Scope, "author", domain.ControlAction{Action: "approve", TaskID: task.ID, Revision: 9, ApprovalID: a.ID}, domain.SourceRef{}, ""); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale revision approved: %v", err)
	}
	task, err = f.ControlTask(ctx, e.Message.Source.Scope, "author", domain.ControlAction{Action: "approve", TaskID: task.ID, Revision: 1, ApprovalID: a.ID}, domain.SourceRef{}, "")
	if err != nil {
		t.Fatal(err)
	}
	ledger, reused, err := f.BeginTool(ctx, j, task, "business_write", `{"amount":1}`, a.ID, true)
	if err != nil || reused {
		t.Fatalf("begin effect %+v %v %v", ledger, reused, err)
	}
	if _, _, err = f.BeginTool(ctx, j, task, "business_write", `{"amount":1}`, a.ID, true); !errors.Is(err, domain.ErrEffectUnknown) {
		t.Fatalf("inflight side effect replayed: %v", err)
	}
	_, err = f.ControlTask(ctx, e.Message.Source.Scope, "author", domain.ControlAction{Action: "cancel", TaskID: task.ID, Revision: 1}, domain.SourceRef{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = f.FinishTool(ctx, j, task, ledger, "receipt", nil); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("late receipt advanced canceled task: %v", err)
	}
	audit, err := f.ToolExecution(ctx, task.Scope, task.ID, ledger.Key)
	if err != nil || audit.Result != "receipt" || audit.Status != "completed_late" {
		t.Fatalf("late receipt not audited: %+v %v", audit, err)
	}
}

func TestRetrievedSourceEditInvalidatesFrozenApproval(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	root := event("root", 1)
	root.Message.Source.MessageID = "root"
	accept(t, f, root)
	material := event("material", 1)
	material.Message.Source.MessageID = "material"
	material.Message.Source.AuthorID = "contributor"
	accept(t, f, material)
	task, e := f.CreateTask(ctx, root.Message, time.Now().Add(time.Hour))
	if e != nil {
		t.Fatal(e)
	}
	j, e := f.ClaimJob(ctx, []domain.JobKind{domain.JobAgentTurn}, "worker", time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	if e = f.AddTurnSources(ctx, j, task, []domain.SourceRef{material.Message.Source}); e != nil {
		t.Fatal(e)
	}
	a, e := f.FreezeApproval(ctx, j, task, "write", `{"amount":1}`)
	if e != nil {
		t.Fatal(e)
	}
	f.BindInterrupt(ctx, j, task, a.ID, "native")
	material.ID = "material_edit"
	material.Message.EventID = material.ID
	material.Message.Source.Version = 2
	material.Message.UpdatedAt = material.Message.UpdatedAt.Add(time.Second)
	material.Message.Text = "edited"
	accept(t, f, material)
	if _, e = f.ControlTask(ctx, task.Scope, task.OwnerID, domain.ControlAction{Action: "approve", TaskID: task.ID, Revision: 1, ApprovalID: a.ID}, domain.SourceRef{}, ""); !errors.Is(e, domain.ErrStaleSource) {
		t.Fatalf("retrieved source edit did not revoke eligibility: %v", e)
	}
}

func TestRuntimeTaskLockWaitCannotResurrectExpiredClaim(t *testing.T) {
	f := setup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	e := event("request", 1)
	accept(t, f, e)
	task, err := f.CreateTask(ctx, e.Message, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	j, err := f.ClaimJob(ctx, []domain.JobKind{domain.JobAgentTurn}, "worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.Exec(`UPDATE "`+f.schema+`".jobs SET lease_until=clock_timestamp()+interval '500 milliseconds' WHERE id=$1`, j.ID); err != nil {
		t.Fatal(err)
	}
	tx, err := f.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var pid int
	if err = tx.QueryRowContext(ctx, `SELECT pg_backend_pid() FROM "`+f.schema+`".tasks WHERE task_id=$1 FOR UPDATE`, task.ID).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, e := f.FreezeApproval(ctx, j, task, "write", `{"amount":1}`); done <- e }()
	waitForPGCondition(t, ctx, f.db, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))`, pid)
	waitForPGCondition(t, ctx, f.db, `SELECT clock_timestamp()>=lease_until FROM "`+f.schema+`".jobs WHERE id=$1`, j.ID)
	tx.Commit()
	if err = <-done; !errors.Is(err, domain.ErrStaleClaim) {
		t.Fatalf("task lock wait resurrected expired execution: %v", err)
	}
}

func TestTaskFactsExposeDurableEffectProgress(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	e := event("progress", 1)
	accept(t, f, e)
	task, err := f.CreateTask(ctx, e.Message, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	j, err := f.ClaimJob(ctx, []domain.JobKind{domain.JobAgentTurn}, "worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	a, err := f.FreezeApproval(ctx, j, task, "write", `{"amount":1}`)
	if err != nil {
		t.Fatal(err)
	}
	f.BindInterrupt(ctx, j, task, a.ID, "native")
	if _, err = f.ControlTask(ctx, task.Scope, task.OwnerID, domain.ControlAction{Action: "approve", TaskID: task.ID, Revision: 1, ApprovalID: a.ID}, domain.SourceRef{}, ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err = f.BeginTool(ctx, j, task, "write", `{"amount":1}`, a.ID, true); err != nil {
		t.Fatal(err)
	}
	facts, err := f.TaskFacts(ctx, task.Scope, task.ID)
	if err != nil || !strings.Contains(string(facts), "running_effect") {
		t.Fatalf("durable in-flight effect missing from task progress: %s %v", facts, err)
	}
}

func TestTaskFactsPrioritizeUnresolvedExecutionsOverCompletedReadHistory(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	root := event("progress_history", 1)
	accept(t, f, root)
	task, err := f.CreateTask(ctx, root.Message, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	j, err := f.ClaimJob(ctx, []domain.JobKind{domain.JobAgentTurn}, "progress", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	args, key := "", ""
	for n := 1; n < 10000; n++ {
		b, _ := json.Marshal(map[string]int{"amount": n})
		candidate := domain.StableID(task.ID, "write", string(b))
		if candidate[0] >= 'e' {
			args, key = string(b), candidate
			break
		}
	}
	if args == "" {
		t.Fatal("could not select effect fixture")
	}
	lower := 0
	for n := 0; lower < 128 && n < 1024; n++ {
		read, reused, e := f.BeginTool(ctx, j, task, "task_facts", "{}", "", false)
		if e != nil || reused {
			t.Fatalf("fresh read: %+v %v %v", read, reused, e)
		}
		if e = f.FinishTool(ctx, j, task, read, "receipt_body_should_not_leak", nil); e != nil {
			t.Fatal(e)
		}
		if read.Key < key {
			lower++
		}
	}
	if lower < 128 {
		t.Fatal("insufficient completed read history")
	}
	a, err := f.FreezeApproval(ctx, j, task, "write", args)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.BindInterrupt(ctx, j, task, a.ID, "progress_interrupt"); err != nil {
		t.Fatal(err)
	}
	if _, err = f.ControlTask(ctx, task.Scope, task.OwnerID, domain.ControlAction{Action: "approve", TaskID: task.ID, Revision: task.Revision, ApprovalID: a.ID}, domain.SourceRef{}, ""); err != nil {
		t.Fatal(err)
	}
	effect, _, err := f.BeginTool(ctx, j, task, "write", args, a.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	read, _, err := f.BeginTool(ctx, j, task, "task_facts", "{}", "", false)
	if err != nil {
		t.Fatal(err)
	}
	check := func(effectStatus string) {
		t.Helper()
		facts, e := f.TaskFacts(ctx, task.Scope, task.ID)
		if e != nil {
			t.Fatal(e)
		}
		var projection struct {
			Executions []struct{ ExecutionKey, Status string }
		}
		if e = json.Unmarshal(facts, &projection); e != nil {
			t.Fatal(e)
		}
		if len(projection.Executions) != 128 || strings.Contains(string(facts), "receipt_body_should_not_leak") {
			t.Fatal("execution projection lost its count/content bounds")
		}
		seenEffect, seenRead := false, false
		for _, x := range projection.Executions {
			seenEffect = seenEffect || x.ExecutionKey == effect.Key && x.Status == effectStatus
			seenRead = seenRead || x.ExecutionKey == read.Key && x.Status == "running_read"
		}
		if !seenEffect || !seenRead {
			t.Fatalf("completed reads hid unresolved executions: effect=%v read=%v", seenEffect, seenRead)
		}
	}
	check("running_effect")
	if err = f.FinishTool(ctx, j, task, effect, "", errors.New("ambiguous external failure")); err != nil {
		t.Fatal(err)
	}
	check("unknown")
}

func TestPolicySynchronizationPreservesRevocationAcrossRestartAndReaddition(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	initial := []pgstore.ToolPolicy{{Name: "write", SideEffect: true, Digest: "v1"}, {Name: "removed", SideEffect: true, Digest: "v1"}}
	if e := f.SyncToolPolicies(ctx, initial); e != nil {
		t.Fatal(e)
	}
	if e := f.CheckToolPolicy(ctx, "write", "v1", true); e != nil {
		t.Fatalf("first configured tool not granted: %v", e)
	}
	if _, e := f.db.Exec(`UPDATE "` + f.schema + `".tool_policies SET enabled=false WHERE tool_name='write'`); e != nil {
		t.Fatal(e)
	}
	if e := f.SyncToolPolicies(ctx, initial); e != nil {
		t.Fatal(e)
	}
	if e := f.CheckToolPolicy(ctx, "write", "v1", true); !errors.Is(e, domain.ErrUnauthorized) {
		t.Fatalf("same config restarted a revoked grant: %v", e)
	}
	if e := f.SyncToolPolicies(ctx, initial[:1]); e != nil {
		t.Fatal(e)
	}
	if e := f.SyncToolPolicies(ctx, initial); e != nil {
		t.Fatal(e)
	}
	if e := f.CheckToolPolicy(ctx, "removed", "v1", true); !errors.Is(e, domain.ErrUnauthorized) {
		t.Fatalf("readding a removed tool reauthorized it: %v", e)
	}
	updated := []pgstore.ToolPolicy{{Name: "write", SideEffect: true, Digest: "v2"}, {Name: "first_seen", SideEffect: true, Digest: "v1"}}
	if e := f.SyncToolPolicies(ctx, updated); e != nil {
		t.Fatal(e)
	}
	if e := f.CheckToolPolicy(ctx, "write", "v2", true); !errors.Is(e, domain.ErrUnauthorized) {
		t.Fatalf("digest change reauthorized revoked tool: %v", e)
	}
	if e := f.CheckToolPolicy(ctx, "first_seen", "v1", true); e != nil {
		t.Fatalf("new configured tool not granted: %v", e)
	}
	if _, e := f.db.Exec(`UPDATE "` + f.schema + `".tool_policies SET enabled=true WHERE tool_name='write'`); e != nil {
		t.Fatal(e)
	}
	if e := f.CheckToolPolicy(ctx, "write", "v2", true); e != nil {
		t.Fatalf("explicit operator grant not honored: %v", e)
	}
	if e := f.CheckToolPolicy(ctx, "write", "v1", true); !errors.Is(e, domain.ErrUnauthorized) {
		t.Fatalf("old digest resurrected after operator grant: %v", e)
	}
	if e := f.SyncToolPolicies(ctx, nil); e != nil {
		t.Fatal(e)
	}
	if e := f.SyncToolPolicies(ctx, updated); e != nil {
		t.Fatal(e)
	}
	if e := f.CheckToolPolicy(ctx, "first_seen", "v1", true); !errors.Is(e, domain.ErrUnauthorized) {
		t.Fatalf("empty config and readdition reauthorized tool: %v", e)
	}
}
