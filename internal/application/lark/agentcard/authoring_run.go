package agentcard

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentcardtool"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
)

type AuthoringRunStore interface {
	agentruntime.CoordinatorStore
	FindActiveRun(context.Context, string) (*agentruntime.AgentRun, error)
}

type DurableAuthoringRunResolverOptions struct {
	Store     AuthoringRunStore
	AppID     string
	BotOpenID string
}

type DurableAuthoringRunResolver struct {
	store     AuthoringRunStore
	appID     string
	botOpenID string
}

func NewDurableAuthoringRunResolver(
	options DurableAuthoringRunResolverOptions,
) (*DurableAuthoringRunResolver, error) {
	if options.Store == nil {
		return nil, errors.New("agent card authoring run store is required")
	}
	if strings.TrimSpace(options.AppID) == "" ||
		strings.TrimSpace(options.BotOpenID) == "" {
		return nil, errors.New("agent card authoring bot identity is required")
	}
	return &DurableAuthoringRunResolver{
		store: options.Store, appID: strings.TrimSpace(options.AppID),
		botOpenID: strings.TrimSpace(options.BotOpenID),
	}, nil
}

func (r *DurableAuthoringRunResolver) ResolveAuthoringRun(
	ctx context.Context,
	toolContext agentcardtool.ComposeContext,
	purpose string,
) (*agentruntime.AgentRun, error) {
	if r == nil || r.store == nil {
		return nil, errors.New("agent card authoring run resolver is not configured")
	}
	for name, value := range map[string]string{
		"chat_id":             toolContext.ChatID,
		"actor_open_id":       toolContext.ActorOpenID,
		"reply_to_message_id": toolContext.ReplyToMessageID,
	} {
		if strings.TrimSpace(value) == "" ||
			strings.TrimSpace(value) != value {
			return nil, fmt.Errorf("%s is required and must be canonical", name)
		}
	}
	start := agentruntime.StartRunRequest{
		AppID: r.appID, BotOpenID: r.botOpenID,
		ChatID: toolContext.ChatID, ScopeType: agentruntime.ScopeTypeChat,
		ScopeID: toolContext.ChatID, TriggerType: agentruntime.TriggerTypeShadow,
		TriggerMessageID: toolContext.ReplyToMessageID,
		TriggerEventID:   strings.TrimSpace(toolContext.TriggerEventID),
		ActorOpenID:      toolContext.ActorOpenID,
		Goal:             "Agent-authored card: " + strings.TrimSpace(purpose),
		InputText:        strings.TrimSpace(purpose),
	}
	session, err := r.store.GetOrCreateSession(
		ctx,
		agentruntime.NewSessionForRun(start),
	)
	if err != nil {
		return nil, err
	}
	active, err := r.store.FindActiveRun(ctx, session.ID)
	switch {
	case err == nil:
		if active.Status != agentruntime.RunStatusQueued &&
			active.Status != agentruntime.RunStatusRunning {
			return nil, agentruntime.ErrActiveRunConflict
		}
		if active.TriggerMessageID != toolContext.ReplyToMessageID {
			return nil, agentruntime.ErrActiveRunConflict
		}
		return active, nil
	case !errors.Is(err, agentruntime.ErrNotFound):
		return nil, err
	}
	result, err := agentruntime.NewRunCoordinator(r.store).
		StartShadowRun(ctx, start)
	if err != nil {
		return nil, err
	}
	if result == nil || result.Run == nil {
		return nil, errors.New("agent card authoring run was not created")
	}
	return result.Run, nil
}
