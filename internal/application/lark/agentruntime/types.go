package agentruntime

import (
	"fmt"
	"time"
)

type ScopeType string

const (
	ScopeTypeChat   ScopeType = "chat"
	ScopeTypeThread ScopeType = "thread"
)

type SessionStatus string

const (
	SessionStatusActive   SessionStatus = "active"
	SessionStatusIdle     SessionStatus = "idle"
	SessionStatusArchived SessionStatus = "archived"
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

type StepKind string

const (
	StepKindDecide          StepKind = "decide"
	StepKindPlan            StepKind = "plan"
	StepKindCapabilityCall  StepKind = "capability_call"
	StepKindObserve         StepKind = "observe"
	StepKindReply           StepKind = "reply"
	StepKindApprovalRequest StepKind = "approval_request"
	StepKindWait            StepKind = "wait"
	StepKindResume          StepKind = "resume"
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
	TriggerTypeFollowUp       TriggerType = "follow_up"
	TriggerTypeP2P            TriggerType = "p2p"
)

type WaitingReason string

const (
	WaitingReasonNone     WaitingReason = ""
	WaitingReasonApproval WaitingReason = "approval"
	WaitingReasonSchedule WaitingReason = "schedule"
	WaitingReasonCallback WaitingReason = "callback"
	WaitingReasonAsync    WaitingReason = "async"
)

type AgentSession struct {
	ID              string        `json:"id"`
	AppID           string        `json:"app_id"`
	BotOpenID       string        `json:"bot_open_id"`
	ChatID          string        `json:"chat_id"`
	ScopeType       ScopeType     `json:"scope_type"`
	ScopeID         string        `json:"scope_id"`
	Status          SessionStatus `json:"status"`
	ActiveRunID     string        `json:"active_run_id,omitempty"`
	LastMessageID   string        `json:"last_message_id,omitempty"`
	LastActorOpenID string        `json:"last_actor_open_id,omitempty"`
	MemoryVersion   int64         `json:"memory_version"`
	CreatedAt       time.Time     `json:"created_at"`
	UpdatedAt       time.Time     `json:"updated_at"`
}

type AgentRun struct {
	ID               string        `json:"id"`
	SessionID        string        `json:"session_id"`
	TriggerType      TriggerType   `json:"trigger_type"`
	TriggerMessageID string        `json:"trigger_message_id,omitempty"`
	TriggerEventID   string        `json:"trigger_event_id,omitempty"`
	ActorOpenID      string        `json:"actor_open_id,omitempty"`
	ParentRunID      string        `json:"parent_run_id,omitempty"`
	Status           RunStatus     `json:"status"`
	Goal             string        `json:"goal,omitempty"`
	InputText        string        `json:"input_text,omitempty"`
	CurrentStepIndex int           `json:"current_step_index"`
	WaitingReason    WaitingReason `json:"waiting_reason,omitempty"`
	WaitingToken     string        `json:"waiting_token,omitempty"`
	LastResponseID   string        `json:"last_response_id,omitempty"`
	ResultSummary    string        `json:"result_summary,omitempty"`
	ErrorText        string        `json:"error_text,omitempty"`
	Revision         int64         `json:"revision"`
	StartedAt        *time.Time    `json:"started_at,omitempty"`
	FinishedAt       *time.Time    `json:"finished_at,omitempty"`
	CreatedAt        time.Time     `json:"created_at"`
	UpdatedAt        time.Time     `json:"updated_at"`
}

type AgentStep struct {
	ID             string     `json:"id"`
	RunID          string     `json:"run_id"`
	Index          int        `json:"index"`
	Kind           StepKind   `json:"kind"`
	Status         StepStatus `json:"status"`
	CapabilityName string     `json:"capability_name,omitempty"`
	InputJSON      []byte     `json:"input_json,omitempty"`
	OutputJSON     []byte     `json:"output_json,omitempty"`
	ErrorText      string     `json:"error_text,omitempty"`
	ExternalRef    string     `json:"external_ref,omitempty"`
	StartedAt      *time.Time `json:"started_at,omitempty"`
	FinishedAt     *time.Time `json:"finished_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
}

func (s RunStatus) IsTerminal() bool {
	switch s {
	case RunStatusCompleted, RunStatusFailed, RunStatusCancelled:
		return true
	default:
		return false
	}
}

func (s StepStatus) IsTerminal() bool {
	switch s {
	case StepStatusCompleted, StepStatusFailed, StepStatusSkipped:
		return true
	default:
		return false
	}
}

func ValidateRunStatusTransition(from, to RunStatus) error {
	if from == to {
		return nil
	}

	allowed := map[RunStatus]map[RunStatus]struct{}{
		RunStatusQueued: {
			RunStatusRunning:   {},
			RunStatusCancelled: {},
		},
		RunStatusRunning: {
			RunStatusQueued:          {},
			RunStatusWaitingApproval: {},
			RunStatusWaitingSchedule: {},
			RunStatusWaitingCallback: {},
			RunStatusCompleted:       {},
			RunStatusFailed:          {},
			RunStatusCancelled:       {},
		},
		RunStatusWaitingApproval: {
			RunStatusRunning:   {},
			RunStatusQueued:    {},
			RunStatusCancelled: {},
		},
		RunStatusWaitingSchedule: {
			RunStatusRunning:   {},
			RunStatusQueued:    {},
			RunStatusCancelled: {},
		},
		RunStatusWaitingCallback: {
			RunStatusRunning:   {},
			RunStatusQueued:    {},
			RunStatusCancelled: {},
		},
	}

	if _, ok := allowed[from][to]; ok {
		return nil
	}
	return fmt.Errorf("invalid run status transition: %s -> %s", from, to)
}

func ValidateStepStatusTransition(from, to StepStatus) error {
	if from == to {
		return nil
	}

	allowed := map[StepStatus]map[StepStatus]struct{}{
		StepStatusQueued: {
			StepStatusRunning: {},
			StepStatusSkipped: {},
		},
		StepStatusRunning: {
			StepStatusCompleted: {},
			StepStatusFailed:    {},
			StepStatusSkipped:   {},
		},
	}

	if _, ok := allowed[from][to]; ok {
		return nil
	}
	return fmt.Errorf("invalid step status transition: %s -> %s", from, to)
}
