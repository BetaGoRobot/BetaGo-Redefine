package agentruntime

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"strconv"
	"strings"
	"time"
)

type EventType string

const (
	EventTypeMessage          EventType = "message"
	EventTypeCardAction       EventType = "card_action"
	EventTypeCapabilityResult EventType = "capability_result"
	EventTypeSchedule         EventType = "schedule"
	EventTypeAsyncResult      EventType = "async_result"
	EventTypeTimeout          EventType = "timeout"
)

var ErrInvalidRuntimeContract = errors.New("invalid conversation runtime contract")

type RuntimeEnvelope struct {
	RunID           string `json:"run_id"`
	StepID          string `json:"step_id"`
	InteractionID   string `json:"interaction_id"`
	Revision        int64  `json:"revision"`
	Token           string `json:"token"`
	InteractionKind string `json:"interaction_kind"`
	ContinueAgent   bool   `json:"continue_agent"`
}

func (e RuntimeEnvelope) Validate() error {
	if err := validateCanonical("run_id", e.RunID); err != nil {
		return err
	}
	if err := validateCanonical("step_id", e.StepID); err != nil {
		return err
	}
	if err := validateCanonical("interaction_id", e.InteractionID); err != nil {
		return err
	}
	if err := validateCanonical("interaction_kind", e.InteractionKind); err != nil {
		return err
	}
	if err := validateCanonical("token", e.Token); err != nil {
		return err
	}
	if e.Revision <= 0 {
		return invalidRuntimeContract("revision must be positive")
	}
	if !e.ContinueAgent {
		return invalidRuntimeContract("continue_agent must be true")
	}
	return nil
}

type ConversationEvent struct {
	ID            string          `json:"id"`
	Type          EventType       `json:"type"`
	ChatID        string          `json:"chat_id"`
	ActorOpenID   string          `json:"actor_open_id"`
	RunID         string          `json:"run_id"`
	InteractionID string          `json:"interaction_id"`
	Revision      int64           `json:"revision"`
	Action        string          `json:"action"`
	SourceRef     string          `json:"source_ref"`
	OccurredAt    time.Time       `json:"occurred_at"`
	Payload       json.RawMessage `json:"payload"`
}

func (e ConversationEvent) DedupeKey() (string, error) {
	if sourceRef := strings.TrimSpace(e.SourceRef); sourceRef != "" {
		return "source:" + sourceRef, nil
	}
	if e.Type == EventTypeCardAction {
		interactionID := strings.TrimSpace(e.InteractionID)
		action := strings.TrimSpace(e.Action)
		if interactionID == "" || action == "" || e.Revision <= 0 {
			return "", invalidRuntimeContract("card action dedupe fields are invalid")
		}
		return fmt.Sprintf("card_action:%s:%d:%s", interactionID, e.Revision, action), nil
	}
	if !isKnownEventType(e.Type) {
		return "", invalidRuntimeContract("event type is invalid")
	}

	eventType := string(e.Type)
	if id := strings.TrimSpace(e.ID); id != "" {
		return eventType + ":" + id, nil
	}
	if !e.hasDedupeMaterial() {
		return "", invalidRuntimeContract("event has no dedupe material")
	}

	digest := sha256.New()
	writeDedupePart(digest, string(e.Type))
	writeDedupePart(digest, strings.TrimSpace(e.ChatID))
	writeDedupePart(digest, strings.TrimSpace(e.ActorOpenID))
	writeDedupePart(digest, strings.TrimSpace(e.RunID))
	writeDedupePart(digest, strings.TrimSpace(e.InteractionID))
	writeDedupePart(digest, strconv.FormatInt(e.Revision, 10))
	writeDedupePart(digest, strings.TrimSpace(e.Action))
	writeDedupePart(digest, e.OccurredAt.UTC().Format(time.RFC3339Nano))
	writeDedupePart(digest, string(e.Payload))
	return fmt.Sprintf("%s:%x", eventType, digest.Sum(nil)), nil
}

func (e ConversationEvent) hasDedupeMaterial() bool {
	return strings.TrimSpace(e.ChatID) != "" ||
		strings.TrimSpace(e.ActorOpenID) != "" ||
		strings.TrimSpace(e.RunID) != "" ||
		strings.TrimSpace(e.InteractionID) != "" ||
		e.Revision != 0 ||
		strings.TrimSpace(e.Action) != "" ||
		!e.OccurredAt.IsZero() ||
		len(bytes.TrimSpace(e.Payload)) > 0
}

func isKnownEventType(eventType EventType) bool {
	switch eventType {
	case EventTypeMessage,
		EventTypeCardAction,
		EventTypeCapabilityResult,
		EventTypeSchedule,
		EventTypeAsyncResult,
		EventTypeTimeout:
		return true
	default:
		return false
	}
}

func writeDedupePart(digest hash.Hash, value string) {
	_, _ = fmt.Fprintf(digest, "%d:", len(value))
	_, _ = digest.Write([]byte(value))
	_, _ = digest.Write([]byte{0})
}

func validateCanonical(field, value string) error {
	if value == "" {
		return invalidRuntimeContract(field + " is required")
	}
	if strings.TrimSpace(value) != value {
		return invalidRuntimeContract(field + " must not have surrounding whitespace")
	}
	return nil
}

func invalidRuntimeContract(reason string) error {
	return fmt.Errorf("%w: %s", ErrInvalidRuntimeContract, reason)
}
