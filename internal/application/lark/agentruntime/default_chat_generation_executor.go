package agentruntime

import (
	"context"
	"errors"
	"iter"
	"strings"
	"time"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/runtimecontext"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/toolmeta"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	arktools "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

type defaultChatGenerationPlanExecutor struct{}

var (
	defaultChatToolProvider               = func() *arktools.Impl[larkim.P2MessageReceiveV1] { return nil }
	defaultInitialChatPlanBuilder         = BuildInitialChatExecutionPlan
	defaultAgenticInitialChatPlanBuilder  = BuildAgenticChatExecutionPlan
	defaultInitialChatTurnExecutor        = ExecuteInitialChatTurn
	defaultInitialChatStreamFinalizer     = FinalizeInitialChatStream
	defaultStandardChatGenerationExecutor = generateStandardChatPlan
	defaultAgenticChatGenerationExecutor  = generateAgenticChatPlan
)

const (
	defaultInitialChatToolTurns        = 8
	defaultAgenticInitialChatToolTurns = 8
)

func SetDefaultChatToolProvider(provider func() *arktools.Impl[larkim.P2MessageReceiveV1]) {
	if provider == nil {
		defaultChatToolProvider = func() *arktools.Impl[larkim.P2MessageReceiveV1] { return nil }
		return
	}
	defaultChatToolProvider = provider
}

func NewDefaultChatGenerationPlanExecutor() ChatGenerationPlanExecutor {
	return defaultChatGenerationPlanExecutor{}
}

func (defaultChatGenerationPlanExecutor) Generate(ctx context.Context, event *larkim.P2MessageReceiveV1, plan ChatGenerationPlan) (iter.Seq[*ark_dal.ModelStreamRespReasoning], error) {
	if plan.EnableDeferredToolCollector && runtimecontext.CollectorFromContext(ctx) == nil {
		ctx = runtimecontext.WithDeferredToolCallCollector(ctx, runtimecontext.NewDeferredToolCallCollector())
	}
	tools := decorateRuntimeChatTools(defaultChatToolProvider())
	if tools == nil {
		return nil, errors.New("default chat tool provider is not configured")
	}

	if plan.Mode.Normalize() == appconfig.ChatModeAgentic {
		return defaultAgenticChatGenerationExecutor(ctx, event, plan, tools)
	}
	return defaultStandardChatGenerationExecutor(ctx, event, plan, tools)
}

func generateStandardChatPlan(
	ctx context.Context,
	event *larkim.P2MessageReceiveV1,
	plan ChatGenerationPlan,
	tools *arktools.Impl[larkim.P2MessageReceiveV1],
) (iter.Seq[*ark_dal.ModelStreamRespReasoning], error) {
	return generateChatPlanWithVariant(ctx, event, plan, tools, chatGenerationVariant{
		planBuilder: defaultInitialChatPlanBuilder,
		finalizer:   defaultInitialChatStreamFinalizer,
		toolTurns:   defaultInitialChatToolTurns,
	})
}

func generateAgenticChatPlan(
	ctx context.Context,
	event *larkim.P2MessageReceiveV1,
	plan ChatGenerationPlan,
	tools *arktools.Impl[larkim.P2MessageReceiveV1],
) (iter.Seq[*ark_dal.ModelStreamRespReasoning], error) {
	return generateChatPlanWithVariant(ctx, event, plan, tools, chatGenerationVariant{
		planBuilder: defaultAgenticInitialChatPlanBuilder,
		finalizer:   defaultInitialChatStreamFinalizer,
		toolTurns:   defaultAgenticInitialChatToolTurns,
	})
}

type chatGenerationVariant struct {
	planBuilder func(context.Context, InitialChatGenerationRequest) (InitialChatExecutionPlan, error)
	finalizer   func(context.Context, InitialChatExecutionPlan, iter.Seq[*ark_dal.ModelStreamRespReasoning]) iter.Seq[*ark_dal.ModelStreamRespReasoning]
	toolTurns   int
}

func generateChatPlanWithVariant(
	ctx context.Context,
	event *larkim.P2MessageReceiveV1,
	plan ChatGenerationPlan,
	tools *arktools.Impl[larkim.P2MessageReceiveV1],
	variant chatGenerationVariant,
) (iter.Seq[*ark_dal.ModelStreamRespReasoning], error) {
	builder := variant.planBuilder
	if builder == nil {
		builder = defaultInitialChatPlanBuilder
	}
	toolTurns := variant.toolTurns
	if toolTurns <= 0 {
		toolTurns = defaultInitialChatToolTurns
	}

	initialPlan, err := builder(ctx, InitialChatGenerationRequest{
		Event:           event,
		ModelID:         plan.ModelID,
		ReasoningEffort: plan.ReasoningEffort,
		Size:            plan.Size,
		Files:           append([]string(nil), plan.Files...),
		Input:           append([]string(nil), plan.Args...),
		Tools:           tools,
	})
	if err != nil {
		return nil, err
	}

	registry := buildInitialChatCapabilityRegistry(event, tools)
	return func(yield func(*ark_dal.ModelStreamRespReasoning) bool) {
		turnReq := InitialChatTurnRequest{Plan: initialPlan}
		for turn := 0; turn < toolTurns; turn++ {
			turnResult, turnErr := defaultInitialChatTurnExecutor(ctx, turnReq)
			if turnErr != nil {
				return
			}

			stream := turnResult.Stream
			if variant.finalizer != nil {
				stream = variant.finalizer(ctx, initialPlan, stream)
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

			execution := executeInitialChatToolCall(ctx, registry, event, *snapshot.ToolCall)
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
				Plan:               initialPlan,
				PreviousResponseID: strings.TrimSpace(snapshot.ResponseID),
				ToolOutput:         execution.NextOutput,
			}
		}
	}, nil
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
	behavior, ok := toolmeta.LookupRuntimeBehavior(strings.TrimSpace(name))
	if !ok {
		return desc
	}

	switch {
	case behavior.RequiresApproval():
		return appendRuntimeToolDescription(desc, "该工具属于有副作用动作，调用后会先进入审批等待，不会立刻执行。只有在用户明确要求执行这个动作时才使用。")
	case behavior.SideEffectLevel == toolmeta.SideEffectLevelNone:
		return appendRuntimeToolDescription(desc, "该工具是只读查询工具，不会修改群聊、配置或共享状态。遇到金价、股价、历史检索等需要事实的问题时应优先使用。")
	default:
		return desc
	}
}

func appendRuntimeToolDescription(base, note string) string {
	base = strings.TrimSpace(base)
	note = strings.TrimSpace(note)
	if note == "" || strings.Contains(base, note) {
		return base
	}
	if base == "" {
		return note
	}
	if strings.HasSuffix(base, "。") || strings.HasSuffix(base, ".") {
		return base + " " + note
	}
	return base + "。 " + note
}

type initialChatToolExecutionResult struct {
	Trace      *ark_dal.CapabilityCallTrace
	NextOutput *InitialChatToolOutput
}

func buildInitialChatCapabilityRegistry(event *larkim.P2MessageReceiveV1, tools *arktools.Impl[larkim.P2MessageReceiveV1]) *CapabilityRegistry {
	registry := NewCapabilityRegistry()
	for _, capability := range BuildToolCapabilities(tools, nil, event) {
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
		ChatID:      strings.TrimSpace(initialReplyChatID(event)),
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
