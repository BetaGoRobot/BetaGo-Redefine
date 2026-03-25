package cardaction

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime/runtimewire"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	cardactionproto "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
	"go.uber.org/zap"
)

type agentRuntimeResumeDispatcher interface {
	Dispatch(context.Context, agentruntime.ResumeEvent) (*agentruntime.AgentRun, error)
}

type agentRuntimeApprovalRejector interface {
	RejectApproval(context.Context, agentruntime.ResumeEvent) (*agentruntime.AgentRun, error)
}

type agentRuntimeActionDeps struct {
	resumeDispatcher        agentRuntimeResumeDispatcher
	approvalRejector        agentRuntimeApprovalRejector
	deleteEphemeralApproval func(context.Context, string) error
	deleteMessageApproval   func(context.Context, string) error
	withdrawApproval        func(context.Context, string, func(context.Context, string) error)
}

type agentRuntimeActionDepsContextKey struct{}

const defaultApprovalWithdrawDelay = 1200 * time.Millisecond

func withAgentRuntimeActionDeps(ctx context.Context, deps agentRuntimeActionDeps) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, agentRuntimeActionDepsContextKey{}, deps)
}

func resolveAgentRuntimeActionDeps(ctx context.Context) agentRuntimeActionDeps {
	if ctx != nil {
		if deps, ok := ctx.Value(agentRuntimeActionDepsContextKey{}).(agentRuntimeActionDeps); ok {
			return deps
		}
	}
	return agentRuntimeActionDeps{
		resumeDispatcher:        runtimewire.BuildResumeDispatcher(ctx),
		approvalRejector:        runtimewire.BuildCoordinator(ctx),
		deleteEphemeralApproval: larkmsg.DeleteEphemeralMessage,
		deleteMessageApproval:   larkmsg.DeleteMessage,
		withdrawApproval:        scheduleApprovalWithdraw,
	}
}

func dispatchAgentRuntimeAction(ctx context.Context, actionCtx *Context) (bool, *callback.CardActionTriggerResponse, error) {
	if actionCtx == nil || actionCtx.Action == nil {
		return false, nil, nil
	}
	switch actionCtx.Action.Name {
	case cardactionproto.ActionAgentRuntimeResume, cardactionproto.ActionAgentRuntimeReject:
	default:
		return false, nil, nil
	}
	return dispatchAgentRuntimeActionWithDeps(ctx, actionCtx, resolveAgentRuntimeActionDeps(ctx))
}

func dispatchAgentRuntimeActionWithDeps(ctx context.Context, actionCtx *Context, deps agentRuntimeActionDeps) (bool, *callback.CardActionTriggerResponse, error) {
	if actionCtx == nil || actionCtx.Action == nil {
		return false, nil, nil
	}

	switch actionCtx.Action.Name {
	case cardactionproto.ActionAgentRuntimeResume:
		event, err := buildAgentRuntimeResumeEvent(actionCtx)
		if err != nil {
			return true, nil, err
		}
		dispatcher := deps.resumeDispatcher
		if dispatcher == nil {
			return true, nil, fmt.Errorf("agent runtime resume dispatcher unavailable")
		}
		_, err = dispatcher.Dispatch(ctx, event)
		if err != nil {
			return true, nil, err
		}
		withdrawAgentRuntimeApprovalCard(ctx, actionCtx, event, resolveApprovalDeleteFunc(actionCtx, deps), deps.withdrawApproval)
		return true, approvalWithdrawToast(actionCtx.Action.Name, event), nil
	case cardactionproto.ActionAgentRuntimeReject:
		event, err := buildAgentRuntimeResumeEvent(actionCtx)
		if err != nil {
			return true, nil, err
		}
		rejector := deps.approvalRejector
		if rejector == nil {
			return true, nil, fmt.Errorf("agent runtime approval rejector unavailable")
		}
		_, err = rejector.RejectApproval(ctx, event)
		if err != nil {
			return true, nil, err
		}
		withdrawAgentRuntimeApprovalCard(ctx, actionCtx, event, resolveApprovalDeleteFunc(actionCtx, deps), deps.withdrawApproval)
		return true, approvalWithdrawToast(actionCtx.Action.Name, event), nil
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

func withdrawAgentRuntimeApprovalCard(
	ctx context.Context,
	actionCtx *Context,
	event agentruntime.ResumeEvent,
	deleteApproval func(context.Context, string) error,
	withdrawApproval func(context.Context, string, func(context.Context, string) error),
) {
	if actionCtx == nil || event.Source != agentruntime.ResumeSourceApproval {
		return
	}
	messageID := strings.TrimSpace(actionCtx.MessageID())
	if messageID == "" {
		return
	}
	if deleteApproval == nil {
		return
	}
	if withdrawApproval == nil {
		withdrawApproval = scheduleApprovalWithdraw
	}
	withdrawApproval(ctx, messageID, deleteApproval)
}

func resolveApprovalDeleteFunc(actionCtx *Context, deps agentRuntimeActionDeps) func(context.Context, string) error {
	if delivery := approvalCardDeliveryFromAction(actionCtx); delivery == agentruntime.ApprovalCardDeliveryMessage {
		return fallbackApprovalDeleteFunc(deps.deleteMessageApproval, deps.deleteEphemeralApproval)
	} else if delivery == agentruntime.ApprovalCardDeliveryEphemeral {
		return fallbackApprovalDeleteFunc(deps.deleteEphemeralApproval, deps.deleteMessageApproval)
	}
	return fallbackApprovalDeleteFunc(deps.deleteEphemeralApproval, deps.deleteMessageApproval)
}

func approvalCardDeliveryFromAction(actionCtx *Context) agentruntime.ApprovalCardDelivery {
	if actionCtx == nil || actionCtx.Action == nil {
		return ""
	}
	delivery, ok := actionCtx.Action.String(cardactionproto.ApprovalDeliveryField)
	if !ok {
		return ""
	}
	return agentruntime.ApprovalCardDelivery(strings.TrimSpace(delivery))
}

func fallbackApprovalDeleteFunc(
	primary func(context.Context, string) error,
	fallback func(context.Context, string) error,
) func(context.Context, string) error {
	return func(ctx context.Context, messageID string) error {
		if primary != nil {
			if err := primary(ctx, messageID); err == nil || fallback == nil {
				return err
			}
		}
		if fallback != nil {
			return fallback(ctx, messageID)
		}
		return nil
	}
}

func scheduleApprovalWithdraw(ctx context.Context, messageID string, deleteApproval func(context.Context, string) error) {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" || deleteApproval == nil {
		return
	}
	go func() {
		time.Sleep(defaultApprovalWithdrawDelay)
		if err := deleteApproval(context.WithoutCancel(ctx), messageID); err != nil {
			logs.L().Ctx(ctx).Warn("delete agent runtime approval card failed", zap.String("message_id", messageID), zap.Error(err))
		}
	}()
}

func withdrawApprovalImmediately(ctx context.Context, messageID string, deleteApproval func(context.Context, string) error) {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" || deleteApproval == nil {
		return
	}
	if err := deleteApproval(ctx, messageID); err != nil {
		logs.L().Ctx(ctx).Warn("delete agent runtime approval card failed", zap.String("message_id", messageID), zap.Error(err))
	}
}

func approvalWithdrawToast(actionName string, event agentruntime.ResumeEvent) *callback.CardActionTriggerResponse {
	if event.Source != agentruntime.ResumeSourceApproval {
		return nil
	}
	switch actionName {
	case cardactionproto.ActionAgentRuntimeResume:
		return InfoToast("已批准，审批卡已撤回")
	case cardactionproto.ActionAgentRuntimeReject:
		return InfoToast("已拒绝，审批卡已撤回")
	default:
		return nil
	}
}
