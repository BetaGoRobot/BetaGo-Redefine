package agentstore

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
)

func TestExpireScheduleEditInteractionsTerminalizesRunAndReleasesSession(t *testing.T) {
	f := newRepositoryFixture(t, agentruntime.RunStatusCompleted)
	request := testCreateScheduleEditInteractionRequest(
		"source_expiry_1",
		"step_wait_expiry_1",
		"interaction_expiry_1",
	)
	request.WaitTTL = time.Nanosecond
	started, err := f.repo.CreateScheduleEditInteraction(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	now := started.ExpiresAt.Add(time.Second)
	expired, err := f.repo.ExpireScheduleEditInteractions(context.Background(), now, 32)
	if err != nil {
		t.Fatalf("ExpireScheduleEditInteractions() error = %v", err)
	}
	if expired != 1 {
		t.Fatalf("expired count = %d, want 1", expired)
	}

	var run model.AgentRun
	if err := f.db.First(&run, "id = ?", started.RunID).Error; err != nil {
		t.Fatal(err)
	}
	var session model.AgentSession
	if err := f.db.First(&session, "id = ?", run.SessionID).Error; err != nil {
		t.Fatal(err)
	}
	if run.Status != string(agentruntime.RunStatusCancelled) ||
		run.WaitingReason != "" ||
		run.WaitingToken != "" ||
		run.FinishedAt.IsZero() ||
		session.ActiveRunID != "" {
		t.Fatalf("expired run=%#v session=%#v", run, session)
	}

	var timeout model.AgentStep
	if err := f.db.Where(
		"run_id = ? AND kind = ?", run.ID, string(agentruntime.StepKindResume),
	).Order(`"index" DESC`).First(&timeout).Error; err != nil {
		t.Fatal(err)
	}
	var event agentruntime.ConversationEvent
	if json.Unmarshal([]byte(timeout.InputJSON), &event) != nil ||
		event.Type != agentruntime.EventTypeTimeout ||
		event.InteractionID != request.InteractionID {
		t.Fatalf("timeout event = %s", timeout.InputJSON)
	}
	var outbox model.AgentProjectionOutbox
	if err := f.db.First(&outbox, "step_id = ?", timeout.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(outbox.DocumentID, ":"+timeout.ID) {
		t.Fatalf("timeout projection document = %q, want current step suffix", outbox.DocumentID)
	}

	expired, err = f.repo.ExpireScheduleEditInteractions(context.Background(), now.Add(time.Minute), 32)
	if err != nil || expired != 0 {
		t.Fatalf("replayed expiry = %d, %v, want 0", expired, err)
	}
}
