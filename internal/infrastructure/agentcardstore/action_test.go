package agentcardstore

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentcard"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	appcardaction "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/cardaction"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/agentcardcompiler"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/agentstore"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	cardactionproto "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
)

func TestAgentCardCallbackClaimQueuesContinuationAndIsIdempotent(t *testing.T) {
	fixture := newCardStoreFixture(t)
	compiler := agentcardcompiler.New()
	binder, err := agentcard.NewBinder(agentcard.BinderOptions{
		Store: fixture.repo, Compiler: compiler,
		BindingKey: []byte("0123456789abcdef0123456789abcdef"),
		Now:        time.Now,
	})
	if err != nil {
		t.Fatalf("NewBinder() error = %v", err)
	}
	bound, err := binder.BindAndBegin(context.Background(), agentcard.BindRequest{
		RunID: fixture.runID, ExpectedRunRevision: 1,
		ChatID: "chat-1", ReplyToMessageID: "source-message",
		ExpectedActorOpenID: "owner-1", InteractionKind: "agent_card",
		IdempotencyKey: "callback-e2e", ExpiresAt: time.Now().UTC().Add(time.Hour),
		Spec: agentcard.CardSpec{
			Version: agentcard.VersionV1, Title: "提交",
			Blocks: []agentcard.Block{
				agentcard.TextInput("reason_input", agentcard.InputField{
					FieldID: "reason", FormID: "form", Label: "原因",
					Required: true,
				}, agentcard.TextInputConfig{MinLength: 2, MaxLength: 20}),
			},
			Actions: []agentcard.Action{{
				Kind: agentcard.ActionSubmit, ID: "submit", Label: "提交",
				Mode: agentcard.ActionModeUI, Intent: "submit_form",
				FormRef: "form",
			}},
		},
		Projection: agentruntime.ProjectionDocument{
			IndexAlias: "agent-conversations", DocumentID: fixture.runID,
			Payload: json.RawMessage(`{"event_type":"agent_card_wait"}`),
		},
	})
	if err != nil {
		t.Fatalf("BindAndBegin() error = %v", err)
	}
	token := findJSONToken(t, bound.CompiledJSON)
	if _, err := fixture.repo.MarkSurfaceSent(
		context.Background(),
		agentcard.MarkSurfaceSentRequest{
			SurfaceID:        bound.Surface.ID,
			ExpectedRevision: bound.Surface.Revision,
			MessageID:        "card-message", SourceRef: "delivery:callback-e2e",
			SentAt: time.Now().UTC(),
		},
	); err != nil {
		t.Fatalf("MarkSurfaceSent() error = %v", err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	dispatcher, err := agentcard.NewCallbackDispatcher(
		agentcard.CallbackDispatcherOptions{
			Store: fixture.repo, Compiler: compiler,
			Now: func() time.Time { return now },
		},
	)
	if err != nil {
		t.Fatalf("NewCallbackDispatcher() error = %v", err)
	}
	action := &cardactionproto.Parsed{
		Name: cardactionproto.ActionAgentRuntimeResume,
		Runtime: &cardactionproto.RuntimeEnvelope{
			RunID: fixture.runID, StepID: bound.Surface.WaitStepID,
			InteractionID: bound.Surface.InteractionID,
			Revision:      bound.Surface.Revision, Token: token,
			InteractionKind: "agent_card", ContinueAgent: true,
			ActionID: "submit",
		},
		FormValue: map[string]any{"reason": "approved"},
		Source: cardactionproto.CallbackSource{
			EventID: "event-callback-1", MessageID: "card-message",
			ChatID: "chat-1", OperatorOpenID: "owner-1",
		},
	}
	request := appcardaction.ContinuationRequest{Action: action}
	if _, err := dispatcher.Dispatch(context.Background(), request); err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if _, err := dispatcher.Dispatch(context.Background(), request); err != nil {
		t.Fatalf("Dispatch(replay) error = %v", err)
	}

	surface, err := fixture.repo.GetByInteraction(
		context.Background(),
		agentcard.GetSurfaceRequest{
			RunID:         fixture.runID,
			InteractionID: bound.Surface.InteractionID,
		},
	)
	if err != nil {
		t.Fatalf("GetByInteraction() error = %v", err)
	}
	if surface.Status != agentcard.SurfaceStatusSubmitted ||
		surface.PatchStatus != agentcard.PatchStatusPending ||
		surface.LastActionID != "submit" ||
		surface.LastSourceRef != "event:event-callback-1" {
		t.Fatalf("claimed surface = %#v", surface)
	}
	var run model.AgentRun
	if err := fixture.db.First(&run, "id = ?", fixture.runID).Error; err != nil {
		t.Fatalf("load run: %v", err)
	}
	if run.Status != string(agentruntime.RunStatusQueued) ||
		run.Revision != bound.Surface.Revision+1 ||
		run.WaitingReason != "" || run.WaitingToken != "" {
		t.Fatalf("continued run = %#v", run)
	}
	var eventCount, resumeCount, continuationCount int64
	if err := fixture.db.Model(&model.AgentStep{}).
		Where(
			"run_id = ? AND kind = ?",
			fixture.runID,
			string(agentruntime.StepKindResume),
		).Count(&resumeCount).Error; err != nil {
		t.Fatalf("count resume: %v", err)
	}
	if err := fixture.db.Model(&model.AgentStep{}).
		Where(
			"run_id = ? AND kind = ?",
			fixture.runID,
			string(agentruntime.StepKindCardAction),
		).Count(&eventCount).Error; err != nil {
		t.Fatalf("count card event: %v", err)
	}
	if err := fixture.db.Model(&model.AgentStep{}).
		Where(
			"run_id = ? AND kind = ?",
			fixture.runID,
			string(agentruntime.StepKindDecide),
		).Count(&continuationCount).Error; err != nil {
		t.Fatalf("count continuation: %v", err)
	}
	if eventCount != 1 || resumeCount != 1 || continuationCount != 1 {
		t.Fatalf(
			"event count=%d resume count=%d continuation count=%d",
			eventCount,
			resumeCount,
			continuationCount,
		)
	}
	dueRunIDs, err := agentstore.NewRepository(fixture.db).
		ListDueContinuationRunIDs(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListDueContinuationRunIDs() error = %v", err)
	}
	foundDue := false
	for _, runID := range dueRunIDs {
		if runID == fixture.runID {
			foundDue = true
			break
		}
	}
	if !foundDue {
		t.Fatalf("callback continuation run %q is not due: %#v", fixture.runID, dueRunIDs)
	}
	var event model.AgentStep
	if err := fixture.db.Where(
		"run_id = ? AND kind = ?",
		fixture.runID,
		string(agentruntime.StepKindCardAction),
	).First(&event).Error; err != nil {
		t.Fatalf("load card event: %v", err)
	}
	if strings.Contains(event.InputJSON, token) ||
		strings.Contains(event.InputJSON, "trusted-task") {
		t.Fatalf("public callback event leaked trusted data: %s", event.InputJSON)
	}
}
