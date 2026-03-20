package runtimecutover

import (
	"context"
	"iter"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestHandlerStartsRunStreamsCardAndCompletesReply(t *testing.T) {
	fakeExecutor := &fakeChatGenerationPlanExecutor{
		result: seqFromItems(&ark_dal.ModelStreamRespReasoning{ReasoningContent: "先读上下文"}, &ark_dal.ModelStreamRespReasoning{ContentStruct: ark_dal.ContentStruct{Thought: "先读上下文", Reply: "这是最终回复"}}),
	}
	setTestChatGenerationPlanExecutor(fakeExecutor)
	defer resetTestChatGenerationPlanExecutor()

	now := time.Date(2026, 3, 18, 14, 0, 0, 0, time.UTC)
	chatID := "oc_chat"
	openID := "ou_actor"
	msgID := "om_runtime"
	chatType := "group"
	event := &larkim.P2MessageReceiveV1{
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

	sent := make([]*ark_dal.ModelStreamRespReasoning, 0)
	processor := &fakeRunProcessor{}
	handler := &Handler{
		now: func() time.Time { return now },
		processorBuilder: func(_ context.Context, emitter agentruntime.InitialReplyEmitter) runProcessor {
			processor.initialReplyEmitter = emitter
			return processor
		},
		cardSender: func(ctx context.Context, msg *larkim.EventMessage, seq iter.Seq[*ark_dal.ModelStreamRespReasoning]) (larkmsg.AgentStreamingCardRefs, error) {
			for item := range seq {
				sent = append(sent, item)
			}
			return larkmsg.AgentStreamingCardRefs{
				MessageID: "om_runtime_reply",
				CardID:    "card_runtime_reply",
			}, nil
		},
	}

	err := handler.Handle(context.Background(), agentruntime.RuntimeAgenticCutoverRequest{
		Event: event,
		Plan: agentruntime.ChatGenerationPlan{
			ModelID: "ep-test-agentic",
			Size:    20,
			Args:    []string{"帮我总结"},
		},
		StartedAt: now,
	})
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	if len(processor.inputs) != 1 {
		t.Fatalf("processor input count = %d, want 1", len(processor.inputs))
	}
	initial := processor.inputs[0].Initial
	if initial == nil {
		t.Fatal("expected initial run input")
	}
	if initial.Start.TriggerMessageID != "om_runtime" {
		t.Fatalf("trigger message id = %q, want %q", initial.Start.TriggerMessageID, "om_runtime")
	}
	if len(processor.initialResults) != 1 {
		t.Fatalf("initial result count = %d, want 1", len(processor.initialResults))
	}
	if processor.initialResults[0].ThoughtText != "先读上下文" {
		t.Fatalf("thought text = %q, want %q", processor.initialResults[0].ThoughtText, "先读上下文")
	}
	if processor.initialResults[0].ReplyText != "这是最终回复" {
		t.Fatalf("reply text = %q, want %q", processor.initialResults[0].ReplyText, "这是最终回复")
	}
	if processor.initialResults[0].ResponseMessageID != "om_runtime_reply" {
		t.Fatalf("response message id = %q, want %q", processor.initialResults[0].ResponseMessageID, "om_runtime_reply")
	}
	if processor.initialResults[0].ResponseCardID != "card_runtime_reply" {
		t.Fatalf("response card id = %q, want %q", processor.initialResults[0].ResponseCardID, "card_runtime_reply")
	}
	if processor.initialResults[0].DeliveryMode != agentruntime.ReplyDeliveryModeCreate {
		t.Fatalf("delivery mode = %q, want %q", processor.initialResults[0].DeliveryMode, agentruntime.ReplyDeliveryModeCreate)
	}
	if len(processor.initialResults[0].CapabilityCalls) != 0 {
		t.Fatalf("capability call count = %d, want 0", len(processor.initialResults[0].CapabilityCalls))
	}
	if fakeExecutor.calls != 1 {
		t.Fatalf("executor calls = %d, want 1", fakeExecutor.calls)
	}
	if len(sent) != 2 {
		t.Fatalf("sent item count = %d, want 2", len(sent))
	}
}

func TestHandlerCapturesCapabilityCallTraceForCompletion(t *testing.T) {
	fakeExecutor := &fakeChatGenerationPlanExecutor{
		result: seqFromItems(
			&ark_dal.ModelStreamRespReasoning{CapabilityCall: &ark_dal.CapabilityCallTrace{
				CallID:       "call_1",
				FunctionName: "send_message",
				Arguments:    `{"text":"hi"}`,
				Output:       "ok",
			}},
			&ark_dal.ModelStreamRespReasoning{ContentStruct: ark_dal.ContentStruct{Reply: "发送完成"}},
		),
	}
	setTestChatGenerationPlanExecutor(fakeExecutor)
	defer resetTestChatGenerationPlanExecutor()

	now := time.Date(2026, 3, 18, 14, 5, 0, 0, time.UTC)
	chatID := "oc_chat"
	openID := "ou_actor"
	msgID := "om_runtime_trace"
	chatType := "group"
	event := &larkim.P2MessageReceiveV1{
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

	processor := &fakeRunProcessor{}
	handler := &Handler{
		now: func() time.Time { return now },
		processorBuilder: func(_ context.Context, emitter agentruntime.InitialReplyEmitter) runProcessor {
			processor.initialReplyEmitter = emitter
			return processor
		},
		cardSender: func(ctx context.Context, msg *larkim.EventMessage, seq iter.Seq[*ark_dal.ModelStreamRespReasoning]) (larkmsg.AgentStreamingCardRefs, error) {
			for range seq {
			}
			return larkmsg.AgentStreamingCardRefs{
				MessageID: "om_runtime_reply",
				CardID:    "card_runtime_reply",
			}, nil
		},
	}

	err := handler.Handle(context.Background(), agentruntime.RuntimeAgenticCutoverRequest{
		Event: event,
		Plan: agentruntime.ChatGenerationPlan{
			ModelID: "ep-test-agentic",
			Size:    20,
			Args:    []string{"执行发送"},
		},
		StartedAt: now,
	})
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	if len(processor.initialResults) != 1 {
		t.Fatalf("initial result count = %d, want 1", len(processor.initialResults))
	}
	if len(processor.initialResults[0].CapabilityCalls) != 1 {
		t.Fatalf("capability call count = %d, want 1", len(processor.initialResults[0].CapabilityCalls))
	}
	call := processor.initialResults[0].CapabilityCalls[0]
	if call.CallID != "call_1" {
		t.Fatalf("call id = %q, want %q", call.CallID, "call_1")
	}
	if call.CapabilityName != "send_message" {
		t.Fatalf("capability name = %q, want %q", call.CapabilityName, "send_message")
	}
	if call.Arguments != `{"text":"hi"}` {
		t.Fatalf("arguments = %q, want %q", call.Arguments, `{"text":"hi"}`)
	}
	if call.Output != "ok" {
		t.Fatalf("output = %q, want %q", call.Output, "ok")
	}
}

func TestHandlerUsesOwnershipOverrideWhenStartingRun(t *testing.T) {
	fakeExecutor := &fakeChatGenerationPlanExecutor{
		result: seqFromItems(&ark_dal.ModelStreamRespReasoning{ContentStruct: ark_dal.ContentStruct{Reply: "继续处理"}}),
	}
	setTestChatGenerationPlanExecutor(fakeExecutor)
	defer resetTestChatGenerationPlanExecutor()

	now := time.Date(2026, 3, 19, 9, 0, 0, 0, time.UTC)
	chatID := "oc_chat"
	openID := "ou_actor"
	msgID := "om_runtime_override"
	chatType := "group"
	event := &larkim.P2MessageReceiveV1{
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

	processor := &fakeRunProcessor{}
	handler := &Handler{
		now: func() time.Time { return now },
		processorBuilder: func(_ context.Context, emitter agentruntime.InitialReplyEmitter) runProcessor {
			processor.initialReplyEmitter = emitter
			return processor
		},
		cardSender: func(ctx context.Context, msg *larkim.EventMessage, seq iter.Seq[*ark_dal.ModelStreamRespReasoning]) (larkmsg.AgentStreamingCardRefs, error) {
			for range seq {
			}
			return larkmsg.AgentStreamingCardRefs{}, nil
		},
	}

	err := handler.Handle(context.Background(), agentruntime.RuntimeAgenticCutoverRequest{
		Event: event,
		Plan: agentruntime.ChatGenerationPlan{
			ModelID: "ep-test-agentic",
			Size:    20,
		},
		StartedAt: now,
		Ownership: agentruntime.InitialRunOwnership{
			TriggerType:    agentruntime.TriggerTypeFollowUp,
			AttachToRunID:  "run_active",
			SupersedeRunID: "run_stale",
		},
	})
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	if len(processor.inputs) != 1 || processor.inputs[0].Initial == nil {
		t.Fatalf("unexpected processor inputs: %+v", processor.inputs)
	}
	start := processor.inputs[0].Initial.Start
	if start.TriggerType != agentruntime.TriggerTypeFollowUp {
		t.Fatalf("trigger type = %q, want %q", start.TriggerType, agentruntime.TriggerTypeFollowUp)
	}
	if start.AttachToRunID != "run_active" {
		t.Fatalf("attach run id = %q, want %q", start.AttachToRunID, "run_active")
	}
	if start.SupersedeRunID != "run_stale" {
		t.Fatalf("supersede run id = %q, want %q", start.SupersedeRunID, "run_stale")
	}
}

func TestHandlerDeduplicatesCapabilityCallTraceByCallID(t *testing.T) {
	fakeExecutor := &fakeChatGenerationPlanExecutor{
		result: seqFromItems(
			&ark_dal.ModelStreamRespReasoning{CapabilityCall: &ark_dal.CapabilityCallTrace{
				CallID:       "call_1",
				FunctionName: "send_message",
				Arguments:    `{"text":"hi"}`,
				Output:       "ok",
			}},
			&ark_dal.ModelStreamRespReasoning{CapabilityCall: &ark_dal.CapabilityCallTrace{
				CallID:       "call_1",
				FunctionName: "send_message",
				Arguments:    `{"text":"hi"}`,
				Output:       "ok",
			}},
			&ark_dal.ModelStreamRespReasoning{ContentStruct: ark_dal.ContentStruct{Reply: "发送完成"}},
		),
	}
	setTestChatGenerationPlanExecutor(fakeExecutor)
	defer resetTestChatGenerationPlanExecutor()

	now := time.Date(2026, 3, 18, 14, 6, 0, 0, time.UTC)
	chatID := "oc_chat"
	openID := "ou_actor"
	msgID := "om_runtime_trace_dup"
	chatType := "group"
	event := &larkim.P2MessageReceiveV1{
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

	processor := &fakeRunProcessor{}
	handler := &Handler{
		now: func() time.Time { return now },
		processorBuilder: func(_ context.Context, emitter agentruntime.InitialReplyEmitter) runProcessor {
			processor.initialReplyEmitter = emitter
			return processor
		},
		cardSender: func(ctx context.Context, msg *larkim.EventMessage, seq iter.Seq[*ark_dal.ModelStreamRespReasoning]) (larkmsg.AgentStreamingCardRefs, error) {
			for range seq {
			}
			return larkmsg.AgentStreamingCardRefs{}, nil
		},
	}

	err := handler.Handle(context.Background(), agentruntime.RuntimeAgenticCutoverRequest{
		Event: event,
		Plan: agentruntime.ChatGenerationPlan{
			ModelID: "ep-test-agentic",
			Size:    20,
		},
		StartedAt: now,
	})
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	if len(processor.initialResults) != 1 {
		t.Fatalf("initial result count = %d, want 1", len(processor.initialResults))
	}
	if len(processor.initialResults[0].CapabilityCalls) != 1 {
		t.Fatalf("capability call count = %d, want 1", len(processor.initialResults[0].CapabilityCalls))
	}
}

func TestHandlerDelegatesPendingCapabilityThroughSingleInitialRunProcessorCall(t *testing.T) {
	now := time.Date(2026, 3, 18, 14, 8, 0, 0, time.UTC)
	fakeExecutor := &fakeChatGenerationPlanExecutor{
		result: seqFromItems(
			&ark_dal.ModelStreamRespReasoning{CapabilityCall: &ark_dal.CapabilityCallTrace{
				CallID:             "call_pending_1",
				FunctionName:       "send_message",
				Arguments:          `{"content":"hello"}`,
				Output:             "已发起审批，等待确认后发送消息。",
				PreviousResponseID: "resp_pending_1",
				Pending:            true,
				ApprovalType:       "capability",
				ApprovalTitle:      "审批发送消息",
				ApprovalSummary:    "将向群里发送一条消息",
				ApprovalExpiresAt:  now.Add(15 * time.Minute),
			}},
			&ark_dal.ModelStreamRespReasoning{ContentStruct: ark_dal.ContentStruct{
				Thought: "先发起审批",
				Reply:   "我已经发起审批，待批准后继续发送。",
			}},
		),
	}
	setTestChatGenerationPlanExecutor(fakeExecutor)
	defer resetTestChatGenerationPlanExecutor()

	chatID := "oc_chat"
	openID := "ou_actor"
	msgID := "om_runtime_pending"
	chatType := "group"
	event := &larkim.P2MessageReceiveV1{
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

	processor := &fakeRunProcessor{}
	handler := &Handler{
		now: func() time.Time { return now },
		processorBuilder: func(_ context.Context, emitter agentruntime.InitialReplyEmitter) runProcessor {
			processor.initialReplyEmitter = emitter
			return processor
		},
		cardSender: func(ctx context.Context, msg *larkim.EventMessage, seq iter.Seq[*ark_dal.ModelStreamRespReasoning]) (larkmsg.AgentStreamingCardRefs, error) {
			for range seq {
			}
			return larkmsg.AgentStreamingCardRefs{
				MessageID: "om_runtime_pending_reply",
				CardID:    "card_runtime_pending_reply",
			}, nil
		},
	}

	err := handler.Handle(context.Background(), agentruntime.RuntimeAgenticCutoverRequest{
		Event: event,
		Plan: agentruntime.ChatGenerationPlan{
			ModelID: "ep-test-agentic",
			Size:    20,
			Args:    []string{"帮我发条消息"},
		},
		StartedAt: now,
	})
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	if len(processor.inputs) != 1 {
		t.Fatalf("processor input count = %d, want 1", len(processor.inputs))
	}
	if processor.inputs[0].Initial == nil {
		t.Fatal("expected initial run input")
	}
	if len(processor.initialResults) != 1 {
		t.Fatalf("initial result count = %d, want 1", len(processor.initialResults))
	}
	if processor.initialResults[0].PendingCapability == nil {
		t.Fatal("expected queued capability in initial result")
	}
	if processor.initialResults[0].PendingCapability.CapabilityName != "send_message" {
		t.Fatalf("queued capability name = %q, want %q", processor.initialResults[0].PendingCapability.CapabilityName, "send_message")
	}
	if processor.initialResults[0].PendingCapability.Input.Continuation == nil || processor.initialResults[0].PendingCapability.Input.Continuation.PreviousResponseID != "resp_pending_1" {
		t.Fatalf("unexpected continuation payload: %+v", processor.initialResults[0].PendingCapability.Input.Continuation)
	}
}

func TestHandlerChainsMultiplePendingCapabilitiesIntoQueueTail(t *testing.T) {
	now := time.Date(2026, 3, 18, 14, 9, 0, 0, time.UTC)
	fakeExecutor := &fakeChatGenerationPlanExecutor{
		result: seqFromItems(
			&ark_dal.ModelStreamRespReasoning{CapabilityCall: &ark_dal.CapabilityCallTrace{
				CallID:             "call_pending_1",
				FunctionName:       "search_history",
				Arguments:          `{"q":"one"}`,
				Output:             "queued one",
				PreviousResponseID: "resp_pending_1",
				Pending:            true,
			}},
			&ark_dal.ModelStreamRespReasoning{CapabilityCall: &ark_dal.CapabilityCallTrace{
				CallID:             "call_pending_2",
				FunctionName:       "search_history",
				Arguments:          `{"q":"two"}`,
				Output:             "queued two",
				PreviousResponseID: "resp_pending_2",
				Pending:            true,
			}},
			&ark_dal.ModelStreamRespReasoning{ContentStruct: ark_dal.ContentStruct{
				Thought: "串行处理两个能力",
				Reply:   "我开始依次处理。",
			}},
		),
	}
	setTestChatGenerationPlanExecutor(fakeExecutor)
	defer resetTestChatGenerationPlanExecutor()

	chatID := "oc_chat"
	openID := "ou_actor"
	msgID := "om_runtime_pending_chain"
	chatType := "group"
	event := &larkim.P2MessageReceiveV1{
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

	processor := &fakeRunProcessor{}
	handler := &Handler{
		now: func() time.Time { return now },
		processorBuilder: func(_ context.Context, emitter agentruntime.InitialReplyEmitter) runProcessor {
			processor.initialReplyEmitter = emitter
			return processor
		},
		cardSender: func(ctx context.Context, msg *larkim.EventMessage, seq iter.Seq[*ark_dal.ModelStreamRespReasoning]) (larkmsg.AgentStreamingCardRefs, error) {
			for range seq {
			}
			return larkmsg.AgentStreamingCardRefs{
				MessageID: "om_runtime_pending_chain_reply",
				CardID:    "card_runtime_pending_chain_reply",
			}, nil
		},
	}

	err := handler.Handle(context.Background(), agentruntime.RuntimeAgenticCutoverRequest{
		Event: event,
		Plan: agentruntime.ChatGenerationPlan{
			ModelID: "ep-test-agentic",
			Size:    20,
			Args:    []string{"连续处理两个能力"},
		},
		StartedAt: now,
	})
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	if len(processor.initialResults) != 1 {
		t.Fatalf("initial result count = %d, want 1", len(processor.initialResults))
	}
	root := processor.initialResults[0].PendingCapability
	if root == nil {
		t.Fatal("expected queued capability root")
	}
	if root.CapabilityName != "search_history" {
		t.Fatalf("root capability name = %q, want %q", root.CapabilityName, "search_history")
	}
	if len(root.Input.QueueTail) != 1 {
		t.Fatalf("queue tail length = %d, want 1", len(root.Input.QueueTail))
	}
	if root.Input.QueueTail[0].CapabilityName != "search_history" {
		t.Fatalf("tail capability name = %q, want %q", root.Input.QueueTail[0].CapabilityName, "search_history")
	}
	if root.Input.QueueTail[0].Input.Continuation == nil || root.Input.QueueTail[0].Input.Continuation.PreviousResponseID != "resp_pending_2" {
		t.Fatalf("unexpected tail continuation payload: %+v", root.Input.QueueTail[0].Input.Continuation)
	}
}

type fakeRunProcessor struct {
	inputs              []agentruntime.RunProcessorInput
	initialResults      []agentruntime.InitialReplyResult
	resumeEvents        []agentruntime.ResumeEvent
	processRunError     error
	initialReplyEmitter agentruntime.InitialReplyEmitter
}

func (f *fakeRunProcessor) ProcessRun(ctx context.Context, input agentruntime.RunProcessorInput) error {
	f.inputs = append(f.inputs, input)
	if input.Initial != nil {
		executor, err := input.Initial.BuildExecutor(f.initialReplyEmitter)
		if err != nil {
			return err
		}
		result, err := executor.ProduceInitialReply(ctx)
		if err != nil {
			return err
		}
		f.initialResults = append(f.initialResults, result)
	}
	if input.Resume != nil {
		f.resumeEvents = append(f.resumeEvents, *input.Resume)
	}
	return f.processRunError
}

func seqFromItems(items ...*ark_dal.ModelStreamRespReasoning) iter.Seq[*ark_dal.ModelStreamRespReasoning] {
	return func(yield func(*ark_dal.ModelStreamRespReasoning) bool) {
		for _, item := range items {
			if !yield(item) {
				return
			}
		}
	}
}

type fakeChatGenerationPlanExecutor struct {
	calls     int
	lastPlan  agentruntime.ChatGenerationPlan
	lastEvent *larkim.P2MessageReceiveV1
	result    iter.Seq[*ark_dal.ModelStreamRespReasoning]
	err       error
}

func (f *fakeChatGenerationPlanExecutor) Generate(ctx context.Context, event *larkim.P2MessageReceiveV1, plan agentruntime.ChatGenerationPlan) (iter.Seq[*ark_dal.ModelStreamRespReasoning], error) {
	f.calls++
	f.lastPlan = plan
	f.lastEvent = event
	return f.result, f.err
}

func setTestChatGenerationPlanExecutor(executor *fakeChatGenerationPlanExecutor) {
	if executor == nil {
		agentruntime.SetChatGenerationPlanExecutor(nil)
		agentruntime.SetAgenticInitialReplyStreamGenerator(nil)
		return
	}
	agentruntime.SetChatGenerationPlanExecutor(executor)
	agentruntime.SetAgenticInitialReplyStreamGenerator(func(ctx context.Context, event *larkim.P2MessageReceiveV1, plan agentruntime.ChatGenerationPlan) (iter.Seq[*ark_dal.ModelStreamRespReasoning], error) {
		return executor.Generate(ctx, event, plan)
	})
}

func resetTestChatGenerationPlanExecutor() {
	agentruntime.SetChatGenerationPlanExecutor(nil)
	agentruntime.SetAgenticInitialReplyStreamGenerator(nil)
}
