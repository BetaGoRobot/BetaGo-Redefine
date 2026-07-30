package agentstore

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
)

func TestContinuationCatalogListsDueRunsWithoutClaiming(t *testing.T) {
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
	chatID, err := f.repo.FindRunChatID(context.Background(), f.runID)
	if err != nil || chatID == "" {
		t.Fatalf("FindRunChatID() = %q, %v", chatID, err)
	}
	runIDs, err := f.repo.ListDueContinuationRunIDs(context.Background(), 32)
	if err != nil {
		t.Fatalf("ListDueContinuationRunIDs() error = %v", err)
	}
	if !slices.Contains(runIDs, f.runID) {
		t.Fatalf("due run IDs = %v, want %s", runIDs, f.runID)
	}
	var step model.AgentStep
	if err := f.db.Where("run_id = ? AND kind = ?", f.runID, string(agentruntime.StepKindDecide)).
		First(&step).Error; err != nil {
		t.Fatal(err)
	}
	if step.Status != string(agentruntime.StepStatusQueued) ||
		step.WorkerID != "" ||
		!step.LeaseExpiresAt.IsZero() {
		t.Fatalf("catalog mutated continuation step: %#v", step)
	}

	future := time.Now().UTC().Add(time.Hour)
	if err := f.db.Model(&model.AgentStep{}).Where("id = ?", step.ID).
		Updates(map[string]any{
			"status":           string(agentruntime.StepStatusRunning),
			"worker_id":        "lost-worker",
			"attempt_count":    1,
			"lease_expires_at": future,
		}).Error; err != nil {
		t.Fatal(err)
	}
	runIDs, err = f.repo.ListDueContinuationRunIDs(context.Background(), 32)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(runIDs, f.runID) {
		t.Fatalf("future leased run unexpectedly due: %v", runIDs)
	}
	if err := f.db.Model(&model.AgentStep{}).Where("id = ?", step.ID).
		Update("lease_expires_at", time.Now().UTC().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	runIDs, err = f.repo.ListDueContinuationRunIDs(context.Background(), 32)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(runIDs, f.runID) {
		t.Fatalf("stale leased run IDs = %v, want %s", runIDs, f.runID)
	}
}
