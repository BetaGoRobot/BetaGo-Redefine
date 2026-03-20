package agentruntime

import (
	"context"
	"iter"
	"time"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

var (
	runtimeAgenticCutoverBuilder  = func(context.Context) RuntimeAgenticCutoverHandler { return nil }
	runtimeStandardCutoverBuilder = func(context.Context) RuntimeStandardCutoverHandler { return nil }
	runtimeAgenticCardSender      = larkmsg.SendAndUpdateStreamingCard
	runtimeStandardReplySender    = func(ctx context.Context, replyText string, msgID string) error {
		_, err := larkmsg.ReplyMsgText(ctx, replyText, msgID, "_chat_random", false)
		return err
	}
)

func NewDefaultChatEntryHandler() *ChatEntryHandler {
	return NewChatEntryHandler(ChatEntryHandlerOptions{
		AgenticResponder: func(ctx context.Context, req ChatResponseRequest) error {
			return handleAgenticChatResponse(ctx, req.Event, req.Plan, req.RuntimeEnabled, req.CutoverEnabled, req.StartedAt, req.Ownership)
		},
		StandardResponder: func(ctx context.Context, req ChatResponseRequest) error {
			return handleStandardChatResponse(ctx, req.Event, req.Plan, req.RuntimeEnabled, req.CutoverEnabled, req.StartedAt, req.Ownership)
		},
	})
}

func SetRuntimeAgenticCutoverBuilder(builder func(context.Context) RuntimeAgenticCutoverHandler) {
	if builder == nil {
		runtimeAgenticCutoverBuilder = func(context.Context) RuntimeAgenticCutoverHandler { return nil }
		return
	}
	runtimeAgenticCutoverBuilder = builder
}

func SetRuntimeStandardCutoverBuilder(builder func(context.Context) RuntimeStandardCutoverHandler) {
	if builder == nil {
		runtimeStandardCutoverBuilder = func(context.Context) RuntimeStandardCutoverHandler { return nil }
		return
	}
	runtimeStandardCutoverBuilder = builder
}

func handleAgenticChatResponse(
	ctx context.Context,
	event *larkim.P2MessageReceiveV1,
	plan ChatGenerationPlan,
	runtimeEnabled bool,
	cutoverEnabled bool,
	startedAt time.Time,
	ownership InitialRunOwnership,
) error {
	plan.Mode = appconfig.ChatModeAgentic.Normalize()
	if runtimeEnabled && cutoverEnabled {
		if runtimeHandler := runtimeAgenticCutoverBuilder(ctx); runtimeHandler != nil {
			return runtimeHandler.Handle(ctx, RuntimeAgenticCutoverRequest{
				Event:     event,
				Plan:      cloneChatGenerationPlan(plan),
				StartedAt: startedAt,
				Ownership: ownership,
			})
		}
	}
	msgSeq, err := plan.Generate(ctx, event)
	if err != nil {
		return err
	}
	return runtimeAgenticCardSender(ctx, event.Event.Message, msgSeq)
}

func handleStandardChatResponse(
	ctx context.Context,
	event *larkim.P2MessageReceiveV1,
	plan ChatGenerationPlan,
	runtimeEnabled bool,
	cutoverEnabled bool,
	startedAt time.Time,
	ownership InitialRunOwnership,
) error {
	plan.Mode = appconfig.ChatModeStandard.Normalize()
	if runtimeEnabled && cutoverEnabled {
		if runtimeHandler := runtimeStandardCutoverBuilder(ctx); runtimeHandler != nil {
			return runtimeHandler.Handle(ctx, RuntimeStandardCutoverRequest{
				Event:     event,
				Plan:      cloneChatGenerationPlan(plan),
				StartedAt: startedAt,
				Ownership: ownership,
			})
		}
	}

	msgSeq, err := plan.Generate(ctx, event)
	if err != nil {
		return err
	}
	reply, skip := collectStandardChatReply(msgSeq)
	if skip || event == nil || event.Event == nil || event.Event.Message == nil || event.Event.Message.MessageId == nil {
		return nil
	}
	return runtimeStandardReplySender(ctx, reply, *event.Event.Message.MessageId)
}

func cloneChatGenerationPlan(plan ChatGenerationPlan) ChatGenerationPlan {
	plan.Files = append([]string(nil), plan.Files...)
	plan.Args = append([]string(nil), plan.Args...)
	return plan
}

func collectStandardChatReply(msgSeq iter.Seq[*ark_dal.ModelStreamRespReasoning]) (reply string, skip bool) {
	lastData := &ark_dal.ModelStreamRespReasoning{}
	for data := range msgSeq {
		if data == nil {
			continue
		}
		lastData = data
		if lastData.ContentStruct.Decision == "skip" {
			return "", true
		}
	}
	return lastData.ContentStruct.Reply, false
}
