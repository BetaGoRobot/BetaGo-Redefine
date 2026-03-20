package agentruntime

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	cardactionproto "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
)

var (
	ErrApprovalExpired       = errors.New("agent runtime approval expired")
	ErrApprovalStateConflict = errors.New("agent runtime approval state conflict")
)

type RequestApprovalInput struct {
	RunID          string    `json:"run_id"`
	ApprovalType   string    `json:"approval_type,omitempty"`
	Title          string    `json:"title"`
	Summary        string    `json:"summary,omitempty"`
	CapabilityName string    `json:"capability_name,omitempty"`
	PayloadJSON    []byte    `json:"payload_json,omitempty"`
	ExpiresAt      time.Time `json:"expires_at"`
	RequestedAt    time.Time `json:"requested_at,omitempty"`
}

type ApprovalRequest struct {
	RunID          string    `json:"run_id"`
	StepID         string    `json:"step_id"`
	Revision       int64     `json:"revision"`
	ApprovalType   string    `json:"approval_type,omitempty"`
	Title          string    `json:"title"`
	Summary        string    `json:"summary,omitempty"`
	CapabilityName string    `json:"capability_name,omitempty"`
	PayloadJSON    []byte    `json:"payload_json,omitempty"`
	Token          string    `json:"token"`
	RequestedAt    time.Time `json:"requested_at,omitempty"`
	ExpiresAt      time.Time `json:"expires_at"`
}

type approvalStepState struct {
	ApprovalType   string    `json:"approval_type,omitempty"`
	Title          string    `json:"title"`
	Summary        string    `json:"summary,omitempty"`
	CapabilityName string    `json:"capability_name,omitempty"`
	RequestedAt    time.Time `json:"requested_at,omitempty"`
	ExpiresAt      time.Time `json:"expires_at,omitempty"`
}

func (r ApprovalRequest) Validate(now time.Time) error {
	if strings.TrimSpace(r.RunID) == "" {
		return fmt.Errorf("approval request run_id is required")
	}
	if strings.TrimSpace(r.StepID) == "" {
		return fmt.Errorf("approval request step_id is required")
	}
	if r.Revision < 0 {
		return fmt.Errorf("approval request revision must be >= 0")
	}
	if strings.TrimSpace(r.Title) == "" {
		return fmt.Errorf("approval request title is required")
	}
	if strings.TrimSpace(r.Token) == "" {
		return fmt.Errorf("approval request token is required")
	}
	if r.ExpiresAt.IsZero() {
		return fmt.Errorf("approval request expires_at is required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	if !r.ExpiresAt.UTC().After(now) {
		return ErrApprovalExpired
	}
	return nil
}

func (r ApprovalRequest) ApprovePayload() map[string]string {
	return cardactionproto.New(cardactionproto.ActionAgentRuntimeResume).
		WithRunID(strings.TrimSpace(r.RunID)).
		WithStepID(strings.TrimSpace(r.StepID)).
		WithRevision(strconv.FormatInt(r.Revision, 10)).
		WithSource(string(ResumeSourceApproval)).
		WithToken(strings.TrimSpace(r.Token)).
		Payload()
}

func (r ApprovalRequest) RejectPayload() map[string]string {
	return cardactionproto.New(cardactionproto.ActionAgentRuntimeReject).
		WithRunID(strings.TrimSpace(r.RunID)).
		WithStepID(strings.TrimSpace(r.StepID)).
		WithRevision(strconv.FormatInt(r.Revision, 10)).
		WithSource(string(ResumeSourceApproval)).
		WithToken(strings.TrimSpace(r.Token)).
		Payload()
}

func validateRequestApprovalInput(input RequestApprovalInput, now time.Time) error {
	if strings.TrimSpace(input.RunID) == "" {
		return fmt.Errorf("approval input run_id is required")
	}
	if strings.TrimSpace(input.Title) == "" {
		return fmt.Errorf("approval input title is required")
	}
	if input.ExpiresAt.IsZero() {
		return fmt.Errorf("approval input expires_at is required")
	}
	if !input.ExpiresAt.UTC().After(now) {
		return ErrApprovalExpired
	}
	return nil
}

func approvalRequestFromInput(input RequestApprovalInput, stepID, token string, revision int64, requestedAt time.Time) ApprovalRequest {
	return ApprovalRequest{
		RunID:          strings.TrimSpace(input.RunID),
		StepID:         strings.TrimSpace(stepID),
		Revision:       revision,
		ApprovalType:   strings.TrimSpace(input.ApprovalType),
		Title:          strings.TrimSpace(input.Title),
		Summary:        strings.TrimSpace(input.Summary),
		CapabilityName: strings.TrimSpace(input.CapabilityName),
		PayloadJSON:    append([]byte(nil), input.PayloadJSON...),
		Token:          strings.TrimSpace(token),
		RequestedAt:    requestedAt.UTC(),
		ExpiresAt:      input.ExpiresAt.UTC(),
	}
}

func marshalApprovalStepState(request ApprovalRequest) ([]byte, error) {
	return json.Marshal(approvalStepState{
		ApprovalType:   request.ApprovalType,
		Title:          request.Title,
		Summary:        request.Summary,
		CapabilityName: request.CapabilityName,
		RequestedAt:    request.RequestedAt.UTC(),
		ExpiresAt:      request.ExpiresAt.UTC(),
	})
}

func unmarshalApprovalStepState(raw []byte) (approvalStepState, error) {
	state := approvalStepState{}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return state, nil
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		return approvalStepState{}, err
	}
	return state, nil
}
