package agentruntime

import (
	"context"
	"errors"
	"iter"
	"strings"
	"time"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	capdef "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime/capability"
	chatflow "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime/chatflow"
	message "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime/message"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/runtimecontext"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/toolmeta"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	arktools "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

type defaultChatGenerationPlanExecutor struct {
	deps defaultRuntimeExecutorDeps
}

type defaultRuntimeExecutorDeps struct {
	chatGenerationExecutor     ChatGenerationPlanExecutor
	toolProvider               func() *arktools.Impl[larkim.P2MessageReceiveV1]
	initialPlanBuilder         func(context.Context, InitialChatGenerationRequest) (InitialChatExecutionPlan, error)
	agenticInitialPlanBuilder  func(context.Context, InitialChatGenerationRequest) (InitialChatExecutionPlan, error)
	initialChatTurnExecutor    func(context.Context, InitialChatTurnRequest) (InitialChatTurnResult, error)
	initialChatStreamFinalizer func(context.Context, InitialChatExecutionPlan, iter.Seq[*ark_dal.ModelStreamRespReasoning]) iter.Seq[*ark_dal.ModelStreamRespReasoning]
}

var (
	defaultChatToolProvider              = func() *arktools.Impl[larkim.P2MessageReceiveV1] { return nil }
	defaultInitialChatPlanBuilder        = chatflow.BuildInitialChatExecutionPlan
	defaultAgenticInitialChatPlanBuilder = chatflow.BuildAgenticChatExecutionPlan
	defaultInitialChatTurnExecutor       = chatflow.ExecuteInitialChatTurn
	defaultInitialChatStreamFinalizer    = chatflow.FinalizeInitialChatStream
)

func snapshotDefaultRuntimeExecutorDeps() defaultRuntimeExecutorDeps {
	return defaultRuntimeExecutorDeps{
		chatGenerationExecutor:     chatGenerationPlanExecutor,
		toolProvider:               defaultChatToolProvider,
		initialPlanBuilder:         defaultInitialChatPlanBuilder,
		agenticInitialPlanBuilder:  defaultAgenticInitialChatPlanBuilder,
		initialChatTurnExecutor:    defaultInitialChatTurnExecutor,
		initialChatStreamFinalizer: defaultInitialChatStreamFinalizer,
	}
}

// SetDefaultChatToolProvider implements agent runtime behavior.
func SetDefaultChatToolProvider(provider func() *arktools.Impl[larkim.P2MessageReceiveV1]) {
	if provider == nil {
		defaultChatToolProvider = func() *arktools.Impl[larkim.P2MessageReceiveV1] { return nil }
		return
	}
	defaultChatToolProvider = provider
}

// NewDefaultChatGenerationPlanExecutor implements agent runtime behavior.
func NewDefaultChatGenerationPlanExecutor() ChatGenerationPlanExecutor {
	return NewDefaultChatGenerationPlanExecutorWithDeps(snapshotDefaultRuntimeExecutorDeps())
}

// NewDefaultChatGenerationPlanExecutorWithDeps implements agent runtime behavior.
func NewDefaultChatGenerationPlanExecutorWithDeps(deps defaultRuntimeExecutorDeps) ChatGenerationPlanExecutor {
	return defaultChatGenerationPlanExecutor{deps: deps}
}

// Generate implements agent runtime behavior.
func (e defaultChatGenerationPlanExecutor) Generate(ctx context.Context, event *larkim.P2MessageReceiveV1, plan ChatGenerationPlan) (iter.Seq[*ark_dal.ModelStreamRespReasoning], error) {
	var tools *arktools.Impl[larkim.P2MessageReceiveV1]
	if e.deps.toolProvider != nil {
		tools = decorateRuntimeChatTools(e.deps.toolProvider())
}
	if plan.Mode.Normalize() == appconfig.ChatModeAgentic {
		return generateAgenticChatPlan(ctx, event, plan, tools, e.deps)
	}
	return generateStandardChatPlan(ctx, event, plan, tools, e.deps)
}

func generateStandardChatPlan(
	ctx context.Context,
	event *larkim.P2MessageReceiveV1,
	plan ChatGenerationPlan,
	tools *arktools.Impl[larkim.P2MessageReceiveV1],
	deps defaultRuntimeExecutorDeps,
) (iter.Seq[*ark_dal.ModelStreamRespReasoning], error) {
	return generateChatPlanWithVariant(ctx, event, plan, tools, chatGenerationVariant{
		planBuilder: deps.initialPlanBuilder,
		finalizer:   deps.initialChatStreamFinalizer,
		toolTurns:   chatflow.DefaultInitialChatToolTurns,
	}, deps)
}

func generateAgenticChatPlan(
	ctx context.Context,
	event *larkim.P2MessageReceiveV1,
	plan ChatGenerationPlan,
	tools *arktools.Impl[larkim.P2MessageReceiveV1],
	deps defaultRuntimeExecutorDeps,
) (iter.Seq[*ark_dal.ModelStreamRespReasoning], error) {
	return generateChatPlanWithVariant(ctx, event, plan, tools, chatGenerationVariant{
		planBuilder: deps.agenticInitialPlanBuilder,
		finalizer:   deps.initialChatStreamFinalizer,
		toolTurns:   chatflow.DefaultAgenticInitialChatToolTurns,
	}, deps)
}

type chatGenerationVariant struct {
	planBuilder func(context.Context, InitialChatGenerationRequest) (InitialChatExecutionPlan, error)
	finalizer   func(context.Context, InitialChatExecutionPlan, iter.Seq[*ark_dal.ModelStreamRespReasoning]) iter.Seq[*ark_dal.ModelStreamRespReasoning]
	toolTurns   int
}

func buildChatGenerationLoopOptions(plan ChatGenerationPlan, tools *arktools.Impl[larkim.P2MessageReceiveV1], variant chatGenerationVariant, deps defaultRuntimeExecutorDeps) RuntimeInitialChatLoopOptions {
	builder := variant.planBuilder
	turnExecutor := deps.initialChatTurnExecutor
	return RuntimeInitialChatLoopOptions{
		Plan:                plan,
		Tools:               tools,
		Builder:             builder,
		TurnExecutor:        turnExecutor,
		DefaultToolTurns:    variant.toolTurns,
		Finalizer:           variant.finalizer,
		EnsureDeferredTools: true,
	}
}

func generateChatPlanWithVariant(
	ctx context.Context,
	event *larkim.P2MessageReceiveV1,
	plan ChatGenerationPlan,
	tools *arktools.Impl[larkim.P2MessageReceiveV1],
	variant chatGenerationVariant,
	deps defaultRuntimeExecutorDeps,
) (iter.Seq[*ark_dal.ModelStreamRespReasoning], error) {
	return BuildRuntimeInitialChatLoop(ctx, event, buildChatGenerationLoopOptions(plan, tools, variant, deps))
}

func decorateRuntimeChatTools[T any](src *arktools.Impl[T]) *arktools.Impl[T] {
	if src == nil {
		return nil
	}

	decorated := arktools.New[T]()
	decorated.WebsearchTool = src.WebsearchTool
	for name, unit := range src.FunctionCallMap {
		if unit == nil {
			continue
		}
		copied := *unit
		copied.Description = decorateRuntimeToolDescription(strings.TrimSpace(unit.FunctionName), strings.TrimSpace(unit.Description))
		decorated.FunctionCallMap[name] = &copied
	}
	return decorated
}

func decorateRuntimeToolDescription(name, desc string) string {
	return capdef.DecoratePrompt(name, desc)
}

func appendRuntimeToolDescription(base, note string) string {
	return capdef.DecoratePrompt("", base+"\n"+note)
}

type initialChatToolExecutionResult struct {
	Trace      *ark_dal.CapabilityCallTrace
	NextOutput *InitialChatToolOutput
}

// CapabilityReplyTurnRequest carries agent runtime state.
type CapabilityReplyTurnRequest struct {
	Session  *AgentSession        `json:"-"`
	Run      *AgentRun            `json:"-"`
	Step     *AgentStep           `json:"-"`
	Input    CapabilityCallInput  `json:"input"`
	Result   CapabilityResult     `json:"result"`
	Recorder InitialTraceRecorder `json:"-"`
}

// CapabilityReplyTurnResult carries agent runtime state.
type CapabilityReplyTurnResult struct {
	Executed          bool                      `json:"executed"`
	Plan              CapabilityReplyPlan       `json:"plan"`
	CapabilityCalls   []CompletedCapabilityCall `json:"capability_calls,omitempty"`
	PendingCapability *QueuedCapabilityCall     `json:"pending_capability,omitempty"`
}

// CapabilityReplyTurnExecutor defines a agent runtime contract.
type CapabilityReplyTurnExecutor interface {
	ExecuteCapabilityReplyTurn(context.Context, CapabilityReplyTurnRequest) (CapabilityReplyTurnResult, error)
}

// WithCapabilityReplyTurnExecutor implements agent runtime behavior.
func WithCapabilityReplyTurnExecutor(executor CapabilityReplyTurnExecutor) ContinuationProcessorOption {
	return func(p *ContinuationProcessor) {
		if p != nil {
			p.capabilityReplyTurnExecutor = executor
		}
	}
}

// ContinuationReplyTurnRequest carries agent runtime state.
type ContinuationReplyTurnRequest struct {
	Session                 *AgentSession        `json:"-"`
	Run                     *AgentRun            `json:"-"`
	Source                  ResumeSource         `json:"source,omitempty"`
	WaitingReason           WaitingReason        `json:"waiting_reason,omitempty"`
	PreviousStepKind        StepKind             `json:"previous_step_kind,omitempty"`
	PreviousStepTitle       string               `json:"previous_step_title,omitempty"`
	PreviousStepExternalRef string               `json:"previous_step_external_ref,omitempty"`
	ResumeSummary           string               `json:"resume_summary,omitempty"`
	ResumePayloadJSON       []byte               `json:"resume_payload_json,omitempty"`
	ThoughtFallback         string               `json:"thought_fallback,omitempty"`
	ReplyFallback           string               `json:"reply_fallback,omitempty"`
	Recorder                InitialTraceRecorder `json:"-"`
}

// ContinuationReplyTurnResult carries agent runtime state.
type ContinuationReplyTurnResult struct {
	Executed          bool                      `json:"executed"`
	Plan              CapabilityReplyPlan       `json:"plan"`
	CapabilityCalls   []CompletedCapabilityCall `json:"capability_calls,omitempty"`
	PendingCapability *QueuedCapabilityCall     `json:"pending_capability,omitempty"`
}

// ContinuationReplyTurnExecutor defines a agent runtime contract.
type ContinuationReplyTurnExecutor interface {
	ExecuteContinuationReplyTurn(context.Context, ContinuationReplyTurnRequest) (ContinuationReplyTurnResult, error)
}

// WithContinuationReplyTurnExecutor implements agent runtime behavior.
func WithContinuationReplyTurnExecutor(executor ContinuationReplyTurnExecutor) ContinuationProcessorOption {
	return func(p *ContinuationProcessor) {
		if p != nil {
			p.continuationReplyTurnExecutor = executor
		}
	}
}

func buildInitialChatCapabilityRegistry(event *larkim.P2MessageReceiveV1, tools *arktools.Impl[larkim.P2MessageReceiveV1]) *CapabilityRegistry {
	registry := NewCapabilityRegistry()
	for _, capability := range capdef.BuildToolCapabilities(tools, nil, event) {
		if capability == nil {
			continue
		}
		_ = registry.Register(capability)
	}
	return registry
}

func executeInitialChatToolCall(
	ctx context.Context,
	registry *CapabilityRegistry,
	event *larkim.P2MessageReceiveV1,
	call InitialChatToolCall,
) initialChatToolExecutionResult {
	return executeChatToolCall(ctx, registry, CapabilityRequest{
		Scope:       initialReplyCapabilityScope(event),
		ChatID:      strings.TrimSpace(message.ChatID(event)),
		ActorOpenID: strings.TrimSpace(initialReplyActorOpenID(event)),
	}, call)
}

func executeChatToolCall(
	ctx context.Context,
	registry *CapabilityRegistry,
	request CapabilityRequest,
	call InitialChatToolCall,
) initialChatToolExecutionResult {
	trace := &ark_dal.CapabilityCallTrace{
		CallID:       strings.TrimSpace(call.CallID),
		FunctionName: strings.TrimSpace(call.FunctionName),
		Arguments:    strings.TrimSpace(call.Arguments),
	}

	request.PayloadJSON = []byte(strings.TrimSpace(call.Arguments))

	capability, meta, err := lookupInitialChatCapability(registry, trace.FunctionName, request.Scope)
	if err != nil {
		trace.Output = err.Error()
		return initialChatToolExecutionResult{
			Trace: trace,
			NextOutput: &InitialChatToolOutput{
				CallID: trace.CallID,
				Output: trace.Output,
			},
		}
	}

	if pending := buildPendingInitialChatToolExecution(meta, *trace); pending != nil {
		return *pending
	}

	result, execErr := invokeInitialChatCapability(ctx, capability, meta, request)
	deferred, hasDeferred := runtimecontext.PopDeferredToolCall(ctx)
	if hasDeferred && execErr == nil {
		output := strings.TrimSpace(deferred.PlaceholderOutput)
		if output == "" {
			output = strings.TrimSpace(result.OutputText)
		}
		trace.Pending = true
		trace.Output = output
		trace.ApprovalType = strings.TrimSpace(deferred.ApprovalType)
		trace.ApprovalTitle = strings.TrimSpace(deferred.ApprovalTitle)
		trace.ApprovalSummary = strings.TrimSpace(deferred.ApprovalSummary)
		trace.ApprovalExpiresAt = deferred.ApprovalExpiresAt.UTC()
		return initialChatToolExecutionResult{
			Trace: trace,
			NextOutput: &InitialChatToolOutput{
				CallID: trace.CallID,
				Output: output,
			},
		}
	}

	if execErr != nil {
		trace.Output = strings.TrimSpace(execErr.Error())
		return initialChatToolExecutionResult{
			Trace: trace,
			NextOutput: &InitialChatToolOutput{
				CallID: trace.CallID,
				Output: trace.Output,
			},
		}
	}

	trace.Output = resolveInitialChatCapabilityOutput(result)
	return initialChatToolExecutionResult{
		Trace: trace,
		NextOutput: &InitialChatToolOutput{
			CallID: trace.CallID,
			Output: trace.Output,
		},
	}
}

func buildPendingInitialChatToolExecution(meta CapabilityMeta, trace ark_dal.CapabilityCallTrace) *initialChatToolExecutionResult {
	if !meta.RequiresApproval {
		return nil
	}
	behavior, _ := toolmeta.LookupRuntimeBehavior(trace.FunctionName)
	placeholder := "已发起审批，等待确认后继续执行。"
	approvalType := "capability"
	approvalTitle := "审批执行能力"
	if behavior.Approval != nil {
		if v := strings.TrimSpace(behavior.Approval.PlaceholderOutput); v != "" {
			placeholder = v
		}
		if v := strings.TrimSpace(behavior.Approval.ApprovalType); v != "" {
			approvalType = v
		}
		if v := strings.TrimSpace(behavior.Approval.ApprovalTitle); v != "" {
			approvalTitle = v
		}
	}

	trace.Pending = true
	trace.Output = placeholder
	trace.ApprovalType = approvalType
	trace.ApprovalTitle = approvalTitle
	trace.ApprovalSummary = strings.TrimSpace(meta.Description)
	trace.ApprovalExpiresAt = time.Now().UTC().Add(defaultCapabilityApprovalTTL)
	return &initialChatToolExecutionResult{
		Trace: &trace,
		NextOutput: &InitialChatToolOutput{
			CallID: trace.CallID,
			Output: placeholder,
		},
	}
}

func lookupInitialChatCapability(registry *CapabilityRegistry, name string, scope CapabilityScope) (Capability, CapabilityMeta, error) {
	if registry == nil {
		return nil, CapabilityMeta{}, errors.New("initial chat capability registry is nil")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, CapabilityMeta{}, errors.New("initial chat capability name is empty")
	}
	if strings.TrimSpace(string(scope)) == "" {
		capability, ok := registry.Get(name)
		if !ok {
			return nil, CapabilityMeta{}, errors.New("initial chat capability not found: " + name)
		}
		return capability, capability.Meta(), nil
	}
	capability, err := registry.Lookup(name, scope)
	if err != nil {
		return nil, CapabilityMeta{}, err
	}
	return capability, capability.Meta(), nil
}

func invokeInitialChatCapability(
	ctx context.Context,
	capability Capability,
	meta CapabilityMeta,
	request CapabilityRequest,
) (CapabilityResult, error) {
	if capability == nil {
		return CapabilityResult{}, errors.New("initial chat capability is nil")
	}
	if meta.DefaultTimeout <= 0 {
		return capability.Execute(ctx, request)
	}

	return capability.Execute(ctx, request)
}

func resolveInitialChatCapabilityOutput(result CapabilityResult) string {
	if text := strings.TrimSpace(result.OutputText); text != "" {
		return text
	}
	if raw := strings.TrimSpace(string(result.OutputJSON)); raw != "" {
		return raw
	}
	return ""
}

type defaultCapabilityReplyTurnExecutor struct {
	selectModel func(context.Context, string, string) replyTurnModelSelection
	deps        defaultRuntimeExecutorDeps
}

// NewDefaultCapabilityReplyTurnExecutor implements agent runtime behavior.
func NewDefaultCapabilityReplyTurnExecutor() CapabilityReplyTurnExecutor {
	return NewDefaultCapabilityReplyTurnExecutorWithDeps(snapshotDefaultRuntimeExecutorDeps())
}

// NewDefaultCapabilityReplyTurnExecutorWithDeps implements agent runtime behavior.
func NewDefaultCapabilityReplyTurnExecutorWithDeps(deps defaultRuntimeExecutorDeps) CapabilityReplyTurnExecutor {
	return &defaultCapabilityReplyTurnExecutor{
		selectModel: selectReplyTurnModel,
		deps:        deps,
	}
}

// ExecuteCapabilityReplyTurn implements agent runtime behavior.
func (e *defaultCapabilityReplyTurnExecutor) ExecuteCapabilityReplyTurn(ctx context.Context, req CapabilityReplyTurnRequest) (CapabilityReplyTurnResult, error) {
	if req.Input.Continuation == nil {
		return CapabilityReplyTurnResult{}, nil
	}
	previousResponseID := strings.TrimSpace(req.Input.Continuation.PreviousResponseID)
	if previousResponseID == "" {
		return CapabilityReplyTurnResult{}, nil
	}
	if req.Session == nil || req.Run == nil || req.Step == nil {
		return CapabilityReplyTurnResult{}, nil
	}

	chatID := strings.TrimSpace(req.Session.ChatID)
	openID := strings.TrimSpace(req.Run.ActorOpenID)
	callID := strings.TrimSpace(req.Step.ExternalRef)
	if chatID == "" || openID == "" || callID == "" {
		return CapabilityReplyTurnResult{}, nil
	}

	runtime, ok := e.resolveReplyTurnRuntime(ctx, chatID, openID)
	if !ok {
		return CapabilityReplyTurnResult{}, nil
	}

	baseRequest := req.Input.Request
	baseRequest.ChatID = coalesceString(baseRequest.ChatID, chatID)
	baseRequest.ActorOpenID = coalesceString(baseRequest.ActorOpenID, openID)
	if req.Run != nil {
		baseRequest.InputText = coalesceString(baseRequest.InputText, req.Run.InputText)
	}

	loopResult, err := ExecuteReplyTurnLoop(ctx, ReplyTurnLoopRequest{
		TurnRequest: InitialChatTurnRequest{
			Plan: InitialChatExecutionPlan{
				ModelID: runtime.modelID,
				ChatID:  runtime.chatID,
				OpenID:  runtime.openID,
				Tools:   runtime.tools,
			},
			PreviousResponseID: previousResponseID,
			ToolOutput: &InitialChatToolOutput{
				CallID: callID,
				Output: resolveCapabilityReplyTurnToolOutput(req.Step.CapabilityName, req.Result),
			},
		},
		ToolTurns:    chatflow.ResolveResearchToolTurnLimit(coalesceString(req.Run.Goal, req.Run.InputText)),
		BaseRequest:  baseRequest,
		Registry:     runtime.registry,
		Recorder:     req.Recorder,
		TurnExecutor: e.deps.initialChatTurnExecutor,
	})
	if err != nil {
		return CapabilityReplyTurnResult{}, err
	}

	return CapabilityReplyTurnResult{
		Executed:          loopResult.Executed,
		Plan:              loopResult.Plan,
		CapabilityCalls:   loopResult.CapabilityCalls,
		PendingCapability: loopResult.PendingCapability,
	}, nil
}

type defaultContinuationReplyTurnExecutor struct {
	selectModel func(context.Context, string, string) replyTurnModelSelection
	deps        defaultRuntimeExecutorDeps
}

type replyTurnModelSelection struct {
	Mode    appconfig.ChatMode
	ModelID string
}

type replyTurnRuntime struct {
	chatID   string
	openID   string
	modelID  string
	tools    *arktools.Impl[larkim.P2MessageReceiveV1]
	registry *CapabilityRegistry
}

// NewDefaultContinuationReplyTurnExecutor implements agent runtime behavior.
func NewDefaultContinuationReplyTurnExecutor() ContinuationReplyTurnExecutor {
	return NewDefaultContinuationReplyTurnExecutorWithDeps(snapshotDefaultRuntimeExecutorDeps())
}

// NewDefaultContinuationReplyTurnExecutorWithDeps implements agent runtime behavior.
func NewDefaultContinuationReplyTurnExecutorWithDeps(deps defaultRuntimeExecutorDeps) ContinuationReplyTurnExecutor {
	return &defaultContinuationReplyTurnExecutor{
		selectModel: selectReplyTurnModel,
		deps:        deps,
	}
}

// ExecuteContinuationReplyTurn implements agent runtime behavior.
func (e *defaultContinuationReplyTurnExecutor) ExecuteContinuationReplyTurn(ctx context.Context, req ContinuationReplyTurnRequest) (ContinuationReplyTurnResult, error) {
	if req.Session == nil || req.Run == nil {
		return ContinuationReplyTurnResult{}, nil
	}

	chatID := strings.TrimSpace(req.Session.ChatID)
	openID := strings.TrimSpace(req.Run.ActorOpenID)
	if chatID == "" || openID == "" {
		return ContinuationReplyTurnResult{}, nil
	}

	runtime, ok := e.resolveReplyTurnRuntime(ctx, chatID, openID)
	if !ok {
		return ContinuationReplyTurnResult{}, nil
	}

	baseRequest := continuationCapabilityRequest(req)
	loopResult, err := ExecuteReplyTurnLoop(ctx, ReplyTurnLoopRequest{
		TurnRequest: InitialChatTurnRequest{
			Plan: InitialChatExecutionPlan{
				ModelID:   runtime.modelID,
				ChatID:    runtime.chatID,
				OpenID:    runtime.openID,
				Prompt:    continuationReplyTurnSystemPrompt(),
				UserInput: buildContinuationReplyTurnUserPrompt(req),
				Tools:     runtime.tools,
			},
		},
		ToolTurns: chatflow.ResolveResearchToolTurnLimit(
			coalesceString(req.Run.Goal, req.Run.InputText),
			req.ResumeSummary,
			strings.TrimSpace(string(req.ResumePayloadJSON)),
		),
		BaseRequest:  baseRequest,
		Registry:     runtime.registry,
		FallbackPlan: CapabilityReplyPlan{ThoughtText: strings.TrimSpace(req.ThoughtFallback), ReplyText: strings.TrimSpace(req.ReplyFallback)},
		Recorder:     req.Recorder,
		TurnExecutor: e.deps.initialChatTurnExecutor,
	})
	if err != nil {
		return ContinuationReplyTurnResult{}, err
	}

	return ContinuationReplyTurnResult{
		Executed:          loopResult.Executed,
		Plan:              loopResult.Plan,
		CapabilityCalls:   loopResult.CapabilityCalls,
		PendingCapability: loopResult.PendingCapability,
	}, nil
}

func (e *defaultCapabilityReplyTurnExecutor) resolveReplyTurnRuntime(ctx context.Context, chatID, openID string) (replyTurnRuntime, bool) {
	return resolveReplyTurnRuntime(ctx, e.deps, e.selectModel, chatID, openID)
}

func (e *defaultContinuationReplyTurnExecutor) resolveReplyTurnRuntime(ctx context.Context, chatID, openID string) (replyTurnRuntime, bool) {
	return resolveReplyTurnRuntime(ctx, e.deps, e.selectModel, chatID, openID)
}

func resolveReplyTurnRuntime(
	ctx context.Context,
	deps defaultRuntimeExecutorDeps,
	selectModel func(context.Context, string, string) replyTurnModelSelection,
	chatID, openID string,
) (replyTurnRuntime, bool) {
	var tools *arktools.Impl[larkim.P2MessageReceiveV1]
	if deps.toolProvider != nil {
		tools = decorateRuntimeChatTools(deps.toolProvider())
	}
	if tools == nil {
		return replyTurnRuntime{}, false
	}
	if selectModel == nil {
		selectModel = selectReplyTurnModel
	}
	modelID := strings.TrimSpace(selectModel(ctx, chatID, openID).ModelID)
	if modelID == "" {
		return replyTurnRuntime{}, false
	}
	return replyTurnRuntime{
		chatID:   chatID,
		openID:   openID,
		modelID:  modelID,
		tools:    tools,
		registry: buildInitialChatCapabilityRegistry(nil, tools),
	}, true
}

func selectReplyTurnModel(ctx context.Context, chatID, openID string) replyTurnModelSelection {
	accessor := appconfig.NewAccessor(ctx, strings.TrimSpace(chatID), strings.TrimSpace(openID))
	if accessor == nil {
		return replyTurnModelSelection{}
	}

	mode := accessor.ChatMode().Normalize()
	modelID := ""
	switch mode {
	case appconfig.ChatModeAgentic:
		modelID = strings.TrimSpace(accessor.ChatReasoningModel())
	default:
		modelID = strings.TrimSpace(accessor.ChatNormalModel())
	}
	if modelID == "" {
		modelID = strings.TrimSpace(accessor.ChatNormalModel())
	}
	if modelID == "" {
		modelID = strings.TrimSpace(accessor.ChatReasoningModel())
	}

	return replyTurnModelSelection{
		Mode:    mode,
		ModelID: modelID,
	}
}

func continuationCapabilityRequest(req ContinuationReplyTurnRequest) CapabilityRequest {
	request := CapabilityRequest{
		Scope:       continuationCapabilityScope(req.Run),
		ChatID:      strings.TrimSpace(req.Session.ChatID),
		ActorOpenID: strings.TrimSpace(req.Run.ActorOpenID),
		InputText:   coalesceString(req.Run.Goal, req.Run.InputText),
	}
	if req.Run != nil {
		request.SessionID = strings.TrimSpace(req.Run.SessionID)
		request.RunID = strings.TrimSpace(req.Run.ID)
	}
	return request
}

func continuationCapabilityScope(run *AgentRun) CapabilityScope {
	if run == nil {
		return CapabilityScopeGroup
	}
	switch run.TriggerType {
	case TriggerTypeP2P:
		return CapabilityScopeP2P
	default:
		return CapabilityScopeGroup
	}
}

func continuationReplyTurnSystemPrompt() string {
	return strings.TrimSpace(`
你是一个 durable agent runtime 的恢复阶段规划器。
当前发生了一个外部恢复事件（例如回调、定时任务、审批完成），你需要基于上下文继续处理原请求。

要求：
- 优先围绕原始用户意图继续，而不是只复述“已经恢复”
- 如有必要可以调用工具补充读取信息
- 如果当前更像调研/写综述/要出处/比较来源的 research 请求，默认不要因为第一次搜索就直接收尾
- 研究型请求优先用 web_search 找来源，再用 research_read_url 读正文，用 research_extract_evidence 抽取证据，用 research_source_ledger 整理引用
- 如果还没有读过正文，或者来源数量、来源多样性、关键问题覆盖度还不够，就视为证据不足；证据不足时继续调用工具
- 当前 runtime 每一轮最多只接受一个需要继续喂回结果的工具调用；如果需要多个工具，请串行规划，一次只发起一个
- 你的输出只能二选一：要么直接给最终回答，要么只发起一个 function call
- 如果需要调用工具，不要输出任何 JSON、解释或额外文本，只发起一个 function call
- 如果不需要调用工具，只输出 JSON object
- 不能同时输出最终回答和 function call
- 字段只使用 thought 和 reply
- thought: 一句简短内部思路摘要
- reply: 给用户看的最终续写回复
- reply 默认像群里正常成员接话，不要写成系统播报、审批回执或工单状态单
- 能直接说结果就直接说结果，非必要不要拉长
`)
}

func buildContinuationReplyTurnUserPrompt(req ContinuationReplyTurnRequest) string {
	var builder strings.Builder
	builder.WriteString("请继续这次 agent runtime 恢复。\n")
	builder.WriteString("原始目标:\n")
	builder.WriteString(replyTurnTextBlock(coalesceString(req.Run.Goal, req.Run.InputText)))
	builder.WriteString("\n恢复来源:\n")
	builder.WriteString(replyTurnTextBlock(continuationSourceLabel(req.Source)))
	builder.WriteString("\n等待原因:\n")
	builder.WriteString(replyTurnTextBlock(string(req.WaitingReason)))
	builder.WriteString("\n前置步骤类型:\n")
	builder.WriteString(replyTurnTextBlock(continuationStepKindLabel(req.PreviousStepKind)))
	builder.WriteString("\n前置步骤标题:\n")
	builder.WriteString(replyTurnTextBlock(req.PreviousStepTitle))
	builder.WriteString("\n前置步骤引用:\n")
	builder.WriteString(replyTurnTextBlock(req.PreviousStepExternalRef))
	builder.WriteString("\n恢复摘要:\n")
	builder.WriteString(replyTurnTextBlock(req.ResumeSummary))
	builder.WriteString("\n恢复 payload:\n")
	builder.WriteString(replyTurnTextBlock(strings.TrimSpace(string(req.ResumePayloadJSON))))
	builder.WriteString("\n静态续写候选思路:\n")
	builder.WriteString(replyTurnTextBlock(req.ThoughtFallback))
	builder.WriteString("\n静态续写候选回复:\n")
	builder.WriteString(replyTurnTextBlock(req.ReplyFallback))
	builder.WriteString("\n请基于这些信息判断：如果需要补充工具，就只发起一个 function call；如果不需要，就输出最终 JSON。不要同时输出两种格式。")
	return builder.String()
}

func resolveCapabilityReplyTurnToolOutput(capabilityName string, result CapabilityResult) string {
	if text := strings.TrimSpace(result.OutputText); text != "" {
		return text
	}
	if raw := strings.TrimSpace(string(result.OutputJSON)); raw != "" {
		return raw
	}
	return resolveCapabilityResultSummary(capabilityName, result)
}

func collectCapabilityReplyTurnPlan(stream iter.Seq[*ark_dal.ModelStreamRespReasoning]) CapabilityReplyPlan {
	var (
		thoughtBuilder strings.Builder
		rawContent     strings.Builder
		plan           CapabilityReplyPlan
	)
	for item := range stream {
		if item == nil {
			continue
		}
		if item.ReasoningContent != "" {
			thoughtBuilder.WriteString(item.ReasoningContent)
			if strings.TrimSpace(plan.ThoughtText) == "" {
				plan.ThoughtText = strings.TrimSpace(thoughtBuilder.String())
			}
		}
		if thought := strings.TrimSpace(item.ContentStruct.Thought); thought != "" {
			plan.ThoughtText = thought
		}
		if item.Content != "" {
			rawContent.WriteString(item.Content)
		}
		if reply := strings.TrimSpace(item.ContentStruct.Reply); reply != "" {
			plan.ReplyText = reply
		}
	}

	parsed := parseCapabilityReplyContent(strings.TrimSpace(rawContent.String()))
	if strings.TrimSpace(plan.ThoughtText) == "" {
		plan.ThoughtText = strings.TrimSpace(parsed.Thought)
	}
	if strings.TrimSpace(plan.ReplyText) == "" {
		plan.ReplyText = strings.TrimSpace(parsed.Reply)
	}
	return plan
}

func replyTurnTextBlock(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "<empty>"
	}
	return trimmed
}

func appendQueuedPendingCapability(root **QueuedCapabilityCall, next *QueuedCapabilityCall) {
	if root == nil || next == nil {
		return
	}
	if *root == nil {
		copied := cloneQueuedCapabilityCall(*next)
		*root = &copied
		return
	}
	(*root).Input.QueueTail = mergeQueuedCapabilityQueue((*root).Input.QueueTail, []QueuedCapabilityCall{*next})
}

func parseCapabilityReplyContent(raw string) ark_dal.ContentStruct {
	return message.ParseContentStruct(raw)
}

func buildQueuedCapabilityCallFromTrace(baseRequest CapabilityRequest, trace ark_dal.CapabilityCallTrace) *QueuedCapabilityCall {
	if !trace.Pending || strings.TrimSpace(trace.FunctionName) == "" {
		return nil
	}

	request := baseRequest
	request.PayloadJSON = []byte(strings.TrimSpace(trace.Arguments))
	call := &QueuedCapabilityCall{
		CallID:         strings.TrimSpace(trace.CallID),
		CapabilityName: strings.TrimSpace(trace.FunctionName),
		Input: CapabilityCallInput{
			Request: request,
		},
	}
	if previousResponseID := strings.TrimSpace(trace.PreviousResponseID); previousResponseID != "" {
		call.Input.Continuation = &CapabilityContinuationInput{
			PreviousResponseID: previousResponseID,
		}
	}
	if strings.TrimSpace(trace.ApprovalType) != "" ||
		strings.TrimSpace(trace.ApprovalTitle) != "" ||
		strings.TrimSpace(trace.ApprovalSummary) != "" ||
		!trace.ApprovalExpiresAt.IsZero() ||
		strings.TrimSpace(trace.ApprovalStepID) != "" ||
		strings.TrimSpace(trace.ApprovalToken) != "" {
		call.Input.Approval = &CapabilityApprovalSpec{
			Type:              strings.TrimSpace(trace.ApprovalType),
			Title:             strings.TrimSpace(trace.ApprovalTitle),
			Summary:           strings.TrimSpace(trace.ApprovalSummary),
			ExpiresAt:         trace.ApprovalExpiresAt.UTC(),
			ReservationStepID: strings.TrimSpace(trace.ApprovalStepID),
			ReservationToken:  strings.TrimSpace(trace.ApprovalToken),
		}
	}
	return call
}

// ReplyTurnLoopRequest carries agent runtime state.
type ReplyTurnLoopRequest struct {
	TurnRequest  InitialChatTurnRequest
	ToolTurns    int
	TurnExecutor func(context.Context, InitialChatTurnRequest) (InitialChatTurnResult, error)
	BaseRequest  CapabilityRequest
	Registry     *CapabilityRegistry
	FallbackPlan CapabilityReplyPlan
	Recorder     InitialTraceRecorder
}

// ReplyTurnLoopResult carries agent runtime state.
type ReplyTurnLoopResult struct {
	Executed          bool
	Plan              CapabilityReplyPlan
	CapabilityCalls   []CompletedCapabilityCall
	PendingCapability *QueuedCapabilityCall
}

// ExecuteReplyTurnLoop executes the reply-turn loop until the model reaches a final reply or yields a queued capability call.
func ExecuteReplyTurnLoop(ctx context.Context, req ReplyTurnLoopRequest) (ReplyTurnLoopResult, error) {
	result := ReplyTurnLoopResult{}
	turnReq := req.TurnRequest
	turnExecutor := req.TurnExecutor
	if turnExecutor == nil {
		turnExecutor = defaultInitialChatTurnExecutor
	}
	for turn := 0; turn < req.ToolTurns; turn++ {
		turnResult, err := turnExecutor(ctx, turnReq)
		if err != nil {
			return ReplyTurnLoopResult{}, nil
		}

		result.Plan = collectCapabilityReplyTurnPlan(turnResult.Stream)
		snapshot := turnResult.Snapshot()
		if snapshot.ToolCall == nil {
			result.Plan = normalizeCapabilityReplyPlan(result.Plan, req.FallbackPlan.ReplyText)
			if strings.TrimSpace(result.Plan.ThoughtText) == "" {
				result.Plan.ThoughtText = strings.TrimSpace(req.FallbackPlan.ThoughtText)
			}
			result.Executed = strings.TrimSpace(result.Plan.ThoughtText) != "" ||
				strings.TrimSpace(result.Plan.ReplyText) != "" ||
				result.PendingCapability != nil
			return result, nil
		}
		if req.Recorder != nil {
			if err := req.Recorder.RecordReplyTurnPlan(ctx, result.Plan, snapshot.ToolCall); err != nil {
				return ReplyTurnLoopResult{}, err
			}
		}

		execution := executeChatToolCall(ctx, req.Registry, req.BaseRequest, *snapshot.ToolCall)
		if execution.Trace != nil {
			traceCopy := *execution.Trace
			traceCopy.PreviousResponseID = strings.TrimSpace(snapshot.ResponseID)
			if traceCopy.Pending {
				appendQueuedPendingCapability(&result.PendingCapability, buildQueuedCapabilityCallFromTrace(req.BaseRequest, traceCopy))
			} else {
				call := CompletedCapabilityCall{
					CallID:             traceCopy.CallID,
					CapabilityName:     traceCopy.FunctionName,
					Arguments:          traceCopy.Arguments,
					Output:             traceCopy.Output,
					PreviousResponseID: traceCopy.PreviousResponseID,
				}
				if req.Recorder != nil {
					if err := req.Recorder.RecordCompletedCapabilityCall(ctx, call); err != nil {
						return ReplyTurnLoopResult{}, err
					}
				} else {
					result.CapabilityCalls = append(result.CapabilityCalls, call)
				}
			}
		}
		if execution.NextOutput == nil || strings.TrimSpace(snapshot.ResponseID) == "" {
			return ReplyTurnLoopResult{}, nil
		}

		turnReq = InitialChatTurnRequest{
			Plan:               turnReq.Plan,
			PreviousResponseID: strings.TrimSpace(snapshot.ResponseID),
			ToolOutput:         execution.NextOutput,
		}
	}

	return ReplyTurnLoopResult{}, nil
}

// InitialChatLoopRequest carries agent runtime state.
type InitialChatLoopRequest struct {
	Plan         InitialChatExecutionPlan
	ToolTurns    int
	TurnExecutor func(context.Context, InitialChatTurnRequest) (InitialChatTurnResult, error)
	Event        *larkim.P2MessageReceiveV1
	Registry     *CapabilityRegistry
	Finalizer    func(context.Context, InitialChatExecutionPlan, iter.Seq[*ark_dal.ModelStreamRespReasoning]) iter.Seq[*ark_dal.ModelStreamRespReasoning]
}

// BuildInitialChatLoopRequest implements agent runtime behavior.
func BuildInitialChatLoopRequest(
	ctx context.Context,
	event *larkim.P2MessageReceiveV1,
	plan ChatGenerationPlan,
	tools *CapabilityRegistryTools,
) (InitialChatLoopRequest, error) {
	if tools == nil {
		return InitialChatLoopRequest{}, nil
	}
	builder := tools.Builder
	if builder == nil {
		builder = defaultInitialChatPlanBuilder
	}
	initialPlan, err := builder(ctx, InitialChatGenerationRequest{
		Event:           event,
		ModelID:         plan.ModelID,
		ReasoningEffort: plan.ReasoningEffort,
		Size:            plan.Size,
		Files:           append([]string(nil), plan.Files...),
		Input:           append([]string(nil), plan.Args...),
		Tools:           tools.Tools,
	})
	if err != nil {
		return InitialChatLoopRequest{}, err
	}
	toolTurns := tools.DefaultToolTurns
	if initialPlan.MaxToolTurns > 0 {
		toolTurns = initialPlan.MaxToolTurns
	}
	if toolTurns <= 0 {
		toolTurns = chatflow.DefaultInitialChatToolTurns
	}
	return InitialChatLoopRequest{
		Plan:         initialPlan,
		ToolTurns:    toolTurns,
		TurnExecutor: tools.TurnExecutor,
		Event:        event,
		Registry:     buildInitialChatCapabilityRegistry(event, tools.Tools),
		Finalizer:    tools.Finalizer,
	}, nil
}

// CapabilityRegistryTools carries agent runtime state.
type CapabilityRegistryTools struct {
	Tools            *arktools.Impl[larkim.P2MessageReceiveV1]
	Builder          func(context.Context, InitialChatGenerationRequest) (InitialChatExecutionPlan, error)
	TurnExecutor     func(context.Context, InitialChatTurnRequest) (InitialChatTurnResult, error)
	DefaultToolTurns int
	Finalizer        func(context.Context, InitialChatExecutionPlan, iter.Seq[*ark_dal.ModelStreamRespReasoning]) iter.Seq[*ark_dal.ModelStreamRespReasoning]
}

// RuntimeInitialChatLoopOptions carries agent runtime state.
type RuntimeInitialChatLoopOptions struct {
	Plan                ChatGenerationPlan
	Tools               *arktools.Impl[larkim.P2MessageReceiveV1]
	Builder             func(context.Context, InitialChatGenerationRequest) (InitialChatExecutionPlan, error)
	TurnExecutor        func(context.Context, InitialChatTurnRequest) (InitialChatTurnResult, error)
	DefaultToolTurns    int
	Finalizer           func(context.Context, InitialChatExecutionPlan, iter.Seq[*ark_dal.ModelStreamRespReasoning]) iter.Seq[*ark_dal.ModelStreamRespReasoning]
	EnsureDeferredTools bool
}

// BuildRuntimeInitialChatLoop builds and executes the initial chat loop with the supplied planning, tool, and finalization dependencies.
func BuildRuntimeInitialChatLoop(
	ctx context.Context,
	event *larkim.P2MessageReceiveV1,
	opts RuntimeInitialChatLoopOptions,
) (iter.Seq[*ark_dal.ModelStreamRespReasoning], error) {
	if opts.EnsureDeferredTools && opts.Plan.EnableDeferredToolCollector && runtimecontext.CollectorFromContext(ctx) == nil {
		ctx = runtimecontext.WithDeferredToolCallCollector(ctx, runtimecontext.NewDeferredToolCallCollector())
	}

	tools := opts.Tools
	if tools == nil {
		tools = decorateRuntimeChatTools(defaultChatToolProvider())
	}
	if tools == nil {
		return nil, errors.New("default chat tool provider is not configured")
	}

	loopReq, err := BuildInitialChatLoopRequest(ctx, event, opts.Plan, &CapabilityRegistryTools{
		Tools:            tools,
		Builder:          opts.Builder,
		TurnExecutor:     opts.TurnExecutor,
		DefaultToolTurns: opts.DefaultToolTurns,
		Finalizer:        opts.Finalizer,
	})
	if err != nil {
		return nil, err
	}
	return StreamInitialChatLoop(ctx, loopReq), nil
}

// StreamInitialChatLoop implements agent runtime behavior.
func StreamInitialChatLoop(ctx context.Context, req InitialChatLoopRequest) iter.Seq[*ark_dal.ModelStreamRespReasoning] {
	return func(yield func(*ark_dal.ModelStreamRespReasoning) bool) {
		turnExecutor := req.TurnExecutor
		if turnExecutor == nil {
			turnExecutor = defaultInitialChatTurnExecutor
		}
		turnReq := InitialChatTurnRequest{Plan: req.Plan}
		for turn := 0; turn < req.ToolTurns; turn++ {
			turnResult, turnErr := turnExecutor(ctx, turnReq)
			if turnErr != nil {
				return
			}

			stream := turnResult.Stream
			if req.Finalizer != nil {
				stream = req.Finalizer(ctx, req.Plan, stream)
			}
			for item := range stream {
				if !yield(item) {
					return
				}
			}

			snapshot := turnResult.Snapshot()
			if snapshot.ToolCall == nil {
				return
			}

			execution := executeInitialChatToolCall(ctx, req.Registry, req.Event, *snapshot.ToolCall)
			if execution.Trace != nil {
				traceCopy := *execution.Trace
				traceCopy.PreviousResponseID = strings.TrimSpace(snapshot.ResponseID)
				if !yield(&ark_dal.ModelStreamRespReasoning{CapabilityCall: &traceCopy}) {
					return
				}
			}
			if execution.NextOutput == nil || strings.TrimSpace(snapshot.ResponseID) == "" {
				return
			}

			turnReq = InitialChatTurnRequest{
				Plan:               req.Plan,
				PreviousResponseID: strings.TrimSpace(snapshot.ResponseID),
				ToolOutput:         execution.NextOutput,
			}
		}
	}
}
