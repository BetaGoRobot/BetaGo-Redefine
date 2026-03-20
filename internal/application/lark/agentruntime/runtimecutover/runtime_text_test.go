package runtimecutover

import (
	"context"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestStandardHandlerStartsRunRepliesTextAndCompletesReply(t *testing.T) {
	fakeExecutor := &fakeChatGenerationPlanExecutor{
		result: seqFromItems(
			&ark_dal.ModelStreamRespReasoning{ReasoningContent: "先读上下文"},
			&ark_dal.ModelStreamRespReasoning{CapabilityCall: &ark_dal.CapabilityCallTrace{
				CallID:       "call_1",
				FunctionName: "send_message",
				Arguments:    `{"text":"hi"}`,
				Output:       "ok",
			}},
			&ark_dal.ModelStreamRespReasoning{ContentStruct: ark_dal.ContentStruct{Thought: "先读上下文", Reply: "这是最终回复"}},
		),
	}
	setTestChatGenerationPlanExecutor(fakeExecutor)
	defer resetTestChatGenerationPlanExecutor()

	now := time.Date(2026, 3, 18, 15, 10, 0, 0, time.UTC)
	chatID := "oc_chat"
	openID := "ou_actor"
	msgID := "om_runtime_standard"
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
	sent := struct {
		replyText string
		msgID     string
	}{}
	handler := &StandardHandler{
		now: func() time.Time { return now },
		processorBuilder: func(_ context.Context, emitter agentruntime.InitialReplyEmitter) runProcessor {
			processor.initialReplyEmitter = emitter
			return processor
		},
		replySender: func(ctx context.Context, msg *larkim.EventMessage, replyText string) (string, error) {
			sent.replyText = replyText
			if msg != nil && msg.MessageId != nil {
				sent.msgID = *msg.MessageId
			}
			return "om_runtime_standard_reply", nil
		},
	}

	err := handler.Handle(context.Background(), agentruntime.RuntimeStandardCutoverRequest{
		Event: event,
		Plan: agentruntime.ChatGenerationPlan{
			ModelID: "ep-test-standard",
			Size:    20,
			Args:    []string{"帮我总结"},
		},
		StartedAt: now,
	})
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	if len(processor.inputs) != 1 || processor.inputs[0].Initial == nil {
		t.Fatalf("unexpected processor inputs: %+v", processor.inputs)
	}
	if len(processor.initialResults) != 1 {
		t.Fatalf("initial result count = %d, want 1", len(processor.initialResults))
	}
	if sent.replyText != "这是最终回复" {
		t.Fatalf("replyText = %q, want %q", sent.replyText, "这是最终回复")
	}
	if sent.msgID != "om_runtime_standard" {
		t.Fatalf("msgID = %q, want %q", sent.msgID, "om_runtime_standard")
	}
	if processor.initialResults[0].ResponseMessageID != "om_runtime_standard_reply" {
		t.Fatalf("response message id = %q, want %q", processor.initialResults[0].ResponseMessageID, "om_runtime_standard_reply")
	}
	if processor.initialResults[0].ResponseCardID != "" {
		t.Fatalf("response card id = %q, want empty", processor.initialResults[0].ResponseCardID)
	}
	if processor.initialResults[0].DeliveryMode != agentruntime.ReplyDeliveryModeReply {
		t.Fatalf("delivery mode = %q, want %q", processor.initialResults[0].DeliveryMode, agentruntime.ReplyDeliveryModeReply)
	}
	if processor.initialResults[0].ThoughtText != "先读上下文" {
		t.Fatalf("thought text = %q, want %q", processor.initialResults[0].ThoughtText, "先读上下文")
	}
	if processor.initialResults[0].ReplyText != "这是最终回复" {
		t.Fatalf("reply text = %q, want %q", processor.initialResults[0].ReplyText, "这是最终回复")
	}
	if len(processor.initialResults[0].CapabilityCalls) != 1 {
		t.Fatalf("capability call count = %d, want 1", len(processor.initialResults[0].CapabilityCalls))
	}
	if fakeExecutor.calls != 1 {
		t.Fatalf("executor calls = %d, want 1", fakeExecutor.calls)
	}
}

func TestStandardHandlerSkipsSendingWhenReplyIsEmpty(t *testing.T) {
	fakeExecutor := &fakeChatGenerationPlanExecutor{
		result: seqFromItems(
			&ark_dal.ModelStreamRespReasoning{ContentStruct: ark_dal.ContentStruct{Decision: "skip"}},
		),
	}
	setTestChatGenerationPlanExecutor(fakeExecutor)
	defer resetTestChatGenerationPlanExecutor()

	now := time.Date(2026, 3, 18, 15, 11, 0, 0, time.UTC)
	chatID := "oc_chat"
	openID := "ou_actor"
	msgID := "om_runtime_standard_skip"
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
	replySent := false
	handler := &StandardHandler{
		now: func() time.Time { return now },
		processorBuilder: func(_ context.Context, emitter agentruntime.InitialReplyEmitter) runProcessor {
			processor.initialReplyEmitter = emitter
			return processor
		},
		replySender: func(ctx context.Context, msg *larkim.EventMessage, replyText string) (string, error) {
			replySent = true
			return "", nil
		},
	}

	err := handler.Handle(context.Background(), agentruntime.RuntimeStandardCutoverRequest{
		Event: event,
		Plan: agentruntime.ChatGenerationPlan{
			ModelID: "ep-test-standard",
			Size:    20,
		},
		StartedAt: now,
	})
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	if replySent {
		t.Fatal("expected reply sender not to be called")
	}
	if len(processor.initialResults) != 1 {
		t.Fatalf("initial result count = %d, want 1", len(processor.initialResults))
	}
	if processor.initialResults[0].ResponseMessageID != "" {
		t.Fatalf("response message id = %q, want empty", processor.initialResults[0].ResponseMessageID)
	}
}
