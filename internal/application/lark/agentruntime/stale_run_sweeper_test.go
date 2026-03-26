package agentruntime_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/agentstore"
)

func TestStaleRunSweeperRepairsExpiredRunningRun(t *testing.T) {
	db := openCoordinatorTestDB(t)
	store := openCoordinatorRedisStore(t)
	identity := botidentity.Identity{AppID: "cli_app", BotOpenID: "ou_bot"}
	sessionRepo := agentstore.NewSessionRepository(db)
	runRepo := agentstore.NewRunRepository(db)
	stepRepo := agentstore.NewStepRepository(db)
	coordinator := agentruntime.NewRunCoordinator(sessionRepo, runRepo, stepRepo, store, identity)

	run, err := coordinator.StartShadowRun(context.Background(), agentruntime.StartShadowRunRequest{
		ChatID:           "oc_chat",
		ActorOpenID:      "ou_actor",
		TriggerType:      agentruntime.TriggerTypeMention,
		TriggerMessageID: "om_stale_running",
		InputText:        "@bot 帮我分析这段内容",
		Now:              time.Now().Add(-2 * time.Hour).UTC(),
	})
	if err != nil {
		t.Fatalf("StartShadowRun() error = %v", err)
	}

	expiredAt := time.Now().Add(-90 * time.Second).UTC()
	run, err = runRepo.UpdateStatus(context.Background(), run.ID, run.Revision, func(current *agentruntime.AgentRun) error {
		current.Status = agentruntime.RunStatusRunning
		current.UpdatedAt = expiredAt.Add(-10 * time.Second)
		current.StartedAt = timePtr(expiredAt.Add(-time.Minute))
		current.WorkerID = "worker_stale"
		current.HeartbeatAt = timePtr(expiredAt.Add(-20 * time.Second))
		current.LeaseExpiresAt = timePtr(expiredAt)
		return nil
	})
	if err != nil {
		t.Fatalf("UpdateStatus() running error = %v", err)
	}

	sweeper := agentruntime.NewStaleRunSweeper(runRepo, coordinator)
	sweeper.RunOnce(context.Background())

	repaired, err := runRepo.GetByID(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if repaired.Status != agentruntime.RunStatusFailed {
		t.Fatalf("run status = %q, want %q", repaired.Status, agentruntime.RunStatusFailed)
	}
	if !strings.Contains(repaired.ErrorText, "stale_run_timeout") {
		t.Fatalf("error text = %q, want contain stale_run_timeout", repaired.ErrorText)
	}
	if repaired.HeartbeatAt != nil || repaired.LeaseExpiresAt != nil || repaired.WorkerID != "" {
		t.Fatalf("expected run liveness to be cleared, got worker=%q heartbeat=%+v lease=%+v", repaired.WorkerID, repaired.HeartbeatAt, repaired.LeaseExpiresAt)
	}

	session, err := sessionRepo.GetByID(context.Background(), repaired.SessionID)
	if err != nil {
		t.Fatalf("GetByID() session error = %v", err)
	}
	if session.ActiveRunID != "" {
		t.Fatalf("session active run = %q, want empty", session.ActiveRunID)
	}

	chatID, actorOpenID, err := store.DequeuePendingInitialScope(context.Background(), time.Second)
	if err != nil {
		t.Fatalf("DequeuePendingInitialScope() error = %v", err)
	}
	if chatID != "oc_chat" || actorOpenID != "ou_actor" {
		t.Fatalf("pending scope notification = (%q, %q), want (%q, %q)", chatID, actorOpenID, "oc_chat", "ou_actor")
	}
}

func TestStaleRunSweeperDoesNotRepairLegacyQueuedRunWithoutLease(t *testing.T) {
	db := openCoordinatorTestDB(t)
	store := openCoordinatorRedisStore(t)
	identity := botidentity.Identity{AppID: "cli_app", BotOpenID: "ou_bot"}
	sessionRepo := agentstore.NewSessionRepository(db)
	runRepo := agentstore.NewRunRepository(db)
	stepRepo := agentstore.NewStepRepository(db)
	coordinator := agentruntime.NewRunCoordinator(sessionRepo, runRepo, stepRepo, store, identity)

	run, err := coordinator.StartShadowRun(context.Background(), agentruntime.StartShadowRunRequest{
		ChatID:           "oc_chat",
		ActorOpenID:      "ou_actor",
		TriggerType:      agentruntime.TriggerTypeMention,
		TriggerMessageID: "om_stale_queued",
		InputText:        "@bot 帮我排个计划",
		Now:              time.Now().Add(-2 * time.Hour).UTC(),
	})
	if err != nil {
		t.Fatalf("StartShadowRun() error = %v", err)
	}

	legacyUpdatedAt := time.Now().Add(-2 * time.Hour).UTC()
	run, err = runRepo.UpdateStatus(context.Background(), run.ID, run.Revision, func(current *agentruntime.AgentRun) error {
		current.Status = agentruntime.RunStatusQueued
		current.UpdatedAt = legacyUpdatedAt
		current.WorkerID = ""
		current.HeartbeatAt = nil
		current.LeaseExpiresAt = nil
		return nil
	})
	if err != nil {
		t.Fatalf("UpdateStatus() queued error = %v", err)
	}

	sweeper := agentruntime.NewStaleRunSweeper(runRepo, coordinator)
	sweeper.RunOnce(context.Background())

	repaired, err := runRepo.GetByID(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if repaired.Status != agentruntime.RunStatusQueued {
		t.Fatalf("run status = %q, want %q", repaired.Status, agentruntime.RunStatusQueued)
	}
	if strings.Contains(repaired.ErrorText, "stale_run_timeout") {
		t.Fatalf("error text = %q, want queued run to stay unrepaired", repaired.ErrorText)
	}
}

func TestStaleRunSweeperDoesNotRepairQueuedRunWaitingForExecutionSlot(t *testing.T) {
	db := openCoordinatorTestDB(t)
	store := openCoordinatorRedisStore(t)
	identity := botidentity.Identity{AppID: "cli_app", BotOpenID: "ou_bot"}
	sessionRepo := agentstore.NewSessionRepository(db)
	runRepo := agentstore.NewRunRepository(db)
	stepRepo := agentstore.NewStepRepository(db)
	coordinator := agentruntime.NewRunCoordinator(sessionRepo, runRepo, stepRepo, store, identity)

	queuedAt := time.Now().Add(-2 * time.Minute).UTC()
	run, err := coordinator.StartShadowRun(context.Background(), agentruntime.StartShadowRunRequest{
		ChatID:           "oc_chat",
		ActorOpenID:      "ou_actor",
		TriggerType:      agentruntime.TriggerTypeMention,
		TriggerMessageID: "om_waiting_slot",
		InputText:        "@bot 帮我继续刚才的任务",
		Now:              queuedAt,
	})
	if err != nil {
		t.Fatalf("StartShadowRun() error = %v", err)
	}

	expiredLeaseAt := time.Now().Add(-30 * time.Second).UTC()
	run, err = runRepo.UpdateStatus(context.Background(), run.ID, run.Revision, func(current *agentruntime.AgentRun) error {
		current.Status = agentruntime.RunStatusQueued
		current.UpdatedAt = queuedAt
		current.WorkerID = ""
		current.HeartbeatAt = timePtr(queuedAt)
		current.LeaseExpiresAt = timePtr(expiredLeaseAt)
		return nil
	})
	if err != nil {
		t.Fatalf("UpdateStatus() queued error = %v", err)
	}

	sweeper := agentruntime.NewStaleRunSweeper(runRepo, coordinator)
	sweeper.RunOnce(context.Background())

	reloaded, err := runRepo.GetByID(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if reloaded.Status != agentruntime.RunStatusQueued {
		t.Fatalf("run status = %q, want %q", reloaded.Status, agentruntime.RunStatusQueued)
	}
	if strings.Contains(reloaded.ErrorText, "stale_run_timeout") {
		t.Fatalf("error text = %q, want queued waiting run to stay unrepaired", reloaded.ErrorText)
	}
}

func timePtr(v time.Time) *time.Time {
	return &v
}
