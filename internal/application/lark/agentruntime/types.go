package agentruntime

import (
	"fmt"
	"time"

	uuid "github.com/satori/go.uuid"
)

type RunStatus string

const (
	RunStatusQueued          RunStatus = "queued"
	RunStatusRunning         RunStatus = "running"
	RunStatusWaitingApproval RunStatus = "waiting_approval"
	RunStatusWaitingSchedule RunStatus = "waiting_schedule"
	RunStatusWaitingCallback RunStatus = "waiting_callback"
	RunStatusCompleted       RunStatus = "completed"
	RunStatusFailed          RunStatus = "failed"
	RunStatusCancelled       RunStatus = "cancelled"
)

type StepStatus string

const (
	StepStatusQueued    StepStatus = "queued"
	StepStatusRunning   StepStatus = "running"
	StepStatusCompleted StepStatus = "completed"
	StepStatusFailed    StepStatus = "failed"
	StepStatusSkipped   StepStatus = "skipped"
)

type TriggerType string

const (
	TriggerTypeMention        TriggerType = "mention"
	TriggerTypeReplyToBot     TriggerType = "reply_to_bot"
	TriggerTypeCommandBridge  TriggerType = "command_bridge"
	TriggerTypeCardCallback   TriggerType = "card_callback"
	TriggerTypeScheduleResume TriggerType = "schedule_resume"
	TriggerTypeP2P            TriggerType = "p2p"
	TriggerTypeShadow         TriggerType = "shadow"
)

type WaitingReason string

const (
	WaitingReasonApproval WaitingReason = "approval"
	WaitingReasonSchedule WaitingReason = "schedule"
	WaitingReasonCallback WaitingReason = "callback"
)

type StepKind string

const (
	StepKindDecide          StepKind = "decide"
	StepKindComposeContext  StepKind = "compose_context"
	StepKindPlan            StepKind = "plan"
	StepKindCapabilityCall  StepKind = "capability_call"
	StepKindObserve         StepKind = "observe"
	StepKindReply           StepKind = "reply"
	StepKindApprovalRequest StepKind = "approval_request"
	StepKindWait            StepKind = "wait"
	StepKindResume          StepKind = "resume"
)

type AgentSession struct {
	ID              string
	AppID           string
	BotOpenID       string
	ChatID          string
	ScopeType       string
	ScopeID         string
	Status          string
	ActiveRunID     string
	LastMessageID   string
	LastActorOpenID string
	MemoryVersion   int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type AgentRun struct {
	ID               string
	SessionID        string
	TriggerType      TriggerType
	TriggerMessageID string
	TriggerEventID   string
	ActorOpenID      string
	ParentRunID      string
	Status           RunStatus
	Goal             string
	InputText        string
	CurrentStepIndex int32
	WaitingReason    WaitingReason
	WaitingToken     string
	LastResponseID   string
	ResultSummary    string
	ErrorText        string
	Revision         int64
	StartedAt        time.Time
	FinishedAt       time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
	WorkerID         string
	HeartbeatAt      time.Time
	LeaseExpiresAt   time.Time
	RepairAttempts   int64
}

type AgentStep struct {
	ID             string
	RunID          string
	Index          int32
	Kind           StepKind
	Status         StepStatus
	CapabilityName string
	InputJSON      string
	OutputJSON     string
	ErrorText      string
	ExternalRef    string
	StartedAt      time.Time
	FinishedAt     time.Time
	CreatedAt      time.Time
}

type NewRunRequest struct {
	SessionID        string
	TriggerType      TriggerType
	TriggerMessageID string
	TriggerEventID   string
	ActorOpenID      string
	ParentRunID      string
	Goal             string
	InputText        string
}

type NewStepRequest struct {
	RunID          string
	Index          int32
	Kind           StepKind
	CapabilityName string
	InputJSON      string
	ExternalRef    string
}

func NewRun(req NewRunRequest) *AgentRun {
	now := time.Now()
	return &AgentRun{
		ID:               newAgentID("run"),
		SessionID:        req.SessionID,
		TriggerType:      req.TriggerType,
		TriggerMessageID: req.TriggerMessageID,
		TriggerEventID:   req.TriggerEventID,
		ActorOpenID:      req.ActorOpenID,
		ParentRunID:      req.ParentRunID,
		Status:           RunStatusQueued,
		Goal:             req.Goal,
		InputText:        req.InputText,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
}

func NewStep(req NewStepRequest) *AgentStep {
	return &AgentStep{
		ID:             newAgentID("step"),
		RunID:          req.RunID,
		Index:          req.Index,
		Kind:           req.Kind,
		Status:         StepStatusQueued,
		CapabilityName: req.CapabilityName,
		InputJSON:      req.InputJSON,
		ExternalRef:    req.ExternalRef,
		CreatedAt:      time.Now(),
	}
}

func ValidateRunTransition(from, to RunStatus) error {
	if from == to {
		return nil
	}
	if isTerminalRunStatus(from) {
		return fmt.Errorf("agent run status transition %q -> %q is invalid: terminal run cannot resume", from, to)
	}
	switch from {
	case RunStatusQueued:
		if to == RunStatusRunning || to == RunStatusCancelled {
			return nil
		}
	case RunStatusRunning:
		if to == RunStatusWaitingApproval || to == RunStatusWaitingSchedule || to == RunStatusWaitingCallback ||
			to == RunStatusCompleted || to == RunStatusFailed || to == RunStatusCancelled {
			return nil
		}
	case RunStatusWaitingApproval, RunStatusWaitingSchedule, RunStatusWaitingCallback:
		if to == RunStatusQueued || to == RunStatusRunning || to == RunStatusCancelled || to == RunStatusFailed {
			return nil
		}
	}
	return fmt.Errorf("agent run status transition %q -> %q is invalid", from, to)
}

func ValidateStepTransition(from, to StepStatus) error {
	if from == to {
		return nil
	}
	if isTerminalStepStatus(from) {
		return fmt.Errorf("agent step status transition %q -> %q is invalid: terminal step cannot resume", from, to)
	}
	switch from {
	case StepStatusQueued:
		if to == StepStatusRunning || to == StepStatusSkipped {
			return nil
		}
	case StepStatusRunning:
		if to == StepStatusCompleted || to == StepStatusFailed || to == StepStatusSkipped {
			return nil
		}
	}
	return fmt.Errorf("agent step status transition %q -> %q is invalid", from, to)
}

func isTerminalRunStatus(status RunStatus) bool {
	return status == RunStatusCompleted || status == RunStatusFailed || status == RunStatusCancelled
}

func isTerminalStepStatus(status StepStatus) bool {
	return status == StepStatusCompleted || status == StepStatusFailed || status == StepStatusSkipped
}

func newAgentID(prefix string) string {
	return prefix + "_" + uuid.NewV4().String()
}
