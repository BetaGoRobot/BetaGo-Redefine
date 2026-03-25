package approval

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ApprovalReservationDecisionOutcome names a approval flow type.
type ApprovalReservationDecisionOutcome string

const (
	ApprovalReservationDecisionApproved ApprovalReservationDecisionOutcome = "approved"
	ApprovalReservationDecisionRejected ApprovalReservationDecisionOutcome = "rejected"
)

// ApprovalReservationDecision carries approval flow state.
type ApprovalReservationDecision struct {
	Outcome     ApprovalReservationDecisionOutcome `json:"outcome,omitempty"`
	ActorOpenID string                             `json:"actor_open_id,omitempty"`
	OccurredAt  time.Time                          `json:"occurred_at,omitempty"`
}

// ApprovalReservation carries approval flow state.
type ApprovalReservation struct {
	RunID          string                       `json:"run_id"`
	StepID         string                       `json:"step_id"`
	Token          string                       `json:"token"`
	ApprovalType   string                       `json:"approval_type,omitempty"`
	Title          string                       `json:"title"`
	Summary        string                       `json:"summary,omitempty"`
	CapabilityName string                       `json:"capability_name,omitempty"`
	PayloadJSON    []byte                       `json:"payload_json,omitempty"`
	RequestedAt    time.Time                    `json:"requested_at,omitempty"`
	ExpiresAt      time.Time                    `json:"expires_at"`
	Decision       *ApprovalReservationDecision `json:"decision,omitempty"`
}

// ActivateReservedApprovalInput carries approval flow state.
type ActivateReservedApprovalInput struct {
	RunID       string    `json:"run_id"`
	StepID      string    `json:"step_id,omitempty"`
	Token       string    `json:"token,omitempty"`
	RequestedAt time.Time `json:"requested_at,omitempty"`
}

// NewApprovalReservationDecision implements approval flow behavior.
func NewApprovalReservationDecision(outcome ApprovalReservationDecisionOutcome, actorOpenID string, occurredAt time.Time) ApprovalReservationDecision {
	return ApprovalReservationDecision{
		Outcome:     ApprovalReservationDecisionOutcome(strings.TrimSpace(string(outcome))),
		ActorOpenID: strings.TrimSpace(actorOpenID),
		OccurredAt:  normalizeTime(occurredAt),
	}
}

// Validate implements approval flow behavior.
func (d ApprovalReservationDecision) Validate() error {
	switch d.Outcome {
	case ApprovalReservationDecisionApproved, ApprovalReservationDecisionRejected:
		return nil
	default:
		return fmt.Errorf("approval reservation decision outcome is invalid: %q", d.Outcome)
	}
}

func (r ApprovalReservation) normalize() ApprovalReservation {
	reservation := ApprovalReservation{
		RunID:          strings.TrimSpace(r.RunID),
		StepID:         strings.TrimSpace(r.StepID),
		Token:          strings.TrimSpace(r.Token),
		ApprovalType:   strings.TrimSpace(r.ApprovalType),
		Title:          strings.TrimSpace(r.Title),
		Summary:        strings.TrimSpace(r.Summary),
		CapabilityName: strings.TrimSpace(r.CapabilityName),
		PayloadJSON:    append([]byte(nil), r.PayloadJSON...),
		RequestedAt:    normalizeTime(r.RequestedAt),
		ExpiresAt:      r.ExpiresAt.UTC(),
	}
	if r.Decision != nil {
		decision := NewApprovalReservationDecision(r.Decision.Outcome, r.Decision.ActorOpenID, r.Decision.OccurredAt)
		reservation.Decision = &decision
	}
	return reservation
}

// Validate implements approval flow behavior.
func (r ApprovalReservation) Validate(now time.Time) error {
	reservation := r.normalize()
	if strings.TrimSpace(reservation.RunID) == "" {
		return fmt.Errorf("approval reservation run_id is required")
	}
	if strings.TrimSpace(reservation.StepID) == "" {
		return fmt.Errorf("approval reservation step_id is required")
	}
	if strings.TrimSpace(reservation.Token) == "" {
		return fmt.Errorf("approval reservation token is required")
	}
	if strings.TrimSpace(reservation.Title) == "" {
		return fmt.Errorf("approval reservation title is required")
	}
	if reservation.ExpiresAt.IsZero() {
		return fmt.Errorf("approval reservation expires_at is required")
	}
	now = normalizeTime(now)
	if !reservation.ExpiresAt.After(now) {
		return ErrApprovalExpired
	}
	if reservation.Decision != nil {
		if err := reservation.Decision.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// ApprovalRequest implements approval flow behavior.
func (r ApprovalReservation) ApprovalRequest(revision int64) ApprovalRequest {
	reservation := r.normalize()
	return ApprovalRequest{
		RunID:          reservation.RunID,
		StepID:         reservation.StepID,
		Revision:       revision,
		ApprovalType:   reservation.ApprovalType,
		Title:          reservation.Title,
		Summary:        reservation.Summary,
		CapabilityName: reservation.CapabilityName,
		PayloadJSON:    append([]byte(nil), reservation.PayloadJSON...),
		Token:          reservation.Token,
		RequestedAt:    reservation.RequestedAt.UTC(),
		ExpiresAt:      reservation.ExpiresAt.UTC(),
	}
}

// Encode implements approval flow behavior.
func (r ApprovalReservation) Encode() ([]byte, error) {
	return json.Marshal(r.normalize())
}

// DecodeApprovalReservation implements approval flow behavior.
func DecodeApprovalReservation(raw []byte) (ApprovalReservation, error) {
	reservation := ApprovalReservation{}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return reservation, nil
	}
	if err := json.Unmarshal(raw, &reservation); err != nil {
		return ApprovalReservation{}, err
	}
	return reservation.normalize(), nil
}

// TTL implements approval flow behavior.
func (r ApprovalReservation) TTL(now time.Time) time.Duration {
	now = normalizeTime(now)
	expiresAt := r.ExpiresAt.UTC()
	if expiresAt.IsZero() || !expiresAt.After(now) {
		return time.Hour
	}
	ttl := expiresAt.Sub(now) + time.Hour
	if ttl < time.Minute {
		return time.Minute
	}
	return ttl
}

func normalizeTime(ts time.Time) time.Time {
	if ts.IsZero() {
		return time.Now().UTC()
	}
	return ts.UTC()
}
