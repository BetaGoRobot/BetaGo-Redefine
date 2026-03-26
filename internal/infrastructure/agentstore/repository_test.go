package agentstore

import (
	"context"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/testsupport/pgtest"
	"gorm.io/gorm"
)

func TestAgentSessionRepositoryFindOrCreateChatSession(t *testing.T) {
	db := openAgentStoreTestDB(t)
	ctx := context.Background()
	repo := NewSessionRepository(db)

	session, err := repo.FindOrCreateChatSession(ctx, "cli_app", "ou_bot", "oc_chat")
	if err != nil {
		t.Fatalf("FindOrCreateChatSession() error = %v", err)
	}
	if session.ScopeType != agentruntime.ScopeTypeChat || session.ScopeID != "oc_chat" {
		t.Fatalf("unexpected session scope: %+v", session)
	}

	same, err := repo.FindOrCreateChatSession(ctx, "cli_app", "ou_bot", "oc_chat")
	if err != nil {
		t.Fatalf("FindOrCreateChatSession() second call error = %v", err)
	}
	if same.ID != session.ID {
		t.Fatalf("FindOrCreateChatSession() created duplicate session: %q vs %q", session.ID, same.ID)
	}
}

func TestAgentRunRepositoryCreateAndUpdateStatusWithRevisionCheck(t *testing.T) {
	db := openAgentStoreTestDB(t)
	ctx := context.Background()
	sessionRepo := NewSessionRepository(db)
	runRepo := NewRunRepository(db)

	session, err := sessionRepo.FindOrCreateChatSession(ctx, "cli_app", "ou_bot", "oc_chat")
	if err != nil {
		t.Fatalf("FindOrCreateChatSession() error = %v", err)
	}

	now := time.Date(2026, 3, 18, 12, 0, 0, 0, time.UTC)
	run := &agentruntime.AgentRun{
		ID:               "run_01",
		SessionID:        session.ID,
		TriggerType:      agentruntime.TriggerTypeMention,
		TriggerMessageID: "om_message",
		ActorOpenID:      "ou_actor",
		Status:           agentruntime.RunStatusQueued,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := runRepo.Create(ctx, run); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	updated, err := runRepo.UpdateStatus(ctx, run.ID, 0, func(current *agentruntime.AgentRun) error {
		current.Status = agentruntime.RunStatusRunning
		current.StartedAt = &now
		return nil
	})
	if err != nil {
		t.Fatalf("UpdateStatus() error = %v", err)
	}
	if updated.Status != agentruntime.RunStatusRunning || updated.Revision != 1 {
		t.Fatalf("unexpected updated run: %+v", updated)
	}

	if _, err := runRepo.UpdateStatus(ctx, run.ID, 0, func(current *agentruntime.AgentRun) error {
		current.Status = agentruntime.RunStatusCompleted
		return nil
	}); err != ErrRevisionConflict {
		t.Fatalf("UpdateStatus() stale revision error = %v, want %v", err, ErrRevisionConflict)
	}
}

func TestAgentStepRepositoryAppendAndListByRun(t *testing.T) {
	db := openAgentStoreTestDB(t)
	ctx := context.Background()
	sessionRepo := NewSessionRepository(db)
	runRepo := NewRunRepository(db)
	stepRepo := NewStepRepository(db)

	session, err := sessionRepo.FindOrCreateChatSession(ctx, "cli_app", "ou_bot", "oc_chat")
	if err != nil {
		t.Fatalf("FindOrCreateChatSession() error = %v", err)
	}
	run := &agentruntime.AgentRun{
		ID:        "run_02",
		SessionID: session.ID,
		Status:    agentruntime.RunStatusQueued,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := runRepo.Create(ctx, run); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	step := &agentruntime.AgentStep{
		ID:        "step_01",
		RunID:     run.ID,
		Index:     0,
		Kind:      agentruntime.StepKindDecide,
		Status:    agentruntime.StepStatusQueued,
		CreatedAt: time.Now(),
	}
	if err := stepRepo.Append(ctx, step); err != nil {
		t.Fatalf("Append() error = %v", err)
	}

	steps, err := stepRepo.ListByRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("ListByRun() error = %v", err)
	}
	if len(steps) != 1 || steps[0].ID != step.ID {
		t.Fatalf("ListByRun() = %+v, want step %q", steps, step.ID)
	}
}

func TestAgentRunRepositoryPersistsLivenessFields(t *testing.T) {
	now := time.Date(2026, 3, 25, 13, 0, 0, 0, time.UTC)
	heartbeatAt := now
	leaseExpiresAt := now.Add(30 * time.Second)
	run := &agentruntime.AgentRun{
		ID:               "run_liveness",
		SessionID:        "session_liveness",
		TriggerType:      agentruntime.TriggerTypeMention,
		TriggerMessageID: "om_message_liveness",
		ActorOpenID:      "ou_actor",
		Status:           agentruntime.RunStatusRunning,
		WorkerID:         "worker_initial",
		HeartbeatAt:      &heartbeatAt,
		LeaseExpiresAt:   &leaseExpiresAt,
		RepairAttempts:   2,
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	entity := runToModel(run)
	if entity.WorkerID != "worker_initial" {
		t.Fatalf("model worker_id = %q, want %q", entity.WorkerID, "worker_initial")
	}
	if entity.HeartbeatAt.IsZero() || !entity.HeartbeatAt.Equal(heartbeatAt) {
		t.Fatalf("model heartbeat_at = %s, want %s", entity.HeartbeatAt, heartbeatAt)
	}
	if entity.LeaseExpiresAt.IsZero() || !entity.LeaseExpiresAt.Equal(leaseExpiresAt) {
		t.Fatalf("model lease_expires_at = %s, want %s", entity.LeaseExpiresAt, leaseExpiresAt)
	}
	if entity.RepairAttempts != 2 {
		t.Fatalf("model repair_attempts = %d, want 2", entity.RepairAttempts)
	}

	decoded := runFromModel(entity)
	if decoded.WorkerID != "worker_initial" {
		t.Fatalf("decoded worker_id = %q, want %q", decoded.WorkerID, "worker_initial")
	}
	if decoded.HeartbeatAt == nil || !decoded.HeartbeatAt.Equal(heartbeatAt) {
		t.Fatalf("decoded heartbeat_at = %+v, want %s", decoded.HeartbeatAt, heartbeatAt)
	}
	if decoded.LeaseExpiresAt == nil || !decoded.LeaseExpiresAt.Equal(leaseExpiresAt) {
		t.Fatalf("decoded lease_expires_at = %+v, want %s", decoded.LeaseExpiresAt, leaseExpiresAt)
	}
	if decoded.RepairAttempts != 2 {
		t.Fatalf("decoded repair_attempts = %d, want 2", decoded.RepairAttempts)
	}

	renewedAt := now.Add(15 * time.Second)
	renewedLeaseAt := renewedAt.Add(30 * time.Second)
	run.WorkerID = "worker_renewed"
	run.HeartbeatAt = &renewedAt
	run.LeaseExpiresAt = &renewedLeaseAt
	run.RepairAttempts = 3
	run.UpdatedAt = renewedAt
	updateMap := runUpdateMap(run)
	if updateMap["worker_id"] != "worker_renewed" {
		t.Fatalf("update worker_id = %+v, want %q", updateMap["worker_id"], "worker_renewed")
	}
	heartbeatValue, ok := updateMap["heartbeat_at"].(time.Time)
	if !ok || !heartbeatValue.Equal(renewedAt) {
		t.Fatalf("update heartbeat_at = %+v, want %s", updateMap["heartbeat_at"], renewedAt)
	}
	leaseValue, ok := updateMap["lease_expires_at"].(time.Time)
	if !ok || !leaseValue.Equal(renewedLeaseAt) {
		t.Fatalf("update lease_expires_at = %+v, want %s", updateMap["lease_expires_at"], renewedLeaseAt)
	}
	if updateMap["repair_attempts"] != int64(3) {
		t.Fatalf("update repair_attempts = %+v, want 3", updateMap["repair_attempts"])
	}
}

func openAgentStoreTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := pgtest.OpenTempSchema(t)
	if err := AutoMigrate(db); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	return db
}
