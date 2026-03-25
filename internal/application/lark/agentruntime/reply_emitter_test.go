package agentruntime_test

import (
	"context"
	"iter"
	"testing"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestLarkReplyEmitterUsesAgenticSenderInAgenticMode(t *testing.T) {
	emitter := agentruntime.NewLarkReplyEmitterForTest(
		func(context.Context, string, string) appconfig.ChatMode { return appconfig.ChatModeAgentic },
		func(ctx context.Context, msg *larkim.EventMessage, seq iter.Seq[*ark_dal.ModelStreamRespReasoning], opts ...larkmsg.AgentStreamingCardOptions) (larkmsg.AgentStreamingCardRefs, error) {
			items := make([]*ark_dal.ModelStreamRespReasoning, 0)
			for item := range seq {
				items = append(items, item)
			}
			if len(opts) != 1 || opts[0].MentionOpenID != "ou_actor" {
				t.Fatalf("agentic sender options = %+v, want mention open id ou_actor", opts)
			}
			if len(items) != 1 {
				t.Fatalf("item count = %d, want 1", len(items))
			}
			if items[0].ContentStruct.Thought != "先读上下文" {
				t.Fatalf("thought = %q, want %q", items[0].ContentStruct.Thought, "先读上下文")
			}
			if items[0].ContentStruct.Reply != "这是最终回复" {
				t.Fatalf("reply = %q, want %q", items[0].ContentStruct.Reply, "这是最终回复")
			}
			return larkmsg.AgentStreamingCardRefs{
				MessageID: "om_agentic_reply",
				CardID:    "card_agentic_reply",
			}, nil
		},
		func(context.Context, *larkim.EventMessage, iter.Seq[*ark_dal.ModelStreamRespReasoning], bool, ...larkmsg.AgentStreamingCardOptions) (larkmsg.AgentStreamingCardRefs, error) {
			t.Fatal("reply path should not be used without target message")
			return larkmsg.AgentStreamingCardRefs{}, nil
		},
		func(context.Context, larkmsg.AgentStreamingCardRefs, iter.Seq[*ark_dal.ModelStreamRespReasoning]) (larkmsg.AgentStreamingCardRefs, error) {
			t.Fatal("patch path should not be used without target card")
			return larkmsg.AgentStreamingCardRefs{}, nil
		},
		func(context.Context, string, string, string, bool) (string, error) {
			t.Fatal("text reply path should not be used in agentic mode")
			return "", nil
		},
		func(context.Context, string, string, string, string) (string, error) {
			t.Fatal("text create path should not be used in agentic mode")
			return "", nil
		},
		func(context.Context, string, string) error {
			t.Fatal("text patch path should not be used without target message")
			return nil
		},
	)

	result, err := emitter.EmitReply(context.Background(), agentruntime.ReplyEmissionRequest{
		Session:     &agentruntime.AgentSession{ChatID: "oc_chat"},
		Run:         &agentruntime.AgentRun{ID: "run_agentic", TriggerMessageID: "om_trigger", ActorOpenID: "ou_actor"},
		ThoughtText: "先读上下文",
		ReplyText:   "这是最终回复",
	})
	if err != nil {
		t.Fatalf("EmitReply() error = %v", err)
	}
	if result.MessageID != "om_agentic_reply" || result.CardID != "card_agentic_reply" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if result.DeliveryMode != agentruntime.ReplyDeliveryModeCreate {
		t.Fatalf("delivery mode = %q, want %q", result.DeliveryMode, agentruntime.ReplyDeliveryModeCreate)
	}
}

func TestLarkReplyEmitterUsesTextReplyInStandardMode(t *testing.T) {
	emitter := agentruntime.NewLarkReplyEmitterForTest(
		func(context.Context, string, string) appconfig.ChatMode { return appconfig.ChatModeStandard },
		func(context.Context, *larkim.EventMessage, iter.Seq[*ark_dal.ModelStreamRespReasoning], ...larkmsg.AgentStreamingCardOptions) (larkmsg.AgentStreamingCardRefs, error) {
			t.Fatal("agentic path should not be used in standard mode")
			return larkmsg.AgentStreamingCardRefs{}, nil
		},
		func(context.Context, *larkim.EventMessage, iter.Seq[*ark_dal.ModelStreamRespReasoning], bool, ...larkmsg.AgentStreamingCardOptions) (larkmsg.AgentStreamingCardRefs, error) {
			t.Fatal("agentic reply path should not be used in standard mode")
			return larkmsg.AgentStreamingCardRefs{}, nil
		},
		func(context.Context, larkmsg.AgentStreamingCardRefs, iter.Seq[*ark_dal.ModelStreamRespReasoning]) (larkmsg.AgentStreamingCardRefs, error) {
			t.Fatal("agentic patch path should not be used in standard mode")
			return larkmsg.AgentStreamingCardRefs{}, nil
		},
		func(ctx context.Context, text, msgID, suffix string, replyInThread bool) (string, error) {
			if text != "这是最终回复" {
				t.Fatalf("text = %q, want %q", text, "这是最终回复")
			}
			if msgID != "om_trigger" {
				t.Fatalf("msgID = %q, want %q", msgID, "om_trigger")
			}
			return "om_text_reply", nil
		},
		func(context.Context, string, string, string, string) (string, error) {
			t.Fatal("text create path should not be used when trigger exists")
			return "", nil
		},
		func(context.Context, string, string) error {
			t.Fatal("text patch path should not be used without target message")
			return nil
		},
	)

	result, err := emitter.EmitReply(context.Background(), agentruntime.ReplyEmissionRequest{
		Session:   &agentruntime.AgentSession{ChatID: "oc_chat"},
		Run:       &agentruntime.AgentRun{ID: "run_standard", TriggerMessageID: "om_trigger", ActorOpenID: "ou_actor"},
		ReplyText: "这是最终回复",
	})
	if err != nil {
		t.Fatalf("EmitReply() error = %v", err)
	}
	if result.MessageID != "om_text_reply" || result.CardID != "" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if result.DeliveryMode != agentruntime.ReplyDeliveryModeReply {
		t.Fatalf("delivery mode = %q, want %q", result.DeliveryMode, agentruntime.ReplyDeliveryModeReply)
	}
}

func TestLarkReplyEmitterPatchesExistingAgenticCardWhenTargetCardProvided(t *testing.T) {
	emitter := agentruntime.NewLarkReplyEmitterForTest(
		func(context.Context, string, string) appconfig.ChatMode { return appconfig.ChatModeAgentic },
		func(context.Context, *larkim.EventMessage, iter.Seq[*ark_dal.ModelStreamRespReasoning], ...larkmsg.AgentStreamingCardOptions) (larkmsg.AgentStreamingCardRefs, error) {
			t.Fatal("create sender should not be used when target card exists")
			return larkmsg.AgentStreamingCardRefs{}, nil
		},
		func(context.Context, *larkim.EventMessage, iter.Seq[*ark_dal.ModelStreamRespReasoning], bool, ...larkmsg.AgentStreamingCardOptions) (larkmsg.AgentStreamingCardRefs, error) {
			t.Fatal("reply path should not be used when patch target exists")
			return larkmsg.AgentStreamingCardRefs{}, nil
		},
		func(ctx context.Context, refs larkmsg.AgentStreamingCardRefs, seq iter.Seq[*ark_dal.ModelStreamRespReasoning]) (larkmsg.AgentStreamingCardRefs, error) {
			if refs.MessageID != "om_existing_reply" {
				t.Fatalf("message id = %q, want %q", refs.MessageID, "om_existing_reply")
			}
			if refs.CardID != "card_existing_reply" {
				t.Fatalf("card id = %q, want %q", refs.CardID, "card_existing_reply")
			}
			items := make([]*ark_dal.ModelStreamRespReasoning, 0)
			for item := range seq {
				items = append(items, item)
			}
			if len(items) != 1 {
				t.Fatalf("item count = %d, want 1", len(items))
			}
			if items[0].ContentStruct.Thought != "复用已有卡片" {
				t.Fatalf("thought = %q, want %q", items[0].ContentStruct.Thought, "复用已有卡片")
			}
			if items[0].ContentStruct.Reply != "这是续写结果" {
				t.Fatalf("reply = %q, want %q", items[0].ContentStruct.Reply, "这是续写结果")
			}
			return refs, nil
		},
		func(context.Context, string, string, string, bool) (string, error) {
			t.Fatal("text reply path should not be used in agentic mode")
			return "", nil
		},
		func(context.Context, string, string, string, string) (string, error) {
			t.Fatal("text create path should not be used in agentic mode")
			return "", nil
		},
		func(context.Context, string, string) error {
			t.Fatal("text patch path should not be used in agentic mode")
			return nil
		},
	)

	result, err := emitter.EmitReply(context.Background(), agentruntime.ReplyEmissionRequest{
		Session:         &agentruntime.AgentSession{ChatID: "oc_chat"},
		Run:             &agentruntime.AgentRun{ID: "run_agentic", TriggerMessageID: "om_trigger", ActorOpenID: "ou_actor"},
		ThoughtText:     "复用已有卡片",
		ReplyText:       "这是续写结果",
		TargetMessageID: "om_existing_reply",
		TargetCardID:    "card_existing_reply",
	})
	if err != nil {
		t.Fatalf("EmitReply() error = %v", err)
	}
	if result.MessageID != "om_existing_reply" || result.CardID != "card_existing_reply" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if result.DeliveryMode != agentruntime.ReplyDeliveryModePatch {
		t.Fatalf("delivery mode = %q, want %q", result.DeliveryMode, agentruntime.ReplyDeliveryModePatch)
	}
}

func TestLarkReplyEmitterPatchesExistingStandardMessageWhenTargetMessageProvided(t *testing.T) {
	emitter := agentruntime.NewLarkReplyEmitterForTest(
		func(context.Context, string, string) appconfig.ChatMode { return appconfig.ChatModeStandard },
		func(context.Context, *larkim.EventMessage, iter.Seq[*ark_dal.ModelStreamRespReasoning], ...larkmsg.AgentStreamingCardOptions) (larkmsg.AgentStreamingCardRefs, error) {
			t.Fatal("agentic create path should not be used in standard mode")
			return larkmsg.AgentStreamingCardRefs{}, nil
		},
		func(context.Context, *larkim.EventMessage, iter.Seq[*ark_dal.ModelStreamRespReasoning], bool, ...larkmsg.AgentStreamingCardOptions) (larkmsg.AgentStreamingCardRefs, error) {
			t.Fatal("agentic reply path should not be used in standard mode")
			return larkmsg.AgentStreamingCardRefs{}, nil
		},
		func(context.Context, larkmsg.AgentStreamingCardRefs, iter.Seq[*ark_dal.ModelStreamRespReasoning]) (larkmsg.AgentStreamingCardRefs, error) {
			t.Fatal("agentic patch path should not be used in standard mode")
			return larkmsg.AgentStreamingCardRefs{}, nil
		},
		func(context.Context, string, string, string, bool) (string, error) {
			t.Fatal("text reply path should not be used when target message exists")
			return "", nil
		},
		func(context.Context, string, string, string, string) (string, error) {
			t.Fatal("text create path should not be used when target message exists")
			return "", nil
		},
		func(ctx context.Context, msgID, text string) error {
			if msgID != "om_existing_text_reply" {
				t.Fatalf("msgID = %q, want %q", msgID, "om_existing_text_reply")
			}
			if text != "这是更新后的正文" {
				t.Fatalf("text = %q, want %q", text, "这是更新后的正文")
			}
			return nil
		},
	)

	result, err := emitter.EmitReply(context.Background(), agentruntime.ReplyEmissionRequest{
		Session:         &agentruntime.AgentSession{ChatID: "oc_chat"},
		Run:             &agentruntime.AgentRun{ID: "run_standard", TriggerMessageID: "om_trigger", ActorOpenID: "ou_actor"},
		ReplyText:       "这是更新后的正文",
		TargetMessageID: "om_existing_text_reply",
	})
	if err != nil {
		t.Fatalf("EmitReply() error = %v", err)
	}
	if result.MessageID != "om_existing_text_reply" || result.CardID != "" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if result.DeliveryMode != agentruntime.ReplyDeliveryModePatch {
		t.Fatalf("delivery mode = %q, want %q", result.DeliveryMode, agentruntime.ReplyDeliveryModePatch)
	}
}

func TestLarkReplyEmitterRepliesInThreadForAgenticSideEffect(t *testing.T) {
	replyCalled := false
	replyInThread := false
	replyMsgID := ""

	emitter := agentruntime.NewLarkReplyEmitterForTest(
		func(context.Context, string, string) appconfig.ChatMode { return appconfig.ChatModeAgentic },
		func(context.Context, *larkim.EventMessage, iter.Seq[*ark_dal.ModelStreamRespReasoning], ...larkmsg.AgentStreamingCardOptions) (larkmsg.AgentStreamingCardRefs, error) {
			t.Fatal("create sender should not be used when follow-up thread target exists")
			return larkmsg.AgentStreamingCardRefs{}, nil
		},
		func(ctx context.Context, msg *larkim.EventMessage, seq iter.Seq[*ark_dal.ModelStreamRespReasoning], inThread bool, opts ...larkmsg.AgentStreamingCardOptions) (larkmsg.AgentStreamingCardRefs, error) {
			replyCalled = true
			replyInThread = inThread
			if msg == nil || msg.MessageId == nil {
				t.Fatal("expected reply message")
			}
			replyMsgID = *msg.MessageId
			for range seq {
			}
			return larkmsg.AgentStreamingCardRefs{
				MessageID: "om_followup_reply",
				CardID:    "card_followup_reply",
			}, nil
		},
		func(context.Context, larkmsg.AgentStreamingCardRefs, iter.Seq[*ark_dal.ModelStreamRespReasoning]) (larkmsg.AgentStreamingCardRefs, error) {
			t.Fatal("patch path should not be used for follow-up thread reply")
			return larkmsg.AgentStreamingCardRefs{}, nil
		},
		func(context.Context, string, string, string, bool) (string, error) {
			t.Fatal("text reply path should not be used in agentic mode")
			return "", nil
		},
		func(context.Context, string, string, string, string) (string, error) {
			t.Fatal("text create path should not be used in agentic mode")
			return "", nil
		},
		func(context.Context, string, string) error {
			t.Fatal("text patch path should not be used in agentic mode")
			return nil
		},
	)

	result, err := emitter.EmitReply(context.Background(), agentruntime.ReplyEmissionRequest{
		Session:         &agentruntime.AgentSession{ChatID: "oc_chat"},
		Run:             &agentruntime.AgentRun{ID: "run_agentic_followup", TriggerMessageID: "om_trigger", ActorOpenID: "ou_actor"},
		OutputKind:      agentruntime.AgenticOutputKindSideEffect,
		ThoughtText:     "继续处理",
		ReplyText:       "这是线程续写",
		TargetMessageID: "om_root_agentic",
		TargetCardID:    "card_root_agentic",
		ReplyInThread:   true,
	})
	if err != nil {
		t.Fatalf("EmitReply() error = %v", err)
	}
	if !replyCalled {
		t.Fatal("expected agentic reply path to be used")
	}
	if !replyInThread {
		t.Fatal("expected follow-up reply to force in-thread delivery")
	}
	if replyMsgID != "om_root_agentic" {
		t.Fatalf("reply target message id = %q, want %q", replyMsgID, "om_root_agentic")
	}
	if result.MessageID != "om_followup_reply" || result.CardID != "card_followup_reply" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if result.DeliveryMode != agentruntime.ReplyDeliveryModeReply {
		t.Fatalf("delivery mode = %q, want %q", result.DeliveryMode, agentruntime.ReplyDeliveryModeReply)
	}
}
