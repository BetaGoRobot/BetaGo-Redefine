package agentruntime

import (
	"fmt"
	"time"
)

// ScopeType identifies the session scope tracked by the runtime persistence layer.
type ScopeType string

const (
	ScopeTypeChat   ScopeType = "chat"
	ScopeTypeThread ScopeType = "thread"
)

// SessionStatus describes whether a session is active, idle, or archived.
type SessionStatus string

const (
	SessionStatusActive   SessionStatus = "active"
	SessionStatusIdle     SessionStatus = "idle"
	SessionStatusArchived SessionStatus = "archived"
)

// RunStatus captures the lifecycle state of a runtime run.
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

// StepKind identifies the semantic role of a persisted step within a run.
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

// StepStatus tracks whether a step is queued, running, or already finalized.
type StepStatus string

const (
	StepStatusQueued    StepStatus = "queued"
	StepStatusRunning   StepStatus = "running"
	StepStatusCompleted StepStatus = "completed"
	StepStatusFailed    StepStatus = "failed"
	StepStatusSkipped   StepStatus = "skipped"
)

// TriggerType records which incoming signal caused the run to start or resume.
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

// WaitingReason records why a run is paused instead of executing immediately.
type WaitingReason string

const (
	WaitingReasonNone     WaitingReason = ""
	WaitingReasonApproval WaitingReason = "approval"
	WaitingReasonSchedule WaitingReason = "schedule"
	WaitingReasonCallback WaitingReason = "callback"
	WaitingReasonAsync    WaitingReason = "async"
)

// AgentSession is the long-lived runtime container for one chat or scope.
// It keeps the active-run pointer, dedupe anchors, and memory version shared by
// the runs created under that scope.
type AgentSession struct {
	ID          string        `json:"id"`
	AppID       string        `json:"app_id"`
	BotOpenID   string        `json:"bot_open_id"`
	ChatID      string        `json:"chat_id"`
	ScopeType   ScopeType     `json:"scope_type"`
	ScopeID     string        `json:"scope_id"`
	Status      SessionStatus `json:"status"`
	ActiveRunID string        `json:"active_run_id,omitempty"`
	// LastMessageID and LastActorOpenID are session-level dedupe anchors for
	// attaching follow-up triggers onto the current active run.
	LastMessageID   string    `json:"last_message_id,omitempty"`
	LastActorOpenID string    `json:"last_actor_open_id,omitempty"`
	MemoryVersion   int64     `json:"memory_version"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// AgentRun is the persisted execution record for one runtime task. It stores
// the trigger metadata, current step cursor, waiting state, and final outcome
// needed for continuation and inspection.
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
	// LastResponseID is the latest emitted reply message anchor for this run.
	// New logic should prefer projecting reply targets from steps when possible,
	// but this field remains as a compatibility cursor for fast run lookup.
	LastResponseID string     `json:"last_response_id,omitempty"`
	ResultSummary  string     `json:"result_summary,omitempty"`
	ErrorText      string     `json:"error_text,omitempty"`
	WorkerID       string     `json:"worker_id,omitempty"`
	HeartbeatAt    *time.Time `json:"heartbeat_at,omitempty"`
	LeaseExpiresAt *time.Time `json:"lease_expires_at,omitempty"`
	RepairAttempts int64      `json:"repair_attempts"`
	Revision       int64      `json:"revision"`
	StartedAt      *time.Time `json:"started_at,omitempty"`
	FinishedAt     *time.Time `json:"finished_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// AgentStep is the append-only unit of run history. It records a typed stage
// such as planning, capability execution, waiting, or reply emission together
// with serialized inputs and outputs.
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

// IsTerminal reports whether the run status is terminal and no longer resumable.
func (s RunStatus) IsTerminal() bool {
	switch s {
	case RunStatusCompleted, RunStatusFailed, RunStatusCancelled:
		return true
	default:
		return false
	}
}

// IsTerminal reports whether the step status is terminal and should not transition further.
func (s StepStatus) IsTerminal() bool {
	switch s {
	case StepStatusCompleted, StepStatusFailed, StepStatusSkipped:
		return true
	default:
		return false
	}
}

// ValidateRunStatusTransition checks whether a run-status transition is allowed
// by the runtime state machine.
func ValidateRunStatusTransition(from, to RunStatus) error {
	if from == to {
		return nil
	}

	allowed := map[RunStatus]map[RunStatus]struct{}{
		RunStatusQueued: {
			RunStatusRunning:   {},
			RunStatusFailed:    {},
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

// ValidateStepStatusTransition checks whether a step-status transition is allowed
// by the runtime step state machine.
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
