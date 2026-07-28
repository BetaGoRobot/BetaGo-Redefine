package agentstore

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	uuid "github.com/satori/go.uuid"
	"gorm.io/gorm"
)

type continuationGeneratorCounter struct{ calls int }

func (f *continuationGeneratorCounter) Generate(
	context.Context,
	agentruntime.ContinuationContext,
) (agentruntime.TurnDecision, error) {
	f.calls++
	return agentruntime.TurnDecision{}, errors.New("generator must not run")
}

type continuationDelivererCounter struct{ calls int }

func (f *continuationDelivererCounter) Deliver(
	context.Context,
	agentruntime.ReplyRequest,
) (string, error) {
	f.calls++
	return "om-retry-delivered", nil
}

func TestClaimContinuationStepFiltersKindsAndTransitionsRun(t *testing.T) {
	f := newRepositoryFixture(t, agentruntime.RunStatusQueued)
	if err := f.db.Model(&model.AgentSession{}).Where("id = ?", f.sessionID).
		Update("active_run_id", f.runID).Error; err != nil {
		t.Fatal(err)
	}
	ordinaryObservation := &agentruntime.AgentStep{
		Index: 1, Kind: agentruntime.StepKindObserve, Status: agentruntime.StepStatusQueued,
		DedupeKey: "ordinary:observe",
	}
	f.createStep(t, ordinaryObservation)
	_, err := f.repo.ClaimContinuationStep(context.Background(), agentruntime.ContinuationClaim{
		RunID: f.runID, WorkerID: "worker", LeaseTTL: time.Minute, Now: time.Now().UTC(),
	})
	if !errors.Is(err, agentruntime.ErrNotFound) {
		t.Fatalf("ordinary observation claim error = %v, want not found", err)
	}

	if err := f.db.Model(&model.AgentStep{}).Where("run_id = ?", f.runID).
		Update("status", string(agentruntime.StepStatusCompleted)).Error; err != nil {
		t.Fatal(err)
	}
	f.createStep(t, &agentruntime.AgentStep{
		Index: 2, Kind: agentruntime.StepKindDecide, Status: agentruntime.StepStatusQueued,
		InputJSON: `{"version":1,"source_step_id":"` + ordinaryObservation.ID + `"}`,
		DedupeKey: ordinaryObservation.DedupeKey + ":continuation",
	})
	if _, err := f.repo.ClaimContinuationStep(context.Background(), agentruntime.ContinuationClaim{
		RunID: f.runID, WorkerID: "worker", LeaseTTL: time.Minute, Now: time.Now().UTC(),
	}); !errors.Is(err, agentruntime.ErrNotFound) {
		t.Fatalf("forged observation-anchor decide claim error = %v, want not found", err)
	}
	if err := f.db.Model(&model.AgentStep{}).Where(
		"run_id = ? AND kind = ?", f.runID, string(agentruntime.StepKindDecide),
	).Update("status", string(agentruntime.StepStatusCompleted)).Error; err != nil {
		t.Fatal(err)
	}
	f.createStep(t, &agentruntime.AgentStep{
		Index: 3, Kind: agentruntime.StepKindDecide, Status: agentruntime.StepStatusQueued,
		InputJSON: `{"planner":"ordinary"}`, DedupeKey: "ordinary:decide",
	})
	if _, err := f.repo.ClaimContinuationStep(context.Background(), agentruntime.ContinuationClaim{
		RunID: f.runID, WorkerID: "worker", LeaseTTL: time.Minute, Now: time.Now().UTC(),
	}); !errors.Is(err, agentruntime.ErrNotFound) {
		t.Fatalf("ordinary decide claim error = %v, want not found", err)
	}
	if err := f.db.Model(&model.AgentStep{}).Where(
		"run_id = ? AND kind = ?", f.runID, string(agentruntime.StepKindDecide),
	).Update("status", string(agentruntime.StepStatusCompleted)).Error; err != nil {
		t.Fatal(err)
	}
	crossRunReplyID := "step_test_" + uuid.NewV4().String()
	f.createStep(t, &agentruntime.AgentStep{
		ID: crossRunReplyID, Index: 4, Kind: agentruntime.StepKindReply, Status: agentruntime.StepStatusQueued,
		InputJSON: `{"version":1,"step_id":"` + crossRunReplyID +
			`","run_id":"run-other","text":"bad","chat_id":"oc","idempotency_key":"` + crossRunReplyID + `"}`,
		DedupeKey: "anchor:continuation:reply",
	})
	if _, err := f.repo.ClaimContinuationStep(context.Background(), agentruntime.ContinuationClaim{
		RunID: f.runID, WorkerID: "worker", LeaseTTL: time.Minute, Now: time.Now().UTC(),
	}); !errors.Is(err, agentruntime.ErrNotFound) {
		t.Fatalf("ordinary reply claim error = %v, want not found", err)
	}
	if err := f.db.Model(&model.AgentStep{}).Where("run_id = ? AND kind = ?", f.runID, string(agentruntime.StepKindReply)).
		Update("status", string(agentruntime.StepStatusCompleted)).Error; err != nil {
		t.Fatal(err)
	}
	anchor := &agentruntime.AgentStep{
		Index: 5, Kind: agentruntime.StepKindResume, Status: agentruntime.StepStatusCompleted,
		InputJSON: `{}`, OutputJSON: `{}`, DedupeKey: "anchor",
	}
	f.createStep(t, anchor)
	f.createStep(t, &agentruntime.AgentStep{
		Index: 6, Kind: agentruntime.StepKindDecide, Status: agentruntime.StepStatusQueued,
		InputJSON: `{"version":1,"source_step_id":"` + anchor.ID + `"}`,
		DedupeKey: anchor.DedupeKey + ":continuation",
	})
	if err := f.db.Model(&model.AgentSession{}).Where("id = ?", f.sessionID).
		Update("active_run_id", "newer-run").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.ClaimContinuationStep(context.Background(), agentruntime.ContinuationClaim{
		RunID: f.runID, WorkerID: "stale-worker", LeaseTTL: time.Minute, Now: time.Now().UTC(),
	}); !errors.Is(err, agentruntime.ErrNotFound) {
		t.Fatalf("inactive run claim error = %v, want not found", err)
	}
	if err := f.db.Model(&model.AgentSession{}).Where("id = ?", f.sessionID).
		Update("active_run_id", f.runID).Error; err != nil {
		t.Fatal(err)
	}
	claimed, err := f.repo.ClaimContinuationStep(context.Background(), agentruntime.ContinuationClaim{
		RunID: f.runID, WorkerID: "worker", LeaseTTL: time.Minute, Now: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	var run model.AgentRun
	if err := f.db.First(&run, "id = ?", f.runID).Error; err != nil {
		t.Fatal(err)
	}
	if claimed.Kind != agentruntime.StepKindDecide || run.Status != string(agentruntime.RunStatusRunning) {
		t.Fatalf("claimed=%#v run=%#v", claimed, run)
	}
}

func TestContinuationContextDecisionAndDeliveryAreDurableAndFenced(t *testing.T) {
	f, _, request := newScheduleInteractionFixture(t)
	if _, err := f.repo.ClaimScheduleInteraction(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.ExecuteScheduleInteraction(
		context.Background(), request,
		func(context.Context, agentruntime.ScheduleEditTrustedInput) (agentruntime.ScheduleInteractionOutcome, error) {
			return validScheduleOutcome(request, "task-1", "new-name"), nil
		},
	); err != nil {
		t.Fatal(err)
	}
	now := request.ResolvedAt.Add(time.Second)
	modelStep, err := f.repo.ClaimContinuationStep(context.Background(), agentruntime.ContinuationClaim{
		RunID: f.runID, WorkerID: "model-worker", LeaseTTL: time.Minute, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	input, err := f.repo.LoadContinuationContext(context.Background(), agentruntime.LoadContinuationContextRequest{
		RunID: f.runID, AnchorStepID: modelStep.ID, RecentLimit: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if input.LatestOutcome.Type != agentruntime.EventTypeCapabilityResult ||
		input.LatestOutcome.ID == "" || input.LatestOutcome.Action != string(request.Action) {
		t.Fatalf("latest outcome = %#v", input.LatestOutcome)
	}
	reply, err := f.repo.PersistDecision(context.Background(), agentruntime.PersistDecisionRequest{
		StepID: modelStep.ID, WorkerID: modelStep.WorkerID, AttemptCount: modelStep.AttemptCount,
		Decision: agentruntime.TurnDecision{
			Decision: agentruntime.TurnDecisionReply, Reply: "修改完成", Reason: "反馈结果",
		},
		FinishedAt: now.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	var frozen agentruntime.ReplyRequest
	if json.Unmarshal([]byte(reply.InputJSON), &frozen) != nil ||
		frozen.IdempotencyKey != reply.ID || frozen.Text != "修改完成" {
		t.Fatalf("frozen reply = %#v", frozen)
	}
	claimedReply, err := f.repo.ClaimContinuationStep(context.Background(), agentruntime.ContinuationClaim{
		RunID: f.runID, WorkerID: "delivery-worker", LeaseTTL: time.Minute, Now: now.Add(2 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&model.AgentSession{}).Where("id = ?", f.sessionID).
		Update("active_run_id", "newer-run").Error; err != nil {
		t.Fatal(err)
	}
	complete := agentruntime.CompleteReplyDeliveryRequest{
		StepID: claimedReply.ID, WorkerID: claimedReply.WorkerID, AttemptCount: claimedReply.AttemptCount,
		MessageID: "om-result", FinishedAt: now.Add(3 * time.Second),
	}
	if err := f.repo.CompleteReplyDelivery(context.Background(), complete); !errors.Is(err, agentruntime.ErrLeaseLost) {
		t.Fatalf("completion with newer active run error = %v, want lease lost", err)
	}
	var session model.AgentSession
	if err := f.db.First(&session, "id = ?", f.sessionID).Error; err != nil {
		t.Fatal(err)
	}
	if session.ActiveRunID != "newer-run" {
		t.Fatalf("newer active run cleared: %#v", session)
	}
	if err := f.db.Model(&model.AgentSession{}).Where("id = ?", f.sessionID).
		Update("active_run_id", f.runID).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.repo.CompleteReplyDelivery(context.Background(), complete); err != nil {
		t.Fatal(err)
	}
	var stored model.AgentStep
	if err := f.db.First(&stored, "id = ?", claimedReply.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.ExternalRef != "om-result" || stored.Status != string(agentruntime.StepStatusCompleted) {
		t.Fatalf("delivered step = %#v", stored)
	}
}

func TestRepairContinuationRestoresExactlyOneMissingStage(t *testing.T) {
	f, _, request := newScheduleInteractionFixture(t)
	if _, err := f.repo.ClaimScheduleInteraction(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.ExecuteScheduleInteraction(
		context.Background(), request,
		func(context.Context, agentruntime.ScheduleEditTrustedInput) (agentruntime.ScheduleInteractionOutcome, error) {
			return validScheduleOutcome(request, "task-1", "new-name"), nil
		},
	); err != nil {
		t.Fatal(err)
	}
	if err := f.db.Where("run_id = ? AND kind = ?", f.runID, string(agentruntime.StepKindDecide)).
		Delete(&model.AgentStep{}).Error; err != nil {
		t.Fatal(err)
	}
	f.createStep(t, &agentruntime.AgentStep{
		Index: -2, Kind: agentruntime.StepKindObserve, Status: agentruntime.StepStatusCompleted,
		InputJSON: `{"ordinary":true}`, OutputJSON: `{"observed":true}`,
		DedupeKey: "ordinary:observe",
	})
	f.createStep(t, &agentruntime.AgentStep{
		Index: -1, Kind: agentruntime.StepKindReply, Status: agentruntime.StepStatusCompleted,
		InputJSON: `{"ordinary":true}`, OutputJSON: `{"message_id":"om-old"}`,
		DedupeKey: "ordinary:reply",
	})
	now := request.ResolvedAt.Add(time.Minute)
	if err := f.repo.RepairContinuation(context.Background(), f.runID, now); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.RepairContinuation(context.Background(), f.runID, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := f.db.Model(&model.AgentStep{}).Where(
		"run_id = ? AND kind = ?", f.runID, string(agentruntime.StepKindDecide),
	).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("repaired continuation count = %d", count)
	}
}

func TestRepairContinuationNoOpsForCompletedDecisionWithFutureReplyRetry(t *testing.T) {
	f, _, request := newScheduleInteractionFixture(t)
	if _, err := f.repo.ClaimScheduleInteraction(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.ExecuteScheduleInteraction(
		context.Background(), request,
		func(context.Context, agentruntime.ScheduleEditTrustedInput) (agentruntime.ScheduleInteractionOutcome, error) {
			return validScheduleOutcome(request, "task-1", "new-name"), nil
		},
	); err != nil {
		t.Fatal(err)
	}
	now := request.ResolvedAt.Add(time.Second)
	decision, err := f.repo.ClaimContinuationStep(context.Background(), agentruntime.ContinuationClaim{
		RunID: f.runID, WorkerID: "model", LeaseTTL: time.Minute, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	reply, err := f.repo.PersistDecision(context.Background(), agentruntime.PersistDecisionRequest{
		StepID: decision.ID, WorkerID: decision.WorkerID, AttemptCount: decision.AttemptCount,
		Decision: agentruntime.TurnDecision{
			Decision: agentruntime.TurnDecisionReply, Reply: "完成", Reason: "反馈",
		},
		FinishedAt: now.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	claimedReply, err := f.repo.ClaimContinuationStep(context.Background(), agentruntime.ContinuationClaim{
		RunID: f.runID, WorkerID: "delivery", LeaseTTL: time.Minute, Now: now.Add(2 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	retryAt := now.Add(time.Hour)
	if err := f.repo.RetryContinuationStep(context.Background(), agentruntime.RetryStepRequest{
		StepID: claimedReply.ID, WorkerID: claimedReply.WorkerID,
		AttemptCount: claimedReply.AttemptCount, ErrorText: "temporary", RetryAt: retryAt,
	}); err != nil {
		t.Fatal(err)
	}
	generator := &continuationGeneratorCounter{}
	deliverer := &continuationDelivererCounter{}
	earlyProcessor := agentruntime.NewContinuationProcessor(
		f.repo, generator, deliverer, agentruntime.ContinuationProcessorConfig{
			WorkerID: "early", LeaseTTL: time.Minute, RetryDelay: time.Second,
			Now: func() time.Time { return retryAt.Add(-time.Second) },
		},
	)
	if err := earlyProcessor.ProcessRun(context.Background(), f.runID); err != nil {
		t.Fatalf("early ProcessRun() error = %v", err)
	}
	if generator.calls != 0 || deliverer.calls != 0 {
		t.Fatalf("early generator=%d deliverer=%d", generator.calls, deliverer.calls)
	}
	dueProcessor := agentruntime.NewContinuationProcessor(
		f.repo, generator, deliverer, agentruntime.ContinuationProcessorConfig{
			WorkerID: "due", LeaseTTL: time.Minute, RetryDelay: time.Second,
			Now: func() time.Time { return retryAt },
		},
	)
	if err := dueProcessor.ProcessRun(context.Background(), f.runID); err != nil {
		t.Fatalf("due ProcessRun() error = %v", err)
	}
	if generator.calls != 0 || deliverer.calls != 1 {
		t.Fatalf("due generator=%d deliverer=%d", generator.calls, deliverer.calls)
	}
	var delivered model.AgentStep
	if err := f.db.First(&delivered, "id = ?", reply.ID).Error; err != nil {
		t.Fatal(err)
	}
	if delivered.Status != string(agentruntime.StepStatusCompleted) ||
		delivered.ExternalRef != "om-retry-delivered" {
		t.Fatalf("delivered reply = %#v", delivered)
	}
}

func TestPersistDecisionRollsBackModelCompletionAndReplyEnqueue(t *testing.T) {
	f := newRepositoryFixture(t, agentruntime.RunStatusRunning)
	if err := f.db.Model(&model.AgentSession{}).Where("id = ?", f.sessionID).
		Update("active_run_id", f.runID).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	step := &agentruntime.AgentStep{
		Index: 1, Kind: agentruntime.StepKindDecide, Status: agentruntime.StepStatusRunning,
		InputJSON: `{"version":1,"source_step_id":"source"}`, OutputJSON: "{}",
		DedupeKey: "continuation:rollback", WorkerID: "worker", AttemptCount: 1,
		LeaseExpiresAt: now.Add(time.Minute),
	}
	f.createStep(t, step)
	remove := failAgentRunUpdate(t, f.db)
	_, err := f.repo.PersistDecision(context.Background(), agentruntime.PersistDecisionRequest{
		StepID: step.ID, WorkerID: "worker", AttemptCount: 1,
		Decision: agentruntime.TurnDecision{
			Decision: agentruntime.TurnDecisionReply, Reply: "完成", Reason: "反馈",
		},
		FinishedAt: now,
	})
	remove()
	if err == nil {
		t.Fatal("PersistDecision() error = nil")
	}
	var stored model.AgentStep
	if err := f.db.First(&stored, "id = ?", step.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != string(agentruntime.StepStatusRunning) {
		t.Fatalf("model step = %#v", stored)
	}
	var replies int64
	if err := f.db.Model(&model.AgentStep{}).Where(
		"run_id = ? AND kind = ?", f.runID, string(agentruntime.StepKindReply),
	).Count(&replies).Error; err != nil {
		t.Fatal(err)
	}
	if replies != 0 {
		t.Fatalf("reply steps after rollback = %d", replies)
	}
}

func TestCompleteReplyDeliveryRollsBackStepWhenRunFinalizeFails(t *testing.T) {
	f := newRepositoryFixture(t, agentruntime.RunStatusRunning)
	if err := f.db.Model(&model.AgentSession{}).Where("id = ?", f.sessionID).
		Update("active_run_id", f.runID).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	step := &agentruntime.AgentStep{
		Index: 1, Kind: agentruntime.StepKindReply, Status: agentruntime.StepStatusRunning,
		InputJSON:  `{"version":1,"step_id":"reply","run_id":"run","text":"完成","chat_id":"oc","idempotency_key":"reply"}`,
		OutputJSON: "{}", DedupeKey: "continuation:reply", WorkerID: "worker",
		AttemptCount: 1, LeaseExpiresAt: now.Add(time.Minute),
	}
	f.createStep(t, step)
	remove := failAgentRunUpdate(t, f.db)
	err := f.repo.CompleteReplyDelivery(context.Background(), agentruntime.CompleteReplyDeliveryRequest{
		StepID: step.ID, WorkerID: "worker", AttemptCount: 1,
		MessageID: "om-sent", FinishedAt: now,
	})
	remove()
	if err == nil {
		t.Fatal("CompleteReplyDelivery() error = nil")
	}
	var stored model.AgentStep
	if err := f.db.First(&stored, "id = ?", step.ID).Error; err != nil {
		t.Fatal(err)
	}
	var frozen agentruntime.ReplyRequest
	if json.Unmarshal([]byte(stored.InputJSON), &frozen) != nil {
		t.Fatal("decode frozen reply after rollback")
	}
	if stored.Status != string(agentruntime.StepStatusRunning) ||
		stored.ExternalRef != "" || frozen.IdempotencyKey != "reply" {
		t.Fatalf("reply step after rollback = %#v", stored)
	}
}

func failAgentRunUpdate(t *testing.T, db *gorm.DB) func() {
	t.Helper()
	name := "agentstore_fail_run_update_" + uuid.NewV4().String()
	if err := db.Callback().Update().Before("gorm:update").Register(name, func(tx *gorm.DB) {
		if tx.Statement != nil && tx.Statement.Table == model.TableNameAgentRun {
			tx.AddError(errors.New("forced run update failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	return func() {
		if err := db.Callback().Update().Remove(name); err != nil {
			t.Errorf("remove callback: %v", err)
		}
	}
}
