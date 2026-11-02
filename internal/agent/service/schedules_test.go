package service_test

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/pgstore"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/service"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestScheduleQueryAmbiguousSendRetriesFrozenPayloadWithoutRequery(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	now := time.Now().UTC()
	m := domain.Message{Source: domain.SourceRef{Scope: f.scope, MessageID: "schedule-business", AuthorID: "member", Version: 1, OccurredAt: now}, EventID: "schedule-business", Text: "每天发金价", UpdatedAt: now}
	if _, err := f.store.Accept(ctx, domain.Event{ID: m.EventID, Type: domain.EventMessage, Message: m, OccurredAt: now}); err != nil {
		t.Fatal(err)
	}
	task, err := f.store.CreateTask(ctx, m, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	j, err := f.store.ClaimJob(ctx, []domain.JobKind{domain.JobAgentTurn}, "schedule-test", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	task, err = f.store.StartTurn(ctx, j, domain.TurnRequest{TaskID: task.ID, Revision: task.Revision})
	if err != nil {
		t.Fatal(err)
	}
	policy := pgstore.ToolPolicy{Name: "schedule_create", SideEffect: true, AutoExecute: true, Digest: "schedule-query-policy"}
	if err = f.store.SyncToolPolicies(ctx, []pgstore.ToolPolicy{policy, {Name: "gold_price_get", Digest: "read-policy"}}); err != nil {
		t.Fatal(err)
	}
	input := domain.ScheduleInput{StartAt: time.Now().Add(time.Second), IntervalSeconds: 60, Runs: 2, Query: &domain.ScheduleQuery{Name: "gold_price_get", Arguments: json.RawMessage(`{"days":1}`)}}
	raw, _ := json.Marshal(input)
	x, _, err := f.store.BeginTool(ctx, j, task, policy.Name, string(raw), "", true, policy.Digest)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := f.store.CreateSchedule(ctx, j, task, x.Key, policy.Digest, input)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.store.CompleteJob(ctx, j); err != nil {
		t.Fatal(err)
	}
	calls := &atomic.Int32{}
	svc, err := service.New(service.Options{Store: f.store, Engine: f.eng, Messenger: f.messenger, Owner: "query-schedule", ScheduleRender: func(_ context.Context, run domain.ScheduleRun) (string, string, error) {
		n := calls.Add(1)
		if run.Query == nil || run.Query.Name != "gold_price_get" {
			t.Fatal("lost typed plan")
		}
		return "text", fmt.Sprintf(`{"text":"price query %d"}`, n), nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Until(plan.Plan.StartAt) + 10*time.Millisecond)
	f.messenger.failFinal = true
	if err = svc.Step(ctx, domain.JobSchedule); err == nil {
		t.Fatal("ambiguous send failure swallowed")
	}
	if _, err = f.db.Exec(`UPDATE "` + f.schema + `".jobs SET available_at=clock_timestamp() WHERE kind='schedule' AND status='pending'`); err != nil {
		t.Fatal(err)
	}
	if err = svc.Step(ctx, domain.JobSchedule); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || len(f.messenger.sends) != 2 || f.messenger.sends[0].uuid != f.messenger.sends[1].uuid || f.messenger.sends[0].content != f.messenger.sends[1].content {
		t.Fatalf("requeried ambiguous send calls=%d sends=%v", calls.Load(), f.messenger.sends)
	}
	runs, err := f.store.ScheduleRuns(ctx, f.scope, plan.ID, 20)
	if err != nil || runs[0].MessageID == "" || runs[0].Content != f.messenger.sends[0].content {
		t.Fatalf("receipt %+v %v", runs, err)
	}
}
func TestScheduleDefiniteRenderingFailuresAreDurableAndNeverSend(t *testing.T) {
	for _, tc := range []struct {
		name         string
		renderErr    error
		attempts     int
		parentStatus string
	}{
		{"missing", domain.ErrNotFound, 8, "paused"}, {"denied", domain.ErrUnauthorized, 8, "paused"}, {"invalid", domain.ErrInvalid, 8, "paused"}, {"exhausted", fmt.Errorf("sensitive provider diagnostic"), 1, "completed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := setup(t)
			ctx := context.Background()
			now := time.Now().UTC()
			m := domain.Message{Source: domain.SourceRef{Scope: f.scope, MessageID: "schedule-failed-render", AuthorID: "member", Version: 1, OccurredAt: now}, EventID: "schedule-failed-render", Text: "每天发金价", UpdatedAt: now}
			if _, err := f.store.Accept(ctx, domain.Event{ID: m.EventID, Type: domain.EventMessage, Message: m, OccurredAt: now}); err != nil {
				t.Fatal(err)
			}
			task, err := f.store.CreateTask(ctx, m, now.Add(time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			j, err := f.store.ClaimJob(ctx, []domain.JobKind{domain.JobAgentTurn}, "render-failure-create", time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			task, err = f.store.StartTurn(ctx, j, domain.TurnRequest{TaskID: task.ID, Revision: task.Revision})
			if err != nil {
				t.Fatal(err)
			}
			policies := []pgstore.ToolPolicy{{Name: "schedule_create", SideEffect: true, AutoExecute: true, Digest: "schedule-policy"}, {Name: "gold_price_get", Digest: "read-policy"}}
			if err = f.store.SyncToolPolicies(ctx, policies); err != nil {
				t.Fatal(err)
			}
			input := domain.ScheduleInput{StartAt: time.Now().Add(time.Second), IntervalSeconds: 60, Runs: 1, Query: &domain.ScheduleQuery{Name: "gold_price_get", Arguments: json.RawMessage(`{"days":1}`)}}
			raw, _ := json.Marshal(input)
			x, _, err := f.store.BeginTool(ctx, j, task, "schedule_create", string(raw), "", true, "schedule-policy")
			if err != nil {
				t.Fatal(err)
			}
			plan, err := f.store.CreateSchedule(ctx, j, task, x.Key, "schedule-policy", input)
			if err != nil {
				t.Fatal(err)
			}
			if err = f.store.CompleteJob(ctx, j); err != nil {
				t.Fatal(err)
			}
			if _, err = f.db.Exec(`UPDATE "`+f.schema+`".jobs SET max_attempts=$1 WHERE kind='schedule'`, tc.attempts); err != nil {
				t.Fatal(err)
			}
			svc, err := service.New(service.Options{Store: f.store, Engine: f.eng, Messenger: f.messenger, Owner: "render-failure", ScheduleRender: func(context.Context, domain.ScheduleRun) (string, string, error) { return "", "", tc.renderErr }})
			if err != nil {
				t.Fatal(err)
			}
			time.Sleep(time.Until(plan.Plan.StartAt) + 10*time.Millisecond)
			_ = svc.Step(ctx, domain.JobSchedule)
			runs, err := f.store.ScheduleRuns(ctx, f.scope, plan.ID, 20)
			if err != nil || len(runs) != 1 || runs[0].Status != "failed" || runs[0].FailureReason == "" || !runs[0].SendStartedAt.IsZero() || len(f.messenger.sends) != 0 {
				t.Fatalf("orphan or send %+v %v sends=%v", runs, err, f.messenger.sends)
			}
			if strings.Contains(runs[0].FailureReason, "sensitive") {
				t.Fatal("raw provider failure persisted")
			}
			after, err := f.store.QuerySchedule(ctx, f.scope, plan.ID)
			if err != nil || after.Status != tc.parentStatus {
				t.Fatalf("parent %+v %v", after, err)
			}
		})
	}
}
