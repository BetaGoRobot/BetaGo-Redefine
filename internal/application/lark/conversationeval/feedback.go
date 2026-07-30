package conversationeval

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

// FeedbackSink is the transport-facing contract for user interactions that
// may evaluate a previously delivered agent response.
type FeedbackSink interface {
	ObserveMessage(context.Context, MessageFeedback) error
	ObserveReaction(context.Context, ReactionFeedback) error
	ObserveCardAction(context.Context, CardFeedback) error
}

type MessageFeedback struct {
	EventID            string
	MessageID          string
	ChatID             string
	TopicID            string
	ActorOpenID        string
	ReplyToMessageID   string
	Content            string
	ExplicitCorrection bool
	OccurredAt         time.Time
}

type ReactionFeedback struct {
	EventID         string
	ChatID          string
	ActorOpenID     string
	TargetMessageID string
	ReactionType    string
	OccurredAt      time.Time
}

type CardFeedback struct {
	EventID         string
	ChatID          string
	ActorOpenID     string
	TargetMessageID string
	ActionName      string
	Value           json.RawMessage
	OccurredAt      time.Time
}

// FeedbackCandidate contains only serving-lane delivery information. Shadow
// output identities must never be exposed through this contract.
type FeedbackCandidate struct {
	Episode           Episode
	DeliveryMessageID string
}

type FeedbackAttributionStore interface {
	FeedbackCandidates(context.Context, string, time.Time) ([]FeedbackCandidate, error)
	AppendFeedback(context.Context, Feedback) error
}

type FeedbackAttributor struct {
	store FeedbackAttributionStore
}

func NewFeedbackAttributor(store FeedbackAttributionStore) (*FeedbackAttributor, error) {
	if store == nil {
		return nil, fmt.Errorf("%w: feedback store is nil", ErrEvaluationUnavailable)
	}
	return &FeedbackAttributor{store: store}, nil
}

func (a *FeedbackAttributor) ObserveMessage(ctx context.Context, event MessageFeedback) error {
	if err := validateFeedbackEnvelope(
		event.EventID,
		event.ChatID,
		event.OccurredAt,
	); err != nil {
		return err
	}
	if err := validateID("feedback message_id", event.MessageID); err != nil {
		return err
	}
	feedbackType := FeedbackTypeSemanticInference
	explicitness := FeedbackInferred
	targetMessageID := ""
	sameTopicOnly := false
	switch {
	case event.ReplyToMessageID != "":
		if err := validateID("feedback reply_to_message_id", event.ReplyToMessageID); err != nil {
			return err
		}
		feedbackType = FeedbackTypeDirectReply
		explicitness = FeedbackExplicit
		targetMessageID = event.ReplyToMessageID
	case event.ExplicitCorrection:
		feedbackType = FeedbackTypeCorrection
		explicitness = FeedbackExplicit
		sameTopicOnly = true
	}
	content, err := json.Marshal(map[string]any{
		"message_id": event.MessageID, "topic_id": event.TopicID,
		"actor_open_id": event.ActorOpenID, "reply_to_message_id": event.ReplyToMessageID,
		"text": event.Content,
	})
	if err != nil {
		return fmt.Errorf("marshal message feedback: %w", err)
	}
	return a.attribute(ctx, feedbackObservation{
		eventID: event.EventID, chatID: event.ChatID, topicID: event.TopicID,
		targetMessageID: targetMessageID, feedbackType: feedbackType,
		explicitness: explicitness, sameTopicOnly: sameTopicOnly,
		content: content, occurredAt: event.OccurredAt,
	})
}

func (a *FeedbackAttributor) ObserveReaction(ctx context.Context, event ReactionFeedback) error {
	if err := validateFeedbackEnvelope(event.EventID, event.ChatID, event.OccurredAt); err != nil {
		return err
	}
	if err := validateID("reaction target_message_id", event.TargetMessageID); err != nil {
		return err
	}
	if strings.TrimSpace(event.ReactionType) == "" {
		return contractError("reaction type must not be empty")
	}
	content, err := json.Marshal(map[string]any{
		"actor_open_id": event.ActorOpenID, "reaction_type": event.ReactionType,
	})
	if err != nil {
		return fmt.Errorf("marshal reaction feedback: %w", err)
	}
	return a.attribute(ctx, feedbackObservation{
		eventID: event.EventID, chatID: event.ChatID,
		targetMessageID: event.TargetMessageID, feedbackType: FeedbackTypeReaction,
		explicitness: FeedbackExplicit, content: content, occurredAt: event.OccurredAt,
	})
}

func (a *FeedbackAttributor) ObserveCardAction(ctx context.Context, event CardFeedback) error {
	if err := validateFeedbackEnvelope(event.EventID, event.ChatID, event.OccurredAt); err != nil {
		return err
	}
	if err := validateID("card target_message_id", event.TargetMessageID); err != nil {
		return err
	}
	if strings.TrimSpace(event.ActionName) == "" {
		return contractError("card action name must not be empty")
	}
	value := event.Value
	if len(value) == 0 {
		value = json.RawMessage(`{}`)
	}
	if err := validateJSONObject("card action value", value); err != nil {
		return err
	}
	var decodedValue map[string]any
	if err := json.Unmarshal(value, &decodedValue); err != nil {
		return fmt.Errorf("decode card action value: %w", err)
	}
	content, err := json.Marshal(map[string]any{
		"actor_open_id": event.ActorOpenID, "action_name": event.ActionName,
		"value": decodedValue,
	})
	if err != nil {
		return fmt.Errorf("marshal card feedback: %w", err)
	}
	return a.attribute(ctx, feedbackObservation{
		eventID: event.EventID, chatID: event.ChatID,
		targetMessageID: event.TargetMessageID, feedbackType: FeedbackTypeCardAction,
		explicitness: FeedbackExplicit, content: content, occurredAt: event.OccurredAt,
	})
}

type feedbackObservation struct {
	eventID         string
	chatID          string
	topicID         string
	targetMessageID string
	feedbackType    FeedbackType
	explicitness    FeedbackExplicitness
	sameTopicOnly   bool
	content         json.RawMessage
	occurredAt      time.Time
}

func (a *FeedbackAttributor) attribute(ctx context.Context, observation feedbackObservation) error {
	candidates, err := a.store.FeedbackCandidates(ctx, observation.chatID, observation.occurredAt)
	if err != nil {
		return fmt.Errorf("load feedback candidates: %w", err)
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Episode.AnchorAt.Equal(candidates[j].Episode.AnchorAt) {
			return candidates[i].Episode.ID < candidates[j].Episode.ID
		}
		return candidates[i].Episode.AnchorAt.After(candidates[j].Episode.AnchorAt)
	})

	selected := exactDeliveryCandidates(candidates, observation)
	targeted := len(selected) > 0
	if !targeted {
		selected = contextCandidates(candidates, observation)
	}
	for _, candidate := range selected {
		decision := DecideFeedbackWindow(
			candidate.Episode,
			observation.explicitness,
			observation.occurredAt,
			false,
		)
		if !decision.Attach {
			continue
		}
		feedback := Feedback{
			ID:        evaluationID("feedback", candidate.Episode.ID, observation.eventID),
			EpisodeID: candidate.Episode.ID, FeedbackEventID: observation.eventID,
			FeedbackType: observation.feedbackType, Explicitness: observation.explicitness,
			ContentJSON: observation.content, AttributionConfidence: feedbackConfidence(
				observation.feedbackType,
				targeted,
			),
			OccurredAt: observation.occurredAt,
		}
		if targeted {
			feedback.TargetLane = candidate.Episode.ServingLane
			feedback.TargetMessageID = candidate.DeliveryMessageID
		}
		if err := a.store.AppendFeedback(ctx, feedback); err != nil {
			return fmt.Errorf("append feedback for episode %q: %w", candidate.Episode.ID, err)
		}
	}
	return nil
}

func exactDeliveryCandidates(
	candidates []FeedbackCandidate,
	observation feedbackObservation,
) []FeedbackCandidate {
	if observation.targetMessageID == "" {
		return nil
	}
	selected := make([]FeedbackCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.DeliveryMessageID == observation.targetMessageID {
			selected = append(selected, candidate)
		}
	}
	return selected
}

func contextCandidates(
	candidates []FeedbackCandidate,
	observation feedbackObservation,
) []FeedbackCandidate {
	selected := make([]FeedbackCandidate, 0, len(candidates))
	seenCohorts := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		if _, exists := seenCohorts[candidate.Episode.CohortID]; exists {
			continue
		}
		if observation.sameTopicOnly &&
			(observation.topicID == "" || candidate.Episode.TopicID != observation.topicID) {
			continue
		}
		decision := DecideFeedbackWindow(
			candidate.Episode,
			observation.explicitness,
			observation.occurredAt,
			false,
		)
		if !decision.Attach {
			continue
		}
		seenCohorts[candidate.Episode.CohortID] = struct{}{}
		selected = append(selected, candidate)
	}
	return selected
}

func feedbackConfidence(feedbackType FeedbackType, targeted bool) int {
	if targeted {
		return 100
	}
	switch feedbackType {
	case FeedbackTypeCorrection:
		return 90
	case FeedbackTypeDirectReply, FeedbackTypeReaction, FeedbackTypeCardAction:
		return 75
	default:
		return 60
	}
}

func validateFeedbackEnvelope(eventID, chatID string, occurredAt time.Time) error {
	if err := validateID("feedback event_id", eventID); err != nil {
		return err
	}
	if err := validateID("feedback chat_id", chatID); err != nil {
		return err
	}
	if occurredAt.IsZero() {
		return contractError("feedback occurred_at must not be zero")
	}
	return nil
}

// IsExplicitCorrection intentionally uses a narrow lexical gate. Broad
// semantic inference remains a lower-priority, non-explicit signal.
func IsExplicitCorrection(content string) bool {
	normalized := strings.ToLower(strings.TrimSpace(content))
	if normalized == "" || strings.Contains(normalized, "不错") {
		return false
	}
	for _, marker := range []string{
		"不对", "错了", "不是这个", "应该是", "我说的是", "我的意思是",
		"纠正", "更正", "i meant", "that's wrong", "that is wrong", "correction",
	} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

// FeedbackRouter is a process-scoped DI bridge. It lets transport handlers be
// constructed before the database-backed runtime starts without introducing a
// mutable package-global sink.
type FeedbackRouter struct {
	target atomic.Pointer[feedbackSinkHolder]
}

type feedbackSinkHolder struct {
	sink FeedbackSink
}

func NewFeedbackRouter() *FeedbackRouter {
	return &FeedbackRouter{}
}

func (r *FeedbackRouter) Bind(sink FeedbackSink) {
	if r == nil {
		return
	}
	if sink == nil {
		r.target.Store(nil)
		return
	}
	r.target.Store(&feedbackSinkHolder{sink: sink})
}

func (r *FeedbackRouter) ObserveMessage(ctx context.Context, event MessageFeedback) error {
	if holder := r.load(); holder != nil {
		return holder.sink.ObserveMessage(ctx, event)
	}
	return nil
}

func (r *FeedbackRouter) ObserveReaction(ctx context.Context, event ReactionFeedback) error {
	if holder := r.load(); holder != nil {
		return holder.sink.ObserveReaction(ctx, event)
	}
	return nil
}

func (r *FeedbackRouter) ObserveCardAction(ctx context.Context, event CardFeedback) error {
	if holder := r.load(); holder != nil {
		return holder.sink.ObserveCardAction(ctx, event)
	}
	return nil
}

func (r *FeedbackRouter) load() *feedbackSinkHolder {
	if r == nil {
		return nil
	}
	return r.target.Load()
}
