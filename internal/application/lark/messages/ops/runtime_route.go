package ops

import (
	"context"
	"strings"
	"time"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xcommand"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func observeRuntimeMessage(ctx context.Context, event *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData) (agentruntime.ShadowObservation, bool) {
	accessor := messageConfigAccessor(ctx, event, meta)
	if accessor == nil {
		return agentruntime.ShadowObservation{}, false
	}
	if accessor.ChatMode().Normalize() != appconfig.ChatModeAgentic {
		return agentruntime.ShadowObservation{}, false
	}

	observer := agentruntime.NewShadowObserver(
		agentruntime.NewDefaultGroupPolicy(agentruntime.DefaultGroupPolicyConfig{}),
		nil,
		func(ctx context.Context, chatID string) *agentruntime.ActiveRunSnapshot {
			return runtimeActiveRunSnapshot(ctx, chatID)
		},
	)
	observation := observer.Observe(ctx, agentruntime.ShadowObserveInput{
		Now:         time.Now().UTC(),
		ChatID:      messageChatID(event, meta),
		ChatType:    currentChatType(event),
		Mentioned:   runtimeIsMentioned(event),
		ReplyToBot:  detectReplyToCurrentBot(ctx, event),
		IsCommand:   isCommandMessage(ctx, event),
		CommandName: runtimeCommandName(ctx, event),
		ActorOpenID: messageOpenID(event, meta),
		InputText:   strings.TrimSpace(messageText(ctx, event)),
	})
	if !observation.EnterRuntime {
		return observation, false
	}
	return observation, true
}

func runtimeIsMentioned(event *larkim.P2MessageReceiveV1) bool {
	if event == nil || event.Event == nil || event.Event.Message == nil {
		return false
	}
	return larkmsg.IsMentioned(event.Event.Message.Mentions)
}

func runtimeCommandName(ctx context.Context, event *larkim.P2MessageReceiveV1) string {
	if !isCommandMessage(ctx, event) {
		return ""
	}
	parts := xcommand.GetCommand(ctx, messageText(ctx, event))
	if len(parts) == 0 {
		return ""
	}
	return strings.TrimSpace(parts[0])
}

func runtimeActiveRunSnapshot(ctx context.Context, chatID string) *agentruntime.ActiveRunSnapshot {
	coordinator := buildDefaultShadowRunCoordinator(ctx)
	if coordinator == nil {
		return nil
	}
	provider, ok := coordinator.(agentRuntimeActiveRunProvider)
	if !ok || provider == nil {
		return nil
	}
	snapshot, err := provider.ActiveRunSnapshot(ctx, strings.TrimSpace(chatID))
	if err != nil {
		return nil
	}
	return snapshot
}

func runtimeOwnershipContext(ctx context.Context, observation agentruntime.ShadowObservation) context.Context {
	return agentruntime.WithInitialRunOwnership(ctx, agentruntime.InitialRunOwnership{
		TriggerType:    observation.TriggerType,
		AttachToRunID:  strings.TrimSpace(observation.AttachToRunID),
		SupersedeRunID: strings.TrimSpace(observation.SupersedeRunID),
	})
}

func runtimeContextForObservedMessage(
	ctx context.Context,
	mode appconfig.ChatMode,
	observation agentruntime.ShadowObservation,
	observed bool,
	triggers ...agentruntime.TriggerType,
) context.Context {
	if mode.Normalize() != appconfig.ChatModeAgentic || !observed || !shouldDirectRouteRuntime(observation, triggers...) {
		return ctx
	}
	return runtimeOwnershipContext(ctx, observation)
}

func shouldDirectRouteRuntime(observation agentruntime.ShadowObservation, triggers ...agentruntime.TriggerType) bool {
	if !observation.EnterRuntime {
		return false
	}
	if len(triggers) == 0 {
		return true
	}
	for _, trigger := range triggers {
		if observation.TriggerType == trigger {
			return true
		}
	}
	return false
}
