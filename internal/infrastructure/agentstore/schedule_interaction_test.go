package agentstore

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	scheduleapp "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/schedule"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	scheduleinfra "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/schedule"
	uuid "github.com/satori/go.uuid"
)

type completeScheduleInteractionRequest struct {
	Request agentruntime.ScheduleInteractionRequest
	Outcome agentruntime.ScheduleInteractionOutcome
}

func (r *Repository) CompleteScheduleInteraction(
	ctx context.Context,
	req completeScheduleInteractionRequest,
) (agentruntime.ScheduleInteractionOutcome, error) {
	return r.ExecuteScheduleInteraction(
		ctx,
		req.Request,
		func(context.Context, agentruntime.ScheduleEditTrustedInput) (agentruntime.ScheduleInteractionOutcome, error) {
			return req.Outcome, nil
		},
	)
}

func TestScheduleInteractionAtomicExecuteRejectsStaleClaimerBeforeExecutor(t *testing.T) {
	f, _, first := newScheduleInteractionFixture(t)
	first.RunningTTL = time.Second
	if claim, err := f.repo.ClaimScheduleInteraction(context.Background(), first); err != nil ||
		claim.State != agentruntime.ScheduleClaimAcquired {
		t.Fatalf("first claim = %#v, %v", claim, err)
	}

	reclaimed := first
	reclaimed.ClaimID = "claim-reclaimed-" + uuid.NewV4().String()
	reclaimed.EventID = "event-reclaimed-" + uuid.NewV4().String()
	reclaimed.ResolvedAt = first.ResolvedAt.Add(first.RunningTTL + time.Second)
	if claim, err := f.repo.ClaimScheduleInteraction(context.Background(), reclaimed); err != nil ||
		claim.State != agentruntime.ScheduleClaimAcquired {
		t.Fatalf("reclaimed claim = %#v, %v", claim, err)
	}

	var executions atomic.Int32
	if _, err := f.repo.ExecuteScheduleInteraction(
		context.Background(),
		first,
		func(context.Context, agentruntime.ScheduleEditTrustedInput) (agentruntime.ScheduleInteractionOutcome, error) {
			executions.Add(1)
			return validScheduleOutcome(first, "task-1", "new-name"), nil
		},
	); !errors.Is(err, agentruntime.ErrScheduleInteractionClaimLost) {
		t.Fatalf("stale ExecuteScheduleInteraction() error = %v, want claim lost", err)
	}
	if executions.Load() != 0 {
		t.Fatalf("stale executor calls = %d, want 0", executions.Load())
	}

	if _, err := f.repo.ExecuteScheduleInteraction(
		context.Background(),
		reclaimed,
		func(context.Context, agentruntime.ScheduleEditTrustedInput) (agentruntime.ScheduleInteractionOutcome, error) {
			executions.Add(1)
			return validScheduleOutcome(reclaimed, "task-1", "new-name"), nil
		},
	); err != nil {
		t.Fatalf("reclaimed ExecuteScheduleInteraction() error = %v", err)
	}
	if executions.Load() != 1 {
		t.Fatalf("total executor calls = %d, want 1", executions.Load())
	}
}

func TestScheduleInteractionAtomicExecuteSerializesClaimBehindExecution(t *testing.T) {
	f, _, request := newScheduleInteractionFixture(t)
	if _, err := f.repo.ClaimScheduleInteraction(context.Background(), request); err != nil {
		t.Fatal(err)
	}

	entered := make(chan struct{})
	release := make(chan struct{})
	executeDone := make(chan error, 1)
	go func() {
		_, err := f.repo.ExecuteScheduleInteraction(
			context.Background(),
			request,
			func(context.Context, agentruntime.ScheduleEditTrustedInput) (agentruntime.ScheduleInteractionOutcome, error) {
				close(entered)
				<-release
				return validScheduleOutcome(request, "task-1", "new-name"), nil
			},
		)
		executeDone <- err
	}()
	<-entered

	replay := request
	replay.ClaimID = "claim-blocked-" + uuid.NewV4().String()
	replay.EventID = "event-blocked-" + uuid.NewV4().String()
	claimDone := make(chan struct {
		claim agentruntime.ScheduleInteractionClaim
		err   error
	}, 1)
	go func() {
		claim, err := f.repo.ClaimScheduleInteraction(context.Background(), replay)
		claimDone <- struct {
			claim agentruntime.ScheduleInteractionClaim
			err   error
		}{claim: claim, err: err}
	}()

	select {
	case result := <-claimDone:
		t.Fatalf("claim returned while execution lock was held: %#v, %v", result.claim, result.err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if err := <-executeDone; err != nil {
		t.Fatalf("atomic execution error = %v", err)
	}
	result := <-claimDone
	if result.err != nil || result.claim.State != agentruntime.ScheduleClaimCompleted {
		t.Fatalf("claim after commit = %#v, %v; want completed", result.claim, result.err)
	}
}

func TestScheduleInteractionAtomicExecuteRollsBackScheduleAndCanRetry(t *testing.T) {
	f, _, request := newScheduleInteractionFixture(t)
	service := createScheduleServiceFixture(t, f, "task-1", "old-name")
	capability := scheduleapp.NewRuntimeScheduleEditCapability(service)
	if _, err := f.repo.ClaimScheduleInteraction(context.Background(), request); err != nil {
		t.Fatal(err)
	}

	_, err := f.repo.ExecuteScheduleInteraction(
		context.Background(),
		request,
		func(
			ctx context.Context,
			trusted agentruntime.ScheduleEditTrustedInput,
		) (agentruntime.ScheduleInteractionOutcome, error) {
			if _, executeErr := capability.ExecuteScheduleEdit(ctx, request.ActorOpenID, trusted); executeErr != nil {
				return agentruntime.ScheduleInteractionOutcome{}, executeErr
			}
			outcome := validScheduleOutcome(request, trusted.TaskID, "new-name")
			outcome.Result = json.RawMessage(`{"status":"updated","task_id":"task-1"}`)
			return outcome, nil
		},
	)
	if !errors.Is(err, agentruntime.ErrInteractionConflict) {
		t.Fatalf("forced finalization failure = %v, want conflict", err)
	}
	assertScheduledTaskName(t, f, "task-1", "old-name")
	assertScheduleInteractionStillWaiting(t, f)

	if err := f.repo.FailScheduleInteraction(context.Background(), agentruntime.FailScheduleInteractionRequest{
		Request: request, ErrorText: "forced rollback",
	}); err != nil {
		t.Fatalf("FailScheduleInteraction() error = %v", err)
	}
	retry := request
	retry.ClaimID = "claim-retry-" + uuid.NewV4().String()
	retry.EventID = "event-retry-" + uuid.NewV4().String()
	retry.ResolvedAt = request.ResolvedAt.Add(time.Second)
	if claim, claimErr := f.repo.ClaimScheduleInteraction(context.Background(), retry); claimErr != nil ||
		claim.State != agentruntime.ScheduleClaimAcquired {
		t.Fatalf("retry claim = %#v, %v", claim, claimErr)
	}
	if _, err := f.repo.ExecuteScheduleInteraction(
		context.Background(),
		retry,
		func(
			ctx context.Context,
			trusted agentruntime.ScheduleEditTrustedInput,
		) (agentruntime.ScheduleInteractionOutcome, error) {
			result, executeErr := capability.ExecuteScheduleEdit(ctx, retry.ActorOpenID, trusted)
			if executeErr != nil {
				return agentruntime.ScheduleInteractionOutcome{}, executeErr
			}
			outcome := validScheduleOutcome(retry, trusted.TaskID, "new-name")
			outcome.Result = result
			return outcome, nil
		},
	); err != nil {
		t.Fatalf("retry ExecuteScheduleInteraction() error = %v", err)
	}
	assertScheduledTaskName(t, f, "task-1", "new-name")

	ordinaryName := "ordinary-name"
	if _, err := service.UpdateTask(context.Background(), &scheduleapp.UpdateTaskRequest{
		ID: "task-1", ActorOpenID: request.ActorOpenID, Name: &ordinaryName,
	}); err != nil {
		t.Fatalf("ordinary UpdateTask() error = %v", err)
	}
	assertScheduledTaskName(t, f, "task-1", ordinaryName)
}

func TestScheduleInteractionClaimCompleteAndReplay(t *testing.T) {
	f, start, request := newScheduleInteractionFixture(t)

	inspection, err := f.repo.InspectScheduleInteraction(context.Background(), request)
	if err != nil {
		t.Fatalf("InspectScheduleInteraction() error = %v", err)
	}
	if !equalJSONDocument(inspection.TrustedInput, start.TrustedInput) {
		t.Fatal("inspected trusted input differs from persisted wait input")
	}
	claim, err := f.repo.ClaimScheduleInteraction(context.Background(), request)
	if err != nil {
		t.Fatalf("ClaimScheduleInteraction(first) error = %v", err)
	}
	if claim.State != agentruntime.ScheduleClaimAcquired {
		t.Fatalf("first claim state = %q, want acquired", claim.State)
	}
	running, err := f.repo.ClaimScheduleInteraction(context.Background(), request)
	if err != nil {
		t.Fatalf("ClaimScheduleInteraction(running replay) error = %v", err)
	}
	if running.State != agentruntime.ScheduleClaimRunning {
		t.Fatalf("running replay state = %q, want running", running.State)
	}

	outcome := agentruntime.ScheduleInteractionOutcome{
		Status: "updated", TaskID: "task-1", InteractionID: start.InteractionID,
		Action: agentruntime.ScheduleInteractionConfirm,
		Result: json.RawMessage(`{"status":"updated","task_id":"task-1","name":"new-name"}`),
	}
	completed, err := f.repo.CompleteScheduleInteraction(context.Background(), completeScheduleInteractionRequest{
		Request: request,
		Outcome: outcome,
	})
	if err != nil {
		t.Fatalf("CompleteScheduleInteraction() error = %v", err)
	}
	if completed.Status != outcome.Status || completed.TaskID != outcome.TaskID {
		t.Fatalf("completed outcome = %#v, want %#v", completed, outcome)
	}

	replayRequest := request
	replayRequest.ClaimID = "claim-replay-" + uuid.NewV4().String()
	replay, err := f.repo.ClaimScheduleInteraction(context.Background(), replayRequest)
	if err != nil {
		t.Fatalf("ClaimScheduleInteraction(completed replay) error = %v", err)
	}
	if replay.State != agentruntime.ScheduleClaimCompleted || replay.Outcome.Status != outcome.Status {
		t.Fatalf("completed replay = %#v", replay)
	}
	if replay.ResolvedActorOpenID != request.ActorOpenID {
		t.Fatalf("completed replay actor = %q, want %q", replay.ResolvedActorOpenID, request.ActorOpenID)
	}

	var run model.AgentRun
	if err := f.db.First(&run, "id = ?", f.runID).Error; err != nil {
		t.Fatal(err)
	}
	if run.Status != string(agentruntime.RunStatusQueued) || run.WaitingReason != "" ||
		run.WaitingToken != "" || run.Revision != start.Revision+1 {
		t.Fatalf("resolved run = status:%q reason:%q token_empty:%v revision:%d",
			run.Status, run.WaitingReason, run.WaitingToken == "", run.Revision)
	}
	var cardActions, capabilityResults, continuations int64
	if err := f.db.Model(&model.AgentStep{}).
		Where("run_id = ? AND kind = ?", f.runID, string(agentruntime.StepKindCardAction)).
		Count(&cardActions).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&model.AgentStep{}).
		Where("run_id = ? AND kind = ?", f.runID, string(agentruntime.StepKindCapabilityResult)).
		Count(&capabilityResults).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&model.AgentStep{}).
		Where("run_id = ? AND kind = ? AND status = ?",
			f.runID, string(agentruntime.StepKindObserve), string(agentruntime.StepStatusQueued)).
		Count(&continuations).Error; err != nil {
		t.Fatal(err)
	}
	if cardActions != 1 || capabilityResults != 1 || continuations != 1 {
		t.Fatalf(
			"persisted steps = card_action:%d capability_result:%d continuation:%d, want 1/1/1",
			cardActions, capabilityResults, continuations,
		)
	}
	var cardStep model.AgentStep
	if err := f.db.First(&cardStep,
		"run_id = ? AND kind = ?", f.runID, string(agentruntime.StepKindCardAction),
	).Error; err != nil {
		t.Fatal(err)
	}
	var event agentruntime.ConversationEvent
	if err := json.Unmarshal([]byte(cardStep.InputJSON), &event); err != nil {
		t.Fatalf("decode card action event: %v", err)
	}
	if event.ChatID != "oc-chat" || event.SourceRef != request.SourceRef {
		t.Fatalf("card action event chat/source = %q/%q", event.ChatID, event.SourceRef)
	}
	var outboxes []model.AgentProjectionOutbox
	if err := f.db.Where("step_id IN (?)",
		f.db.Model(&model.AgentStep{}).
			Select("id").
			Where("run_id = ? AND kind IN ?", f.runID, []string{
				string(agentruntime.StepKindCardAction),
				string(agentruntime.StepKindCapabilityResult),
			}),
	).Order("document_id").Find(&outboxes).Error; err != nil {
		t.Fatal(err)
	}
	if len(outboxes) != 2 || outboxes[0].DocumentID == outboxes[1].DocumentID {
		t.Fatalf("fact projection outboxes = %#v, want two distinct document IDs", outboxes)
	}
	eventTypes := make(map[string]bool, 2)
	for _, outbox := range outboxes {
		var projectionPayload map[string]any
		if err := json.Unmarshal([]byte(outbox.PayloadJSON), &projectionPayload); err != nil {
			t.Fatalf("decode projection payload: %v", err)
		}
		eventType, _ := projectionPayload["event_type"].(string)
		stepID, _ := projectionPayload["step_id"].(string)
		if stepID == "" || projectionPayload["action"] != string(request.Action) ||
			projectionPayload["status"] != outcome.Status ||
			projectionPayload["structured_payload"] == nil {
			t.Fatalf("semantic projection payload = %#v", projectionPayload)
		}
		eventTypes[eventType] = true
	}
	if !eventTypes[string(agentruntime.StepKindCardAction)] ||
		!eventTypes[string(agentruntime.StepKindCapabilityResult)] {
		t.Fatalf("projection event types = %#v", eventTypes)
	}
	var execution model.AgentCapabilityExecution
	if err := f.db.First(&execution, "run_id = ?", f.runID).Error; err != nil {
		t.Fatal(err)
	}
	if execution.Status != "completed" || execution.FinishedAt.IsZero() {
		t.Fatalf("capability execution = status:%q finished:%v", execution.Status, execution.FinishedAt)
	}
	var claimInput scheduleCapabilityClaimInput
	if err := json.Unmarshal([]byte(execution.InputJSON), &claimInput); err != nil {
		t.Fatal(err)
	}
	if claimInput.ActorOpenID != request.ActorOpenID {
		t.Fatalf("persisted claim actor = %q, want %q", claimInput.ActorOpenID, request.ActorOpenID)
	}
}

func TestScheduleInteractionRejectsWrongTokenAndStaleRevisionBeforeClaim(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*agentruntime.ScheduleInteractionRequest)
		want   error
	}{
		{name: "wrong token", mutate: func(r *agentruntime.ScheduleInteractionRequest) {
			r.PresentedToken = "wrong-token"
		}, want: agentruntime.ErrInteractionTokenMismatch},
		{name: "stale revision", mutate: func(r *agentruntime.ScheduleInteractionRequest) {
			r.Revision--
		}, want: agentruntime.ErrInteractionConflict},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, _, request := newScheduleInteractionFixture(t)
			tt.mutate(&request)
			if _, err := f.repo.ClaimScheduleInteraction(context.Background(), request); !errors.Is(err, tt.want) {
				t.Fatalf("ClaimScheduleInteraction() error = %v, want %v", err, tt.want)
			}
			var count int64
			if err := f.db.Model(&model.AgentCapabilityExecution{}).Count(&count).Error; err != nil {
				t.Fatal(err)
			}
			if count != 0 {
				t.Fatalf("capability executions = %d, want 0", count)
			}
		})
	}
}

func TestScheduleInteractionConcurrentClaimHasOneWinner(t *testing.T) {
	f, _, request := newScheduleInteractionFixture(t)
	requests := []agentruntime.ScheduleInteractionRequest{request, request}
	requests[1].ClaimID = "claim-second-" + uuid.NewV4().String()
	results := make(chan agentruntime.ScheduleClaimState, 2)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, req := range requests {
		req := req
		wg.Add(1)
		go func() {
			defer wg.Done()
			claim, err := NewRepository(f.db).ClaimScheduleInteraction(context.Background(), req)
			results <- claim.State
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("ClaimScheduleInteraction() error = %v", err)
		}
	}
	var acquired, running int
	for state := range results {
		switch state {
		case agentruntime.ScheduleClaimAcquired:
			acquired++
		case agentruntime.ScheduleClaimRunning:
			running++
		}
	}
	if acquired != 1 || running != 1 {
		t.Fatalf("claim states = acquired:%d running:%d, want 1/1", acquired, running)
	}
}

func TestScheduleInteractionStaleRunningClaimIsReclaimedAndFenced(t *testing.T) {
	f, start, first := newScheduleInteractionFixture(t)
	if claim, err := f.repo.ClaimScheduleInteraction(context.Background(), first); err != nil ||
		claim.State != agentruntime.ScheduleClaimAcquired {
		t.Fatalf("first claim = %#v, error = %v", claim, err)
	}
	reclaimed := first
	reclaimed.ClaimID = "claim-reclaimed-" + uuid.NewV4().String()
	reclaimed.ResolvedAt = first.ResolvedAt.Add(first.RunningTTL + time.Microsecond)
	claim, err := f.repo.ClaimScheduleInteraction(context.Background(), reclaimed)
	if err != nil {
		t.Fatalf("reclaim error = %v", err)
	}
	if claim.State != agentruntime.ScheduleClaimAcquired {
		t.Fatalf("reclaim state = %q, want acquired", claim.State)
	}
	outcome := agentruntime.ScheduleInteractionOutcome{
		Status: "updated", TaskID: "task-1", InteractionID: start.InteractionID,
		Action: agentruntime.ScheduleInteractionConfirm,
		Result: json.RawMessage(`{"status":"updated","task_id":"task-1","name":"new-name"}`),
	}
	if _, err := f.repo.CompleteScheduleInteraction(context.Background(), completeScheduleInteractionRequest{
		Request: first, Outcome: outcome,
	}); !errors.Is(err, agentruntime.ErrScheduleInteractionClaimLost) {
		t.Fatalf("old claim completion error = %v, want claim lost", err)
	}
	if _, err := f.repo.CompleteScheduleInteraction(context.Background(), completeScheduleInteractionRequest{
		Request: reclaimed, Outcome: outcome,
	}); err != nil {
		t.Fatalf("reclaimed completion error = %v", err)
	}
}

func TestScheduleInteractionFirstActionWinsAcrossStaleAndFailedReclaim(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(*repositoryFixture, agentruntime.ScheduleInteractionRequest)
	}{
		{
			name: "stale running",
			prepare: func(_ *repositoryFixture, _ agentruntime.ScheduleInteractionRequest) {
			},
		},
		{
			name: "failed",
			prepare: func(f *repositoryFixture, req agentruntime.ScheduleInteractionRequest) {
				if err := f.repo.FailScheduleInteraction(context.Background(), agentruntime.FailScheduleInteractionRequest{
					Request: req, ErrorText: "forced failure",
				}); err != nil {
					t.Fatalf("FailScheduleInteraction() error = %v", err)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, _, first := newScheduleInteractionFixture(t)
			if _, err := f.repo.ClaimScheduleInteraction(context.Background(), first); err != nil {
				t.Fatal(err)
			}
			tt.prepare(f, first)
			cancel := first
			cancel.Action = agentruntime.ScheduleInteractionCancel
			cancel.ClaimID = "claim-cancel-" + uuid.NewV4().String()
			cancel.ResolvedAt = first.ResolvedAt.Add(first.RunningTTL + time.Microsecond)

			if _, err := f.repo.ClaimScheduleInteraction(context.Background(), cancel); !errors.Is(
				err, agentruntime.ErrInteractionConflict,
			) {
				t.Fatalf("opposite-action reclaim error = %v, want interaction conflict", err)
			}
			var execution model.AgentCapabilityExecution
			if err := f.db.First(&execution, "run_id = ?", f.runID).Error; err != nil {
				t.Fatal(err)
			}
			var claimInput scheduleCapabilityClaimInput
			if err := json.Unmarshal([]byte(execution.InputJSON), &claimInput); err != nil {
				t.Fatal(err)
			}
			if claimInput.Action != string(agentruntime.ScheduleInteractionConfirm) ||
				claimInput.ClaimID != first.ClaimID {
				t.Fatalf("opposite action altered claim input = %#v", claimInput)
			}
		})
	}
}

func TestScheduleInteractionFinalizeFencesClaimActionAndActor(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*agentruntime.ScheduleInteractionRequest)
		fail   bool
	}{
		{name: "complete different action", mutate: func(req *agentruntime.ScheduleInteractionRequest) {
			req.Action = agentruntime.ScheduleInteractionCancel
		}},
		{name: "complete different actor", mutate: func(req *agentruntime.ScheduleInteractionRequest) {
			req.ActorOpenID = "ou-other"
		}},
		{name: "fail different action", fail: true, mutate: func(req *agentruntime.ScheduleInteractionRequest) {
			req.Action = agentruntime.ScheduleInteractionCancel
		}},
		{name: "fail different actor", fail: true, mutate: func(req *agentruntime.ScheduleInteractionRequest) {
			req.ActorOpenID = "ou-other"
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, start, claimed := newScheduleInteractionFixture(t)
			if _, err := f.repo.ClaimScheduleInteraction(context.Background(), claimed); err != nil {
				t.Fatal(err)
			}
			fenced := claimed
			tt.mutate(&fenced)
			var err error
			if tt.fail {
				err = f.repo.FailScheduleInteraction(context.Background(), agentruntime.FailScheduleInteractionRequest{
					Request: fenced, ErrorText: "must be fenced",
				})
			} else {
				status := "updated"
				if fenced.Action == agentruntime.ScheduleInteractionCancel {
					status = "cancelled_by_user"
				}
				_, err = f.repo.CompleteScheduleInteraction(context.Background(), completeScheduleInteractionRequest{
					Request: fenced,
					Outcome: agentruntime.ScheduleInteractionOutcome{
						Status: status, TaskID: "task-1", InteractionID: start.InteractionID,
						Action: fenced.Action, Result: json.RawMessage(`{}`),
					},
				})
			}
			if !errors.Is(err, agentruntime.ErrScheduleInteractionClaimLost) {
				t.Fatalf("fenced finalize error = %v, want claim lost", err)
			}
			assertScheduleInteractionStillWaiting(t, f)
		})
	}
}

func TestScheduleInteractionRejectsMismatchedOutcomeWithoutResolving(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*agentruntime.ScheduleInteractionOutcome)
	}{
		{name: "action", mutate: func(outcome *agentruntime.ScheduleInteractionOutcome) {
			outcome.Action = agentruntime.ScheduleInteractionCancel
		}},
		{name: "interaction", mutate: func(outcome *agentruntime.ScheduleInteractionOutcome) {
			outcome.InteractionID = "interaction-forged"
		}},
		{name: "task", mutate: func(outcome *agentruntime.ScheduleInteractionOutcome) {
			outcome.TaskID = "task-forged"
		}},
		{name: "status", mutate: func(outcome *agentruntime.ScheduleInteractionOutcome) {
			outcome.Status = "cancelled_by_user"
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, start, request := newScheduleInteractionFixture(t)
			if _, err := f.repo.ClaimScheduleInteraction(context.Background(), request); err != nil {
				t.Fatal(err)
			}
			outcome := agentruntime.ScheduleInteractionOutcome{
				Status: "updated", TaskID: "task-1", InteractionID: start.InteractionID,
				Action: request.Action,
				Result: json.RawMessage(`{"status":"updated","task_id":"task-1","name":"new-name"}`),
			}
			tt.mutate(&outcome)

			if _, err := f.repo.CompleteScheduleInteraction(context.Background(), completeScheduleInteractionRequest{
				Request: request, Outcome: outcome,
			}); !errors.Is(err, agentruntime.ErrInteractionConflict) {
				t.Fatalf("mismatched outcome error = %v, want interaction conflict", err)
			}
			assertScheduleInteractionStillWaiting(t, f)
			var stepCount int64
			if err := f.db.Model(&model.AgentStep{}).
				Where("run_id = ? AND kind IN ?", f.runID, []string{
					string(agentruntime.StepKindCardAction),
					string(agentruntime.StepKindCapabilityResult),
				}).Count(&stepCount).Error; err != nil {
				t.Fatal(err)
			}
			if stepCount != 0 {
				t.Fatalf("mismatched outcome persisted %d result steps", stepCount)
			}
		})
	}
}

func TestScheduleInteractionRejectsInvalidResultContractWithoutResolving(t *testing.T) {
	tests := []struct {
		name   string
		action agentruntime.ScheduleInteractionAction
		result json.RawMessage
	}{
		{
			name: "confirm forged inner status", action: agentruntime.ScheduleInteractionConfirm,
			result: json.RawMessage(`{"status":"cancelled_by_user","task_id":"task-1","name":"new-name"}`),
		},
		{
			name: "confirm forged inner task", action: agentruntime.ScheduleInteractionConfirm,
			result: json.RawMessage(`{"status":"updated","task_id":"task-forged","name":"new-name"}`),
		},
		{
			name: "confirm unknown field", action: agentruntime.ScheduleInteractionConfirm,
			result: json.RawMessage(`{"status":"updated","task_id":"task-1","name":"new-name","hidden":true}`),
		},
		{
			name: "confirm non object", action: agentruntime.ScheduleInteractionConfirm,
			result: json.RawMessage(`["updated","task-1"]`),
		},
		{
			name: "confirm trailing json", action: agentruntime.ScheduleInteractionConfirm,
			result: json.RawMessage(`{"status":"updated","task_id":"task-1"}{"hidden":true}`),
		},
		{
			name: "confirm null name", action: agentruntime.ScheduleInteractionConfirm,
			result: json.RawMessage(`{"status":"updated","task_id":"task-1","name":null}`),
		},
		{
			name: "confirm missing name", action: agentruntime.ScheduleInteractionConfirm,
			result: json.RawMessage(`{"status":"updated","task_id":"task-1"}`),
		},
		{
			name: "confirm nonstring name", action: agentruntime.ScheduleInteractionConfirm,
			result: json.RawMessage(`{"status":"updated","task_id":"task-1","name":42}`),
		},
		{
			name: "confirm forged edited name", action: agentruntime.ScheduleInteractionConfirm,
			result: json.RawMessage(`{"status":"updated","task_id":"task-1","name":"forged-name"}`),
		},
		{
			name: "cancel arbitrary result", action: agentruntime.ScheduleInteractionCancel,
			result: json.RawMessage(`{"status":"cancelled_by_user","task_id":"task-1"}`),
		},
		{
			name: "cancel null result", action: agentruntime.ScheduleInteractionCancel,
			result: json.RawMessage(`null`),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, start, request := newScheduleInteractionFixture(t)
			request.Action = tt.action
			if _, err := f.repo.ClaimScheduleInteraction(context.Background(), request); err != nil {
				t.Fatal(err)
			}
			status := "updated"
			if tt.action == agentruntime.ScheduleInteractionCancel {
				status = "cancelled_by_user"
			}
			outcome := agentruntime.ScheduleInteractionOutcome{
				Status: status, TaskID: "task-1", InteractionID: start.InteractionID,
				Action: tt.action, Result: tt.result,
			}

			if _, err := f.repo.CompleteScheduleInteraction(context.Background(), completeScheduleInteractionRequest{
				Request: request, Outcome: outcome,
			}); !errors.Is(err, agentruntime.ErrInteractionConflict) {
				t.Fatalf("invalid result contract error = %v, want interaction conflict", err)
			}
			assertScheduleInteractionStillWaiting(t, f)
		})
	}
}

func TestScheduleInteractionAllowsCurrentNameWhenEditingDifferentField(t *testing.T) {
	trusted := json.RawMessage(`{
		"version":1,
		"task_id":"task-1",
		"initiator_open_id":"ou-actor",
		"chat_id":"oc-chat",
		"new_values":{"message":"new-message"}
	}`)
	f, start, request := newScheduleInteractionFixtureWithTrustedInput(t, trusted)
	if _, err := f.repo.ClaimScheduleInteraction(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	outcome := agentruntime.ScheduleInteractionOutcome{
		Status: "updated", TaskID: "task-1", InteractionID: start.InteractionID,
		Action: request.Action,
		Result: json.RawMessage(`{"status":"updated","task_id":"task-1","name":"existing-name"}`),
	}

	if _, err := f.repo.CompleteScheduleInteraction(context.Background(), completeScheduleInteractionRequest{
		Request: request, Outcome: outcome,
	}); err != nil {
		t.Fatalf("CompleteScheduleInteraction(non-name edit) error = %v", err)
	}
}

func TestScheduleInteractionCompletedAtomicReplayReturnsStoredWithoutExecutor(t *testing.T) {
	f, start, request := newScheduleInteractionFixture(t)
	if _, err := f.repo.ClaimScheduleInteraction(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	outcome := agentruntime.ScheduleInteractionOutcome{
		Status: "updated", TaskID: "task-1", InteractionID: start.InteractionID,
		Action: request.Action,
		Result: json.RawMessage(`{"status":"updated","task_id":"task-1","name":"new-name"}`),
	}
	if _, err := f.repo.CompleteScheduleInteraction(context.Background(), completeScheduleInteractionRequest{
		Request: request, Outcome: outcome,
	}); err != nil {
		t.Fatal(err)
	}
	var executorCalls atomic.Int32
	replayed, err := f.repo.ExecuteScheduleInteraction(
		context.Background(),
		request,
		func(context.Context, agentruntime.ScheduleEditTrustedInput) (agentruntime.ScheduleInteractionOutcome, error) {
			executorCalls.Add(1)
			return agentruntime.ScheduleInteractionOutcome{}, errors.New("must not execute")
		},
	)
	if err != nil {
		t.Fatalf("completed ExecuteScheduleInteraction() error = %v", err)
	}
	if executorCalls.Load() != 0 {
		t.Fatalf("completed executor calls = %d, want 0", executorCalls.Load())
	}
	if replayed.Status != outcome.Status ||
		replayed.TaskID != outcome.TaskID ||
		replayed.InteractionID != outcome.InteractionID ||
		replayed.Action != outcome.Action ||
		!equalJSONDocument(replayed.Result, outcome.Result) {
		t.Fatalf("completed outcome = %#v, want stored %#v", replayed, outcome)
	}
}

func TestScheduleInteractionCompletedInspectionFailsClosedOnCorruptClaimInput(t *testing.T) {
	f, start, request := newScheduleInteractionFixture(t)
	if _, err := f.repo.ClaimScheduleInteraction(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	outcome := agentruntime.ScheduleInteractionOutcome{
		Status: "updated", TaskID: "task-1", InteractionID: start.InteractionID,
		Action: request.Action,
		Result: json.RawMessage(`{"status":"updated","task_id":"task-1","name":"new-name"}`),
	}
	if _, err := f.repo.CompleteScheduleInteraction(context.Background(), completeScheduleInteractionRequest{
		Request: request, Outcome: outcome,
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&model.AgentCapabilityExecution{}).
		Where("run_id = ?", f.runID).Update("input_json", `{"version":1}`).Error; err != nil {
		t.Fatal(err)
	}

	if _, err := f.repo.InspectScheduleInteraction(context.Background(), request); !errors.Is(
		err, agentruntime.ErrInteractionConflict,
	) {
		t.Fatalf("InspectScheduleInteraction(corrupt claim) error = %v, want conflict", err)
	}
}

func TestScheduleInteractionCompletedReplayStillRejectsWrongToken(t *testing.T) {
	f, start, request := newScheduleInteractionFixture(t)
	if _, err := f.repo.ClaimScheduleInteraction(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	outcome := agentruntime.ScheduleInteractionOutcome{
		Status: "updated", TaskID: "task-1", InteractionID: start.InteractionID,
		Action: agentruntime.ScheduleInteractionConfirm,
		Result: json.RawMessage(`{"status":"updated","task_id":"task-1","name":"new-name"}`),
	}
	if _, err := f.repo.CompleteScheduleInteraction(context.Background(), completeScheduleInteractionRequest{
		Request: request, Outcome: outcome,
	}); err != nil {
		t.Fatal(err)
	}
	replay := request
	replay.PresentedToken = "wrong-token"
	replay.ClaimID = "claim-wrong-" + uuid.NewV4().String()
	if _, err := f.repo.InspectScheduleInteraction(context.Background(), replay); !errors.Is(
		err, agentruntime.ErrInteractionTokenMismatch,
	) {
		t.Fatalf("completed replay inspect error = %v, want token mismatch", err)
	}
}

func TestScheduleInteractionCompletedInspectionFailsClosedOnCorruptOutcome(t *testing.T) {
	f, start, request := newScheduleInteractionFixture(t)
	if _, err := f.repo.ClaimScheduleInteraction(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	outcome := agentruntime.ScheduleInteractionOutcome{
		Status: "updated", TaskID: "task-1", InteractionID: start.InteractionID,
		Action: request.Action,
		Result: json.RawMessage(`{"status":"updated","task_id":"task-1","name":"new-name"}`),
	}
	if _, err := f.repo.CompleteScheduleInteraction(context.Background(), completeScheduleInteractionRequest{
		Request: request, Outcome: outcome,
	}); err != nil {
		t.Fatal(err)
	}
	corrupt := outcome
	corrupt.Action = agentruntime.ScheduleInteractionCancel
	corrupt.Status = "cancelled_by_user"
	raw, err := json.Marshal(corrupt)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&model.AgentCapabilityExecution{}).
		Where("run_id = ?", f.runID).Update("output_json", string(raw)).Error; err != nil {
		t.Fatal(err)
	}

	if _, err := f.repo.InspectScheduleInteraction(context.Background(), request); !errors.Is(
		err, agentruntime.ErrInteractionConflict,
	) {
		t.Fatalf("InspectScheduleInteraction(corrupt outcome) error = %v, want conflict", err)
	}
}

func TestScheduleInteractionCompletedReplayFailsClosedOnCorruptInnerResult(t *testing.T) {
	f, start, request := newScheduleInteractionFixture(t)
	if _, err := f.repo.ClaimScheduleInteraction(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	outcome := agentruntime.ScheduleInteractionOutcome{
		Status: "updated", TaskID: "task-1", InteractionID: start.InteractionID,
		Action: request.Action,
		Result: json.RawMessage(`{"status":"updated","task_id":"task-1","name":"new-name"}`),
	}
	if _, err := f.repo.CompleteScheduleInteraction(context.Background(), completeScheduleInteractionRequest{
		Request: request, Outcome: outcome,
	}); err != nil {
		t.Fatal(err)
	}
	outcome.Result = json.RawMessage(`{"status":"updated","task_id":"task-forged","name":"new-name"}`)
	raw, err := json.Marshal(outcome)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&model.AgentCapabilityExecution{}).
		Where("run_id = ?", f.runID).Update("output_json", string(raw)).Error; err != nil {
		t.Fatal(err)
	}

	if _, err := f.repo.InspectScheduleInteraction(context.Background(), request); !errors.Is(
		err, agentruntime.ErrInteractionConflict,
	) {
		t.Errorf("InspectScheduleInteraction(corrupt inner result) error = %v, want conflict", err)
	}
	if _, err := f.repo.ClaimScheduleInteraction(context.Background(), request); !errors.Is(
		err, agentruntime.ErrInteractionConflict,
	) {
		t.Errorf("ClaimScheduleInteraction(corrupt inner result) error = %v, want conflict", err)
	}
}

func TestScheduleInteractionConfirmAfterCancelReturnsCancelledOutcome(t *testing.T) {
	f, start, request := newScheduleInteractionFixture(t)
	request.Action = agentruntime.ScheduleInteractionCancel
	if _, err := f.repo.ClaimScheduleInteraction(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	cancelled := agentruntime.ScheduleInteractionOutcome{
		Status: "cancelled_by_user", TaskID: "task-1", InteractionID: start.InteractionID,
		Action: agentruntime.ScheduleInteractionCancel, Result: json.RawMessage(`{}`),
	}
	if _, err := f.repo.CompleteScheduleInteraction(context.Background(), completeScheduleInteractionRequest{
		Request: request, Outcome: cancelled,
	}); err != nil {
		t.Fatal(err)
	}

	confirm := request
	confirm.Action = agentruntime.ScheduleInteractionConfirm
	confirm.ClaimID = "claim-confirm-" + uuid.NewV4().String()
	claim, err := f.repo.ClaimScheduleInteraction(context.Background(), confirm)
	if err != nil {
		t.Fatal(err)
	}
	if claim.State != agentruntime.ScheduleClaimCompleted ||
		claim.Outcome.Status != "cancelled_by_user" ||
		claim.Outcome.Action != agentruntime.ScheduleInteractionCancel {
		t.Fatalf("confirm-after-cancel claim = %#v", claim)
	}
}

func newScheduleInteractionFixture(
	t *testing.T,
) (*repositoryFixture, agentruntime.StartInteractionRequest, agentruntime.ScheduleInteractionRequest) {
	t.Helper()
	return newScheduleInteractionFixtureWithTrustedInput(t, json.RawMessage(`{
		"version":1,
		"task_id":"task-1",
		"initiator_open_id":"ou-actor",
		"chat_id":"oc-chat",
		"new_values":{"name":"new-name"}
	}`))
}

func newScheduleInteractionFixtureWithTrustedInput(
	t *testing.T,
	trustedInput json.RawMessage,
) (*repositoryFixture, agentruntime.StartInteractionRequest, agentruntime.ScheduleInteractionRequest) {
	t.Helper()
	f := newRepositoryFixture(t, agentruntime.RunStatusRunning)
	now := time.Now().UTC().Truncate(time.Microsecond)
	start := startInteractionRequest(f.runID, "correct-token", now)
	start.InteractionKind = "schedule_edit"
	start.TrustedInput = append(json.RawMessage(nil), trustedInput...)
	run, _, err := f.repo.StartInteraction(context.Background(), start)
	if err != nil {
		t.Fatalf("StartInteraction() error = %v", err)
	}
	if run.Status != agentruntime.RunStatusWaitingApproval {
		t.Fatalf("schedule edit wait status = %q, want waiting_approval", run.Status)
	}
	request := agentruntime.ScheduleInteractionRequest{
		RunID: start.RunID, StepID: start.StepID, InteractionID: start.InteractionID,
		Revision: start.Revision, PresentedToken: "correct-token", ActorOpenID: "ou-actor",
		Action: agentruntime.ScheduleInteractionConfirm, EventID: "event-" + uuid.NewV4().String(),
		SourceRef:  "card-message-1",
		ResolvedAt: now.Add(time.Minute), ClaimID: "claim-" + uuid.NewV4().String(),
		RunningTTL: time.Minute, Projection: testProjection(f.runID),
	}
	return f, start, request
}

func assertScheduleInteractionStillWaiting(t *testing.T, f *repositoryFixture) {
	t.Helper()
	var run model.AgentRun
	if err := f.db.First(&run, "id = ?", f.runID).Error; err != nil {
		t.Fatal(err)
	}
	if run.Status != string(agentruntime.RunStatusWaitingApproval) ||
		run.WaitingToken == "" || run.WaitingReason == "" {
		t.Fatalf("run unexpectedly resolved: status=%q reason=%q token_empty=%v",
			run.Status, run.WaitingReason, run.WaitingToken == "")
	}
}

func validScheduleOutcome(
	request agentruntime.ScheduleInteractionRequest,
	taskID string,
	name string,
) agentruntime.ScheduleInteractionOutcome {
	result, _ := json.Marshal(map[string]any{
		"status":  "updated",
		"task_id": taskID,
		"name":    name,
	})
	return agentruntime.ScheduleInteractionOutcome{
		Status:        "updated",
		TaskID:        taskID,
		InteractionID: request.InteractionID,
		Action:        request.Action,
		Result:        result,
	}
}

func createScheduleServiceFixture(
	t *testing.T,
	f *repositoryFixture,
	taskID string,
	name string,
) *scheduleapp.Service {
	t.Helper()
	identity := botidentity.Identity{
		AppID:     "app-schedule-" + uuid.NewV4().String(),
		BotOpenID: "bot-schedule-" + uuid.NewV4().String(),
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	task := &model.ScheduledTask{
		ID: taskID, Name: name, Type: model.ScheduleTaskTypeCron,
		ChatID: "oc-chat", CreatorID: "ou-actor", ToolName: "send_message",
		ToolArgs: "{}", CronExpr: "0 10 * * *", Timezone: "Asia/Shanghai",
		Status: model.ScheduleTaskStatusEnabled, NextRunAt: now.Add(time.Hour),
		CreatedAt: now, UpdatedAt: now, AppID: identity.AppID, BotOpenID: identity.BotOpenID,
		SourceMessageID: "om-source",
	}
	if err := f.db.Create(task).Error; err != nil {
		t.Fatalf("create scheduled task fixture: %v", err)
	}
	repo := scheduleinfra.NewRepository(f.db, identity)
	return scheduleapp.NewService(repo, scheduleapp.NewToolExecutor(nil, identity), identity)
}

func assertScheduledTaskName(
	t *testing.T,
	f *repositoryFixture,
	taskID string,
	want string,
) {
	t.Helper()
	var task model.ScheduledTask
	if err := f.db.First(&task, "id = ?", taskID).Error; err != nil {
		t.Fatalf("read scheduled task: %v", err)
	}
	if task.Name != want {
		t.Fatalf("scheduled task name = %q, want %q", task.Name, want)
	}
}
