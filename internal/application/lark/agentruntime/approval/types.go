package approval

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	cardactionproto "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
)

const ResumeSourceApproval = "approval"

var (
	// ErrApprovalExpired is an exported approval flow constant.
	ErrApprovalExpired = errors.New("agent runtime approval expired")
	// ErrApprovalStateConflict is an exported approval flow constant.
	ErrApprovalStateConflict = errors.New("agent runtime approval state conflict")
	// ErrApprovalReservationNotFound is an exported approval flow constant.
	ErrApprovalReservationNotFound = errors.New("agent runtime approval reservation not found")
)

// ApprovalCardDelivery names a approval flow type.
type ApprovalCardDelivery string

const (
	ApprovalCardDeliveryEphemeral ApprovalCardDelivery = "ephemeral"
	ApprovalCardDeliveryMessage   ApprovalCardDelivery = "message"
)

// RequestApprovalInput carries approval flow state.
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

// Validate implements approval flow behavior.
func (r RequestApprovalInput) Validate(now time.Time) error {
	if strings.TrimSpace(r.RunID) == "" {
		return fmt.Errorf("approval input run_id is required")
	}
	if strings.TrimSpace(r.Title) == "" {
		return fmt.Errorf("approval input title is required")
	}
	if r.ExpiresAt.IsZero() {
		return fmt.Errorf("approval input expires_at is required")
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

// ApprovalRequest carries approval flow state.
type ApprovalRequest struct {
	RunID          string               `json:"run_id"`
	StepID         string               `json:"step_id"`
	Revision       int64                `json:"revision"`
	ApprovalType   string               `json:"approval_type,omitempty"`
	Title          string               `json:"title"`
	Summary        string               `json:"summary,omitempty"`
	CapabilityName string               `json:"capability_name,omitempty"`
	PayloadJSON    []byte               `json:"payload_json,omitempty"`
	Token          string               `json:"token"`
	RequestedAt    time.Time            `json:"requested_at,omitempty"`
	ExpiresAt      time.Time            `json:"expires_at"`
	Delivery       ApprovalCardDelivery `json:"delivery,omitempty"`
}

type approvalStepState struct {
	ApprovalType   string    `json:"approval_type,omitempty"`
	Title          string    `json:"title"`
	Summary        string    `json:"summary,omitempty"`
	CapabilityName string    `json:"capability_name,omitempty"`
	RequestedAt    time.Time `json:"requested_at,omitempty"`
	ExpiresAt      time.Time `json:"expires_at,omitempty"`
}

// Validate implements approval flow behavior.
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

// ApprovePayload implements approval flow behavior.
func (r ApprovalRequest) ApprovePayload() map[string]string {
	builder := cardactionproto.New(cardactionproto.ActionAgentRuntimeResume).
		WithRunID(strings.TrimSpace(r.RunID)).
		WithStepID(strings.TrimSpace(r.StepID)).
		WithRevision(strconv.FormatInt(r.Revision, 10)).
		WithSource(ResumeSourceApproval).
		WithToken(strings.TrimSpace(r.Token))
	if delivery := string(normalizeApprovalCardDelivery(r.Delivery)); delivery != "" {
		builder.WithValue(cardactionproto.ApprovalDeliveryField, delivery)
	}
	return builder.Payload()
}

// RejectPayload implements approval flow behavior.
func (r ApprovalRequest) RejectPayload() map[string]string {
	builder := cardactionproto.New(cardactionproto.ActionAgentRuntimeReject).
		WithRunID(strings.TrimSpace(r.RunID)).
		WithStepID(strings.TrimSpace(r.StepID)).
		WithRevision(strconv.FormatInt(r.Revision, 10)).
		WithSource(ResumeSourceApproval).
		WithToken(strings.TrimSpace(r.Token))
	if delivery := string(normalizeApprovalCardDelivery(r.Delivery)); delivery != "" {
		builder.WithValue(cardactionproto.ApprovalDeliveryField, delivery)
	}
	return builder.Payload()
}

// BuildApprovalRequest implements approval flow behavior.
func BuildApprovalRequest(input RequestApprovalInput, stepID, token string, revision int64, requestedAt time.Time) ApprovalRequest {
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

// EncodeStepState implements approval flow behavior.
func (r ApprovalRequest) EncodeStepState() ([]byte, error) {
	return json.Marshal(approvalStepState{
		ApprovalType:   r.ApprovalType,
		Title:          r.Title,
		Summary:        r.Summary,
		CapabilityName: r.CapabilityName,
		RequestedAt:    r.RequestedAt.UTC(),
		ExpiresAt:      r.ExpiresAt.UTC(),
	})
}

// DecodeApprovalRequest implements approval flow behavior.
func DecodeApprovalRequest(runID, stepID string, revision int64, token string, raw []byte) (ApprovalRequest, error) {
	state := approvalStepState{}
	if len(strings.TrimSpace(string(raw))) > 0 {
		if err := json.Unmarshal(raw, &state); err != nil {
			return ApprovalRequest{}, err
		}
	}
	return ApprovalRequest{
		RunID:          strings.TrimSpace(runID),
		StepID:         strings.TrimSpace(stepID),
		Revision:       revision,
		ApprovalType:   state.ApprovalType,
		Title:          state.Title,
		Summary:        state.Summary,
		CapabilityName: state.CapabilityName,
		Token:          strings.TrimSpace(token),
		RequestedAt:    state.RequestedAt,
		ExpiresAt:      state.ExpiresAt,
	}, nil
}

func normalizeApprovalCardDelivery(delivery ApprovalCardDelivery) ApprovalCardDelivery {
	switch ApprovalCardDelivery(strings.TrimSpace(string(delivery))) {
	case ApprovalCardDeliveryEphemeral:
		return ApprovalCardDeliveryEphemeral
	case ApprovalCardDeliveryMessage:
		return ApprovalCardDeliveryMessage
	default:
		return ""
	}
}
