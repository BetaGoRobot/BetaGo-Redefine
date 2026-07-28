package agentstore

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	uuid "github.com/satori/go.uuid"
	"gorm.io/gorm"
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
	var firstOutbox, secondOutbox model.AgentProjectionOutbox
	if err := f.db.First(&firstOutbox, "step_id = ?", stored.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.First(&secondOutbox, "step_id = ?", storedSecond.ID).Error; err != nil {
		t.Fatal(err)
	}
	if firstOutbox.DocumentID == secondOutbox.DocumentID {
		t.Fatalf("different steps overwrite document %q", firstOutbox.DocumentID)
	}
	if firstOutbox.DocumentID != f.runID+":"+stored.ID ||
		secondOutbox.DocumentID != f.runID+":"+storedSecond.ID {
		t.Fatalf("stable per-step document IDs = %q, %q", firstOutbox.DocumentID, secondOutbox.DocumentID)
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

func TestAppendEventRejectsDocumentIDThatOverflowsAfterStepScoping(t *testing.T) {
	f := newRepositoryFixture(t, agentruntime.RunStatusQueued)
	step := &agentruntime.AgentStep{
		ID: "step_" + uuid.NewV4().String(), RunID: f.runID,
		Kind: agentruntime.StepKindObserve, Status: agentruntime.StepStatusQueued,
		InputJSON: "{}", OutputJSON: "{}", DedupeKey: "event:oversized-doc",
		CreatedAt: time.Now().UTC(),
	}
	projection := testProjection(f.runID)
	projection.DocumentID = strings.Repeat("d", 1000)
	if _, err := f.repo.AppendEvent(context.Background(), step, projection); !errors.Is(err, agentruntime.ErrInvalidRuntimeContract) {
		t.Fatalf("AppendEvent() error = %v", err)
	}
	var count int64
	if err := f.db.Model(&model.AgentStep{}).Where("id = ?", step.ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("step persisted despite oversized scoped document ID")
	}
}

func TestAppendEventQueuedStepWakesRunAndRefreshesRelevance(t *testing.T) {
	f := newRepositoryFixture(t, agentruntime.RunStatusRunning)
	createdAt := time.Date(2026, 7, 28, 22, 0, 0, 0, time.UTC)
	step := appendEventStep(f.runID, "event:wake", agentruntime.StepStatusQueued, createdAt)
	stored, err := f.repo.AppendEvent(context.Background(), step, testProjection(f.runID))
	if err != nil {
		t.Fatalf("AppendEvent(): %v", err)
	}
	var run model.AgentRun
	if err := f.db.First(&run, "id = ?", f.runID).Error; err != nil {
		t.Fatal(err)
	}
	if run.Status != string(agentruntime.RunStatusQueued) {
		t.Fatalf("run status = %q, want queued", run.Status)
	}
	if !run.LastRelevantAt.Equal(createdAt) {
		t.Fatalf("last relevant = %v, want %v", run.LastRelevantAt, createdAt)
	}
	if run.CurrentStepIndex != stored.Index {
		t.Fatalf("current index = %d, want %d", run.CurrentStepIndex, stored.Index)
	}
}

func TestAppendEventCompletedStepDoesNotRefreshRunRelevance(t *testing.T) {
	f := newRepositoryFixture(t, agentruntime.RunStatusRunning)
	lastRelevantAt := time.Date(2026, 7, 28, 20, 0, 0, 0, time.UTC)
	if err := f.db.Model(&model.AgentRun{}).Where("id = ?", f.runID).
		Update("last_relevant_at", lastRelevantAt).Error; err != nil {
		t.Fatal(err)
	}
	step := appendEventStep(
		f.runID, "event:unrelated", agentruntime.StepStatusCompleted, lastRelevantAt.Add(time.Hour),
	)
	stored, err := f.repo.AppendEvent(context.Background(), step, testProjection(f.runID))
	if err != nil {
		t.Fatalf("AppendEvent(): %v", err)
	}
	var run model.AgentRun
	if err := f.db.First(&run, "id = ?", f.runID).Error; err != nil {
		t.Fatal(err)
	}
	if run.Status != string(agentruntime.RunStatusRunning) {
		t.Fatalf("run status = %q, want running", run.Status)
	}
	if !run.LastRelevantAt.Equal(lastRelevantAt) {
		t.Fatalf("last relevant = %v, want unchanged %v", run.LastRelevantAt, lastRelevantAt)
	}
	if run.CurrentStepIndex != stored.Index {
		t.Fatalf("current index = %d, want %d", run.CurrentStepIndex, stored.Index)
	}
}

func TestAppendEventRejectsQueuedStepForTerminalRun(t *testing.T) {
	f := newRepositoryFixture(t, agentruntime.RunStatusCompleted)
	step := appendEventStep(f.runID, "event:terminal", agentruntime.StepStatusQueued, time.Now().UTC())
	_, err := f.repo.AppendEvent(context.Background(), step, testProjection(f.runID))
	if err == nil {
		t.Fatal("AppendEvent() error = nil, want terminal run rejection")
	}
	var count int64
	if err := f.db.Model(&model.AgentStep{}).Where("id = ?", step.ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("persisted terminal-run steps = %d, want 0", count)
	}
}

func TestAppendEventRejectsQueuedStepForWaitingRunWithoutChangingState(t *testing.T) {
	tests := []struct {
		name   string
		status agentruntime.RunStatus
		reason agentruntime.WaitingReason
	}{
		{name: "approval", status: agentruntime.RunStatusWaitingApproval, reason: agentruntime.WaitingReasonApproval},
		{name: "callback", status: agentruntime.RunStatusWaitingCallback, reason: agentruntime.WaitingReasonCallback},
		{name: "schedule", status: agentruntime.RunStatusWaitingSchedule, reason: agentruntime.WaitingReasonSchedule},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newRepositoryFixture(t, tt.status)
			lastRelevantAt := time.Date(2026, 7, 28, 20, 0, 0, 0, time.UTC)
			waitingToken := agentruntime.HashInteractionToken("waiting-token")
			if err := f.db.Model(&model.AgentRun{}).Where("id = ?", f.runID).Updates(map[string]any{
				"waiting_reason":   string(tt.reason),
				"waiting_token":    waitingToken,
				"revision":         int64(2),
				"last_relevant_at": lastRelevantAt,
			}).Error; err != nil {
				t.Fatal(err)
			}
			step := appendEventStep(
				f.runID, "event:waiting-"+tt.name, agentruntime.StepStatusQueued, lastRelevantAt.Add(time.Hour),
			)
			_, err := f.repo.AppendEvent(context.Background(), step, testProjection(f.runID))
			if !errors.Is(err, ErrInteractionConflict) {
				t.Fatalf("AppendEvent() error = %v, want ErrInteractionConflict", err)
			}
			var run model.AgentRun
			if err := f.db.First(&run, "id = ?", f.runID).Error; err != nil {
				t.Fatal(err)
			}
			if run.Status != string(tt.status) || run.WaitingReason != string(tt.reason) ||
				run.WaitingToken != waitingToken || run.Revision != 2 ||
				!run.LastRelevantAt.Equal(lastRelevantAt) || run.CurrentStepIndex != 0 {
				t.Fatalf("waiting run changed: %#v", run)
			}
			requireRunStepAndOutboxCounts(t, f, 0, 0, 0)
		})
	}
}

func TestAppendEventCompletedObservationPreservesWaitingState(t *testing.T) {
	f := newRepositoryFixture(t, agentruntime.RunStatusWaitingCallback)
	lastRelevantAt := time.Date(2026, 7, 28, 20, 0, 0, 0, time.UTC)
	waitingToken := agentruntime.HashInteractionToken("waiting-token")
	if err := f.db.Model(&model.AgentRun{}).Where("id = ?", f.runID).Updates(map[string]any{
		"waiting_reason":   string(agentruntime.WaitingReasonCallback),
		"waiting_token":    waitingToken,
		"revision":         int64(2),
		"last_relevant_at": lastRelevantAt,
	}).Error; err != nil {
		t.Fatal(err)
	}
	step := appendEventStep(
		f.runID, "event:waiting-observation", agentruntime.StepStatusCompleted, lastRelevantAt.Add(time.Hour),
	)
	stored, err := f.repo.AppendEvent(context.Background(), step, testProjection(f.runID))
	if err != nil {
		t.Fatalf("AppendEvent(): %v", err)
	}
	var run model.AgentRun
	if err := f.db.First(&run, "id = ?", f.runID).Error; err != nil {
		t.Fatal(err)
	}
	if run.Status != string(agentruntime.RunStatusWaitingCallback) ||
		run.WaitingReason != string(agentruntime.WaitingReasonCallback) ||
		run.WaitingToken != waitingToken || run.Revision != 2 ||
		!run.LastRelevantAt.Equal(lastRelevantAt) || run.CurrentStepIndex != stored.Index {
		t.Fatalf("waiting run changed after completed observation: %#v", run)
	}
	requireRunStepAndOutboxCounts(t, f, stored.Index, 1, 1)
}

func TestAppendEventDedupeHitDoesNotRefreshRun(t *testing.T) {
	f := newRepositoryFixture(t, agentruntime.RunStatusRunning)
	firstAt := time.Date(2026, 7, 28, 20, 0, 0, 0, time.UTC)
	first := appendEventStep(f.runID, "event:dedupe-no-refresh", agentruntime.StepStatusQueued, firstAt)
	stored, err := f.repo.AppendEvent(context.Background(), first, testProjection(f.runID))
	if err != nil {
		t.Fatal(err)
	}
	sentinelRelevantAt := firstAt.Add(time.Hour)
	if err := f.db.Model(&model.AgentRun{}).Where("id = ?", f.runID).Updates(map[string]any{
		"status":           string(agentruntime.RunStatusRunning),
		"last_relevant_at": sentinelRelevantAt,
	}).Error; err != nil {
		t.Fatal(err)
	}
	duplicate := appendEventStep(
		f.runID, first.DedupeKey, agentruntime.StepStatusQueued, sentinelRelevantAt.Add(time.Hour),
	)
	again, err := f.repo.AppendEvent(context.Background(), duplicate, testProjection(f.runID))
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != stored.ID {
		t.Fatalf("duplicate returned %q, want %q", again.ID, stored.ID)
	}
	var run model.AgentRun
	if err := f.db.First(&run, "id = ?", f.runID).Error; err != nil {
		t.Fatal(err)
	}
	if run.Status != string(agentruntime.RunStatusRunning) || !run.LastRelevantAt.Equal(sentinelRelevantAt) {
		t.Fatalf("dedupe refreshed run: status=%q last_relevant_at=%v", run.Status, run.LastRelevantAt)
	}
}

func TestAppendEventReplayReturnsExistingAcrossRunStateChanges(t *testing.T) {
	tests := []struct {
		name          string
		status        agentruntime.RunStatus
		waitingReason agentruntime.WaitingReason
		waitingToken  string
	}{
		{
			name: "waiting", status: agentruntime.RunStatusWaitingApproval,
			waitingReason: agentruntime.WaitingReasonApproval,
			waitingToken:  agentruntime.HashInteractionToken("waiting-token"),
		},
		{name: "terminal", status: agentruntime.RunStatusCompleted},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newRepositoryFixture(t, agentruntime.RunStatusRunning)
			firstAt := time.Date(2026, 7, 28, 20, 0, 0, 0, time.UTC)
			first := appendEventStep(f.runID, "event:cross-state-replay", agentruntime.StepStatusQueued, firstAt)
			stored, err := f.repo.AppendEvent(context.Background(), first, testProjection(f.runID))
			if err != nil {
				t.Fatal(err)
			}
			sentinelRelevantAt := firstAt.Add(time.Hour)
			if err := f.db.Model(&model.AgentRun{}).Where("id = ?", f.runID).Updates(map[string]any{
				"status":           string(tt.status),
				"waiting_reason":   string(tt.waitingReason),
				"waiting_token":    tt.waitingToken,
				"last_relevant_at": sentinelRelevantAt,
			}).Error; err != nil {
				t.Fatal(err)
			}
			replay := appendEventStep(
				f.runID, first.DedupeKey, agentruntime.StepStatusQueued, sentinelRelevantAt.Add(time.Hour),
			)
			again, err := f.repo.AppendEvent(context.Background(), replay, testProjection(f.runID))
			if err != nil {
				t.Fatalf("AppendEvent(replay): %v", err)
			}
			if again.ID != stored.ID || again.Index != stored.Index {
				t.Fatalf("replay returned %#v, want %#v", again, stored)
			}
			var run model.AgentRun
			if err := f.db.First(&run, "id = ?", f.runID).Error; err != nil {
				t.Fatal(err)
			}
			if run.Status != string(tt.status) || run.WaitingReason != string(tt.waitingReason) ||
				run.WaitingToken != tt.waitingToken || !run.LastRelevantAt.Equal(sentinelRelevantAt) {
				t.Fatalf("replay changed run: %#v", run)
			}
			requireRunStepAndOutboxCounts(t, f, stored.Index, 1, 1)
		})
	}
}

func TestAppendEventValidatesObservationShapeBeforePersistence(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		name   string
		mutate func(*agentruntime.AgentStep)
	}{
		{name: "empty kind", mutate: func(s *agentruntime.AgentStep) { s.Kind = "" }},
		{name: "wrong kind", mutate: func(s *agentruntime.AgentStep) { s.Kind = agentruntime.StepKindPlan }},
		{name: "empty status", mutate: func(s *agentruntime.AgentStep) { s.Status = "" }},
		{name: "running status", mutate: func(s *agentruntime.AgentStep) { s.Status = agentruntime.StepStatusRunning }},
		{name: "failed status", mutate: func(s *agentruntime.AgentStep) { s.Status = agentruntime.StepStatusFailed }},
		{name: "empty input", mutate: func(s *agentruntime.AgentStep) { s.InputJSON = "" }},
		{name: "invalid input", mutate: func(s *agentruntime.AgentStep) { s.InputJSON = `{"bad":` }},
		{name: "empty output", mutate: func(s *agentruntime.AgentStep) { s.OutputJSON = "" }},
		{name: "invalid output", mutate: func(s *agentruntime.AgentStep) { s.OutputJSON = `{"bad":` }},
		{name: "queued attempt", mutate: func(s *agentruntime.AgentStep) { s.AttemptCount = 1 }},
		{name: "queued worker", mutate: func(s *agentruntime.AgentStep) { s.WorkerID = "worker-a" }},
		{name: "queued lease", mutate: func(s *agentruntime.AgentStep) { s.LeaseExpiresAt = now }},
		{name: "queued retry source", mutate: func(s *agentruntime.AgentStep) { s.RetryOfStepID = "step-old" }},
		{name: "queued started", mutate: func(s *agentruntime.AgentStep) { s.StartedAt = now }},
		{name: "queued finished", mutate: func(s *agentruntime.AgentStep) { s.FinishedAt = now }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newRepositoryFixture(t, agentruntime.RunStatusRunning)
			step := appendEventStep(
				f.runID, "event:invalid-shape-"+tt.name, agentruntime.StepStatusQueued, now,
			)
			tt.mutate(step)
			_, err := f.repo.AppendEvent(context.Background(), step, testProjection(f.runID))
			if !errors.Is(err, agentruntime.ErrInvalidRuntimeContract) {
				t.Fatalf("AppendEvent() error = %v, want ErrInvalidRuntimeContract", err)
			}
			requireRunStepAndOutboxCounts(t, f, 0, 0, 0)
		})
	}
}

func TestAppendEventCompletedObservationAllowsAuditTimes(t *testing.T) {
	f := newRepositoryFixture(t, agentruntime.RunStatusRunning)
	now := time.Now().UTC().Truncate(time.Microsecond)
	step := appendEventStep(f.runID, "event:audit-times", agentruntime.StepStatusCompleted, now)
	step.StartedAt = now.Add(-time.Second)
	step.FinishedAt = now
	stored, err := f.repo.AppendEvent(context.Background(), step, testProjection(f.runID))
	if err != nil {
		t.Fatalf("AppendEvent(): %v", err)
	}
	if !stored.StartedAt.Equal(step.StartedAt) || !stored.FinishedAt.Equal(step.FinishedAt) {
		t.Fatalf("audit times changed: %#v", stored)
	}
	requireRunStepAndOutboxCounts(t, f, stored.Index, 1, 1)
}

func TestConcurrentAppendEventSameDedupeCreatesOneStepAndOutbox(t *testing.T) {
	f := newRepositoryFixture(t, agentruntime.RunStatusRunning)
	createdAt := time.Date(2026, 7, 28, 22, 0, 0, 0, time.UTC)
	steps := []*agentruntime.AgentStep{
		appendEventStep(f.runID, "event:concurrent-same", agentruntime.StepStatusQueued, createdAt),
		appendEventStep(f.runID, "event:concurrent-same", agentruntime.StepStatusQueued, createdAt),
	}
	results := appendConcurrently(t, f.db, steps, testProjection(f.runID))
	if results[0].err != nil || results[1].err != nil {
		t.Fatalf("concurrent append errors = %v / %v", results[0].err, results[1].err)
	}
	if results[0].step.ID != results[1].step.ID {
		t.Fatalf("returned step IDs = %q / %q, want same", results[0].step.ID, results[1].step.ID)
	}
	requireRunStepAndOutboxCounts(t, f, 1, 1, 1)
}

func TestConcurrentAppendEventDifferentDedupeCreatesContinuousIndexes(t *testing.T) {
	f := newRepositoryFixture(t, agentruntime.RunStatusRunning)
	createdAt := time.Date(2026, 7, 28, 22, 0, 0, 0, time.UTC)
	steps := []*agentruntime.AgentStep{
		appendEventStep(f.runID, "event:concurrent-one", agentruntime.StepStatusQueued, createdAt),
		appendEventStep(f.runID, "event:concurrent-two", agentruntime.StepStatusQueued, createdAt),
	}
	results := appendConcurrently(t, f.db, steps, testProjection(f.runID))
	if results[0].err != nil || results[1].err != nil {
		t.Fatalf("concurrent append errors = %v / %v", results[0].err, results[1].err)
	}
	indexes := map[int32]bool{results[0].step.Index: true, results[1].step.Index: true}
	if !indexes[1] || !indexes[2] || len(indexes) != 2 {
		t.Fatalf("concurrent indexes = %d / %d, want 1 / 2", results[0].step.Index, results[1].step.Index)
	}
	requireRunStepAndOutboxCounts(t, f, 2, 2, 2)
}

func TestConcurrentClaimAllowsOnlyOneRunningStepPerRun(t *testing.T) {
	f := newRepositoryFixture(t, agentruntime.RunStatusQueued)
	createdAt := time.Now().UTC().Truncate(time.Microsecond)
	f.createStep(t, &agentruntime.AgentStep{
		Index: 1, Kind: agentruntime.StepKindCapabilityCall,
		Status: agentruntime.StepStatusQueued, CreatedAt: createdAt,
	})
	f.createStep(t, &agentruntime.AgentStep{
		Index: 2, Kind: agentruntime.StepKindCapabilityCall,
		Status: agentruntime.StepStatusQueued, CreatedAt: createdAt.Add(time.Microsecond),
	})
	results := claimConcurrently(f.db, time.Now().UTC())
	var claimed, empty int
	for _, result := range results {
		switch {
		case result.err == nil:
			claimed++
		case errors.Is(result.err, agentruntime.ErrNotFound):
			empty++
		default:
			t.Fatalf("ClaimQueuedStep() error = %v", result.err)
		}
	}
	if claimed != 1 || empty != 1 {
		t.Fatalf("claimed=%d empty=%d, want 1/1", claimed, empty)
	}
	var running, queued int64
	if err := f.db.Model(&model.AgentStep{}).
		Where("run_id = ? AND status = ?", f.runID, string(agentruntime.StepStatusRunning)).
		Count(&running).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&model.AgentStep{}).
		Where("run_id = ? AND status = ?", f.runID, string(agentruntime.StepStatusQueued)).
		Count(&queued).Error; err != nil {
		t.Fatal(err)
	}
	if running != 1 || queued != 1 {
		t.Fatalf("running=%d queued=%d, want 1/1", running, queued)
	}
}

func TestConcurrentClaimCanClaimStepsFromDifferentRuns(t *testing.T) {
	f := newRepositoryFixture(t, agentruntime.RunStatusQueued)
	now := time.Now().UTC().Truncate(time.Microsecond)
	f.createStep(t, &agentruntime.AgentStep{
		Index: 1, Kind: agentruntime.StepKindCapabilityCall,
		Status: agentruntime.StepStatusQueued, CreatedAt: now,
	})
	secondSessionID, secondRunID := createAdditionalRunFixture(t, f, agentruntime.RunStatusQueued, now)
	t.Cleanup(func() { _ = f.db.Exec("DELETE FROM agent_sessions WHERE id = ?", secondSessionID).Error })
	f.createStep(t, &agentruntime.AgentStep{
		ID: "step_test_" + uuid.NewV4().String(), RunID: secondRunID, Index: 1,
		Kind: agentruntime.StepKindCapabilityCall, Status: agentruntime.StepStatusQueued,
		CreatedAt: now.Add(time.Microsecond),
	})
	results := claimConcurrently(f.db, now.Add(time.Second))
	if results[0].err != nil || results[1].err != nil {
		t.Fatalf("different-run claim errors = %v / %v", results[0].err, results[1].err)
	}
	runIDs := map[string]bool{results[0].step.RunID: true, results[1].step.RunID: true}
	if !runIDs[f.runID] || !runIDs[secondRunID] || len(runIDs) != 2 {
		t.Fatalf("claimed run IDs = %q / %q", results[0].step.RunID, results[1].step.RunID)
	}
}

func TestClaimQueuedStepFiltersWaitingAndTerminalRuns(t *testing.T) {
	statuses := []agentruntime.RunStatus{
		agentruntime.RunStatusWaitingApproval,
		agentruntime.RunStatusWaitingCallback,
		agentruntime.RunStatusWaitingSchedule,
		agentruntime.RunStatusCompleted,
		agentruntime.RunStatusFailed,
		agentruntime.RunStatusCancelled,
	}
	for _, status := range statuses {
		t.Run(string(status), func(t *testing.T) {
			f := newRepositoryFixture(t, status)
			step := &agentruntime.AgentStep{
				Index: 1, Kind: agentruntime.StepKindCapabilityCall, Status: agentruntime.StepStatusQueued,
			}
			f.createStep(t, step)
			_, err := f.repo.ClaimQueuedStep(context.Background(), agentruntime.StepClaim{
				WorkerID: "worker-a", LeaseTTL: time.Minute, Now: time.Now().UTC(),
			})
			if !errors.Is(err, agentruntime.ErrNotFound) {
				t.Fatalf("ClaimQueuedStep() error = %v, want ErrNotFound", err)
			}
			var stored model.AgentStep
			if err := f.db.First(&stored, "id = ?", step.ID).Error; err != nil {
				t.Fatal(err)
			}
			if stored.Status != string(agentruntime.StepStatusQueued) || stored.AttemptCount != 0 {
				t.Fatalf("filtered step changed: %#v", stored)
			}
		})
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
		AttemptCount: 1, WorkerID: "worker-a", LeaseExpiresAt: now.Add(time.Minute),
	}
	f.createStep(t, step)
	retryAt := now.Add(10 * time.Minute)
	if err := f.repo.RetryStep(context.Background(), agentruntime.RetryStepRequest{
		StepID: step.ID, WorkerID: step.WorkerID, AttemptCount: step.AttemptCount,
		ErrorText: "temporary", RetryAt: retryAt,
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
	if claimed.ID != step.ID || claimed.AttemptCount != 2 {
		t.Fatalf("claimed = %#v", claimed)
	}
}

func TestCompleteStepRequiresRunningAndClearsLease(t *testing.T) {
	f := newRepositoryFixture(t, agentruntime.RunStatusRunning)
	now := time.Now().UTC().Truncate(time.Microsecond)
	step := &agentruntime.AgentStep{
		Index: 1, Kind: agentruntime.StepKindCapabilityCall, Status: agentruntime.StepStatusRunning,
		AttemptCount: 1, WorkerID: "worker-a", LeaseExpiresAt: now.Add(time.Minute),
	}
	f.createStep(t, step)
	output := []byte(`{"message_id":"om_done"}`)
	if err := f.repo.CompleteStep(context.Background(), agentruntime.CompleteStepRequest{
		StepID: step.ID, WorkerID: step.WorkerID, AttemptCount: step.AttemptCount,
		Output: output, FinishedAt: now,
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
		StepID: step.ID, WorkerID: step.WorkerID, AttemptCount: step.AttemptCount,
		Output: output, FinishedAt: now,
	})
	if !errors.Is(err, agentruntime.ErrLeaseLost) {
		t.Fatalf("second CompleteStep() error = %v, want ErrLeaseLost", err)
	}
}

func TestStepCompletionAndRetryAreFencedByWorkerAndAttempt(t *testing.T) {
	f := newRepositoryFixture(t, agentruntime.RunStatusQueued)
	now := time.Now().UTC().Truncate(time.Microsecond)
	step := &agentruntime.AgentStep{
		Index: 1, Kind: agentruntime.StepKindCapabilityCall, Status: agentruntime.StepStatusQueued,
		ErrorText: "stale failure",
	}
	f.createStep(t, step)
	claimedByA, err := f.repo.ClaimQueuedStep(context.Background(), agentruntime.StepClaim{
		WorkerID: "worker-a", LeaseTTL: time.Minute, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	reclaimAt := now.Add(2 * time.Minute)
	if count, err := f.repo.ReclaimStaleSteps(context.Background(), agentruntime.ReclaimStaleStepsRequest{
		Now: reclaimAt, Limit: 1,
	}); err != nil || count != 1 {
		t.Fatalf("ReclaimStaleSteps() = %d, %v; want 1, nil", count, err)
	}
	claimedByB, err := f.repo.ClaimQueuedStep(context.Background(), agentruntime.StepClaim{
		WorkerID: "worker-b", LeaseTTL: time.Minute, Now: reclaimAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if claimedByA.AttemptCount != 1 || claimedByB.AttemptCount != 2 {
		t.Fatalf("attempts A/B = %d/%d, want 1/2", claimedByA.AttemptCount, claimedByB.AttemptCount)
	}

	staleComplete := agentruntime.CompleteStepRequest{
		StepID: step.ID, WorkerID: claimedByA.WorkerID, AttemptCount: claimedByA.AttemptCount,
		Output: json.RawMessage(`{"owner":"a"}`), FinishedAt: reclaimAt,
	}
	if err := f.repo.CompleteStep(context.Background(), staleComplete); !errors.Is(err, agentruntime.ErrLeaseLost) {
		t.Fatalf("stale CompleteStep() error = %v, want ErrLeaseLost", err)
	}
	requireRunningLeaseUnchanged(t, f, claimedByB, "stale failure")

	staleRetry := agentruntime.RetryStepRequest{
		StepID: step.ID, WorkerID: claimedByA.WorkerID, AttemptCount: claimedByA.AttemptCount,
		ErrorText: "worker a retry", RetryAt: reclaimAt.Add(time.Minute),
	}
	if err := f.repo.RetryStep(context.Background(), staleRetry); !errors.Is(err, agentruntime.ErrLeaseLost) {
		t.Fatalf("stale RetryStep() error = %v, want ErrLeaseLost", err)
	}
	requireRunningLeaseUnchanged(t, f, claimedByB, "stale failure")

	finishedAt := reclaimAt.Add(30 * time.Second)
	if err := f.repo.CompleteStep(context.Background(), agentruntime.CompleteStepRequest{
		StepID: step.ID, WorkerID: claimedByB.WorkerID, AttemptCount: claimedByB.AttemptCount,
		Output: json.RawMessage(`{"owner":"b"}`), FinishedAt: finishedAt,
	}); err != nil {
		t.Fatalf("owner CompleteStep(): %v", err)
	}
	var stored model.AgentStep
	if err := f.db.First(&stored, "id = ?", step.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != string(agentruntime.StepStatusCompleted) ||
		!equalJSON(stored.OutputJSON, `{"owner":"b"}`) || stored.ErrorText != "" ||
		stored.WorkerID != "" || !stored.LeaseExpiresAt.IsZero() {
		t.Fatalf("completed owner step = %#v", stored)
	}
}

func equalJSON(left, right string) bool {
	var leftValue, rightValue any
	if json.Unmarshal([]byte(left), &leftValue) != nil || json.Unmarshal([]byte(right), &rightValue) != nil {
		return false
	}
	return reflect.DeepEqual(leftValue, rightValue)
}

func requireRunningLeaseUnchanged(
	t *testing.T,
	f *repositoryFixture,
	claimed *agentruntime.AgentStep,
	wantError string,
) {
	t.Helper()
	var stored model.AgentStep
	if err := f.db.First(&stored, "id = ?", claimed.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != string(agentruntime.StepStatusRunning) ||
		stored.WorkerID != claimed.WorkerID || stored.AttemptCount != claimed.AttemptCount ||
		!stored.LeaseExpiresAt.Equal(claimed.LeaseExpiresAt) ||
		!equalJSON(stored.OutputJSON, claimed.OutputJSON) || stored.ErrorText != wantError {
		t.Fatalf("running lease changed: %#v", stored)
	}
}

func TestQueueMethodsPreserveRequestValidationErrors(t *testing.T) {
	repo := &Repository{}
	if _, err := repo.ClaimQueuedStep(context.Background(), agentruntime.StepClaim{}); !errors.Is(err, agentruntime.ErrInvalidRuntimeContract) {
		t.Fatalf("ClaimQueuedStep(invalid) error = %v", err)
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

type appendResult struct {
	step *agentruntime.AgentStep
	err  error
}

type claimResult struct {
	step *agentruntime.AgentStep
	err  error
}

func claimConcurrently(db *gorm.DB, now time.Time) []claimResult {
	results := make(chan claimResult, 2)
	var wg sync.WaitGroup
	for worker := range 2 {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			step, err := NewRepository(db.Session(&gorm.Session{})).ClaimQueuedStep(
				context.Background(),
				agentruntime.StepClaim{
					WorkerID: "worker-" + string(rune('a'+worker)),
					LeaseTTL: time.Minute,
					Now:      now,
				},
			)
			results <- claimResult{step: step, err: err}
		}(worker)
	}
	wg.Wait()
	close(results)
	collected := make([]claimResult, 0, 2)
	for result := range results {
		collected = append(collected, result)
	}
	return collected
}

func createAdditionalRunFixture(
	t *testing.T,
	f *repositoryFixture,
	status agentruntime.RunStatus,
	now time.Time,
) (string, string) {
	t.Helper()
	suffix := uuid.NewV4().String()
	sessionID := "session_test_" + suffix
	runID := "run_test_" + suffix
	if err := f.db.Create(&model.AgentSession{
		ID: sessionID, AppID: "app_" + suffix, BotOpenID: "bot_" + suffix,
		ChatID: "chat_" + suffix, ScopeType: "chat", ScopeID: "scope_" + suffix,
		Status: "active", CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Create(&model.AgentRun{
		ID: runID, SessionID: sessionID, TriggerType: string(agentruntime.TriggerTypeMention),
		TriggerMessageID: "message_" + suffix, Status: string(status), Revision: 1,
		CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	return sessionID, runID
}

func appendEventStep(runID, dedupeKey string, status agentruntime.StepStatus, createdAt time.Time) *agentruntime.AgentStep {
	return &agentruntime.AgentStep{
		ID: "step_" + uuid.NewV4().String(), RunID: runID,
		Kind: agentruntime.StepKindObserve, Status: status,
		InputJSON: "{}", OutputJSON: "{}", DedupeKey: dedupeKey, CreatedAt: createdAt,
	}
}

func appendConcurrently(
	t *testing.T,
	db *gorm.DB,
	steps []*agentruntime.AgentStep,
	projection agentruntime.ProjectionDocument,
) []appendResult {
	t.Helper()
	results := make(chan appendResult, len(steps))
	var wg sync.WaitGroup
	for _, step := range steps {
		step := step
		wg.Add(1)
		go func() {
			defer wg.Done()
			stored, err := NewRepository(db.Session(&gorm.Session{})).AppendEvent(
				context.Background(), step, projection,
			)
			results <- appendResult{step: stored, err: err}
		}()
	}
	wg.Wait()
	close(results)
	collected := make([]appendResult, 0, len(steps))
	for result := range results {
		collected = append(collected, result)
	}
	return collected
}

func requireRunStepAndOutboxCounts(
	t *testing.T,
	f *repositoryFixture,
	wantCurrentIndex int32,
	wantSteps,
	wantOutboxes int64,
) {
	t.Helper()
	var run model.AgentRun
	if err := f.db.First(&run, "id = ?", f.runID).Error; err != nil {
		t.Fatal(err)
	}
	if run.CurrentStepIndex != wantCurrentIndex {
		t.Fatalf("current index = %d, want %d", run.CurrentStepIndex, wantCurrentIndex)
	}
	var steps int64
	if err := f.db.Model(&model.AgentStep{}).Where("run_id = ?", f.runID).Count(&steps).Error; err != nil {
		t.Fatal(err)
	}
	if steps != wantSteps {
		t.Fatalf("steps = %d, want %d", steps, wantSteps)
	}
	var outboxes int64
	if err := f.db.Model(&model.AgentProjectionOutbox{}).
		Joins("JOIN agent_steps ON agent_steps.id = agent_projection_outbox.step_id").
		Where("agent_steps.run_id = ?", f.runID).Count(&outboxes).Error; err != nil {
		t.Fatal(err)
	}
	if outboxes != wantOutboxes {
		t.Fatalf("outboxes = %d, want %d", outboxes, wantOutboxes)
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
