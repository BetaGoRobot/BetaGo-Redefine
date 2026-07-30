package conversationeval

import (
	"fmt"
	"sort"
	"time"
)

const (
	PreWindowMessageLimit  = 20
	PostWindowMessageLimit = 50
	PostWindowMaxAge       = 15 * time.Minute
)

type WindowPosition string

const (
	WindowPositionPre    WindowPosition = "pre"
	WindowPositionAnchor WindowPosition = "anchor"
	WindowPositionPost   WindowPosition = "post"
)

type WindowMessage struct {
	EventID          string         `json:"event_id"`
	MessageID        string         `json:"message_id"`
	ChatID           string         `json:"chat_id"`
	TopicID          string         `json:"topic_id,omitempty"`
	SenderOpenID     string         `json:"sender_open_id,omitempty"`
	ReplyToMessageID string         `json:"reply_to_message_id,omitempty"`
	Content          string         `json:"content"`
	OccurredAt       time.Time      `json:"occurred_at"`
	Position         WindowPosition `json:"position"`
	Sequence         int            `json:"sequence"`
}

func (m WindowMessage) Validate() error {
	for name, value := range map[string]string{
		"window event_id":   m.EventID,
		"window message_id": m.MessageID,
		"window chat_id":    m.ChatID,
	} {
		if err := validateID(name, value); err != nil {
			return err
		}
	}
	if m.OccurredAt.IsZero() {
		return contractError("window message occurred_at must not be zero")
	}
	switch m.Position {
	case WindowPositionPre, WindowPositionAnchor, WindowPositionPost:
	default:
		return contractError("invalid window position %q", m.Position)
	}
	if m.Sequence < 0 {
		return contractError("window message sequence must not be negative")
	}
	return nil
}

// SelectPreWindow returns a stable chronological snapshot of the last 20
// messages strictly before the anchor. Input order is deliberately ignored so
// retries and OpenSearch pagination produce the same episode.
func SelectPreWindow(messages []WindowMessage, anchorAt time.Time) ([]WindowMessage, error) {
	if anchorAt.IsZero() {
		return nil, contractError("pre-window anchor_at must not be zero")
	}
	eligible := make([]WindowMessage, 0, len(messages))
	seen := make(map[string]struct{}, len(messages))
	for _, message := range messages {
		if message.OccurredAt.IsZero() {
			return nil, contractError("pre-window message %q has no occurred_at", message.MessageID)
		}
		if !message.OccurredAt.Before(anchorAt) {
			continue
		}
		key := message.EventID
		if key == "" {
			key = message.MessageID
		}
		if key == "" {
			return nil, contractError("pre-window message has no identity")
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		message.Position = WindowPositionPre
		eligible = append(eligible, message)
	}
	sort.SliceStable(eligible, func(i, j int) bool {
		if eligible[i].OccurredAt.Equal(eligible[j].OccurredAt) {
			if eligible[i].EventID == eligible[j].EventID {
				return eligible[i].MessageID < eligible[j].MessageID
			}
			return eligible[i].EventID < eligible[j].EventID
		}
		return eligible[i].OccurredAt.Before(eligible[j].OccurredAt)
	})
	if len(eligible) > PreWindowMessageLimit {
		eligible = eligible[len(eligible)-PreWindowMessageLimit:]
	}
	for index := range eligible {
		eligible[index].Sequence = index
	}
	return eligible, nil
}

type PostWindowCloseReason string

const (
	PostWindowCloseTopicBoundary PostWindowCloseReason = "topic_boundary"
	PostWindowCloseTimeLimit     PostWindowCloseReason = "time_limit"
	PostWindowCloseMessageLimit  PostWindowCloseReason = "message_limit"
)

type PostWindow struct {
	AnchorAt    time.Time             `json:"anchor_at"`
	TopicID     string                `json:"topic_id,omitempty"`
	Messages    []WindowMessage       `json:"messages"`
	ClosedAt    *time.Time            `json:"closed_at,omitempty"`
	CloseReason PostWindowCloseReason `json:"close_reason,omitempty"`
}

func NewPostWindow(anchorAt time.Time, topicID string) (*PostWindow, error) {
	if anchorAt.IsZero() {
		return nil, contractError("post-window anchor_at must not be zero")
	}
	return &PostWindow{
		AnchorAt: anchorAt,
		TopicID:  topicID,
		Messages: []WindowMessage{},
	}, nil
}

func (w *PostWindow) Append(message WindowMessage, topicBoundary bool) (bool, error) {
	if w == nil || w.AnchorAt.IsZero() {
		return false, contractError("post-window is not initialized")
	}
	if w.ClosedAt != nil {
		return false, fmt.Errorf("%w: post-window is already closed", ErrInvalidTransition)
	}
	if message.OccurredAt.IsZero() || !message.OccurredAt.After(w.AnchorAt) {
		return false, contractError("post-window message occurred_at must follow anchor")
	}
	deadline := w.AnchorAt.Add(PostWindowMaxAge)
	if !message.OccurredAt.Before(deadline) {
		w.close(deadline, PostWindowCloseTimeLimit)
		return false, nil
	}
	if topicBoundary {
		w.close(message.OccurredAt, PostWindowCloseTopicBoundary)
		return false, nil
	}
	if message.EventID == "" || message.MessageID == "" || message.ChatID == "" {
		return false, contractError("post-window message identity is incomplete")
	}
	for _, existing := range w.Messages {
		if existing.EventID == message.EventID {
			return false, nil
		}
	}
	message.Position = WindowPositionPost
	message.Sequence = len(w.Messages)
	w.Messages = append(w.Messages, message)
	if len(w.Messages) == PostWindowMessageLimit {
		w.close(message.OccurredAt, PostWindowCloseMessageLimit)
	}
	return true, nil
}

func (w *PostWindow) Advance(now time.Time) (bool, error) {
	if w == nil || w.AnchorAt.IsZero() || now.IsZero() {
		return false, contractError("post-window advance requires initialized timestamps")
	}
	if w.ClosedAt != nil {
		return false, nil
	}
	deadline := w.AnchorAt.Add(PostWindowMaxAge)
	if now.Before(deadline) {
		return false, nil
	}
	w.close(deadline, PostWindowCloseTimeLimit)
	return true, nil
}

func (w *PostWindow) close(at time.Time, reason PostWindowCloseReason) {
	closedAt := at
	w.ClosedAt = &closedAt
	w.CloseReason = reason
}

type FeedbackWindowDecision struct {
	Attach                 bool
	IncrementResultVersion bool
	Reason                 string
}

// DecideFeedbackWindow applies one temporal policy to every feedback source.
// Explicit feedback remains attributable through the full 24-hour grace
// period. Inferred feedback is accepted only while the causal post-window is
// still open.
func DecideFeedbackWindow(
	episode Episode,
	explicitness FeedbackExplicitness,
	occurredAt time.Time,
	aggregateFinalized bool,
) FeedbackWindowDecision {
	if occurredAt.IsZero() || occurredAt.Before(episode.AnchorAt) {
		return FeedbackWindowDecision{Reason: "outside_episode"}
	}
	if occurredAt.After(episode.LateFeedbackUntil) {
		return FeedbackWindowDecision{Reason: "late_feedback_expired"}
	}
	switch explicitness {
	case FeedbackExplicit:
		return FeedbackWindowDecision{
			Attach:                 true,
			IncrementResultVersion: aggregateFinalized,
			Reason:                 "explicit_feedback",
		}
	case FeedbackInferred:
		if episode.PostWindowEnd == nil {
			if !occurredAt.Before(episode.AnchorAt.Add(PostWindowMaxAge)) {
				return FeedbackWindowDecision{Reason: "outside_post_window"}
			}
		} else if occurredAt.After(*episode.PostWindowEnd) {
			return FeedbackWindowDecision{Reason: "outside_post_window"}
		}
		return FeedbackWindowDecision{
			Attach:                 true,
			IncrementResultVersion: aggregateFinalized,
			Reason:                 "inferred_feedback",
		}
	default:
		return FeedbackWindowDecision{Reason: "invalid_explicitness"}
	}
}

func NextAggregateResultVersion(current int64, decision FeedbackWindowDecision) int64 {
	if current < 0 {
		current = 0
	}
	if decision.Attach && decision.IncrementResultVersion {
		return current + 1
	}
	return current
}
