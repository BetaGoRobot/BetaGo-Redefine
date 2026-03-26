package initial_test

import (
	"context"
	"iter"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	initialcore "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime/initial"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestLarkInitialReplyEmitterPatchesExistingAgenticCardAndReturnsSnapshot(t *testing.T) {
	msgID := "om_initial_reply"
	chatID := "oc_chat"
	msg := &larkim.EventMessage{
		MessageId: &msgID,
		ChatId:    &chatID,
	}

	patchCalls := 0
	emitter := initialcore.NewLarkInitialReplyEmitterForTest(
		nil,
		nil,
		func(ctx context.Context, refs larkmsg.AgentStreamingCardRefs, seq iter.Seq[*ark_dal.ModelStreamRespReasoning]) (larkmsg.AgentStreamingCardRefs, error) {
			patchCalls++
			for range seq {
			}
			return refs, nil
		},
		nil,
		nil,
	)

	result, err := emitter.EmitInitialReply(context.Background(), agentruntime.InitialReplyEmissionRequest{
		OutputKind:      agentruntime.AgenticOutputKindModelReply,
		Mode:            agentruntime.InitialReplyOutputModeAgentic,
		MentionOpenID:   "ou_actor",
		Message:         msg,
		TargetMode:      agentruntime.InitialReplyTargetModePatch,
		TargetMessageID: "om_existing_reply",
		TargetCardID:    "card_existing_reply",
		Stream: seqFromEmitterItems(
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
		t.Fatalf("EmitInitialReply() error = %v", err)
	}
	if patchCalls != 1 {
		t.Fatalf("patch calls = %d, want 1", patchCalls)
	}
	if result.DeliveryMode != agentruntime.ReplyDeliveryModePatch {
		t.Fatalf("delivery mode = %q, want %q", result.DeliveryMode, agentruntime.ReplyDeliveryModePatch)
	}
	if result.ResponseMessageID != "om_existing_reply" || result.ResponseCardID != "card_existing_reply" {
		t.Fatalf("unexpected refs: %+v", result)
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
}

func TestLarkInitialReplyEmitterSendsStandardReply(t *testing.T) {
	msgID := "om_initial_text"
	chatID := "oc_chat"
	msg := &larkim.EventMessage{
		MessageId: &msgID,
		ChatId:    &chatID,
	}

	sentReply := ""
	emitter := initialcore.NewLarkInitialReplyEmitterForTest(
		nil,
		nil,
		nil,
		func(ctx context.Context, msg *larkim.EventMessage, replyText string) (string, error) {
			sentReply = replyText
			return "om_text_reply", nil
		},
		nil,
	)

	result, err := emitter.EmitInitialReply(context.Background(), agentruntime.InitialReplyEmissionRequest{
		Mode:    agentruntime.InitialReplyOutputModeStandard,
		Message: msg,
		Stream: seqFromEmitterItems(
			&ark_dal.ModelStreamRespReasoning{ReasoningContent: "先读上下文"},
			&ark_dal.ModelStreamRespReasoning{ContentStruct: ark_dal.ContentStruct{Thought: "先读上下文", Reply: "这是最终回复"}},
		),
	})
	if err != nil {
		t.Fatalf("EmitInitialReply() error = %v", err)
	}
	if sentReply != "这是最终回复" {
		t.Fatalf("sent reply = %q, want %q", sentReply, "这是最终回复")
	}
	if result.ResponseMessageID != "om_text_reply" {
		t.Fatalf("response message id = %q, want %q", result.ResponseMessageID, "om_text_reply")
	}
	if result.DeliveryMode != agentruntime.ReplyDeliveryModeReply {
		t.Fatalf("delivery mode = %q, want %q", result.DeliveryMode, agentruntime.ReplyDeliveryModeReply)
	}
	if result.Reply.ThoughtText != "先读上下文" {
		t.Fatalf("thought text = %q, want %q", result.Reply.ThoughtText, "先读上下文")
	}
}

func seqFromEmitterItems(items ...*ark_dal.ModelStreamRespReasoning) iter.Seq[*ark_dal.ModelStreamRespReasoning] {
	return func(yield func(*ark_dal.ModelStreamRespReasoning) bool) {
		for _, item := range items {
			if !yield(item) {
				return
			}
		}
	}
}
