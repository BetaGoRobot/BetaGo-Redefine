package conversationindex

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/opensearch"
)

func TestOpenSearchWriterReportsUnavailableInsteadOfPretendingSuccess(t *testing.T) {
	err := (OpenSearchWriter{}).Upsert(
		context.Background(),
		"agent_conversation_events",
		"step-1",
		json.RawMessage(`{"event_id":"step-1"}`),
	)
	if !errors.Is(err, opensearch.ErrUnavailable) {
		t.Fatalf("Upsert() error = %v, want ErrUnavailable", err)
	}
}

func TestOpenSearchUnavailableLeavesPostgresOutboxDurable(t *testing.T) {
	db := newProjectionDB(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	createProjection(t, db, &model.AgentProjectionOutbox{
		ID: "outbox-context", StepID: "step-context",
		IndexAlias: "agent_conversation_events", DocumentID: "run-1:step-context",
		PayloadJSON: `{"event_id":"step-context","content":"context remains durable"}`,
		Status:      "pending", NextAttemptAt: now.Add(-time.Second),
		CreatedAt: now, UpdatedAt: now,
	})
	projector := agentruntime.NewProjector(
		NewStore(db), OpenSearchWriter{}, inlineProjectionExecutor{},
		agentruntime.ProjectorConfig{
			WorkerID: "projector", LeaseTTL: time.Minute, Now: func() time.Time { return now },
		},
	)
	if err := projector.SubmitNext(context.Background()); !errors.Is(err, opensearch.ErrUnavailable) {
		t.Fatalf("SubmitNext() error = %v, want ErrUnavailable", err)
	}
	var stored model.AgentProjectionOutbox
	if err := db.First(&stored, "id = ?", "outbox-context").Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != "pending" || stored.PayloadJSON == "" ||
		!stored.NextAttemptAt.Equal(now.Add(5*time.Second)) {
		t.Fatalf("Postgres context after OpenSearch outage = %#v", stored)
	}
}

type inlineProjectionExecutor struct{}

func (inlineProjectionExecutor) Submit(
	ctx context.Context,
	_ string,
	task func(context.Context) error,
) error {
	return task(ctx)
}
