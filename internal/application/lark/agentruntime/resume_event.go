package agentruntime

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrResumeStateConflict = errors.New("agent runtime resume state conflict")
	ErrResumeTokenMismatch = errors.New("agent runtime resume token mismatch")
)

type ResumeSource string

const (
	ResumeSourceApproval ResumeSource = "approval"
	ResumeSourceCallback ResumeSource = "callback"
	ResumeSourceSchedule ResumeSource = "schedule"
)

type ResumeEvent struct {
	RunID       string          `json:"run_id"`
	StepID      string          `json:"step_id,omitempty"`
	Revision    int64           `json:"revision"`
	Source      ResumeSource    `json:"source"`
	Token       string          `json:"token,omitempty"`
	Summary     string          `json:"summary,omitempty"`
	PayloadJSON json.RawMessage `json:"payload_json,omitempty"`
	ActorOpenID string          `json:"actor_open_id,omitempty"`
	OccurredAt  time.Time       `json:"occurred_at,omitempty"`
}

func (e ResumeEvent) Validate() error {
	if strings.TrimSpace(e.RunID) == "" {
		return fmt.Errorf("resume event run_id is required")
	}
	if e.Revision < 0 {
		return fmt.Errorf("resume event revision must be >= 0")
	}
	switch e.Source {
	case ResumeSourceApproval, ResumeSourceCallback, ResumeSourceSchedule:
	default:
		return fmt.Errorf("resume event source is invalid: %q", e.Source)
	}
	if e.Source.requiresToken() && strings.TrimSpace(e.Token) == "" {
		return fmt.Errorf("resume event token is required for source %q", e.Source)
	}
	return nil
}

func (e ResumeEvent) WaitingReason() WaitingReason {
	switch e.Source {
	case ResumeSourceApproval:
		return WaitingReasonApproval
	case ResumeSourceCallback:
		return WaitingReasonCallback
	case ResumeSourceSchedule:
		return WaitingReasonSchedule
	default:
		return WaitingReasonNone
	}
}

func (e ResumeEvent) TriggerType() TriggerType {
	switch e.Source {
	case ResumeSourceSchedule:
		return TriggerTypeScheduleResume
	case ResumeSourceApproval, ResumeSourceCallback:
		return TriggerTypeCardCallback
	default:
		return TriggerTypeFollowUp
	}
}

func (e ResumeEvent) ExternalRef() string {
	if stepID := strings.TrimSpace(e.StepID); stepID != "" {
		return stepID
	}
	return string(e.Source)
}

func (s ResumeSource) requiresToken() bool {
	switch s {
	case ResumeSourceApproval, ResumeSourceCallback:
		return true
	default:
		return false
	}
}
