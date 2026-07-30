package conversationindex

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	uuid "github.com/satori/go.uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestClaimProjectionClaimsDuePendingAndFencesCompletion(t *testing.T) {
	db := newProjectionDB(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	createProjection(t, db, &model.AgentProjectionOutbox{
		ID: "outbox-pending", StepID: "step-1", IndexAlias: "agent_conversation_events",
		DocumentID: "step-1", PayloadJSON: `{"event_id":"step-1"}`,
		Status: "pending", NextAttemptAt: now.Add(-time.Second),
		CreatedAt: now.Add(-time.Minute), UpdatedAt: now.Add(-time.Minute),
	})

	store := NewStore(db)
	claimed, err := store.ClaimProjection(context.Background(), agentruntime.ProjectionClaim{
		WorkerID: "worker-1", LeaseTTL: time.Minute, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if claimed.ID != "outbox-pending" || claimed.Status != agentruntime.ProjectionStatusRunning ||
		claimed.WorkerID != "worker-1" || claimed.AttemptCount != 1 ||
		!claimed.LeaseExpiresAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("claimed = %#v", claimed)
	}

	err = store.CompleteProjection(context.Background(), agentruntime.CompleteProjectionRequest{
		OutboxID: claimed.ID, WorkerID: "stale-worker",
		AttemptCount: claimed.AttemptCount, FinishedAt: now.Add(time.Second),
	})
	if !errors.Is(err, agentruntime.ErrProjectionLeaseLost) {
		t.Fatalf("stale CompleteProjection() error = %v", err)
	}
	err = store.CompleteProjection(context.Background(), agentruntime.CompleteProjectionRequest{
		OutboxID: claimed.ID, WorkerID: claimed.WorkerID,
		AttemptCount: claimed.AttemptCount, FinishedAt: now.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	var stored model.AgentProjectionOutbox
	if err := db.First(&stored, "id = ?", claimed.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != string(agentruntime.ProjectionStatusCompleted) ||
		stored.WorkerID != "" || !stored.LeaseExpiresAt.IsZero() {
		t.Fatalf("stored = %#v", stored)
	}
}

func TestClaimProjectionReclaimsExpiredRunningLease(t *testing.T) {
	db := newProjectionDB(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	createProjection(t, db, &model.AgentProjectionOutbox{
		ID: "outbox-expired", StepID: "step-2", IndexAlias: "agent_conversation_events",
		DocumentID: "step-2", PayloadJSON: `{"event_id":"step-2"}`,
		Status: "running", AttemptCount: 2, WorkerID: "dead-worker",
		NextAttemptAt: now.Add(-time.Hour), LeaseExpiresAt: now.Add(-time.Second),
		CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour),
	})

	claimed, err := NewStore(db).ClaimProjection(context.Background(), agentruntime.ProjectionClaim{
		WorkerID: "reclaimer", LeaseTTL: 30 * time.Second, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if claimed.AttemptCount != 3 || claimed.WorkerID != "reclaimer" ||
		!claimed.LeaseExpiresAt.Equal(now.Add(30*time.Second)) {
		t.Fatalf("claimed = %#v", claimed)
	}
}

func TestRenewProjectionLeaseFencesExpiredAndExtendsCurrentOwner(t *testing.T) {
	db := newProjectionDB(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	for _, outbox := range []*model.AgentProjectionOutbox{
		{
			ID: "outbox-renew-current", StepID: "step-renew-current",
			IndexAlias: "agent_conversation_events", DocumentID: "step-renew-current",
			PayloadJSON: `{"event_id":"step-renew-current"}`,
			Status:      "running", AttemptCount: 2, WorkerID: "worker-renew",
			NextAttemptAt: now.Add(-time.Hour), LeaseExpiresAt: now.Add(time.Second),
			CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour),
		},
		{
			ID: "outbox-renew-expired", StepID: "step-renew-expired",
			IndexAlias: "agent_conversation_events", DocumentID: "step-renew-expired",
			PayloadJSON: `{"event_id":"step-renew-expired"}`,
			Status:      "running", AttemptCount: 3, WorkerID: "worker-expired",
			NextAttemptAt: now.Add(-time.Hour), LeaseExpiresAt: now.Add(-time.Second),
			CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour),
		},
	} {
		createProjection(t, db, outbox)
	}
	store := NewStore(db)
	current := agentruntime.RenewProjectionLeaseRequest{
		OutboxID: "outbox-renew-current", WorkerID: "worker-renew", AttemptCount: 2,
		LeaseTTL: time.Minute, Now: now,
	}
	if err := store.RenewProjectionLease(context.Background(), current); err != nil {
		t.Fatal(err)
	}
	var renewed model.AgentProjectionOutbox
	if err := db.First(&renewed, "id = ?", current.OutboxID).Error; err != nil {
		t.Fatal(err)
	}
	if !renewed.LeaseExpiresAt.Equal(now.Add(time.Minute)) || !renewed.UpdatedAt.Equal(now) {
		t.Fatalf("renewed = %#v", renewed)
	}
	expired := agentruntime.RenewProjectionLeaseRequest{
		OutboxID: "outbox-renew-expired", WorkerID: "worker-expired", AttemptCount: 3,
		LeaseTTL: time.Minute, Now: now,
	}
	if err := store.RenewProjectionLease(context.Background(), expired); !errors.Is(err, agentruntime.ErrProjectionLeaseLost) {
		t.Fatalf("expired RenewProjectionLease() error = %v", err)
	}
}

func TestRetryProjectionRequiresCurrentWorkerAndAttempt(t *testing.T) {
	db := newProjectionDB(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	createProjection(t, db, &model.AgentProjectionOutbox{
		ID: "outbox-running", StepID: "step-3", IndexAlias: "agent_conversation_events",
		DocumentID: "step-3", PayloadJSON: `{"event_id":"step-3"}`,
		Status: "running", AttemptCount: 4, WorkerID: "worker-4",
		NextAttemptAt: now.Add(-time.Minute), LeaseExpiresAt: now.Add(time.Minute),
		CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour),
	})
	store := NewStore(db)
	retry := agentruntime.RetryProjectionRequest{
		OutboxID: "outbox-running", WorkerID: "worker-4", AttemptCount: 3,
		ErrorText: "temporary", FailedAt: now, RetryAt: now.Add(10 * time.Minute),
	}
	if err := store.RetryProjection(context.Background(), retry); !errors.Is(err, agentruntime.ErrProjectionLeaseLost) {
		t.Fatalf("stale RetryProjection() error = %v", err)
	}
	retry.AttemptCount = 4
	if err := store.RetryProjection(context.Background(), retry); err != nil {
		t.Fatal(err)
	}
	var stored model.AgentProjectionOutbox
	if err := db.First(&stored, "id = ?", retry.OutboxID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != string(agentruntime.ProjectionStatusPending) ||
		stored.WorkerID != "" || stored.LastError != "temporary" ||
		!stored.NextAttemptAt.Equal(retry.RetryAt) || !stored.LeaseExpiresAt.IsZero() {
		t.Fatalf("stored = %#v", stored)
	}
}

func TestProjectionFinalizeRejectsExpiredOwnerWithoutReclaim(t *testing.T) {
	db := newProjectionDB(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	createProjection(t, db, &model.AgentProjectionOutbox{
		ID: "outbox-owner-expired", StepID: "step-4", IndexAlias: "agent_conversation_events",
		DocumentID: "step-4", PayloadJSON: `{"event_id":"step-4"}`,
		Status: "running", AttemptCount: 1, WorkerID: "expired-worker",
		NextAttemptAt: now.Add(-time.Minute), LeaseExpiresAt: now.Add(-time.Second),
		CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour),
	})
	store := NewStore(db)
	err := store.CompleteProjection(context.Background(), agentruntime.CompleteProjectionRequest{
		OutboxID: "outbox-owner-expired", WorkerID: "expired-worker",
		AttemptCount: 1, FinishedAt: now,
	})
	if !errors.Is(err, agentruntime.ErrProjectionLeaseLost) {
		t.Fatalf("expired CompleteProjection() error = %v", err)
	}
	err = store.RetryProjection(context.Background(), agentruntime.RetryProjectionRequest{
		OutboxID: "outbox-owner-expired", WorkerID: "expired-worker",
		AttemptCount: 1, ErrorText: "late failure", FailedAt: now, RetryAt: now.Add(time.Minute),
	})
	if !errors.Is(err, agentruntime.ErrProjectionLeaseLost) {
		t.Fatalf("expired RetryProjection() error = %v", err)
	}
}

func TestClaimProjectionUsesIDAsStableTieBreaker(t *testing.T) {
	db := newProjectionDB(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	for _, id := range []string{"outbox-b", "outbox-a"} {
		createProjection(t, db, &model.AgentProjectionOutbox{
			ID: id, StepID: "step-" + id, IndexAlias: "agent_conversation_events",
			DocumentID: "doc-" + id, PayloadJSON: `{"event_id":"` + id + `"}`,
			Status: "pending", NextAttemptAt: now.Add(-time.Second),
			CreatedAt: now, UpdatedAt: now,
		})
	}
	claimed, err := NewStore(db).ClaimProjection(context.Background(), agentruntime.ProjectionClaim{
		WorkerID: "worker", LeaseTTL: time.Minute, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if claimed.ID != "outbox-a" {
		t.Fatalf("claimed ID = %q, want outbox-a", claimed.ID)
	}
}

func newProjectionDB(t *testing.T) *gorm.DB {
	t.Helper()
	configPath := os.Getenv("BETAGO_CONFIG_PATH")
	if configPath == "" {
		t.Skip("BETAGO_CONFIG_PATH is not set; skipping PostgreSQL integration test")
	}
	cfg, err := config.LoadFileE(configPath)
	if err != nil || cfg == nil || cfg.DBConfig == nil {
		t.Skip("PostgreSQL test configuration is unavailable")
	}
	root, err := gorm.Open(postgres.Open(cfg.DBConfig.DSN()), &gorm.Config{})
	if err != nil {
		t.Skip("PostgreSQL is unavailable")
	}
	rootSQL, err := root.DB()
	if err != nil || rootSQL.PingContext(context.Background()) != nil {
		t.Skip("PostgreSQL is unavailable")
	}
	schema := "conversationindex_test_" + strings.ReplaceAll(uuid.NewV4().String(), "-", "")
	if err := root.Exec(fmt.Sprintf(`CREATE SCHEMA %q`, schema)).Error; err != nil {
		t.Fatal(err)
	}
	if err := root.Exec(fmt.Sprintf(
		`CREATE TABLE %q.agent_projection_outbox (LIKE betago.agent_projection_outbox INCLUDING ALL)`,
		schema,
	)).Error; err != nil {
		t.Fatal(err)
	}
	testConfig := *cfg.DBConfig
	testConfig.SearchPath = schema
	db, err := gorm.Open(postgres.Open(testConfig.DSN()), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = sqlDB.Close()
		_ = root.Exec(fmt.Sprintf(`DROP SCHEMA %q CASCADE`, schema)).Error
		_ = rootSQL.Close()
	})
	return db
}

func createProjection(t *testing.T, db *gorm.DB, outbox *model.AgentProjectionOutbox) {
	t.Helper()
	if err := db.Create(outbox).Error; err != nil {
		t.Fatal(err)
	}
}
