package agentruntime

import (
	"context"
	"iter"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	arktools "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/bytedance/gg/gresult"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestDefaultInitialReplyExecutorUsesDedicatedAgenticTurnEngine(t *testing.T) {
	originalProvider := defaultChatToolProvider
	originalAgenticBuilder := defaultAgenticInitialChatPlanBuilder
	originalTurnExecutor := defaultInitialChatTurnExecutor
	originalFinalizer := defaultInitialChatStreamFinalizer
	SetChatGenerationPlanExecutor(&failingChatGenerationPlanExecutorForInitialReply{})
	defer func() {
		SetChatGenerationPlanExecutor(nil)
		defaultChatToolProvider = originalProvider
		defaultAgenticInitialChatPlanBuilder = originalAgenticBuilder
		defaultInitialChatTurnExecutor = originalTurnExecutor
		defaultInitialChatStreamFinalizer = originalFinalizer
	}()

	defaultChatToolProvider = func() *arktools.Impl[larkim.P2MessageReceiveV1] {
		return arktools.New[larkim.P2MessageReceiveV1]()
	}
	defaultAgenticInitialChatPlanBuilder = func(ctx context.Context, req InitialChatGenerationRequest) (InitialChatExecutionPlan, error) {
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
		return InitialChatTurnResult{
			Stream: seqFromInitialReplyItems(
				&ark_dal.ModelStreamRespReasoning{ReasoningContent: "先看上下文"},
				&ark_dal.ModelStreamRespReasoning{ContentStruct: ark_dal.ContentStruct{Thought: "先看上下文", Reply: "这是最终回复"}},
			),
			Snapshot: func() InitialChatTurnSnapshot {
				return InitialChatTurnSnapshot{ResponseID: "resp_agentic_1"}
			},
		}, nil
	}
	defaultInitialChatStreamFinalizer = func(ctx context.Context, plan InitialChatExecutionPlan, stream iter.Seq[*ark_dal.ModelStreamRespReasoning]) iter.Seq[*ark_dal.ModelStreamRespReasoning] {
		return stream
	}

	emitter := &fakeInitialReplyEmitter{
		result: InitialReplyEmissionResult{
			ResponseMessageID: "om_reply",
			ResponseCardID:    "card_reply",
			DeliveryMode:      ReplyDeliveryModeCreate,
			Reply: CapturedInitialReply{
				ThoughtText: "先看上下文",
				ReplyText:   "这是最终回复",
			},
		},
	}
	executor := NewDefaultInitialReplyExecutor(InitialReplyOutputModeAgentic, testInitialReplyEvent(), ChatGenerationPlan{
		ModelID: "ep-test-agentic",
		Args:    []string{"帮我总结"},
	}, emitter)

	result, err := executor.ProduceInitialReply(context.Background())
	if err != nil {
		t.Fatalf("ProduceInitialReply() error = %v", err)
	}
	if emitter.streamCount != 2 {
		t.Fatalf("stream count = %d, want 2", emitter.streamCount)
	}
	if result.ReplyText != "这是最终回复" {
		t.Fatalf("reply text = %q, want %q", result.ReplyText, "这是最终回复")
	}
}

func TestDefaultInitialReplyExecutorProducesReplyViaEmitter(t *testing.T) {
	SetAgenticInitialReplyStreamGenerator(func(context.Context, *larkim.P2MessageReceiveV1, ChatGenerationPlan) (iter.Seq[*ark_dal.ModelStreamRespReasoning], error) {
		return seqFromInitialReplyItems(
			&ark_dal.ModelStreamRespReasoning{ReasoningContent: "先看上下文"},
			&ark_dal.ModelStreamRespReasoning{ContentStruct: ark_dal.ContentStruct{Thought: "先看上下文", Reply: "这是最终回复"}},
		), nil
	})
	defer SetAgenticInitialReplyStreamGenerator(nil)

	event := testInitialReplyEvent()
	emitter := &fakeInitialReplyEmitter{
		result: InitialReplyEmissionResult{
			ResponseMessageID: "om_reply",
			ResponseCardID:    "card_reply",
			DeliveryMode:      ReplyDeliveryModePatch,
			TargetMessageID:   "om_existing",
			TargetCardID:      "card_existing",
			Reply: CapturedInitialReply{
				ThoughtText: "先看上下文",
				ReplyText:   "这是最终回复",
			},
		},
	}
	executor := NewDefaultInitialReplyExecutor(InitialReplyOutputModeAgentic, event, ChatGenerationPlan{
		ModelID: "ep-test-agentic",
		Args:    []string{"帮我总结"},
	}, emitter)

	ctx := WithInitialReplyTarget(context.Background(), InitialReplyTarget{
		MessageID: "om_existing",
		CardID:    "card_existing",
	})
	result, err := executor.ProduceInitialReply(ctx)
	if err != nil {
		t.Fatalf("ProduceInitialReply() error = %v", err)
	}

	if emitter.calls != 1 {
		t.Fatalf("emitter calls = %d, want 1", emitter.calls)
	}
	if emitter.lastRequest.Mode != InitialReplyOutputModeAgentic {
		t.Fatalf("mode = %q, want %q", emitter.lastRequest.Mode, InitialReplyOutputModeAgentic)
	}
	if emitter.lastRequest.TargetMessageID != "om_existing" {
		t.Fatalf("target message id = %q, want %q", emitter.lastRequest.TargetMessageID, "om_existing")
	}
	if emitter.lastRequest.TargetCardID != "card_existing" {
		t.Fatalf("target card id = %q, want %q", emitter.lastRequest.TargetCardID, "card_existing")
	}
	if emitter.streamCount != 2 {
		t.Fatalf("stream count = %d, want 2", emitter.streamCount)
	}
	if result.ThoughtText != "先看上下文" {
		t.Fatalf("thought text = %q, want %q", result.ThoughtText, "先看上下文")
	}
	if result.ReplyText != "这是最终回复" {
		t.Fatalf("reply text = %q, want %q", result.ReplyText, "这是最终回复")
	}
	if result.ResponseMessageID != "om_reply" {
		t.Fatalf("response message id = %q, want %q", result.ResponseMessageID, "om_reply")
	}
	if result.ResponseCardID != "card_reply" {
		t.Fatalf("response card id = %q, want %q", result.ResponseCardID, "card_reply")
	}
	if result.DeliveryMode != ReplyDeliveryModePatch {
		t.Fatalf("delivery mode = %q, want %q", result.DeliveryMode, ReplyDeliveryModePatch)
	}
}

func TestDefaultInitialReplyExecutorBuildsQueuedPendingCapability(t *testing.T) {
	SetChatGenerationPlanExecutor(&fakeChatGenerationPlanExecutorForInitialReply{
		result: seqFromInitialReplyItems(&ark_dal.ModelStreamRespReasoning{ContentStruct: ark_dal.ContentStruct{Reply: "等待审批"}}),
	})
	defer SetChatGenerationPlanExecutor(nil)

	executor := NewDefaultInitialReplyExecutor(InitialReplyOutputModeStandard, testInitialReplyEvent(), ChatGenerationPlan{
		ModelID: "ep-test-standard",
		Args:    []string{"执行发送"},
	}, &fakeInitialReplyEmitter{
		result: InitialReplyEmissionResult{
			ResponseMessageID: "om_reply",
			DeliveryMode:      ReplyDeliveryModeReply,
			Reply: CapturedInitialReply{
				ReplyText: "等待审批",
				PendingCapability: &CapturedInitialPendingCapability{
					CallID:             "call_pending",
					CapabilityName:     "send_message",
					Arguments:          `{"text":"hi"}`,
					PreviousResponseID: "resp_pending_1",
					Approval: &CapabilityApprovalSpec{
						Type:    "capability",
						Title:   "审批发送消息",
						Summary: "等待审批",
					},
				},
			},
		},
	})

	result, err := executor.ProduceInitialReply(context.Background())
	if err != nil {
		t.Fatalf("ProduceInitialReply() error = %v", err)
	}
	if result.PendingCapability == nil {
		t.Fatal("expected pending capability")
	}
	if result.PendingCapability.CapabilityName != "send_message" {
		t.Fatalf("capability name = %q, want %q", result.PendingCapability.CapabilityName, "send_message")
	}
	if result.PendingCapability.Input.Request.ChatID != "oc_chat" {
		t.Fatalf("chat id = %q, want %q", result.PendingCapability.Input.Request.ChatID, "oc_chat")
	}
	if result.PendingCapability.Input.Request.Scope != CapabilityScopeGroup {
		t.Fatalf("scope = %q, want %q", result.PendingCapability.Input.Request.Scope, CapabilityScopeGroup)
	}
	if result.PendingCapability.Input.Approval == nil || result.PendingCapability.Input.Approval.Title != "审批发送消息" {
		t.Fatalf("unexpected approval payload: %+v", result.PendingCapability.Input.Approval)
	}
	if result.PendingCapability.Input.Continuation == nil || result.PendingCapability.Input.Continuation.PreviousResponseID != "resp_pending_1" {
		t.Fatalf("unexpected continuation payload: %+v", result.PendingCapability.Input.Continuation)
	}
}

func TestGenerateAgenticInitialReplyStreamChainsMultiplePendingCapabilities(t *testing.T) {
	originalProvider := defaultChatToolProvider
	originalBuilder := defaultAgenticInitialChatPlanBuilder
	originalTurnExecutor := defaultInitialChatTurnExecutor
	originalFinalizer := defaultInitialChatStreamFinalizer
	defer func() {
		defaultChatToolProvider = originalProvider
		defaultAgenticInitialChatPlanBuilder = originalBuilder
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
	defaultAgenticInitialChatPlanBuilder = func(ctx context.Context, req InitialChatGenerationRequest) (InitialChatExecutionPlan, error) {
		return InitialChatExecutionPlan{
			Event:     req.Event,
			ModelID:   req.ModelID,
			ChatID:    "oc_chat",
			OpenID:    "ou_actor",
			Prompt:    "agentic system prompt",
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
				Stream: defaultExecutorSeqFromItems(&ark_dal.ModelStreamRespReasoning{ContentStruct: ark_dal.ContentStruct{
					Thought: "先把两个发送动作排队",
					Reply:   "我已经把两个发送动作排队了。",
				}}),
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

	stream, err := GenerateAgenticInitialReplyStream(context.Background(), testInitialReplyEvent(), ChatGenerationPlan{
		ModelID: "ep-test-agentic",
	})
	if err != nil {
		t.Fatalf("GenerateAgenticInitialReplyStream() error = %v", err)
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

type fakeInitialReplyEmitter struct {
	calls       int
	streamCount int
	lastRequest InitialReplyEmissionRequest
	result      InitialReplyEmissionResult
	err         error
}

func (f *fakeInitialReplyEmitter) EmitInitialReply(ctx context.Context, req InitialReplyEmissionRequest) (InitialReplyEmissionResult, error) {
	f.calls++
	f.lastRequest = req
	for range req.Stream {
		f.streamCount++
	}
	return f.result, f.err
}

type fakeChatGenerationPlanExecutorForInitialReply struct {
	result iter.Seq[*ark_dal.ModelStreamRespReasoning]
	err    error
}

type failingChatGenerationPlanExecutorForInitialReply struct{}

func (f *fakeChatGenerationPlanExecutorForInitialReply) Generate(context.Context, *larkim.P2MessageReceiveV1, ChatGenerationPlan) (iter.Seq[*ark_dal.ModelStreamRespReasoning], error) {
	return f.result, f.err
}

func (f *failingChatGenerationPlanExecutorForInitialReply) Generate(context.Context, *larkim.P2MessageReceiveV1, ChatGenerationPlan) (iter.Seq[*ark_dal.ModelStreamRespReasoning], error) {
	return nil, context.Canceled
}

func seqFromInitialReplyItems(items ...*ark_dal.ModelStreamRespReasoning) iter.Seq[*ark_dal.ModelStreamRespReasoning] {
	return func(yield func(*ark_dal.ModelStreamRespReasoning) bool) {
		for _, item := range items {
			if !yield(item) {
				return
			}
		}
	}
}

func testInitialReplyEvent() *larkim.P2MessageReceiveV1 {
	chatID := "oc_chat"
	openID := "ou_actor"
	msgID := "om_runtime"
	chatType := "group"
	return &larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Message: &larkim.EventMessage{
				ChatId:    &chatID,
				MessageId: &msgID,
				ChatType:  &chatType,
			},
			Sender: &larkim.EventSender{
				SenderId: &larkim.UserId{
					OpenId: &openID,
				},
			},
		},
	}
}
