package agentstore

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/opensearch"
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
	var sourceOutbox model.AgentProjectionOutbox
	if err := f.db.First(&sourceOutbox, "step_id = ?", input.LatestOutcome.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := opensearch.UpsertData(
		context.Background(), sourceOutbox.IndexAlias, sourceOutbox.DocumentID,
		json.RawMessage(sourceOutbox.PayloadJSON),
	); !errors.Is(err, opensearch.ErrUnavailable) {
		t.Fatalf("unavailable OpenSearch error = %v", err)
	}
	reloaded, err := f.repo.LoadContinuationContext(context.Background(), agentruntime.LoadContinuationContextRequest{
		RunID: f.runID, AnchorStepID: modelStep.ID, RecentLimit: 20,
	})
	if err != nil || reloaded.LatestOutcome.ID != input.LatestOutcome.ID ||
		len(reloaded.RecentSteps) != len(input.RecentSteps) {
		t.Fatalf("Postgres context after OpenSearch outage = %#v, err=%v", reloaded, err)
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
	var decisionOutbox model.AgentProjectionOutbox
	if err := f.db.First(&decisionOutbox, "step_id = ?", modelStep.ID).Error; err != nil {
		t.Fatal(err)
	}
	var decisionProjection map[string]any
	if json.Unmarshal([]byte(decisionOutbox.PayloadJSON), &decisionProjection) != nil {
		t.Fatalf("decision projection = %s", decisionOutbox.PayloadJSON)
	}
	if decisionOutbox.IndexAlias != sourceOutbox.IndexAlias ||
		decisionOutbox.DocumentID == sourceOutbox.DocumentID ||
		decisionProjection["event_type"] != "model_decision" ||
		decisionProjection["step_id"] != modelStep.ID ||
		decisionProjection["source_step_id"] != input.LatestOutcome.ID ||
		decisionProjection["step_status"] != "completed" ||
		decisionProjection["outcome_status"] != "reply" ||
		decisionProjection["content"] != "修改完成" {
		t.Fatalf("decision outbox = %#v payload=%#v", decisionOutbox, decisionProjection)
	}
	if _, leaked := decisionProjection["worker_id"]; leaked {
		t.Fatalf("decision projection leaks worker: %#v", decisionProjection)
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
	var replyOutbox model.AgentProjectionOutbox
	if err := f.db.First(&replyOutbox, "step_id = ?", claimedReply.ID).Error; err != nil {
		t.Fatal(err)
	}
	var replyProjection struct {
		EventType     string `json:"event_type"`
		StepID        string `json:"step_id"`
		SourceStepID  string `json:"source_step_id"`
		StepStatus    string `json:"step_status"`
		OutcomeStatus string `json:"outcome_status"`
		Content       string `json:"content"`
		ExternalRef   string `json:"external_ref"`
		Structured    struct {
			MessageID        string `json:"message_id"`
			Text             string `json:"text"`
			ChatID           string `json:"chat_id"`
			TriggerMessageID string `json:"trigger_message_id"`
			DeliveryKey      string `json:"delivery_key"`
			Route            string `json:"route"`
		} `json:"structured_payload"`
	}
	if json.Unmarshal([]byte(replyOutbox.PayloadJSON), &replyProjection) != nil {
		t.Fatalf("reply projection = %s", replyOutbox.PayloadJSON)
	}
	if replyOutbox.IndexAlias != sourceOutbox.IndexAlias ||
		replyOutbox.DocumentID == decisionOutbox.DocumentID ||
		replyProjection.EventType != "agent_reply" ||
		replyProjection.StepID != claimedReply.ID ||
		replyProjection.SourceStepID != modelStep.ID ||
		replyProjection.StepStatus != "completed" ||
		replyProjection.OutcomeStatus != "delivered" ||
		replyProjection.Content != "修改完成" ||
		replyProjection.ExternalRef != "om-result" ||
		replyProjection.Structured.MessageID != "om-result" ||
		replyProjection.Structured.Text != "修改完成" ||
		replyProjection.Structured.ChatID == "" ||
		replyProjection.Structured.TriggerMessageID == "" ||
		replyProjection.Structured.DeliveryKey != claimedReply.ID ||
		replyProjection.Structured.Route != "reply" {
		t.Fatalf("reply outbox = %#v payload=%#v", replyOutbox, replyProjection)
	}
}

func TestSuppressReplyDeliveryCompletesRunWithoutExternalMessage(t *testing.T) {
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
	decide, err := f.repo.ClaimContinuationStep(context.Background(), agentruntime.ContinuationClaim{
		RunID: f.runID, WorkerID: "model-worker", LeaseTTL: time.Minute, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	reply, err := f.repo.PersistDecision(context.Background(), agentruntime.PersistDecisionRequest{
		StepID: decide.ID, WorkerID: decide.WorkerID, AttemptCount: decide.AttemptCount,
		Decision: agentruntime.TurnDecision{
			Decision: agentruntime.TurnDecisionReply, Reply: "should not send", Reason: "queued before toggle",
		},
		FinishedAt: now.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := f.repo.ClaimContinuationStep(context.Background(), agentruntime.ContinuationClaim{
		RunID: f.runID, WorkerID: "disabled-worker", LeaseTTL: time.Minute, Now: now.Add(2 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if claimed.ID != reply.ID {
		t.Fatalf("claimed reply = %s, want %s", claimed.ID, reply.ID)
	}
	if err := f.repo.SuppressReplyDelivery(context.Background(), agentruntime.SuppressReplyDeliveryRequest{
		StepID: claimed.ID, WorkerID: claimed.WorkerID, AttemptCount: claimed.AttemptCount,
		Reason: "callback continuation disabled", FinishedAt: now.Add(3 * time.Second),
	}); err != nil {
		t.Fatalf("SuppressReplyDelivery() error = %v", err)
	}

	var run model.AgentRun
	var session model.AgentSession
	var stored model.AgentStep
	if err := f.db.First(&run, "id = ?", f.runID).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.First(&session, "id = ?", f.sessionID).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.First(&stored, "id = ?", reply.ID).Error; err != nil {
		t.Fatal(err)
	}
	if run.Status != string(agentruntime.RunStatusCompleted) ||
		session.ActiveRunID != "" ||
		stored.Status != string(agentruntime.StepStatusCompleted) ||
		stored.ExternalRef != "" {
		t.Fatalf("run=%#v session=%#v reply=%#v", run, session, stored)
	}
	var output map[string]any
	if json.Unmarshal([]byte(stored.OutputJSON), &output) != nil ||
		output["status"] != "suppressed" ||
		output["reason"] != "callback continuation disabled" {
		t.Fatalf("suppressed output = %s", stored.OutputJSON)
	}
	var decisionOutbox model.AgentProjectionOutbox
	var suppressedOutbox model.AgentProjectionOutbox
	if err := f.db.First(&decisionOutbox, "step_id = ?", decide.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.First(&suppressedOutbox, "step_id = ?", reply.ID).Error; err != nil {
		t.Fatal(err)
	}
	if decisionOutbox.DocumentID == suppressedOutbox.DocumentID ||
		!strings.HasSuffix(suppressedOutbox.DocumentID, ":"+reply.ID) {
		t.Fatalf("decision document=%q suppressed document=%q, want distinct per-step IDs",
			decisionOutbox.DocumentID, suppressedOutbox.DocumentID)
	}
}

func TestProjectionReplyRouteMatchesDeliveryTarget(t *testing.T) {
	if got := projectionReplyRoute(agentruntime.ReplyRequest{TriggerMessageID: "om-trigger"}); got != "reply" {
		t.Fatalf("reply route = %q", got)
	}
	if got := projectionReplyRoute(agentruntime.ReplyRequest{ChatID: "oc-chat"}); got != "create" {
		t.Fatalf("create route = %q", got)
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

func TestRepairContinuationRequeuesExpiredRunningStage(t *testing.T) {
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
	claimedAt := request.ResolvedAt.Add(time.Second)
	claimed, err := f.repo.ClaimContinuationStep(context.Background(), agentruntime.ContinuationClaim{
		RunID: f.runID, WorkerID: "lost-worker", LeaseTTL: time.Minute, Now: claimedAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	recoverAt := claimedAt.Add(2 * time.Minute)
	if err := f.repo.RepairContinuation(context.Background(), f.runID, recoverAt); err != nil {
		t.Fatalf("RepairContinuation() error = %v", err)
	}
	var step model.AgentStep
	var run model.AgentRun
	if err := f.db.First(&step, "id = ?", claimed.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.First(&run, "id = ?", f.runID).Error; err != nil {
		t.Fatal(err)
	}
	if step.Status != string(agentruntime.StepStatusQueued) ||
		step.WorkerID != "" ||
		!step.LeaseExpiresAt.Equal(recoverAt) ||
		run.Status != string(agentruntime.RunStatusQueued) {
		t.Fatalf("repaired step=%#v run=%#v", step, run)
	}
}

func TestRepairContinuationRequeuesExpiredCapabilityStage(t *testing.T) {
	f := newRepositoryFixture(t, agentruntime.RunStatusRunning)
	if err := f.db.Model(&model.AgentSession{}).Where("id = ?", f.sessionID).
		Update("active_run_id", f.runID).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	source := &agentruntime.AgentStep{
		Index: 1, Kind: agentruntime.StepKindCardAction,
		Status:     agentruntime.StepStatusCompleted,
		InputJSON:  `{"version":1,"action_id":"confirm"}`,
		OutputJSON: `{}`, ExternalRef: "interaction-1",
		DedupeKey: "card:event",
	}
	f.createStep(t, source)
	capability := &agentruntime.AgentStep{
		Index: 2, Kind: agentruntime.StepKindCapabilityCall,
		Status:         agentruntime.StepStatusRunning,
		CapabilityName: "schedule.update",
		InputJSON: `{
			"version":1,
			"source_step_id":"` + source.ID + `",
			"interaction_id":"interaction-1",
			"action_id":"confirm",
			"descriptor":{"capability_name":"schedule.update"}
		}`,
		OutputJSON: `{}`, ExternalRef: "interaction-1",
		DedupeKey: "card:event:capability",
		WorkerID:  "lost-capability-worker", AttemptCount: 1,
		LeaseExpiresAt: now.Add(-time.Minute),
	}
	f.createStep(t, capability)
	if err := f.repo.RepairContinuation(
		context.Background(),
		f.runID,
		now,
	); err != nil {
		t.Fatalf("RepairContinuation() error = %v", err)
	}
	var repaired model.AgentStep
	var run model.AgentRun
	if err := f.db.First(&repaired, "id = ?", capability.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.First(&run, "id = ?", f.runID).Error; err != nil {
		t.Fatal(err)
	}
	if repaired.Status != string(agentruntime.StepStatusQueued) ||
		repaired.WorkerID != "" ||
		!repaired.LeaseExpiresAt.Equal(now) ||
		run.Status != string(agentruntime.RunStatusQueued) {
		t.Fatalf("repaired capability=%#v run=%#v", repaired, run)
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
