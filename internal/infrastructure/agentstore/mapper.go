package agentstore

import (
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
)

func toDBSession(session *agentruntime.AgentSession) *model.AgentSession {
	if session == nil {
		return nil
	}
	return &model.AgentSession{
		ID:              session.ID,
		AppID:           session.AppID,
		BotOpenID:       session.BotOpenID,
		ChatID:          session.ChatID,
		ScopeType:       session.ScopeType,
		ScopeID:         session.ScopeID,
		Status:          session.Status,
		ActiveRunID:     session.ActiveRunID,
		LastMessageID:   session.LastMessageID,
		LastActorOpenID: session.LastActorOpenID,
		MemoryVersion:   session.MemoryVersion,
		CreatedAt:       session.CreatedAt,
		UpdatedAt:       session.UpdatedAt,
	}
}

func toRuntimeSession(session *model.AgentSession) *agentruntime.AgentSession {
	if session == nil {
		return nil
	}
	return &agentruntime.AgentSession{
		ID:              session.ID,
		AppID:           session.AppID,
		BotOpenID:       session.BotOpenID,
		ChatID:          session.ChatID,
		ScopeType:       session.ScopeType,
		ScopeID:         session.ScopeID,
		Status:          session.Status,
		ActiveRunID:     session.ActiveRunID,
		LastMessageID:   session.LastMessageID,
		LastActorOpenID: session.LastActorOpenID,
		MemoryVersion:   session.MemoryVersion,
		CreatedAt:       session.CreatedAt,
		UpdatedAt:       session.UpdatedAt,
	}
}

func toDBRun(run *agentruntime.AgentRun) *model.AgentRun {
	if run == nil {
		return nil
	}
	return &model.AgentRun{
		ID:               run.ID,
		SessionID:        run.SessionID,
		TriggerType:      string(run.TriggerType),
		TriggerMessageID: run.TriggerMessageID,
		TriggerEventID:   run.TriggerEventID,
		ActorOpenID:      run.ActorOpenID,
		ParentRunID:      run.ParentRunID,
		Status:           string(run.Status),
		Goal:             run.Goal,
		InputText:        run.InputText,
		CurrentStepIndex: run.CurrentStepIndex,
		WaitingReason:    string(run.WaitingReason),
		WaitingToken:     run.WaitingToken,
		LastResponseID:   run.LastResponseID,
		ResultSummary:    run.ResultSummary,
		ErrorText:        run.ErrorText,
		Revision:         run.Revision,
		StartedAt:        run.StartedAt,
		FinishedAt:       run.FinishedAt,
		CreatedAt:        run.CreatedAt,
		UpdatedAt:        run.UpdatedAt,
		WorkerID:         run.WorkerID,
		HeartbeatAt:      run.HeartbeatAt,
		LeaseExpiresAt:   run.LeaseExpiresAt,
		RepairAttempts:   run.RepairAttempts,
	}
}

func toRuntimeRun(run *model.AgentRun) *agentruntime.AgentRun {
	if run == nil {
		return nil
	}
	return &agentruntime.AgentRun{
		ID:               run.ID,
		SessionID:        run.SessionID,
		TriggerType:      agentruntime.TriggerType(run.TriggerType),
		TriggerMessageID: run.TriggerMessageID,
		TriggerEventID:   run.TriggerEventID,
		ActorOpenID:      run.ActorOpenID,
		ParentRunID:      run.ParentRunID,
		Status:           agentruntime.RunStatus(run.Status),
		Goal:             run.Goal,
		InputText:        run.InputText,
		CurrentStepIndex: run.CurrentStepIndex,
		WaitingReason:    agentruntime.WaitingReason(run.WaitingReason),
		WaitingToken:     run.WaitingToken,
		LastResponseID:   run.LastResponseID,
		ResultSummary:    run.ResultSummary,
		ErrorText:        run.ErrorText,
		Revision:         run.Revision,
		StartedAt:        run.StartedAt,
		FinishedAt:       run.FinishedAt,
		CreatedAt:        run.CreatedAt,
		UpdatedAt:        run.UpdatedAt,
		WorkerID:         run.WorkerID,
		HeartbeatAt:      run.HeartbeatAt,
		LeaseExpiresAt:   run.LeaseExpiresAt,
		RepairAttempts:   run.RepairAttempts,
	}
}

func toDBStep(step *agentruntime.AgentStep) *model.AgentStep {
	if step == nil {
		return nil
	}
	return &model.AgentStep{
		ID:             step.ID,
		RunID:          step.RunID,
		Index:          step.Index,
		Kind:           string(step.Kind),
		Status:         string(step.Status),
		CapabilityName: step.CapabilityName,
		InputJSON:      step.InputJSON,
		OutputJSON:     step.OutputJSON,
		ErrorText:      step.ErrorText,
		ExternalRef:    step.ExternalRef,
		StartedAt:      step.StartedAt,
		FinishedAt:     step.FinishedAt,
		CreatedAt:      step.CreatedAt,
	}
}

func toRuntimeStep(step *model.AgentStep) *agentruntime.AgentStep {
	if step == nil {
		return nil
	}
	return &agentruntime.AgentStep{
		ID:             step.ID,
		RunID:          step.RunID,
		Index:          step.Index,
		Kind:           agentruntime.StepKind(step.Kind),
		Status:         agentruntime.StepStatus(step.Status),
		CapabilityName: step.CapabilityName,
		InputJSON:      step.InputJSON,
		OutputJSON:     step.OutputJSON,
		ErrorText:      step.ErrorText,
		ExternalRef:    step.ExternalRef,
		StartedAt:      step.StartedAt,
		FinishedAt:     step.FinishedAt,
		CreatedAt:      step.CreatedAt,
	}
}
