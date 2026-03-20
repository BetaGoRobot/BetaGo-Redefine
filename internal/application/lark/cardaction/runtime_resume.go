package cardaction

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime/runtimewire"
	cardactionproto "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
)

type agentRuntimeResumeDispatcher interface {
	Dispatch(context.Context, agentruntime.ResumeEvent) (*agentruntime.AgentRun, error)
}

type agentRuntimeApprovalRejector interface {
	RejectApproval(context.Context, agentruntime.ResumeEvent) (*agentruntime.AgentRun, error)
}

type agentRuntimeApprovalRequestLoader interface {
	LoadApprovalRequest(context.Context, string, string) (*agentruntime.ApprovalRequest, error)
}

var buildAgentRuntimeResumeDispatcher = func(ctx context.Context) agentRuntimeResumeDispatcher {
	return runtimewire.BuildResumeDispatcher(ctx)
}

var buildAgentRuntimeApprovalRejector = func(ctx context.Context) agentRuntimeApprovalRejector {
	return runtimewire.BuildCoordinator(ctx)
}

var buildAgentRuntimeApprovalRequestLoader = func(ctx context.Context) agentRuntimeApprovalRequestLoader {
	return runtimewire.BuildCoordinator(ctx)
}

func dispatchAgentRuntimeAction(ctx context.Context, actionCtx *Context) (bool, *callback.CardActionTriggerResponse, error) {
	if actionCtx == nil || actionCtx.Action == nil {
		return false, nil, nil
	}

	switch actionCtx.Action.Name {
	case cardactionproto.ActionAgentRuntimeResume:
		event, err := buildAgentRuntimeResumeEvent(actionCtx)
		if err != nil {
			return true, nil, err
		}
		dispatcher := buildAgentRuntimeResumeDispatcher(ctx)
		if dispatcher == nil {
			return true, nil, fmt.Errorf("agent runtime resume dispatcher unavailable")
		}
		_, err = dispatcher.Dispatch(ctx, event)
		if err != nil {
			return true, nil, err
		}
		return true, approvalCardResponse(ctx, event, agentruntime.ApprovalCardStateApproved), nil
	case cardactionproto.ActionAgentRuntimeReject:
		event, err := buildAgentRuntimeResumeEvent(actionCtx)
		if err != nil {
			return true, nil, err
		}
		rejector := buildAgentRuntimeApprovalRejector(ctx)
		if rejector == nil {
			return true, nil, fmt.Errorf("agent runtime approval rejector unavailable")
		}
		_, err = rejector.RejectApproval(ctx, event)
		if err != nil {
			return true, nil, err
		}
		return true, approvalCardResponse(ctx, event, agentruntime.ApprovalCardStateRejected), nil
	default:
		return false, nil, nil
	}
}

func buildAgentRuntimeResumeEvent(actionCtx *Context) (agentruntime.ResumeEvent, error) {
	runID, err := actionCtx.Action.RequiredString(cardactionproto.RunIDField)
	if err != nil {
		return agentruntime.ResumeEvent{}, err
	}
	revisionRaw, err := actionCtx.Action.RequiredString(cardactionproto.RevisionField)
	if err != nil {
		return agentruntime.ResumeEvent{}, err
	}
	revision, err := strconv.ParseInt(strings.TrimSpace(revisionRaw), 10, 64)
	if err != nil {
		return agentruntime.ResumeEvent{}, fmt.Errorf("parse revision: %w", err)
	}
	sourceRaw, err := actionCtx.Action.RequiredString(cardactionproto.SourceField)
	if err != nil {
		return agentruntime.ResumeEvent{}, err
	}

	event := agentruntime.ResumeEvent{
		RunID:       strings.TrimSpace(runID),
		Revision:    revision,
		Source:      agentruntime.ResumeSource(strings.TrimSpace(sourceRaw)),
		ActorOpenID: strings.TrimSpace(actionCtx.OpenID()),
		OccurredAt:  time.Now().UTC(),
	}
	if stepID, ok := actionCtx.Action.String(cardactionproto.StepIDField); ok {
		event.StepID = strings.TrimSpace(stepID)
	}
	if token, ok := actionCtx.Action.String(cardactionproto.TokenField); ok {
		event.Token = strings.TrimSpace(token)
	}
	if actionCtx.MetaData != nil && strings.TrimSpace(actionCtx.MetaData.OpenID) != "" {
		event.ActorOpenID = strings.TrimSpace(actionCtx.MetaData.OpenID)
	}
	return event, event.Validate()
}

func approvalCardResponse(ctx context.Context, event agentruntime.ResumeEvent, state agentruntime.ApprovalCardState) *callback.CardActionTriggerResponse {
	if event.Source != agentruntime.ResumeSourceApproval {
		return nil
	}
	loader := buildAgentRuntimeApprovalRequestLoader(ctx)
	if loader == nil {
		return nil
	}
	request, err := loader.LoadApprovalRequest(ctx, event.RunID, event.StepID)
	if err != nil || request == nil {
		return nil
	}
	return RawCardPayloadOnly(agentruntime.BuildApprovalCard(ctx, *request, state))
}
