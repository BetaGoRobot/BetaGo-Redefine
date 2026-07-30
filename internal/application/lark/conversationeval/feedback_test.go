package conversationeval

import (
	"context"
	"testing"
	"time"
)

func TestFeedbackAttributionPriority(t *testing.T) {
	anchor := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	postEnd := anchor.Add(10 * time.Minute)
	candidate := FeedbackCandidate{
		Episode: Episode{
			ID: "episode-1", CohortID: "cohort-1", ChatID: "chat-1",
			AnchorEventID: "anchor-event", AnchorMessageID: "anchor-message",
			TopicID:     "topic-1",
			ServingLane: LaneControl, Status: EpisodeStatusCollecting,
			PreWindowStart: anchor.Add(-time.Minute), AnchorAt: anchor,
			PostWindowEnd: &postEnd, LateFeedbackUntil: anchor.Add(LateFeedbackGracePeriod),
		},
		DeliveryMessageID: "delivered-message",
	}
	tests := []struct {
		name     string
		observe  func(*FeedbackAttributor) error
		wantType FeedbackType
		wantKind FeedbackExplicitness
		wantLane Lane
		wantID   string
	}{
		{
			name: "direct reply wins over same-thread correction",
			observe: func(attributor *FeedbackAttributor) error {
				return attributor.ObserveMessage(context.Background(), MessageFeedback{
					EventID: "feedback-message", MessageID: "feedback-message",
					ChatID: "chat-1", TopicID: "topic-1", Content: "不对，应该是明天",
					ReplyToMessageID: "delivered-message", ExplicitCorrection: true,
					OccurredAt: anchor.Add(time.Minute),
				})
			},
			wantType: FeedbackTypeDirectReply, wantKind: FeedbackExplicit,
			wantLane: LaneControl, wantID: "delivered-message",
		},
		{
			name: "reaction targets actual delivery",
			observe: func(attributor *FeedbackAttributor) error {
				return attributor.ObserveReaction(context.Background(), ReactionFeedback{
					EventID: "reaction-event", ChatID: "chat-1",
					TargetMessageID: "delivered-message", ActorOpenID: "user-1",
					ReactionType: "THUMBSUP", OccurredAt: anchor.Add(2 * time.Minute),
				})
			},
			wantType: FeedbackTypeReaction, wantKind: FeedbackExplicit,
			wantLane: LaneControl, wantID: "delivered-message",
		},
		{
			name: "card click targets actual delivery",
			observe: func(attributor *FeedbackAttributor) error {
				return attributor.ObserveCardAction(context.Background(), CardFeedback{
					EventID: "card-event", ChatID: "chat-1",
					TargetMessageID: "delivered-message", ActorOpenID: "user-1",
					ActionName: "schedule.confirm", OccurredAt: anchor.Add(3 * time.Minute),
				})
			},
			wantType: FeedbackTypeCardAction, wantKind: FeedbackExplicit,
			wantLane: LaneControl, wantID: "delivered-message",
		},
		{
			name: "same-thread explicit correction is episode context",
			observe: func(attributor *FeedbackAttributor) error {
				return attributor.ObserveMessage(context.Background(), MessageFeedback{
					EventID: "correction-event", MessageID: "correction-message",
					ChatID: "chat-1", TopicID: "topic-1", Content: "不对，应该是明天",
					ExplicitCorrection: true, OccurredAt: anchor.Add(4 * time.Minute),
				})
			},
			wantType: FeedbackTypeCorrection, wantKind: FeedbackExplicit,
		},
		{
			name: "semantic time inference is episode context",
			observe: func(attributor *FeedbackAttributor) error {
				return attributor.ObserveMessage(context.Background(), MessageFeedback{
					EventID: "semantic-event", MessageID: "semantic-message",
					ChatID: "chat-1", TopicID: "topic-1", Content: "那我们接着讨论",
					OccurredAt: anchor.Add(5 * time.Minute),
				})
			},
			wantType: FeedbackTypeSemanticInference, wantKind: FeedbackInferred,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &feedbackStoreFake{candidates: []FeedbackCandidate{candidate}}
			attributor, err := NewFeedbackAttributor(store)
			if err != nil {
				t.Fatalf("NewFeedbackAttributor() error = %v", err)
			}
			if err := test.observe(attributor); err != nil {
				t.Fatalf("observe feedback error = %v", err)
			}
			if len(store.feedback) != 1 {
				t.Fatalf("stored feedback = %#v, want one item", store.feedback)
			}
			got := store.feedback[0]
			if got.FeedbackType != test.wantType || got.Explicitness != test.wantKind ||
				got.TargetLane != test.wantLane || got.TargetMessageID != test.wantID {
				t.Fatalf("feedback = %#v", got)
			}
		})
	}
}

func TestDirectFeedbackWithoutActualDeliveryStaysEpisodeContext(t *testing.T) {
	anchor := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	store := &feedbackStoreFake{candidates: []FeedbackCandidate{{
		Episode: feedbackEpisode("episode-context", "cohort-context", anchor),
	}}}
	attributor, err := NewFeedbackAttributor(store)
	if err != nil {
		t.Fatalf("NewFeedbackAttributor() error = %v", err)
	}
	err = attributor.ObserveMessage(context.Background(), MessageFeedback{
		EventID: "direct-context-event", MessageID: "direct-context-message",
		ChatID: "chat-1", ReplyToMessageID: "some-other-message",
		Content: "收到", OccurredAt: anchor.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("ObserveMessage() error = %v", err)
	}
	if len(store.feedback) != 1 {
		t.Fatalf("feedback count = %d, want 1", len(store.feedback))
	}
	got := store.feedback[0]
	if got.FeedbackType != FeedbackTypeDirectReply ||
		got.TargetLane != "" || got.TargetMessageID != "" {
		t.Fatalf("feedback = %#v, want episode-only direct reply", got)
	}
}

func TestDirectFeedbackAttachesToEveryOverlappingCohortEpisode(t *testing.T) {
	anchor := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	store := &feedbackStoreFake{candidates: []FeedbackCandidate{
		{Episode: feedbackEpisode("episode-a", "cohort-a", anchor), DeliveryMessageID: "delivered"},
		{Episode: feedbackEpisode("episode-b", "cohort-b", anchor), DeliveryMessageID: "delivered"},
	}}
	attributor, err := NewFeedbackAttributor(store)
	if err != nil {
		t.Fatalf("NewFeedbackAttributor() error = %v", err)
	}
	err = attributor.ObserveReaction(context.Background(), ReactionFeedback{
		EventID: "reaction-overlap", ChatID: "chat-1", TargetMessageID: "delivered",
		ReactionType: "HEART", OccurredAt: anchor.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("ObserveReaction() error = %v", err)
	}
	if len(store.feedback) != 2 ||
		store.feedback[0].TargetLane != LaneControl ||
		store.feedback[1].TargetLane != LaneControl {
		t.Fatalf("feedback = %#v, want both cohort episodes targeted", store.feedback)
	}
}

func TestFeedbackOutsideCausalWindowsIsIgnored(t *testing.T) {
	anchor := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	store := &feedbackStoreFake{candidates: []FeedbackCandidate{{
		Episode: feedbackEpisode("episode-expired", "cohort-expired", anchor),
	}}}
	attributor, err := NewFeedbackAttributor(store)
	if err != nil {
		t.Fatalf("NewFeedbackAttributor() error = %v", err)
	}
	if err := attributor.ObserveMessage(context.Background(), MessageFeedback{
		EventID: "expired-explicit", MessageID: "expired-explicit-message",
		ChatID: "chat-1", Content: "不对", ExplicitCorrection: true,
		OccurredAt: anchor.Add(LateFeedbackGracePeriod + time.Second),
	}); err != nil {
		t.Fatalf("ObserveMessage(explicit) error = %v", err)
	}
	if err := attributor.ObserveMessage(context.Background(), MessageFeedback{
		EventID: "expired-inferred", MessageID: "expired-inferred-message",
		ChatID: "chat-1", Content: "继续", OccurredAt: anchor.Add(20 * time.Minute),
	}); err != nil {
		t.Fatalf("ObserveMessage(inferred) error = %v", err)
	}
	if len(store.feedback) != 0 {
		t.Fatalf("feedback = %#v, want no writes", store.feedback)
	}
}

func TestInferredFeedbackAttachesWhilePostWindowIsOpen(t *testing.T) {
	anchor := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	episode := feedbackEpisode("episode-open", "cohort-open", anchor)
	episode.PostWindowEnd = nil
	store := &feedbackStoreFake{candidates: []FeedbackCandidate{{Episode: episode}}}
	attributor, err := NewFeedbackAttributor(store)
	if err != nil {
		t.Fatalf("NewFeedbackAttributor() error = %v", err)
	}
	if err := attributor.ObserveMessage(context.Background(), MessageFeedback{
		EventID: "open-inference", MessageID: "open-inference-message",
		ChatID: "chat-1", TopicID: "topic-1", Content: "继续",
		OccurredAt: anchor.Add(time.Minute),
	}); err != nil {
		t.Fatalf("ObserveMessage() error = %v", err)
	}
	if len(store.feedback) != 1 ||
		store.feedback[0].FeedbackType != FeedbackTypeSemanticInference {
		t.Fatalf("feedback = %#v, want one open-window inference", store.feedback)
	}
}

func TestIsExplicitCorrectionIsConservative(t *testing.T) {
	for _, content := range []string{"不对，时间应该是明天", "更正一下", "I meant next week"} {
		if !IsExplicitCorrection(content) {
			t.Fatalf("IsExplicitCorrection(%q) = false", content)
		}
	}
	for _, content := range []string{"不错", "继续", "明天可以"} {
		if IsExplicitCorrection(content) {
			t.Fatalf("IsExplicitCorrection(%q) = true", content)
		}
	}
}

func TestFeedbackRouterSupportsLateBindingAndUnbinding(t *testing.T) {
	router := NewFeedbackRouter()
	event := MessageFeedback{
		EventID: "router-event", MessageID: "router-message", ChatID: "chat-1",
		Content: "continue", OccurredAt: time.Now().UTC(),
	}
	if err := router.ObserveMessage(context.Background(), event); err != nil {
		t.Fatalf("ObserveMessage(unbound) error = %v", err)
	}
	sink := &routerFeedbackSinkFake{}
	router.Bind(sink)
	if err := router.ObserveMessage(context.Background(), event); err != nil {
		t.Fatalf("ObserveMessage(bound) error = %v", err)
	}
	router.Bind(nil)
	if err := router.ObserveMessage(context.Background(), event); err != nil {
		t.Fatalf("ObserveMessage(unbound again) error = %v", err)
	}
	if len(sink.messages) != 1 || sink.messages[0].EventID != event.EventID {
		t.Fatalf("routed messages = %#v", sink.messages)
	}
}

type feedbackStoreFake struct {
	candidates []FeedbackCandidate
	feedback   []Feedback
}

type routerFeedbackSinkFake struct {
	messages []MessageFeedback
}

func (f *routerFeedbackSinkFake) ObserveMessage(
	_ context.Context,
	event MessageFeedback,
) error {
	f.messages = append(f.messages, event)
	return nil
}

func (*routerFeedbackSinkFake) ObserveReaction(context.Context, ReactionFeedback) error {
	return nil
}

func (*routerFeedbackSinkFake) ObserveCardAction(context.Context, CardFeedback) error {
	return nil
}

func (f *feedbackStoreFake) FeedbackCandidates(
	context.Context,
	string,
	time.Time,
) ([]FeedbackCandidate, error) {
	return cloneCaptureValue(f.candidates), nil
}

func (f *feedbackStoreFake) AppendFeedback(_ context.Context, feedback Feedback) error {
	f.feedback = append(f.feedback, cloneCaptureValue(feedback))
	return nil
}

func feedbackEpisode(id, cohortID string, anchor time.Time) Episode {
	postEnd := anchor.Add(10 * time.Minute)
	return Episode{
		ID: id, CohortID: cohortID, ChatID: "chat-1", TopicID: "topic-1",
		AnchorEventID: "anchor-" + id, AnchorMessageID: "message-" + id,
		ServingLane: LaneControl, Status: EpisodeStatusCollecting,
		PreWindowStart: anchor.Add(-time.Minute), AnchorAt: anchor,
		PostWindowEnd: &postEnd, LateFeedbackUntil: anchor.Add(LateFeedbackGracePeriod),
	}
}
