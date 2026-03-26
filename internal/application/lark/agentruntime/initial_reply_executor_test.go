package agentruntime

import (
	"context"
	"iter"
	"testing"

	initialcore "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime/initialcore"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	arktools "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/bytedance/gg/gresult"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestDefaultInitialReplyExecutorUsesDedicatedAgenticTurnEngine(t *testing.T) {
	SetChatGenerationPlanExecutor(&failingChatGenerationPlanExecutorForInitialReply{})
	defer SetChatGenerationPlanExecutor(nil)

	executor := NewDefaultInitialReplyExecutorWithDeps(
		InitialReplyOutputModeAgentic,
		testInitialReplyEvent(),
		ChatGenerationPlan{
			ModelID: "ep-test-agentic",
			Args:    []string{"帮我总结"},
		},
		&fakeInitialReplyEmitter{
			result: InitialReplyEmissionResult{
				ResponseMessageID: "om_reply",
				ResponseCardID:    "card_reply",
				DeliveryMode:      ReplyDeliveryModeCreate,
				Reply: CapturedInitialReply{
					ThoughtText: "先看上下文",
					ReplyText:   "这是最终回复",
				},
			},
		},
		defaultRuntimeExecutorDeps{
			toolProvider: func() *arktools.Impl[larkim.P2MessageReceiveV1] {
				return arktools.New[larkim.P2MessageReceiveV1]()
			},
			agenticInitialPlanBuilder: func(ctx context.Context, req InitialChatGenerationRequest) (InitialChatExecutionPlan, error) {
				return InitialChatExecutionPlan{
					Event:     req.Event,
					ModelID:   req.ModelID,
					ChatID:    "oc_chat",
					OpenID:    "ou_actor",
					Prompt:    "agentic system prompt",
					UserInput: "agentic user prompt",
					Tools:     req.Tools,
				}, nil
			},
			initialChatTurnExecutor: func(ctx context.Context, req InitialChatTurnRequest) (InitialChatTurnResult, error) {
				return InitialChatTurnResult{
					Stream: seqFromInitialReplyItems(
						&ark_dal.ModelStreamRespReasoning{ReasoningContent: "先看上下文"},
						&ark_dal.ModelStreamRespReasoning{ContentStruct: ark_dal.ContentStruct{Thought: "先看上下文", Reply: "这是最终回复"}},
					),
					Snapshot: func() InitialChatTurnSnapshot {
						return InitialChatTurnSnapshot{ResponseID: "resp_agentic_1"}
					},
				}, nil
			},
			initialChatStreamFinalizer: passthroughInitialReplyFinalizer(),
		},
		nil,
	)
	emitter := executor.(defaultInitialReplyExecutor).emitter.(*fakeInitialReplyEmitter)

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

func TestResolveAgenticInitialReplyDelivery(t *testing.T) {
	tests := []struct {
		name     string
		req      InitialReplyEmissionRequest
		canPatch bool
		canReply bool
		want     initialcore.Delivery
	}{
		{
			name: "patches root model reply when patch target is available",
			req: InitialReplyEmissionRequest{
				OutputKind:      AgenticOutputKindModelReply,
				TargetMode:      InitialReplyTargetModePatch,
				TargetMessageID: "om_root",
				TargetCardID:    "card_root",
			},
			canPatch: true,
			want:     initialcore.DeliveryPatch,
		},
		{
			name: "replies when reply target is available",
			req: InitialReplyEmissionRequest{
				OutputKind:      AgenticOutputKindModelReply,
				TargetMode:      InitialReplyTargetModeReply,
				TargetMessageID: "om_followup",
			},
			canReply: true,
			want:     initialcore.DeliveryReply,
		},
		{
			name: "creates new card for non model output even with patch target",
			req: InitialReplyEmissionRequest{
				OutputKind:      AgenticOutputKindSideEffect,
				TargetMode:      InitialReplyTargetModePatch,
				TargetMessageID: "om_root",
				TargetCardID:    "card_root",
			},
			canPatch: true,
			want:     initialcore.DeliveryCreate,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveAgenticInitialReplyDelivery(tt.req, tt.canPatch, tt.canReply)
			if got != tt.want {
				t.Fatalf("resolveAgenticInitialReplyDelivery() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDefaultInitialReplyExecutorProducesReplyViaEmitter(t *testing.T) {
	executor := NewDefaultInitialReplyExecutorWithDeps(
		InitialReplyOutputModeAgentic,
		testInitialReplyEvent(),
		ChatGenerationPlan{
			ModelID: "ep-test-agentic",
			Args:    []string{"帮我总结"},
		},
		&fakeInitialReplyEmitter{
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
		},
		defaultRuntimeExecutorDeps{},
		func(context.Context, *larkim.P2MessageReceiveV1, ChatGenerationPlan) (iter.Seq[*ark_dal.ModelStreamRespReasoning], error) {
			return seqFromInitialReplyItems(
				&ark_dal.ModelStreamRespReasoning{ReasoningContent: "先看上下文"},
				&ark_dal.ModelStreamRespReasoning{ContentStruct: ark_dal.ContentStruct{Thought: "先看上下文", Reply: "这是最终回复"}},
			), nil
		},
	)
	emitter := executor.(defaultInitialReplyExecutor).emitter.(*fakeInitialReplyEmitter)

	ctx := WithInitialReplyTarget(context.Background(), InitialReplyTarget{
		Mode:      InitialReplyTargetModePatch,
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
	if emitter.lastRequest.OutputKind != AgenticOutputKindModelReply {
		t.Fatalf("output kind = %q, want %q", emitter.lastRequest.OutputKind, AgenticOutputKindModelReply)
	}
	if emitter.lastRequest.MentionOpenID != "ou_actor" {
		t.Fatalf("mention open id = %q, want %q", emitter.lastRequest.MentionOpenID, "ou_actor")
	}
	if emitter.lastRequest.TargetMessageID != "om_existing" {
		t.Fatalf("target message id = %q, want %q", emitter.lastRequest.TargetMessageID, "om_existing")
	}
	if emitter.lastRequest.TargetCardID != "card_existing" {
		t.Fatalf("target card id = %q, want %q", emitter.lastRequest.TargetCardID, "card_existing")
	}
	if emitter.lastRequest.TargetMode != InitialReplyTargetModePatch {
		t.Fatalf("target mode = %q, want %q", emitter.lastRequest.TargetMode, InitialReplyTargetModePatch)
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

func TestDefaultInitialReplyExecutorForwardsReplyTargetModeAndThreadFlag(t *testing.T) {
	executor := NewDefaultInitialReplyExecutorWithDeps(
		InitialReplyOutputModeAgentic,
		testInitialReplyEvent(),
		ChatGenerationPlan{
			ModelID: "ep-test-agentic",
		},
		&fakeInitialReplyEmitter{
			result: InitialReplyEmissionResult{
				ResponseMessageID: "om_reply",
				ResponseCardID:    "card_reply",
				DeliveryMode:      ReplyDeliveryModeReply,
				TargetMessageID:   "om_followup",
				Reply: CapturedInitialReply{
					ReplyText: "这是最终回复",
				},
			},
		},
		defaultRuntimeExecutorDeps{},
		func(context.Context, *larkim.P2MessageReceiveV1, ChatGenerationPlan) (iter.Seq[*ark_dal.ModelStreamRespReasoning], error) {
			return seqFromInitialReplyItems(
				&ark_dal.ModelStreamRespReasoning{ContentStruct: ark_dal.ContentStruct{Reply: "这是最终回复"}},
			), nil
		},
	)
	emitter := executor.(defaultInitialReplyExecutor).emitter.(*fakeInitialReplyEmitter)

	ctx := WithInitialReplyTarget(context.Background(), InitialReplyTarget{
		Mode:          InitialReplyTargetModeReply,
		MessageID:     "om_followup",
		ReplyInThread: true,
	})
	if _, err := executor.ProduceInitialReply(ctx); err != nil {
		t.Fatalf("ProduceInitialReply() error = %v", err)
	}

	if emitter.lastRequest.TargetMode != InitialReplyTargetModeReply {
		t.Fatalf("target mode = %q, want %q", emitter.lastRequest.TargetMode, InitialReplyTargetModeReply)
	}
	if emitter.lastRequest.OutputKind != AgenticOutputKindModelReply {
		t.Fatalf("output kind = %q, want %q", emitter.lastRequest.OutputKind, AgenticOutputKindModelReply)
	}
	if emitter.lastRequest.MentionOpenID != "ou_actor" {
		t.Fatalf("mention open id = %q, want %q", emitter.lastRequest.MentionOpenID, "ou_actor")
	}
	if emitter.lastRequest.TargetMessageID != "om_followup" {
		t.Fatalf("target message id = %q, want %q", emitter.lastRequest.TargetMessageID, "om_followup")
	}
	if emitter.lastRequest.TargetCardID != "" {
		t.Fatalf("target card id = %q, want empty", emitter.lastRequest.TargetCardID)
	}
	if !emitter.lastRequest.ReplyInThread {
		t.Fatal("expected reply_in_thread to be forwarded")
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

func TestGenerateAgenticInitialReplyStreamContinuesAfterPendingCapability(t *testing.T) {
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
	deps := defaultRuntimeExecutorDeps{
		toolProvider: func() *arktools.Impl[larkim.P2MessageReceiveV1] {
			return toolset
		},
		agenticInitialPlanBuilder: func(ctx context.Context, req InitialChatGenerationRequest) (InitialChatExecutionPlan, error) {
			return InitialChatExecutionPlan{
				Event:     req.Event,
				ModelID:   req.ModelID,
				ChatID:    "oc_chat",
				OpenID:    "ou_actor",
				Prompt:    "agentic system prompt",
				UserInput: "连续发两条消息",
				Tools:     req.Tools,
			}, nil
		},
		initialChatStreamFinalizer: passthroughInitialReplyFinalizer(),
	}

	turnCalls := 0
	deps.initialChatTurnExecutor = func(ctx context.Context, req InitialChatTurnRequest) (InitialChatTurnResult, error) {
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
			return InitialChatTurnResult{
				Stream: defaultExecutorSeqFromItems(
					&ark_dal.ModelStreamRespReasoning{
						ContentStruct: ark_dal.ContentStruct{Reply: "所有待审批动作都已经排好，继续等待处理。"},
					},
				),
				Snapshot: func() InitialChatTurnSnapshot {
					return InitialChatTurnSnapshot{ResponseID: "resp_3"}
				},
			}, nil
		default:
			t.Fatalf("unexpected turn call count = %d", turnCalls)
			return InitialChatTurnResult{}, nil
		}
	}

	stream, err := generateAgenticInitialReplyStreamWithDeps(context.Background(), testInitialReplyEvent(), ChatGenerationPlan{
		ModelID: "ep-test-agentic",
	}, deps)
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
	if replyText != "所有待审批动作都已经排好，继续等待处理。" {
		t.Fatalf("reply text = %q, want %q", replyText, "所有待审批动作都已经排好，继续等待处理。")
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

func passthroughInitialReplyFinalizer() func(context.Context, InitialChatExecutionPlan, iter.Seq[*ark_dal.ModelStreamRespReasoning]) iter.Seq[*ark_dal.ModelStreamRespReasoning] {
	return func(ctx context.Context, plan InitialChatExecutionPlan, stream iter.Seq[*ark_dal.ModelStreamRespReasoning]) iter.Seq[*ark_dal.ModelStreamRespReasoning] {
		return stream
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
