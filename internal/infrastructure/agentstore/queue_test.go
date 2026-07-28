package agentstore

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	uuid "github.com/satori/go.uuid"
)

func TestAppendEventDeduplicatesAndAdvancesIndex(t *testing.T) {
	f := newRepositoryFixture(t, agentruntime.RunStatusQueued)
	ctx := context.Background()
	first := &agentruntime.AgentStep{
		ID: "step_" + uuid.NewV4().String(), RunID: f.runID, Index: 99,
		Kind: agentruntime.StepKindObserve, Status: agentruntime.StepStatusQueued,
		InputJSON: "{}", OutputJSON: "{}", DedupeKey: "event:one", CreatedAt: time.Now().UTC(),
	}
	stored, err := f.repo.AppendEvent(ctx, first, testProjection(f.runID))
	if err != nil {
		t.Fatalf("AppendEvent(first): %v", err)
	}
	if stored.Index != 1 {
		t.Fatalf("first index = %d, want 1", stored.Index)
	}
	outbox, err := findProjectionOutboxByStep(f.db, stored.ID)
	if err != nil {
		t.Fatalf("find outbox by step: %v", err)
	}
	expectedOutbox := newProjectionOutbox(stored.ID, testProjection(f.runID), outbox.CreatedAt)
	if outbox.ID != expectedOutbox.ID || outbox.IndexAlias != expectedOutbox.IndexAlias ||
		outbox.DocumentID != expectedOutbox.DocumentID || !equalJSON(outbox.PayloadJSON, expectedOutbox.PayloadJSON) ||
		outbox.Status != "pending" || !outbox.NextAttemptAt.Equal(outbox.CreatedAt) {
		t.Fatalf("outbox = %#v, want stable pending projection", outbox)
	}

	duplicate := *first
	duplicate.ID = "step_" + uuid.NewV4().String()
	again, err := f.repo.AppendEvent(ctx, &duplicate, testProjection(f.runID))
	if err != nil {
		t.Fatalf("AppendEvent(duplicate): %v", err)
	}
	if again.ID != stored.ID || again.Index != stored.Index {
		t.Fatalf("duplicate returned %#v, want existing %#v", again, stored)
	}

	second := *first
	second.ID = "step_" + uuid.NewV4().String()
	second.DedupeKey = "event:two"
	storedSecond, err := f.repo.AppendEvent(ctx, &second, testProjection(f.runID))
	if err != nil {
		t.Fatalf("AppendEvent(second): %v", err)
	}
	if storedSecond.Index != 2 {
		t.Fatalf("second index = %d, want 2", storedSecond.Index)
	}

	var steps int64
	if err := f.db.Model(&model.AgentStep{}).Where("run_id = ? AND dedupe_key = ?", f.runID, "event:one").Count(&steps).Error; err != nil {
		t.Fatal(err)
	}
	if steps != 1 {
		t.Fatalf("deduplicated steps = %d, want 1", steps)
	}
	var outboxes int64
	if err := f.db.Model(&model.AgentProjectionOutbox{}).
		Joins("JOIN agent_steps ON agent_steps.id = agent_projection_outbox.step_id").
		Where("agent_steps.run_id = ?", f.runID).Count(&outboxes).Error; err != nil {
		t.Fatal(err)
	}
	if outboxes != 2 {
		t.Fatalf("outboxes = %d, want 2", outboxes)
	}
}

func TestAppendEventRejectsInvalidProjectionWithoutPersistingStep(t *testing.T) {
	f := newRepositoryFixture(t, agentruntime.RunStatusQueued)
	step := &agentruntime.AgentStep{
		ID: "step_" + uuid.NewV4().String(), RunID: f.runID, Kind: agentruntime.StepKindObserve,
		Status: agentruntime.StepStatusQueued, InputJSON: "{}", OutputJSON: "{}",
		DedupeKey: "event:invalid-projection", CreatedAt: time.Now().UTC(),
	}
	projection := testProjection(f.runID)
	projection.DocumentID = ""
	_, err := f.repo.AppendEvent(context.Background(), step, projection)
	if !errors.Is(err, agentruntime.ErrInvalidRuntimeContract) {
		t.Fatalf("AppendEvent() error = %v, want ErrInvalidRuntimeContract", err)
	}
	var count int64
	if err := f.db.Model(&model.AgentStep{}).Where("id = ?", step.ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("persisted steps = %d, want 0", count)
	}
}

func TestClaimQueuedStepAllowsOnlyOneCompetingWorker(t *testing.T) {
	f := newRepositoryFixture(t, agentruntime.RunStatusQueued)
	f.createStep(t, &agentruntime.AgentStep{
		Index: 1, Kind: agentruntime.StepKindCapabilityCall, Status: agentruntime.StepStatusQueued,
	})
	now := time.Now().UTC()
	repositories := []*Repository{NewRepository(f.db), NewRepository(f.db)}
	type result struct {
		step *agentruntime.AgentStep
		err  error
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for i, repo := range repositories {
		wg.Add(1)
		go func(worker int, repository *Repository) {
			defer wg.Done()
			step, err := repository.ClaimQueuedStep(context.Background(), agentruntime.StepClaim{
				WorkerID: "worker-" + string(rune('a'+worker)), LeaseTTL: time.Minute, Now: now,
			})
			results <- result{step: step, err: err}
		}(i, repo)
	}
	wg.Wait()
	close(results)
	var claimed, empty int
	for result := range results {
		switch {
		case result.err == nil:
			claimed++
			if result.step.Status != agentruntime.StepStatusRunning || result.step.AttemptCount != 1 {
				t.Fatalf("claimed step = %#v", result.step)
			}
		case errors.Is(result.err, agentruntime.ErrNotFound):
			empty++
		default:
			t.Fatalf("ClaimQueuedStep() error = %v", result.err)
		}
	}
	if claimed != 1 || empty != 1 {
		t.Fatalf("claimed=%d empty=%d, want 1/1", claimed, empty)
	}
}

func TestRetryStepHonorsRetryAt(t *testing.T) {
	f := newRepositoryFixture(t, agentruntime.RunStatusRunning)
	now := time.Now().UTC().Truncate(time.Microsecond)
	step := &agentruntime.AgentStep{
		Index: 1, Kind: agentruntime.StepKindCapabilityCall, Status: agentruntime.StepStatusRunning,
		WorkerID: "worker-a", LeaseExpiresAt: now.Add(time.Minute),
	}
	f.createStep(t, step)
	retryAt := now.Add(10 * time.Minute)
	if err := f.repo.RetryStep(context.Background(), agentruntime.RetryStepRequest{
		StepID: step.ID, ErrorText: "temporary", RetryAt: retryAt,
	}); err != nil {
		t.Fatalf("RetryStep(): %v", err)
	}
	_, err := f.repo.ClaimQueuedStep(context.Background(), agentruntime.StepClaim{
		WorkerID: "worker-b", LeaseTTL: time.Minute, Now: retryAt.Add(-time.Microsecond),
	})
	if !errors.Is(err, agentruntime.ErrNotFound) {
		t.Fatalf("early claim error = %v, want ErrNotFound", err)
	}
	claimed, err := f.repo.ClaimQueuedStep(context.Background(), agentruntime.StepClaim{
		WorkerID: "worker-b", LeaseTTL: time.Minute, Now: retryAt,
	})
	if err != nil {
		t.Fatalf("due claim: %v", err)
	}
	if claimed.ID != step.ID || claimed.AttemptCount != 1 {
		t.Fatalf("claimed = %#v", claimed)
	}
}

func TestCompleteStepRequiresRunningAndClearsLease(t *testing.T) {
	f := newRepositoryFixture(t, agentruntime.RunStatusRunning)
	now := time.Now().UTC().Truncate(time.Microsecond)
	step := &agentruntime.AgentStep{
		Index: 1, Kind: agentruntime.StepKindCapabilityCall, Status: agentruntime.StepStatusRunning,
		WorkerID: "worker-a", LeaseExpiresAt: now.Add(time.Minute),
	}
	f.createStep(t, step)
	output := []byte(`{"message_id":"om_done"}`)
	if err := f.repo.CompleteStep(context.Background(), agentruntime.CompleteStepRequest{
		StepID: step.ID, Output: output, FinishedAt: now,
	}); err != nil {
		t.Fatalf("CompleteStep(): %v", err)
	}
	var stored model.AgentStep
	if err := f.db.First(&stored, "id = ?", step.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != string(agentruntime.StepStatusCompleted) || !equalJSON(stored.OutputJSON, string(output)) ||
		stored.WorkerID != "" || !stored.LeaseExpiresAt.IsZero() || !stored.FinishedAt.Equal(now) {
		t.Fatalf("completed step = %#v", stored)
	}
	err := f.repo.CompleteStep(context.Background(), agentruntime.CompleteStepRequest{
		StepID: step.ID, Output: output, FinishedAt: now,
	})
	if !errors.Is(err, agentruntime.ErrNotFound) {
		t.Fatalf("second CompleteStep() error = %v, want ErrNotFound", err)
	}
}

func equalJSON(left, right string) bool {
	var leftValue, rightValue any
	if json.Unmarshal([]byte(left), &leftValue) != nil || json.Unmarshal([]byte(right), &rightValue) != nil {
		return false
	}
	return reflect.DeepEqual(leftValue, rightValue)
}

func TestQueueMethodsPreserveRequestValidationErrors(t *testing.T) {
	repo := &Repository{}
	if _, err := repo.ClaimQueuedStep(context.Background(), agentruntime.StepClaim{}); !errors.Is(err, agentruntime.ErrInvalidRuntimeContract) {
		t.Fatalf("ClaimQueuedStep() error = %v", err)
	}
	if err := repo.CompleteStep(context.Background(), agentruntime.CompleteStepRequest{}); !errors.Is(err, agentruntime.ErrInvalidRuntimeContract) {
		t.Fatalf("CompleteStep() error = %v", err)
	}
	if err := repo.RetryStep(context.Background(), agentruntime.RetryStepRequest{}); !errors.Is(err, agentruntime.ErrInvalidRuntimeContract) {
		t.Fatalf("RetryStep() error = %v", err)
	}
	if _, err := repo.ReclaimStaleSteps(context.Background(), agentruntime.ReclaimStaleStepsRequest{}); !errors.Is(err, agentruntime.ErrInvalidRuntimeContract) {
		t.Fatalf("ReclaimStaleSteps() error = %v", err)
	}
}

func TestReclaimStaleStepsLeavesTerminalStepsUntouched(t *testing.T) {
	f := newRepositoryFixture(t, agentruntime.RunStatusRunning)
	now := time.Now().UTC().Truncate(time.Microsecond)
	running := &agentruntime.AgentStep{
		Index: 1, Kind: agentruntime.StepKindCapabilityCall, Status: agentruntime.StepStatusRunning,
		WorkerID: "stale-worker", LeaseExpiresAt: now.Add(-time.Minute),
	}
	terminal := &agentruntime.AgentStep{
		Index: 2, Kind: agentruntime.StepKindCapabilityCall, Status: agentruntime.StepStatusCompleted,
		WorkerID: "old-worker", LeaseExpiresAt: now.Add(-time.Minute),
	}
	f.createStep(t, running)
	f.createStep(t, terminal)
	count, err := f.repo.ReclaimStaleSteps(context.Background(), agentruntime.ReclaimStaleStepsRequest{Now: now, Limit: 10})
	if err != nil {
		t.Fatalf("ReclaimStaleSteps(): %v", err)
	}
	if count != 1 {
		t.Fatalf("reclaimed = %d, want 1", count)
	}
	var gotRunning, gotTerminal model.AgentStep
	if err := f.db.First(&gotRunning, "id = ?", running.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.First(&gotTerminal, "id = ?", terminal.ID).Error; err != nil {
		t.Fatal(err)
	}
	if gotRunning.Status != string(agentruntime.StepStatusQueued) || gotRunning.WorkerID != "" || !gotRunning.LeaseExpiresAt.IsZero() {
		t.Fatalf("reclaimed running step = %#v", gotRunning)
	}
	if gotTerminal.Status != string(agentruntime.StepStatusCompleted) || gotTerminal.WorkerID != "old-worker" {
		t.Fatalf("terminal step changed = %#v", gotTerminal)
	}
}
