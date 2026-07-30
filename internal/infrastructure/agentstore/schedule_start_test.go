package agentstore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
)

func TestCreateScheduleEditInteractionIsAtomicIdempotentAndProtectsActiveRun(t *testing.T) {
	fixture := newRepositoryFixture(t, agentruntime.RunStatusCompleted)
	request := testCreateScheduleEditInteractionRequest("source_atomic_1", "step_wait_atomic_1", "interaction_atomic_1")

	startedAt := time.Now().UTC()
	first, err := fixture.repo.CreateScheduleEditInteraction(context.Background(), request)
	if err != nil {
		t.Fatalf("CreateScheduleEditInteraction() error = %v", err)
	}
	if err := first.Validate(); err != nil {
		t.Fatalf("created interaction is invalid: %v", err)
	}
	if first.StepID != request.StepID || first.InteractionID != request.InteractionID || first.Revision != 1 {
		t.Fatalf("created interaction = %#v, want requested wait at revision 1", first)
	}
	if first.ExpiresAt.Before(startedAt.Add(request.WaitTTL-time.Second)) ||
		first.ExpiresAt.After(time.Now().UTC().Add(request.WaitTTL+time.Second)) {
		t.Fatalf("ExpiresAt = %s, want approximately now + %s", first.ExpiresAt, request.WaitTTL)
	}

	replayed, err := fixture.repo.CreateScheduleEditInteraction(context.Background(), request)
	if err != nil {
		t.Fatalf("replayed CreateScheduleEditInteraction() error = %v", err)
	}
	if replayed != first {
		t.Fatalf("replayed result = %#v, want %#v", replayed, first)
	}
	incompatibleReplay := request
	incompatibleReplay.TokenHash = agentruntime.HashInteractionToken("different-token")
	if _, err := fixture.repo.CreateScheduleEditInteraction(
		context.Background(),
		incompatibleReplay,
	); !errors.Is(err, agentruntime.ErrInteractionConflict) {
		t.Fatalf("incompatible replay error = %v, want ErrInteractionConflict", err)
	}

	var runCount, waitCount, projectionCount int64
	if err := fixture.db.Table("agent_runs").
		Where("trigger_message_id = ?", request.Run.TriggerMessageID).
		Count(&runCount).Error; err != nil {
		t.Fatalf("count runs: %v", err)
	}
	if err := fixture.db.Table("agent_steps").
		Where("id = ?", request.StepID).
		Count(&waitCount).Error; err != nil {
		t.Fatalf("count wait steps: %v", err)
	}
	if err := fixture.db.Table("agent_projection_outbox").
		Where("step_id = ?", request.StepID).
		Count(&projectionCount).Error; err != nil {
		t.Fatalf("count projection outbox: %v", err)
	}
	if runCount != 1 || waitCount != 1 || projectionCount != 1 {
		t.Fatalf("durable records = run:%d wait:%d projection:%d, want 1/1/1",
			runCount, waitCount, projectionCount)
	}

	conflicting := testCreateScheduleEditInteractionRequest(
		"source_atomic_2",
		"step_wait_atomic_2",
		"interaction_atomic_2",
	)
	_, err = fixture.repo.CreateScheduleEditInteraction(context.Background(), conflicting)
	if !errors.Is(err, agentruntime.ErrActiveRunConflict) {
		t.Fatalf("conflicting CreateScheduleEditInteraction() error = %v, want ErrActiveRunConflict", err)
	}
	var conflictingRunCount int64
	if err := fixture.db.Table("agent_runs").
		Where("trigger_message_id = ?", conflicting.Run.TriggerMessageID).
		Count(&conflictingRunCount).Error; err != nil {
		t.Fatalf("count conflicting runs: %v", err)
	}
	if conflictingRunCount != 0 {
		t.Fatalf("conflicting run count = %d, want transaction rollback", conflictingRunCount)
	}
}

func testCreateScheduleEditInteractionRequest(
	sourceMessageID string,
	stepID string,
	interactionID string,
) agentruntime.CreateScheduleEditInteractionRequest {
	trusted, err := agentruntime.EncodeScheduleEditTrustedInput(agentruntime.StartScheduleEditRequest{
		TaskID:          "task_atomic",
		ActorOpenID:     "actor_atomic",
		ChatID:          "chat_atomic",
		SourceMessageID: sourceMessageID,
		NewValues:       map[string]any{"name": "renamed"},
	})
	if err != nil {
		panic(err)
	}
	return agentruntime.CreateScheduleEditInteractionRequest{
		Run: agentruntime.StartRunRequest{
			AppID:            repositoryTestTenant.AppID,
			BotOpenID:        repositoryTestTenant.BotOpenID,
			ChatID:           "chat_atomic",
			ScopeType:        agentruntime.ScopeTypeChat,
			ScopeID:          "chat_atomic",
			TriggerType:      agentruntime.TriggerTypeShadow,
			TriggerMessageID: sourceMessageID,
			ActorOpenID:      "actor_atomic",
			Goal:             "confirm edit",
		},
		StepID:        stepID,
		InteractionID: interactionID,
		TokenHash:     agentruntime.HashInteractionToken("token-" + interactionID),
		TrustedInput:  trusted,
		WaitTTL:       15 * time.Minute,
		Projection: agentruntime.ProjectionDocument{
			IndexAlias: "agent-conversations",
			DocumentID: interactionID + ":wait",
			Payload:    []byte(`{"type":"schedule_edit_wait"}`),
		},
	}
}
