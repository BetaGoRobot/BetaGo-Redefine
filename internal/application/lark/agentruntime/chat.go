package agentruntime

import (
	"context"
	"time"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	chatflow "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime/chatflow"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

// ChatGenerationPlan is the root-package alias for the chatflow execution plan
// used to produce standard or agentic replies.
type ChatGenerationPlan = chatflow.Plan

// ChatGenerationPlanGenerator is the root-package alias for the low-level plan generator callback.
type ChatGenerationPlanGenerator = chatflow.PlanGenerator

// ChatGenerationPlanExecutor is the root-package alias for the chat plan executor contract.
type ChatGenerationPlanExecutor = chatflow.PlanExecutor

// ChatResponseRequest carries the information needed to produce an initial chat response.
type ChatResponseRequest struct {
	Event     *larkim.P2MessageReceiveV1
	Plan      ChatGenerationPlan
	StartedAt time.Time
	Ownership InitialRunOwnership
}

// RuntimeAgenticCutoverRequest is passed into the dedicated agentic runtime
// cutover handler after entry planning succeeds.
type RuntimeAgenticCutoverRequest struct {
	Event     *larkim.P2MessageReceiveV1
	Plan      ChatGenerationPlan
	StartedAt time.Time
	Ownership InitialRunOwnership
}

// RuntimeStandardCutoverRequest is the standard-mode equivalent of the runtime cutover request.
type RuntimeStandardCutoverRequest struct {
	Event     *larkim.P2MessageReceiveV1
	Plan      ChatGenerationPlan
	StartedAt time.Time
	Ownership InitialRunOwnership
}

// RuntimeAgenticCutoverHandler handles runtime handoff for agentic-mode initial replies.
type RuntimeAgenticCutoverHandler interface {
	Handle(context.Context, RuntimeAgenticCutoverRequest) error
}

// RuntimeStandardCutoverHandler handles runtime handoff for standard-mode initial replies.
type RuntimeStandardCutoverHandler interface {
	Handle(context.Context, RuntimeStandardCutoverRequest) error
}

// AgenticChatEntryHandler adapts chatflow entry planning onto the runtime-facing
// handler interface used by message operators.
type AgenticChatEntryHandler struct {
	inner *chatflow.AgenticEntryHandler
}

var (
	chatGenerationPlanExecutor   = chatflow.PlanExecutorOrDefault()
	runtimeAgenticCutoverBuilder = func(context.Context) RuntimeAgenticCutoverHandler { return nil }
	runtimeAgenticCardSender     = larkmsg.SendAndUpdateStreamingCard
)

// NewAgenticChatEntryHandler constructs the default runtime chat entry handler.
func NewAgenticChatEntryHandler() *AgenticChatEntryHandler {
	return &AgenticChatEntryHandler{inner: chatflow.NewAgenticEntryHandler()}
}

// SetChatGenerationPlanExecutor overrides the shared chat executor used by
// root-package chat entry and runtime cutover flows.
func SetChatGenerationPlanExecutor(executor ChatGenerationPlanExecutor) {
	chatflow.SetPlanExecutor(executor)
	chatGenerationPlanExecutor = chatflow.PlanExecutorOrDefault()
}

// SetChatGenerationPlanGenerator overrides only the shared plan generator while
// preserving the standard executor wrapper.
func SetChatGenerationPlanGenerator(generator ChatGenerationPlanGenerator) {
	chatflow.SetPlanGenerator(generator)
	chatGenerationPlanExecutor = chatflow.PlanExecutorOrDefault()
}

// SetRuntimeAgenticCutoverBuilder overrides the builder that resolves the
// agentic cutover handler from the current context.
func SetRuntimeAgenticCutoverBuilder(builder func(context.Context) RuntimeAgenticCutoverHandler) {
	if builder == nil {
		runtimeAgenticCutoverBuilder = func(context.Context) RuntimeAgenticCutoverHandler { return nil }
		return
	}
	runtimeAgenticCutoverBuilder = builder
}

// Handle builds an agentic chat request from an incoming event and then either
// hands it to the dedicated runtime cutover handler or falls back to direct streaming.
func (h *AgenticChatEntryHandler) Handle(
	ctx context.Context,
	event *larkim.P2MessageReceiveV1,
	meta *xhandler.BaseMetaData,
	chatType string,
	size *int,
	args ...string,
) (err error) {
	ctx, span := otel.Start(ctx)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	req, err := h.inner.BuildRequest(ctx, event, meta, chatType, size, args...)
	if err != nil || req == nil {
		return err
	}
	if ownership, ok := InitialRunOwnershipFromContext(ctx); ok {
		return handleAgenticChatResponse(ctx, req.Event, req.Plan, req.StartedAt, ownership)
	}
	return handleAgenticChatResponse(ctx, req.Event, req.Plan, req.StartedAt, InitialRunOwnership{})
}

func handleAgenticChatResponse(
	ctx context.Context,
	event *larkim.P2MessageReceiveV1,
	plan ChatGenerationPlan,
	startedAt time.Time,
	ownership InitialRunOwnership,
) error {
	plan.Mode = appconfig.ChatModeAgentic.Normalize()
	if runtimeHandler := runtimeAgenticCutoverBuilder(ctx); runtimeHandler != nil {
		return runtimeHandler.Handle(ctx, RuntimeAgenticCutoverRequest{
			Event:     event,
			Plan:      chatflow.ClonePlan(plan),
			StartedAt: startedAt,
			Ownership: ownership,
		})
	}
	msgSeq, err := plan.Generate(ctx, event)
	if err != nil {
		return err
	}
	return runtimeAgenticCardSender(ctx, event.Event.Message, msgSeq)
}
