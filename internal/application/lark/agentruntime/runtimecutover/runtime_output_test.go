package runtimecutover

import (
	"context"
	"iter"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestReplyOrchestratorEmitAgenticStreamsCardAndReturnsSnapshot(t *testing.T) {
	msgID := "om_runtime_output"
	chatID := "oc_chat"
	msg := &larkim.EventMessage{
		MessageId: &msgID,
		ChatId:    &chatID,
	}

	sent := make([]*ark_dal.ModelStreamRespReasoning, 0)
	orchestrator := &replyOrchestrator{
		agenticSender: func(ctx context.Context, msg *larkim.EventMessage, seq iter.Seq[*ark_dal.ModelStreamRespReasoning], opts ...larkmsg.AgentStreamingCardOptions) (larkmsg.AgentStreamingCardRefs, error) {
			if len(opts) != 0 {
				t.Fatalf("agentic sender options = %+v, want empty for plain emit()", opts)
			}
			for item := range seq {
				sent = append(sent, item)
			}
			return larkmsg.AgentStreamingCardRefs{
				MessageID: "om_runtime_reply",
				CardID:    "card_runtime_reply",
			}, nil
		},
	}

	result, err := orchestrator.emit(context.Background(), replyOutputRequest{
		Mode:    replyOutputModeAgentic,
		Message: msg,
		Stream: seqFromItems(
			&ark_dal.ModelStreamRespReasoning{ReasoningContent: "先读上下文"},
			&ark_dal.ModelStreamRespReasoning{CapabilityCall: &ark_dal.CapabilityCallTrace{
				CallID:       "call_1",
				FunctionName: "send_message",
				Arguments:    `{"text":"hi"}`,
				Output:       "ok",
			}},
			&ark_dal.ModelStreamRespReasoning{ContentStruct: ark_dal.ContentStruct{Thought: "先读上下文", Reply: "这是最终回复"}},
		),
	})
	if err != nil {
		t.Fatalf("emit() error = %v", err)
	}

	if result.Refs.MessageID != "om_runtime_reply" || result.Refs.CardID != "card_runtime_reply" {
		t.Fatalf("unexpected refs: %+v", result.Refs)
	}
	if result.DeliveryMode != agentruntime.ReplyDeliveryModeCreate {
		t.Fatalf("delivery mode = %q, want %q", result.DeliveryMode, agentruntime.ReplyDeliveryModeCreate)
	}
	if result.Reply.ThoughtText != "先读上下文" {
		t.Fatalf("thought text = %q, want %q", result.Reply.ThoughtText, "先读上下文")
	}
	if result.Reply.ReplyText != "这是最终回复" {
		t.Fatalf("reply text = %q, want %q", result.Reply.ReplyText, "这是最终回复")
	}
	if len(result.Reply.CapabilityCalls) != 1 {
		t.Fatalf("capability call count = %d, want 1", len(result.Reply.CapabilityCalls))
	}
	if len(sent) != 3 {
		t.Fatalf("sent item count = %d, want 3", len(sent))
	}
}

func TestReplyOrchestratorEmitStandardRepliesTextAndReturnsMessageRef(t *testing.T) {
	msgID := "om_runtime_output_standard"
	chatID := "oc_chat"
	msg := &larkim.EventMessage{
		MessageId: &msgID,
		ChatId:    &chatID,
	}

	sent := struct {
		replyText string
	}{}
	orchestrator := &replyOrchestrator{
		standardSender: func(ctx context.Context, msg *larkim.EventMessage, replyText string) (string, error) {
			sent.replyText = replyText
			return "om_runtime_text_reply", nil
		},
	}

	result, err := orchestrator.emit(context.Background(), replyOutputRequest{
		Mode:    replyOutputModeStandard,
		Message: msg,
		Stream: seqFromItems(
			&ark_dal.ModelStreamRespReasoning{ReasoningContent: "先读上下文"},
			&ark_dal.ModelStreamRespReasoning{CapabilityCall: &ark_dal.CapabilityCallTrace{
				CallID:       "call_1",
				FunctionName: "send_message",
				Arguments:    `{"text":"hi"}`,
				Output:       "ok",
			}},
			&ark_dal.ModelStreamRespReasoning{ContentStruct: ark_dal.ContentStruct{Thought: "先读上下文", Reply: "这是最终回复"}},
		),
	})
	if err != nil {
		t.Fatalf("emit() error = %v", err)
	}

	if sent.replyText != "这是最终回复" {
		t.Fatalf("replyText = %q, want %q", sent.replyText, "这是最终回复")
	}
	if result.Refs.MessageID != "om_runtime_text_reply" {
		t.Fatalf("message id = %q, want %q", result.Refs.MessageID, "om_runtime_text_reply")
	}
	if result.Refs.CardID != "" {
		t.Fatalf("card id = %q, want empty", result.Refs.CardID)
	}
	if result.DeliveryMode != agentruntime.ReplyDeliveryModeReply {
		t.Fatalf("delivery mode = %q, want %q", result.DeliveryMode, agentruntime.ReplyDeliveryModeReply)
	}
	if result.Reply.ThoughtText != "先读上下文" {
		t.Fatalf("thought text = %q, want %q", result.Reply.ThoughtText, "先读上下文")
	}
	if len(result.Reply.CapabilityCalls) != 1 {
		t.Fatalf("capability call count = %d, want 1", len(result.Reply.CapabilityCalls))
	}
}

func TestReplyOrchestratorEmitStandardSkipsEmptyReply(t *testing.T) {
	msgID := "om_runtime_output_standard_skip"
	chatID := "oc_chat"
	msg := &larkim.EventMessage{
		MessageId: &msgID,
		ChatId:    &chatID,
	}

	replySent := false
	orchestrator := &replyOrchestrator{
		standardSender: func(ctx context.Context, msg *larkim.EventMessage, replyText string) (string, error) {
			replySent = true
			return "", nil
		},
	}

	result, err := orchestrator.emit(context.Background(), replyOutputRequest{
		Mode:    replyOutputModeStandard,
		Message: msg,
		Stream: seqFromItems(
			&ark_dal.ModelStreamRespReasoning{ContentStruct: ark_dal.ContentStruct{Decision: "skip"}},
		),
	})
	if err != nil {
		t.Fatalf("emit() error = %v", err)
	}

	if replySent {
		t.Fatal("expected standard sender not to be called")
	}
	if result.Refs.MessageID != "" || result.Refs.CardID != "" {
		t.Fatalf("expected empty refs, got %+v", result.Refs)
	}
}

func TestReplyOrchestratorEmitAgenticPatchesExistingCardWhenTargetExists(t *testing.T) {
	msgID := "om_runtime_output_patch"
	chatID := "oc_chat"
	msg := &larkim.EventMessage{
		MessageId: &msgID,
		ChatId:    &chatID,
	}

	patchCalls := 0
	orchestrator := &replyOrchestrator{
		agenticPatcher: func(ctx context.Context, refs larkmsg.AgentStreamingCardRefs, seq iter.Seq[*ark_dal.ModelStreamRespReasoning]) (larkmsg.AgentStreamingCardRefs, error) {
			patchCalls++
			for range seq {
			}
			return refs, nil
		},
	}

	result, err := orchestrator.emit(context.Background(), replyOutputRequest{
		Mode:            replyOutputModeAgentic,
		Message:         msg,
		TargetMode:      agentruntime.InitialReplyTargetModePatch,
		TargetMessageID: "om_existing_reply",
		TargetCardID:    "card_existing_reply",
		Stream: seqFromItems(
			&ark_dal.ModelStreamRespReasoning{ContentStruct: ark_dal.ContentStruct{Thought: "继续执行", Reply: "已更新原卡片"}},
		),
	})
	if err != nil {
		t.Fatalf("emit() error = %v", err)
	}

	if patchCalls != 1 {
		t.Fatalf("patch calls = %d, want 1", patchCalls)
	}
	if result.DeliveryMode != agentruntime.ReplyDeliveryModePatch {
		t.Fatalf("delivery mode = %q, want %q", result.DeliveryMode, agentruntime.ReplyDeliveryModePatch)
	}
	if result.TargetMessageID != "om_existing_reply" {
		t.Fatalf("target message id = %q, want %q", result.TargetMessageID, "om_existing_reply")
	}
	if result.TargetCardID != "card_existing_reply" {
		t.Fatalf("target card id = %q, want %q", result.TargetCardID, "card_existing_reply")
	}
}

func TestReplyOrchestratorEmitAgenticRepliesInThreadForSideEffectWhenReplyTargetExists(t *testing.T) {
	msgID := "om_runtime_output_thread"
	chatID := "oc_chat"
	msg := &larkim.EventMessage{
		MessageId: &msgID,
		ChatId:    &chatID,
	}

	replyCalls := 0
	replyMsgID := ""
	replyInThread := false
	orchestrator := &replyOrchestrator{
		agenticReplier: func(ctx context.Context, msg *larkim.EventMessage, seq iter.Seq[*ark_dal.ModelStreamRespReasoning], inThread bool, opts ...larkmsg.AgentStreamingCardOptions) (larkmsg.AgentStreamingCardRefs, error) {
			replyCalls++
			replyMsgID = *msg.MessageId
			replyInThread = inThread
			for range seq {
			}
			return larkmsg.AgentStreamingCardRefs{
				MessageID: "om_thread_reply",
				CardID:    "card_thread_reply",
			}, nil
		},
	}

	result, err := orchestrator.emit(context.Background(), replyOutputRequest{
		OutputKind:      agentruntime.AgenticOutputKindSideEffect,
		Mode:            replyOutputModeAgentic,
		Message:         msg,
		TargetMode:      agentruntime.InitialReplyTargetModeReply,
		TargetMessageID: "om_runtime_output_thread",
		ReplyInThread:   true,
		Stream: seqFromItems(
			&ark_dal.ModelStreamRespReasoning{ContentStruct: ark_dal.ContentStruct{Reply: "线程内继续回复"}},
		),
	})
	if err != nil {
		t.Fatalf("emit() error = %v", err)
	}

	if replyCalls != 1 {
		t.Fatalf("reply calls = %d, want 1", replyCalls)
	}
	if replyMsgID != "om_runtime_output_thread" {
		t.Fatalf("reply message id = %q, want %q", replyMsgID, "om_runtime_output_thread")
	}
	if !replyInThread {
		t.Fatal("expected reply_in_thread to be true")
	}
	if result.DeliveryMode != agentruntime.ReplyDeliveryModeReply {
		t.Fatalf("delivery mode = %q, want %q", result.DeliveryMode, agentruntime.ReplyDeliveryModeReply)
	}
	if result.Refs.MessageID != "om_thread_reply" || result.Refs.CardID != "card_thread_reply" {
		t.Fatalf("unexpected refs: %+v", result.Refs)
	}
	if result.TargetMessageID != "om_runtime_output_thread" {
		t.Fatalf("target message id = %q, want %q", result.TargetMessageID, "om_runtime_output_thread")
	}
}

func TestReplyOrchestratorEmitStandardPatchesExistingMessageWhenTargetExists(t *testing.T) {
	msgID := "om_runtime_output_patch_text"
	chatID := "oc_chat"
	msg := &larkim.EventMessage{
		MessageId: &msgID,
		ChatId:    &chatID,
	}

	patched := struct {
		messageID string
		text      string
	}{}
	orchestrator := &replyOrchestrator{
		standardPatcher: func(ctx context.Context, messageID, text string) error {
			patched.messageID = messageID
			patched.text = text
			return nil
		},
	}

	result, err := orchestrator.emit(context.Background(), replyOutputRequest{
		Mode:            replyOutputModeStandard,
		Message:         msg,
		TargetMessageID: "om_existing_text_reply",
		Stream: seqFromItems(
			&ark_dal.ModelStreamRespReasoning{ContentStruct: ark_dal.ContentStruct{Reply: "已更新原消息"}},
		),
	})
	if err != nil {
		t.Fatalf("emit() error = %v", err)
	}

	if patched.messageID != "om_existing_text_reply" {
		t.Fatalf("patched message id = %q, want %q", patched.messageID, "om_existing_text_reply")
	}
	if patched.text != "已更新原消息" {
		t.Fatalf("patched text = %q, want %q", patched.text, "已更新原消息")
	}
	if result.DeliveryMode != agentruntime.ReplyDeliveryModePatch {
		t.Fatalf("delivery mode = %q, want %q", result.DeliveryMode, agentruntime.ReplyDeliveryModePatch)
	}
	if result.TargetMessageID != "om_existing_text_reply" {
		t.Fatalf("target message id = %q, want %q", result.TargetMessageID, "om_existing_text_reply")
	}
}
