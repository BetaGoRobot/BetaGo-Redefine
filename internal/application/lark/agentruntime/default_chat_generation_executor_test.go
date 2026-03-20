package agentruntime

import (
	"context"
	"iter"
	"strings"
	"testing"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/runtimecontext"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	arktools "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/bytedance/gg/gresult"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestDefaultChatGenerationPlanExecutorUsesConfiguredToolProvider(t *testing.T) {
	originalProvider := defaultChatToolProvider
	originalStandardExecutor := defaultStandardChatGenerationExecutor
	originalAgenticExecutor := defaultAgenticChatGenerationExecutor
	originalBuilder := defaultInitialChatPlanBuilder
	originalAgenticBuilder := defaultAgenticInitialChatPlanBuilder
	originalTurnExecutor := defaultInitialChatTurnExecutor
	originalFinalizer := defaultInitialChatStreamFinalizer
	defer func() {
		defaultChatToolProvider = originalProvider
		defaultStandardChatGenerationExecutor = originalStandardExecutor
		defaultAgenticChatGenerationExecutor = originalAgenticExecutor
		defaultInitialChatPlanBuilder = originalBuilder
		defaultAgenticInitialChatPlanBuilder = originalAgenticBuilder
		defaultInitialChatTurnExecutor = originalTurnExecutor
		defaultInitialChatStreamFinalizer = originalFinalizer
	}()

	toolset := arktools.New[larkim.P2MessageReceiveV1]()
	defaultChatToolProvider = func() *arktools.Impl[larkim.P2MessageReceiveV1] {
		return toolset
	}

	var (
		capturedReq          InitialChatGenerationRequest
		capturedTurnReq      InitialChatTurnRequest
		collectorInitialized bool
		finalizerCalled      bool
	)
	defaultInitialChatPlanBuilder = func(ctx context.Context, req InitialChatGenerationRequest) (InitialChatExecutionPlan, error) {
		capturedReq = req
		collectorInitialized = runtimecontext.CollectorFromContext(ctx) != nil
		return InitialChatExecutionPlan{
			Event:     req.Event,
			ModelID:   req.ModelID,
			ChatID:    "oc_chat",
			OpenID:    "ou_actor",
			Prompt:    "system prompt",
			UserInput: "formatted user input",
			Files:     append([]string(nil), req.Files...),
			Tools:     req.Tools,
		}, nil
	}
	defaultInitialChatTurnExecutor = func(ctx context.Context, req InitialChatTurnRequest) (InitialChatTurnResult, error) {
		capturedTurnReq = req
		return InitialChatTurnResult{
			Stream: defaultExecutorSeqFromItems(&ark_dal.ModelStreamRespReasoning{ContentStruct: ark_dal.ContentStruct{Reply: "ok"}}),
			Snapshot: func() InitialChatTurnSnapshot {
				return InitialChatTurnSnapshot{}
			},
		}, nil
	}
	defaultInitialChatStreamFinalizer = func(ctx context.Context, plan InitialChatExecutionPlan, stream iter.Seq[*ark_dal.ModelStreamRespReasoning]) iter.Seq[*ark_dal.ModelStreamRespReasoning] {
		finalizerCalled = true
		return stream
	}

	executor := NewDefaultChatGenerationPlanExecutor()
	event := testChatResponseEvent()
	stream, err := executor.Generate(context.Background(), event, ChatGenerationPlan{
		ModelID:                     "ep-test",
		Size:                        12,
		Files:                       []string{"https://example.com/a.png"},
		Args:                        []string{"帮我总结"},
		EnableDeferredToolCollector: true,
	})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	count := 0
	for range stream {
		count++
	}
	if count != 1 {
		t.Fatalf("stream count = %d, want 1", count)
	}
	if capturedReq.Event != event {
		t.Fatal("expected event to be forwarded")
	}
	if capturedReq.ModelID != "ep-test" {
		t.Fatalf("model id = %q, want %q", capturedReq.ModelID, "ep-test")
	}
	if capturedReq.Size != 12 {
		t.Fatalf("size = %d, want %d", capturedReq.Size, 12)
	}
	if len(capturedReq.Files) != 1 || capturedReq.Files[0] != "https://example.com/a.png" {
		t.Fatalf("files = %+v, want one forwarded file", capturedReq.Files)
	}
	if len(capturedReq.Input) != 1 || capturedReq.Input[0] != "帮我总结" {
		t.Fatalf("input = %+v, want forwarded args", capturedReq.Input)
	}
	if capturedReq.Tools == nil {
		t.Fatal("expected decorated toolset to be forwarded")
	}
	if capturedReq.Tools == toolset {
		t.Fatal("expected runtime to decorate toolset before forwarding")
	}
	if capturedTurnReq.Plan.ChatID != "oc_chat" {
		t.Fatalf("turn plan chat id = %q, want %q", capturedTurnReq.Plan.ChatID, "oc_chat")
	}
	if capturedTurnReq.Plan.OpenID != "ou_actor" {
		t.Fatalf("turn plan open id = %q, want %q", capturedTurnReq.Plan.OpenID, "ou_actor")
	}
	if capturedTurnReq.Plan.ModelID != "ep-test" {
		t.Fatalf("turn plan model id = %q, want %q", capturedTurnReq.Plan.ModelID, "ep-test")
	}
	if capturedTurnReq.Plan.Prompt != "system prompt" {
		t.Fatalf("turn plan prompt = %q, want %q", capturedTurnReq.Plan.Prompt, "system prompt")
	}
	if capturedTurnReq.Plan.UserInput != "formatted user input" {
		t.Fatalf("turn plan user input = %q, want %q", capturedTurnReq.Plan.UserInput, "formatted user input")
	}
	if capturedTurnReq.Plan.Tools == nil {
		t.Fatal("expected builder output toolset to reach turn executor")
	}
	if !collectorInitialized {
		t.Fatal("expected deferred tool collector to be initialized")
	}
	if !finalizerCalled {
		t.Fatal("expected finalizer to be invoked")
	}
}

func TestDefaultChatGenerationPlanExecutorUsesAgenticPlanBuilderForAgenticMode(t *testing.T) {
	originalProvider := defaultChatToolProvider
	originalStandardExecutor := defaultStandardChatGenerationExecutor
	originalAgenticExecutor := defaultAgenticChatGenerationExecutor
	originalBuilder := defaultInitialChatPlanBuilder
	originalAgenticBuilder := defaultAgenticInitialChatPlanBuilder
	originalTurnExecutor := defaultInitialChatTurnExecutor
	originalFinalizer := defaultInitialChatStreamFinalizer
	defer func() {
		defaultChatToolProvider = originalProvider
		defaultStandardChatGenerationExecutor = originalStandardExecutor
		defaultAgenticChatGenerationExecutor = originalAgenticExecutor
		defaultInitialChatPlanBuilder = originalBuilder
		defaultAgenticInitialChatPlanBuilder = originalAgenticBuilder
		defaultInitialChatTurnExecutor = originalTurnExecutor
		defaultInitialChatStreamFinalizer = originalFinalizer
	}()

	toolset := arktools.New[larkim.P2MessageReceiveV1]()
	defaultChatToolProvider = func() *arktools.Impl[larkim.P2MessageReceiveV1] {
		return toolset
	}

	agenticBuilderCalled := false
	defaultInitialChatPlanBuilder = func(ctx context.Context, req InitialChatGenerationRequest) (InitialChatExecutionPlan, error) {
		t.Fatal("standard builder should not be called for agentic mode")
		return InitialChatExecutionPlan{}, nil
	}
	defaultAgenticInitialChatPlanBuilder = func(ctx context.Context, req InitialChatGenerationRequest) (InitialChatExecutionPlan, error) {
		agenticBuilderCalled = true
		return InitialChatExecutionPlan{
			Event:     req.Event,
			ModelID:   req.ModelID,
			ChatID:    "oc_chat",
			OpenID:    "ou_actor",
			Prompt:    "agentic system prompt",
			UserInput: "agentic user prompt",
			Tools:     req.Tools,
		}, nil
	}
	defaultInitialChatTurnExecutor = func(ctx context.Context, req InitialChatTurnRequest) (InitialChatTurnResult, error) {
		if req.Plan.Prompt != "agentic system prompt" {
			t.Fatalf("prompt = %q, want %q", req.Plan.Prompt, "agentic system prompt")
		}
		return InitialChatTurnResult{
			Stream: defaultExecutorSeqFromItems(&ark_dal.ModelStreamRespReasoning{ContentStruct: ark_dal.ContentStruct{Reply: "ok"}}),
			Snapshot: func() InitialChatTurnSnapshot {
				return InitialChatTurnSnapshot{}
			},
		}, nil
	}
	defaultInitialChatStreamFinalizer = func(ctx context.Context, plan InitialChatExecutionPlan, stream iter.Seq[*ark_dal.ModelStreamRespReasoning]) iter.Seq[*ark_dal.ModelStreamRespReasoning] {
		return stream
	}

	stream, err := NewDefaultChatGenerationPlanExecutor().Generate(context.Background(), testChatResponseEvent(), ChatGenerationPlan{
		ModelID: "ep-test-agentic",
		Mode:    appconfig.ChatModeAgentic,
	})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	for range stream {
	}
	if !agenticBuilderCalled {
		t.Fatal("expected agentic plan builder to be called")
	}
}

func TestDefaultChatGenerationPlanExecutorDispatchesToAgenticExecutor(t *testing.T) {
	originalProvider := defaultChatToolProvider
	originalStandardExecutor := defaultStandardChatGenerationExecutor
	originalAgenticExecutor := defaultAgenticChatGenerationExecutor
	defer func() {
		defaultChatToolProvider = originalProvider
		defaultStandardChatGenerationExecutor = originalStandardExecutor
		defaultAgenticChatGenerationExecutor = originalAgenticExecutor
	}()

	toolset := arktools.New[larkim.P2MessageReceiveV1]()
	defaultChatToolProvider = func() *arktools.Impl[larkim.P2MessageReceiveV1] {
		return toolset
	}

	agenticCalled := false
	defaultStandardChatGenerationExecutor = func(context.Context, *larkim.P2MessageReceiveV1, ChatGenerationPlan, *arktools.Impl[larkim.P2MessageReceiveV1]) (iter.Seq[*ark_dal.ModelStreamRespReasoning], error) {
		t.Fatal("standard executor should not be called for agentic mode")
		return nil, nil
	}
	defaultAgenticChatGenerationExecutor = func(ctx context.Context, event *larkim.P2MessageReceiveV1, plan ChatGenerationPlan, tools *arktools.Impl[larkim.P2MessageReceiveV1]) (iter.Seq[*ark_dal.ModelStreamRespReasoning], error) {
		agenticCalled = true
		if plan.Mode != appconfig.ChatModeAgentic {
			t.Fatalf("plan mode = %q, want %q", plan.Mode, appconfig.ChatModeAgentic)
		}
		if tools == nil {
			t.Fatal("expected configured toolset")
		}
		if tools == toolset {
			t.Fatal("expected runtime to decorate toolset")
		}
		return defaultExecutorSeqFromItems(&ark_dal.ModelStreamRespReasoning{ContentStruct: ark_dal.ContentStruct{Reply: "ok"}}), nil
	}

	stream, err := NewDefaultChatGenerationPlanExecutor().Generate(context.Background(), testChatResponseEvent(), ChatGenerationPlan{
		ModelID: "ep-test-agentic",
		Mode:    appconfig.ChatModeAgentic,
	})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	for range stream {
	}
	if !agenticCalled {
		t.Fatal("expected agentic executor to be called")
	}
}

func TestDefaultChatGenerationPlanExecutorDispatchesToStandardExecutor(t *testing.T) {
	originalProvider := defaultChatToolProvider
	originalStandardExecutor := defaultStandardChatGenerationExecutor
	originalAgenticExecutor := defaultAgenticChatGenerationExecutor
	defer func() {
		defaultChatToolProvider = originalProvider
		defaultStandardChatGenerationExecutor = originalStandardExecutor
		defaultAgenticChatGenerationExecutor = originalAgenticExecutor
	}()

	toolset := arktools.New[larkim.P2MessageReceiveV1]()
	defaultChatToolProvider = func() *arktools.Impl[larkim.P2MessageReceiveV1] {
		return toolset
	}

	standardCalled := false
	defaultAgenticChatGenerationExecutor = func(context.Context, *larkim.P2MessageReceiveV1, ChatGenerationPlan, *arktools.Impl[larkim.P2MessageReceiveV1]) (iter.Seq[*ark_dal.ModelStreamRespReasoning], error) {
		t.Fatal("agentic executor should not be called for standard mode")
		return nil, nil
	}
	defaultStandardChatGenerationExecutor = func(ctx context.Context, event *larkim.P2MessageReceiveV1, plan ChatGenerationPlan, tools *arktools.Impl[larkim.P2MessageReceiveV1]) (iter.Seq[*ark_dal.ModelStreamRespReasoning], error) {
		standardCalled = true
		if plan.Mode != appconfig.ChatModeStandard {
			t.Fatalf("plan mode = %q, want %q", plan.Mode, appconfig.ChatModeStandard)
		}
		if tools == nil {
			t.Fatal("expected configured toolset")
		}
		if tools == toolset {
			t.Fatal("expected runtime to decorate toolset")
		}
		return defaultExecutorSeqFromItems(&ark_dal.ModelStreamRespReasoning{ContentStruct: ark_dal.ContentStruct{Reply: "ok"}}), nil
	}

	stream, err := NewDefaultChatGenerationPlanExecutor().Generate(context.Background(), testChatResponseEvent(), ChatGenerationPlan{
		ModelID: "ep-test-standard",
		Mode:    appconfig.ChatModeStandard,
	})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	for range stream {
	}
	if !standardCalled {
		t.Fatal("expected standard executor to be called")
	}
}

func TestDefaultChatGenerationPlanExecutorReturnsErrorWithoutToolProvider(t *testing.T) {
	originalProvider := defaultChatToolProvider
	defer func() {
		defaultChatToolProvider = originalProvider
	}()
	defaultChatToolProvider = func() *arktools.Impl[larkim.P2MessageReceiveV1] { return nil }

	_, err := NewDefaultChatGenerationPlanExecutor().Generate(context.Background(), nil, ChatGenerationPlan{})
	if err == nil {
		t.Fatal("expected missing tool provider error")
	}
}

func TestDecorateRuntimeChatToolsAnnotatesApprovalAndReadSemantics(t *testing.T) {
	toolset := arktools.New[larkim.P2MessageReceiveV1]().Add(
		arktools.NewUnit[larkim.P2MessageReceiveV1]().
			Name("send_message").
			Desc("发送一条消息到当前对话").
			Params(arktools.NewParams("object")),
	).Add(
		arktools.NewUnit[larkim.P2MessageReceiveV1]().
			Name("gold_price_get").
			Desc("查询金价变化").
			Params(arktools.NewParams("object")),
	)

	decorated := decorateRuntimeChatTools(toolset)
	if decorated == nil {
		t.Fatal("expected decorated tools")
	}
	sendMessage, ok := decorated.Get("send_message")
	if !ok || sendMessage == nil {
		t.Fatal("expected send_message tool")
	}
	if !strings.Contains(sendMessage.Description, "先进入审批等待") {
		t.Fatalf("send_message description = %q, want contain approval guidance", sendMessage.Description)
	}
	if !strings.Contains(sendMessage.Description, "明确要求执行") {
		t.Fatalf("send_message description = %q, want contain explicit-action guidance", sendMessage.Description)
	}

	goldPrice, ok := decorated.Get("gold_price_get")
	if !ok || goldPrice == nil {
		t.Fatal("expected gold_price_get tool")
	}
	if !strings.Contains(goldPrice.Description, "只读查询") {
		t.Fatalf("gold_price_get description = %q, want contain read-only guidance", goldPrice.Description)
	}
	if !strings.Contains(goldPrice.Description, "优先使用") {
		t.Fatalf("gold_price_get description = %q, want contain prefer-tool guidance", goldPrice.Description)
	}
}

func TestDefaultChatGenerationPlanExecutorOwnsToolLoop(t *testing.T) {
	originalProvider := defaultChatToolProvider
	originalBuilder := defaultInitialChatPlanBuilder
	originalTurnExecutor := defaultInitialChatTurnExecutor
	originalFinalizer := defaultInitialChatStreamFinalizer
	defer func() {
		defaultChatToolProvider = originalProvider
		defaultInitialChatPlanBuilder = originalBuilder
		defaultInitialChatTurnExecutor = originalTurnExecutor
		defaultInitialChatStreamFinalizer = originalFinalizer
	}()

	toolCalls := 0
	toolset := arktools.New[larkim.P2MessageReceiveV1]().Add(
		arktools.NewUnit[larkim.P2MessageReceiveV1]().
			Name("search_history").
			Desc("搜索历史").
			Params(arktools.NewParams("object")).
			Func(func(ctx context.Context, args string, input arktools.FCMeta[larkim.P2MessageReceiveV1]) gresult.R[string] {
				toolCalls++
				if args != `{"q":"agentic"}` {
					t.Fatalf("tool args = %q, want %q", args, `{"q":"agentic"}`)
				}
				return gresult.OK("搜索结果")
			}),
	)
	defaultChatToolProvider = func() *arktools.Impl[larkim.P2MessageReceiveV1] {
		return toolset
	}
	defaultInitialChatPlanBuilder = func(ctx context.Context, req InitialChatGenerationRequest) (InitialChatExecutionPlan, error) {
		return InitialChatExecutionPlan{
			Event:     req.Event,
			ModelID:   req.ModelID,
			ChatID:    "oc_chat",
			OpenID:    "ou_actor",
			Prompt:    "system prompt",
			UserInput: "帮我查 agentic",
			Tools:     req.Tools,
		}, nil
	}

	turnCalls := 0
	defaultInitialChatTurnExecutor = func(ctx context.Context, req InitialChatTurnRequest) (InitialChatTurnResult, error) {
		turnCalls++
		switch turnCalls {
		case 1:
			if req.PreviousResponseID != "" || req.ToolOutput != nil {
				t.Fatalf("unexpected first turn request: %+v", req)
			}
			return InitialChatTurnResult{
				Stream: defaultExecutorSeqFromItems(&ark_dal.ModelStreamRespReasoning{ReasoningContent: "先查资料"}),
				Snapshot: func() InitialChatTurnSnapshot {
					return InitialChatTurnSnapshot{
						ResponseID: "resp_1",
						ToolCall: &InitialChatToolCall{
							CallID:       "call_1",
							FunctionName: "search_history",
							Arguments:    `{"q":"agentic"}`,
						},
					}
				},
			}, nil
		case 2:
			if req.PreviousResponseID != "resp_1" {
				t.Fatalf("previous response id = %q, want %q", req.PreviousResponseID, "resp_1")
			}
			if req.ToolOutput == nil || req.ToolOutput.CallID != "call_1" || req.ToolOutput.Output != "搜索结果" {
				t.Fatalf("unexpected tool output: %+v", req.ToolOutput)
			}
			return InitialChatTurnResult{
				Stream: defaultExecutorSeqFromItems(&ark_dal.ModelStreamRespReasoning{ContentStruct: ark_dal.ContentStruct{Reply: "查到了"}}),
				Snapshot: func() InitialChatTurnSnapshot {
					return InitialChatTurnSnapshot{ResponseID: "resp_2"}
				},
			}, nil
		default:
			t.Fatalf("unexpected turn call count = %d", turnCalls)
			return InitialChatTurnResult{}, nil
		}
	}
	defaultInitialChatStreamFinalizer = func(ctx context.Context, plan InitialChatExecutionPlan, stream iter.Seq[*ark_dal.ModelStreamRespReasoning]) iter.Seq[*ark_dal.ModelStreamRespReasoning] {
		return stream
	}

	stream, err := NewDefaultChatGenerationPlanExecutor().Generate(context.Background(), testChatResponseEvent(), ChatGenerationPlan{
		ModelID: "ep-test",
	})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	var (
		capabilityCalls int
		replyText       string
	)
	for item := range stream {
		if item.CapabilityCall != nil {
			capabilityCalls++
			if item.CapabilityCall.FunctionName != "search_history" {
				t.Fatalf("capability name = %q, want %q", item.CapabilityCall.FunctionName, "search_history")
			}
			if item.CapabilityCall.Output != "搜索结果" {
				t.Fatalf("capability output = %q, want %q", item.CapabilityCall.Output, "搜索结果")
			}
		}
		if item.ContentStruct.Reply != "" {
			replyText = item.ContentStruct.Reply
		}
	}

	if turnCalls != 2 {
		t.Fatalf("turn call count = %d, want 2", turnCalls)
	}
	if toolCalls != 1 {
		t.Fatalf("tool call count = %d, want 1", toolCalls)
	}
	if capabilityCalls != 1 {
		t.Fatalf("capability trace count = %d, want 1", capabilityCalls)
	}
	if replyText != "查到了" {
		t.Fatalf("reply text = %q, want %q", replyText, "查到了")
	}
}

func TestDefaultChatGenerationPlanExecutorTurnsApprovalIntoPendingCapability(t *testing.T) {
	originalProvider := defaultChatToolProvider
	originalBuilder := defaultInitialChatPlanBuilder
	originalTurnExecutor := defaultInitialChatTurnExecutor
	originalFinalizer := defaultInitialChatStreamFinalizer
	defer func() {
		defaultChatToolProvider = originalProvider
		defaultInitialChatPlanBuilder = originalBuilder
		defaultInitialChatTurnExecutor = originalTurnExecutor
		defaultInitialChatStreamFinalizer = originalFinalizer
	}()

	toolCalls := 0
	toolset := arktools.New[larkim.P2MessageReceiveV1]().Add(
		arktools.NewUnit[larkim.P2MessageReceiveV1]().
			Name("send_message").
			Desc("发送消息").
			Params(arktools.NewParams("object")).
			Func(func(ctx context.Context, args string, input arktools.FCMeta[larkim.P2MessageReceiveV1]) gresult.R[string] {
				toolCalls++
				return gresult.OK("should not execute")
			}),
	)
	defaultChatToolProvider = func() *arktools.Impl[larkim.P2MessageReceiveV1] {
		return toolset
	}
	defaultInitialChatPlanBuilder = func(ctx context.Context, req InitialChatGenerationRequest) (InitialChatExecutionPlan, error) {
		return InitialChatExecutionPlan{
			Event:     req.Event,
			ModelID:   req.ModelID,
			ChatID:    "oc_chat",
			OpenID:    "ou_actor",
			Prompt:    "system prompt",
			UserInput: "发条消息",
			Tools:     req.Tools,
		}, nil
	}

	turnCalls := 0
	defaultInitialChatTurnExecutor = func(ctx context.Context, req InitialChatTurnRequest) (InitialChatTurnResult, error) {
		turnCalls++
		switch turnCalls {
		case 1:
			return InitialChatTurnResult{
				Stream: defaultExecutorSeqFromItems(),
				Snapshot: func() InitialChatTurnSnapshot {
					return InitialChatTurnSnapshot{
						ResponseID: "resp_1",
						ToolCall: &InitialChatToolCall{
							CallID:       "call_pending_1",
							FunctionName: "send_message",
							Arguments:    `{"content":"hello"}`,
						},
					}
				},
			}, nil
		case 2:
			if req.PreviousResponseID != "resp_1" {
				t.Fatalf("previous response id = %q, want %q", req.PreviousResponseID, "resp_1")
			}
			if req.ToolOutput == nil || req.ToolOutput.Output != "已发起审批，等待确认后发送消息。" {
				t.Fatalf("unexpected pending tool output: %+v", req.ToolOutput)
			}
			return InitialChatTurnResult{
				Stream: defaultExecutorSeqFromItems(&ark_dal.ModelStreamRespReasoning{ContentStruct: ark_dal.ContentStruct{Reply: "已发起审批"}}),
				Snapshot: func() InitialChatTurnSnapshot {
					return InitialChatTurnSnapshot{ResponseID: "resp_2"}
				},
			}, nil
		default:
			t.Fatalf("unexpected turn call count = %d", turnCalls)
			return InitialChatTurnResult{}, nil
		}
	}
	defaultInitialChatStreamFinalizer = func(ctx context.Context, plan InitialChatExecutionPlan, stream iter.Seq[*ark_dal.ModelStreamRespReasoning]) iter.Seq[*ark_dal.ModelStreamRespReasoning] {
		return stream
	}

	stream, err := NewDefaultChatGenerationPlanExecutor().Generate(context.Background(), testChatResponseEvent(), ChatGenerationPlan{
		ModelID: "ep-test",
	})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	var pending *ark_dal.CapabilityCallTrace
	var replyText string
	for item := range stream {
		if item.CapabilityCall != nil && item.CapabilityCall.Pending {
			traceCopy := *item.CapabilityCall
			pending = &traceCopy
		}
		if item.ContentStruct.Reply != "" {
			replyText = item.ContentStruct.Reply
		}
	}

	if turnCalls != 2 {
		t.Fatalf("turn call count = %d, want 2", turnCalls)
	}
	if toolCalls != 0 {
		t.Fatalf("tool call count = %d, want 0", toolCalls)
	}
	if pending == nil {
		t.Fatal("expected pending capability trace")
	}
	if pending.FunctionName != "send_message" {
		t.Fatalf("pending capability name = %q, want %q", pending.FunctionName, "send_message")
	}
	if pending.ApprovalTitle != "审批发送消息" {
		t.Fatalf("approval title = %q, want %q", pending.ApprovalTitle, "审批发送消息")
	}
	if replyText != "已发起审批" {
		t.Fatalf("reply text = %q, want %q", replyText, "已发起审批")
	}
}

func TestDefaultChatGenerationPlanExecutorChainsMultiplePendingCapabilities(t *testing.T) {
	originalProvider := defaultChatToolProvider
	originalBuilder := defaultInitialChatPlanBuilder
	originalTurnExecutor := defaultInitialChatTurnExecutor
	originalFinalizer := defaultInitialChatStreamFinalizer
	defer func() {
		defaultChatToolProvider = originalProvider
		defaultInitialChatPlanBuilder = originalBuilder
		defaultInitialChatTurnExecutor = originalTurnExecutor
		defaultInitialChatStreamFinalizer = originalFinalizer
	}()

	toolCalls := 0
	toolset := arktools.New[larkim.P2MessageReceiveV1]().Add(
		arktools.NewUnit[larkim.P2MessageReceiveV1]().
			Name("send_message").
			Desc("发送消息").
			Params(arktools.NewParams("object")).
			Func(func(ctx context.Context, args string, input arktools.FCMeta[larkim.P2MessageReceiveV1]) gresult.R[string] {
				toolCalls++
				return gresult.OK("should not execute")
			}),
	)
	defaultChatToolProvider = func() *arktools.Impl[larkim.P2MessageReceiveV1] {
		return toolset
	}
	defaultInitialChatPlanBuilder = func(ctx context.Context, req InitialChatGenerationRequest) (InitialChatExecutionPlan, error) {
		return InitialChatExecutionPlan{
			Event:     req.Event,
			ModelID:   req.ModelID,
			ChatID:    "oc_chat",
			OpenID:    "ou_actor",
			Prompt:    "system prompt",
			UserInput: "连续发两条消息",
			Tools:     req.Tools,
		}, nil
	}

	turnCalls := 0
	defaultInitialChatTurnExecutor = func(ctx context.Context, req InitialChatTurnRequest) (InitialChatTurnResult, error) {
		turnCalls++
		switch turnCalls {
		case 1:
			return InitialChatTurnResult{
				Stream: defaultExecutorSeqFromItems(),
				Snapshot: func() InitialChatTurnSnapshot {
					return InitialChatTurnSnapshot{
						ResponseID: "resp_1",
						ToolCall: &InitialChatToolCall{
							CallID:       "call_pending_1",
							FunctionName: "send_message",
							Arguments:    `{"content":"hello-1"}`,
						},
					}
				},
			}, nil
		case 2:
			if req.PreviousResponseID != "resp_1" {
				t.Fatalf("previous response id = %q, want %q", req.PreviousResponseID, "resp_1")
			}
			if req.ToolOutput == nil || req.ToolOutput.Output != "已发起审批，等待确认后发送消息。" {
				t.Fatalf("unexpected first pending tool output: %+v", req.ToolOutput)
			}
			return InitialChatTurnResult{
				Stream: defaultExecutorSeqFromItems(),
				Snapshot: func() InitialChatTurnSnapshot {
					return InitialChatTurnSnapshot{
						ResponseID: "resp_2",
						ToolCall: &InitialChatToolCall{
							CallID:       "call_pending_2",
							FunctionName: "send_message",
							Arguments:    `{"content":"hello-2"}`,
						},
					}
				},
			}, nil
		case 3:
			if req.PreviousResponseID != "resp_2" {
				t.Fatalf("previous response id = %q, want %q", req.PreviousResponseID, "resp_2")
			}
			if req.ToolOutput == nil || req.ToolOutput.Output != "已发起审批，等待确认后发送消息。" {
				t.Fatalf("unexpected second pending tool output: %+v", req.ToolOutput)
			}
			return InitialChatTurnResult{
				Stream: defaultExecutorSeqFromItems(&ark_dal.ModelStreamRespReasoning{ContentStruct: ark_dal.ContentStruct{Reply: "我已经把两个发送动作排队了。"}}),
				Snapshot: func() InitialChatTurnSnapshot {
					return InitialChatTurnSnapshot{ResponseID: "resp_3"}
				},
			}, nil
		default:
			t.Fatalf("unexpected turn call count = %d", turnCalls)
			return InitialChatTurnResult{}, nil
		}
	}
	defaultInitialChatStreamFinalizer = func(ctx context.Context, plan InitialChatExecutionPlan, stream iter.Seq[*ark_dal.ModelStreamRespReasoning]) iter.Seq[*ark_dal.ModelStreamRespReasoning] {
		return stream
	}

	stream, err := NewDefaultChatGenerationPlanExecutor().Generate(context.Background(), testChatResponseEvent(), ChatGenerationPlan{
		ModelID: "ep-test",
	})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	var (
		pendingPreviousResponseIDs []string
		replyText                  string
	)
	for item := range stream {
		if item.CapabilityCall != nil && item.CapabilityCall.Pending {
			pendingPreviousResponseIDs = append(pendingPreviousResponseIDs, item.CapabilityCall.PreviousResponseID)
		}
		if item.ContentStruct.Reply != "" {
			replyText = item.ContentStruct.Reply
		}
	}

	if turnCalls != 3 {
		t.Fatalf("turn call count = %d, want 3", turnCalls)
	}
	if toolCalls != 0 {
		t.Fatalf("tool call count = %d, want 0", toolCalls)
	}
	if len(pendingPreviousResponseIDs) != 2 {
		t.Fatalf("pending trace count = %d, want 2", len(pendingPreviousResponseIDs))
	}
	if pendingPreviousResponseIDs[0] != "resp_1" || pendingPreviousResponseIDs[1] != "resp_2" {
		t.Fatalf("pending previous response ids = %+v, want [resp_1 resp_2]", pendingPreviousResponseIDs)
	}
	if replyText != "我已经把两个发送动作排队了。" {
		t.Fatalf("reply text = %q, want %q", replyText, "我已经把两个发送动作排队了。")
	}
}

func defaultExecutorSeqFromItems(items ...*ark_dal.ModelStreamRespReasoning) iter.Seq[*ark_dal.ModelStreamRespReasoning] {
	return func(yield func(*ark_dal.ModelStreamRespReasoning) bool) {
		for _, item := range items {
			if !yield(item) {
				return
			}
		}
	}
}
