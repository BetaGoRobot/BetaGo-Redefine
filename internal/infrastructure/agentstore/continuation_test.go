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

func TestClaimContinuationStepFiltersKindsAndTransitionsRun(t *testing.T) {
	f := newRepositoryFixture(t, agentruntime.RunStatusQueued)
	if err := f.db.Model(&model.AgentSession{}).Where("id = ?", f.sessionID).
		Update("active_run_id", f.runID).Error; err != nil {
		t.Fatal(err)
	}
	f.createStep(t, &agentruntime.AgentStep{
		Index: 1, Kind: agentruntime.StepKindCapabilityCall, Status: agentruntime.StepStatusQueued,
	})
	_, err := f.repo.ClaimContinuationStep(context.Background(), agentruntime.ContinuationClaim{
		RunID: f.runID, WorkerID: "worker", LeaseTTL: time.Minute, Now: time.Now().UTC(),
	})
	if !errors.Is(err, agentruntime.ErrNotFound) {
		t.Fatalf("capability claim error = %v, want not found", err)
	}

	if err := f.db.Model(&model.AgentStep{}).Where("run_id = ?", f.runID).
		Update("status", string(agentruntime.StepStatusCompleted)).Error; err != nil {
		t.Fatal(err)
	}
	f.createStep(t, &agentruntime.AgentStep{
		Index: 2, Kind: agentruntime.StepKindObserve, Status: agentruntime.StepStatusQueued,
		InputJSON: `{"version":1,"source_step_id":"source"}`, DedupeKey: "continuation:test",
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
	if claimed.Kind != agentruntime.StepKindObserve || run.Status != string(agentruntime.RunStatusRunning) {
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
	if err := f.db.Where("run_id = ? AND kind = ?", f.runID, string(agentruntime.StepKindObserve)).
		Delete(&model.AgentStep{}).Error; err != nil {
		t.Fatal(err)
	}
	now := request.ResolvedAt.Add(time.Minute)
	if err := f.repo.RepairContinuation(context.Background(), f.runID, now); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.RepairContinuation(context.Background(), f.runID, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := f.db.Model(&model.AgentStep{}).Where(
		"run_id = ? AND kind = ?", f.runID, string(agentruntime.StepKindObserve),
	).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("repaired continuation count = %d", count)
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
		Index: 1, Kind: agentruntime.StepKindObserve, Status: agentruntime.StepStatusRunning,
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
		InputJSON:  `{"step_id":"reply","run_id":"run","text":"完成","chat_id":"oc","idempotency_key":"reply"}`,
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
