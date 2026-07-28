package agentruntime

import (
	"crypto/sha256"
	"encoding/json"
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
	switch {
	case strings.TrimSpace(e.RunID) == "":
		return fmt.Errorf("runtime envelope run_id is required")
	case strings.TrimSpace(e.StepID) == "":
		return fmt.Errorf("runtime envelope step_id is required")
	case strings.TrimSpace(e.InteractionID) == "":
		return fmt.Errorf("runtime envelope interaction_id is required")
	case e.Revision <= 0:
		return fmt.Errorf("runtime envelope revision must be positive")
	case strings.TrimSpace(e.Token) == "":
		return fmt.Errorf("runtime envelope token is required")
	case !e.ContinueAgent:
		return fmt.Errorf("runtime envelope continue_agent must be true")
	default:
		return nil
	}
}

type ConversationEvent struct {
	ID            string
	Type          EventType
	ChatID        string
	ActorOpenID   string
	RunID         string
	InteractionID string
	Revision      int64
	Action        string
	SourceRef     string
	OccurredAt    time.Time
	Payload       json.RawMessage
}

func (e ConversationEvent) DedupeKey() string {
	if sourceRef := strings.TrimSpace(e.SourceRef); sourceRef != "" {
		return "source:" + sourceRef
	}
	if e.Type == EventTypeCardAction {
		return fmt.Sprintf("card_action:%s:%d:%s", e.InteractionID, e.Revision, e.Action)
	}

	eventType := string(e.Type)
	if eventType == "" {
		eventType = "event"
	}
	if id := strings.TrimSpace(e.ID); id != "" {
		return eventType + ":" + id
	}

	digest := sha256.New()
	writeDedupePart(digest, string(e.Type))
	writeDedupePart(digest, e.ChatID)
	writeDedupePart(digest, e.ActorOpenID)
	writeDedupePart(digest, e.RunID)
	writeDedupePart(digest, e.InteractionID)
	writeDedupePart(digest, strconv.FormatInt(e.Revision, 10))
	writeDedupePart(digest, e.Action)
	writeDedupePart(digest, e.OccurredAt.UTC().Format(time.RFC3339Nano))
	writeDedupePart(digest, string(e.Payload))
	return fmt.Sprintf("%s:%x", eventType, digest.Sum(nil))
}

func writeDedupePart(digest hash.Hash, value string) {
	_, _ = fmt.Fprintf(digest, "%d:", len(value))
	_, _ = digest.Write([]byte(value))
	_, _ = digest.Write([]byte{0})
}
