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

type capabilityExecutorRecorder struct {
	calls      int
	invocation agentcard.CapabilityInvocation
}

func (e *capabilityExecutorRecorder) Execute(
	_ context.Context,
	invocation agentcard.CapabilityInvocation,
) (json.RawMessage, error) {
	e.calls++
	e.invocation = invocation
	return json.RawMessage(`{"task_id":"trusted-task","updated":true}`), nil
}

type observeContinuationGenerator struct {
	calls   int
	context agentruntime.ContinuationContext
}

func (g *observeContinuationGenerator) Generate(
	_ context.Context,
	input agentruntime.ContinuationContext,
) (agentruntime.TurnDecision, error) {
	g.calls++
	g.context = input
	return agentruntime.TurnDecision{
		Decision: agentruntime.TurnDecisionObserveOnly,
		Reason:   "resolved card is sufficient",
	}, nil
}

type noReplyDeliverer struct{ calls int }

func (d *noReplyDeliverer) Deliver(
	context.Context,
	agentruntime.ReplyRequest,
) (string, error) {
	d.calls++
	return "unexpected", nil
}

func TestCapabilityConfirmationExecutesTrustedInputOnceAndContinuesAgent(t *testing.T) {
	fixture := newCardStoreFixture(t)
	compiler := agentcardcompiler.New()
	binder, err := agentcard.NewBinder(agentcard.BinderOptions{
		Store: fixture.repo, Compiler: compiler,
		BindingKey: []byte("0123456789abcdef0123456789abcdef"),
		Now:        time.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	capability, err := agentcard.NewTrustedCapability(
		"schedule.update",
		json.RawMessage(`{"task_id":"trusted-task","target_chat":"chat-1","mutation":{"enabled":false}}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := binder.BindAndBegin(context.Background(), agentcard.BindRequest{
		RunID: fixture.runID, ExpectedRunRevision: 1,
		ChatID: "chat-1", ReplyToMessageID: "source-message",
		ExpectedActorOpenID: "owner-1", ActorPolicy: agentcard.ActorPolicyOwner,
		InteractionKind: "agent_card", IdempotencyKey: "capability-e2e",
		ExpiresAt: time.Now().UTC().Add(time.Hour),
		Spec: agentcard.CardSpec{
			Version: agentcard.VersionV1, Title: "确认修改",
			Blocks: []agentcard.Block{
				agentcard.TextInput("object_input", agentcard.InputField{
					FieldID: "object_id", FormID: "confirm_form",
					Label: "对象", Required: true,
				}, agentcard.TextInputConfig{MinLength: 1, MaxLength: 64}),
				agentcard.TextInput("chat_input", agentcard.InputField{
					FieldID: "target_chat", FormID: "confirm_form",
					Label: "目标群", Required: true,
				}, agentcard.TextInputConfig{MinLength: 1, MaxLength: 64}),
			},
			Actions: []agentcard.Action{{
				Kind: agentcard.ActionSubmit, ID: "confirm", Label: "确认执行",
				Mode:   agentcard.ActionModeCapabilityConfirm,
				Intent: "schedule.update", FormRef: "confirm_form",
			}},
		},
		TrustedCapabilities: map[string]agentcard.TrustedCapability{
			"confirm": capability,
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
			SurfaceID: bound.Surface.ID, ExpectedRevision: bound.Surface.Revision,
			MessageID: "card-message", SourceRef: "delivery:capability",
			SentAt: time.Now().UTC(),
		},
	); err != nil {
		t.Fatal(err)
	}
	callbackAt := time.Now().UTC().Truncate(time.Microsecond)
	dispatcher, err := agentcard.NewCallbackDispatcher(
		agentcard.CallbackDispatcherOptions{
			Store: fixture.repo, Compiler: compiler,
			Now: func() time.Time { return callbackAt },
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	callback := &cardactionproto.Parsed{
		Name: cardactionproto.ActionAgentRuntimeResume,
		Runtime: &cardactionproto.RuntimeEnvelope{
			RunID: fixture.runID, StepID: bound.Surface.WaitStepID,
			InteractionID: bound.Surface.InteractionID,
			Revision:      bound.Surface.Revision, Token: token,
			InteractionKind: "agent_card", ContinueAgent: true,
			ActionID: "confirm",
		},
		FormValue: map[string]any{
			"object_id":   "forged-object",
			"target_chat": "forged-chat",
		},
		Source: cardactionproto.CallbackSource{
			EventID: "event-capability", MessageID: "card-message",
			ChatID: "chat-1", OperatorOpenID: "owner-1",
		},
	}
	if _, err := dispatcher.Dispatch(
		context.Background(),
		appcardaction.ContinuationRequest{Action: callback},
	); err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}

	executor := &capabilityExecutorRecorder{}
	capabilityService, err := agentcard.NewCapabilityService(
		agentcard.CapabilityServiceOptions{
			Store: fixture.repo, Executor: executor,
			Compiler: compiler, Now: time.Now,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	generator := &observeContinuationGenerator{}
	deliverer := &noReplyDeliverer{}
	processor := agentruntime.NewContinuationProcessor(
		agentstore.NewRepository(fixture.db),
		generator,
		deliverer,
		agentruntime.ContinuationProcessorConfig{
			WorkerID: "capability-worker", LeaseTTL: time.Minute,
			RetryDelay: time.Second, CapabilityProcessor: capabilityService,
		},
	)
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
		t.Fatalf(
			"capability continuation run %q is not due: %#v",
			fixture.runID,
			dueRunIDs,
		)
	}
	if err := processor.ProcessRun(context.Background(), fixture.runID); err != nil {
		var steps []model.AgentStep
		var outboxes []model.AgentProjectionOutbox
		var executions []model.AgentCapabilityExecution
		var surfaces []model.AgentCardSurface
		_ = fixture.db.Order(`"index"`).Find(&steps, "run_id = ?", fixture.runID).Error
		_ = fixture.db.Find(&outboxes).Error
		_ = fixture.db.Find(&executions).Error
		_ = fixture.db.Find(&surfaces, "run_id = ?", fixture.runID).Error
		t.Fatalf(
			"ProcessRun() error = %v\nsteps=%#v\noutboxes=%#v\nexecutions=%#v\nsurfaces=%#v",
			err,
			steps,
			outboxes,
			executions,
			surfaces,
		)
	}
	if err := processor.ProcessRun(context.Background(), fixture.runID); err != nil {
		t.Fatalf("ProcessRun(replay) error = %v", err)
	}
	var executedInput struct {
		TaskID     string `json:"task_id"`
		TargetChat string `json:"target_chat"`
	}
	if err := json.Unmarshal(executor.invocation.Input, &executedInput); err != nil {
		t.Fatalf("decode executed input: %v", err)
	}
	if executor.calls != 1 ||
		executor.invocation.Name != "schedule.update" ||
		executedInput.TaskID != "trusted-task" ||
		executedInput.TargetChat != "chat-1" ||
		strings.Contains(string(executor.invocation.Input), "forged-object") ||
		executor.invocation.Permission.ChatID != "chat-1" ||
		executor.invocation.Permission.ActorOpenID != "owner-1" {
		t.Fatalf("trusted executor invocation = %#v", executor.invocation)
	}
	var generatedOutcome struct {
		Succeeded bool `json:"succeeded"`
		Output    struct {
			Updated bool `json:"updated"`
		} `json:"output"`
	}
	if err := json.Unmarshal(
		generator.context.LatestOutcome.Payload,
		&generatedOutcome,
	); err != nil {
		t.Fatalf("decode generated outcome: %v", err)
	}
	if generator.calls != 1 || deliverer.calls != 0 ||
		generator.context.LatestOutcome.Type != agentruntime.EventTypeCapabilityResult ||
		generator.context.LatestOutcome.Action != "confirm" ||
		generator.context.LatestOutcome.ActorOpenID != "owner-1" ||
		!generatedOutcome.Succeeded || !generatedOutcome.Output.Updated {
		t.Fatalf(
			"generator calls=%d delivery=%d context=%#v",
			generator.calls,
			deliverer.calls,
			generator.context,
		)
	}

	var execution model.AgentCapabilityExecution
	if err := fixture.db.First(
		&execution,
		"idempotency_key = ?",
		executor.invocation.IdempotencyKey,
	).Error; err != nil {
		t.Fatalf("load execution: %v", err)
	}
	var storedOutput struct {
		Updated bool `json:"updated"`
	}
	if err := json.Unmarshal([]byte(execution.OutputJSON), &storedOutput); err != nil {
		t.Fatalf("decode execution output: %v", err)
	}
	if execution.Status != "completed" ||
		!storedOutput.Updated ||
		strings.Contains(execution.InputJSON, "forged-object") {
		t.Fatalf("execution = %#v", execution)
	}
	var surface model.AgentCardSurface
	if err := fixture.db.First(&surface, "id = ?", bound.Surface.ID).Error; err != nil {
		t.Fatal(err)
	}
	if surface.Status != string(agentcard.SurfaceStatusResolved) ||
		surface.PatchStatus != string(agentcard.PatchStatusPending) {
		t.Fatalf("surface = %#v", surface)
	}
	var resultCount, resumeCount int64
	if err := fixture.db.Model(&model.AgentStep{}).
		Where("run_id = ? AND kind = ?", fixture.runID, string(agentruntime.StepKindCapabilityResult)).
		Count(&resultCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Model(&model.AgentStep{}).
		Where("run_id = ? AND kind = ?", fixture.runID, string(agentruntime.StepKindResume)).
		Count(&resumeCount).Error; err != nil {
		t.Fatal(err)
	}
	if resultCount != 1 || resumeCount != 1 {
		t.Fatalf("capability results=%d resumes=%d", resultCount, resumeCount)
	}
}
