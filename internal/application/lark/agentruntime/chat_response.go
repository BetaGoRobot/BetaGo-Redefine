package agentruntime

import (
	"context"
	"time"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

var (
	runtimeAgenticCutoverBuilder = func(context.Context) RuntimeAgenticCutoverHandler { return nil }
	runtimeAgenticCardSender     = larkmsg.SendAndUpdateStreamingCard
)

func SetRuntimeAgenticCutoverBuilder(builder func(context.Context) RuntimeAgenticCutoverHandler) {
	if builder == nil {
		runtimeAgenticCutoverBuilder = func(context.Context) RuntimeAgenticCutoverHandler { return nil }
		return
	}
	runtimeAgenticCutoverBuilder = builder
}

func handleAgenticChatResponse(
	ctx context.Context,
	event *larkim.P2MessageReceiveV1,
	plan ChatGenerationPlan,
	startedAt time.Time,
	ownership InitialRunOwnership,
) error {
	plan.Mode = appconfig.ChatModeAgentic.Normalize()
	if runtimeHandler := runtimeAgenticCutoverBuilder(ctx); runtimeHandler != nil {
		return runtimeHandler.Handle(ctx, RuntimeAgenticCutoverRequest{
			Event:     event,
			Plan:      cloneChatGenerationPlan(plan),
			StartedAt: startedAt,
			Ownership: ownership,
		})
	}
	msgSeq, err := plan.Generate(ctx, event)
	if err != nil {
		return err
	}
	return runtimeAgenticCardSender(ctx, event.Event.Message, msgSeq)
}

func cloneChatGenerationPlan(plan ChatGenerationPlan) ChatGenerationPlan {
	plan.Files = append([]string(nil), plan.Files...)
	plan.Args = append([]string(nil), plan.Args...)
	return plan
}
