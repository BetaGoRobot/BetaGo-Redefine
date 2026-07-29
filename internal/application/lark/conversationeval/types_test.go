package conversationeval

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestCohortTransitionIsStrictlyForwardOnly(t *testing.T) {
	now := time.Date(2026, 7, 29, 10, 0, 0, 0, time.UTC)
	cohort := Cohort{Status: CohortStatusCollecting}

	if err := cohort.TransitionTo(CohortStatusWaitingLateFeedback, now); err != nil {
		t.Fatalf("TransitionTo(waiting_late_feedback) error = %v", err)
	}
	if cohort.Status != CohortStatusWaitingLateFeedback || !cohort.UpdatedAt.Equal(now) {
		t.Fatalf("cohort after first transition = %#v", cohort)
	}
	if err := cohort.TransitionTo(CohortStatusFinalized, now.Add(time.Hour)); err != nil {
		t.Fatalf("TransitionTo(finalized) error = %v", err)
	}
	if err := cohort.TransitionTo(CohortStatusCollecting, now.Add(2*time.Hour)); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("reverse TransitionTo() error = %v, want ErrInvalidTransition", err)
	}

	direct := Cohort{Status: CohortStatusCollecting}
	if err := direct.TransitionTo(CohortStatusFinalized, now); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("skipped TransitionTo() error = %v, want ErrInvalidTransition", err)
	}
}

func TestContextSnapshotValidate(t *testing.T) {
	anchor := time.Date(2026, 7, 29, 10, 0, 0, 0, time.UTC)
	valid := ContextSnapshot{
		SchemaVersion: SchemaVersion,
		AnchorEventID: "event_anchor",
		AnchorAt:      anchor,
		Messages: []ContextItem{{
			ID: "message_1", Source: "lark_message", SourceID: "om_1",
			Kind: "message", Content: "前向消息", ContentHash: "sha256:1",
			Rank: 0, TokenCount: 30, Selected: true, OccurredAt: anchor.Add(-time.Minute),
			Metadata: json.RawMessage(`{"sender":"ou_1"}`),
		}},
		Retrieved: []ContextItem{{
			ID: "chunk_1", Source: "opensearch", SourceID: "chunk_1",
			Kind: "history_chunk", Content: "召回内容", ContentHash: "sha256:2",
			Rank: 1, TokenCount: 10, Selected: false, ExcludeReason: "token_budget",
			OccurredAt: anchor.Add(-time.Hour),
		}},
		SystemPrompt:  "system",
		UserPrompt:    "user",
		TokenEstimate: 30,
		TokenBudget:   40,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid ContextSnapshot.Validate() error = %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*ContextSnapshot)
	}{
		{
			name: "selected tokens exceed budget",
			mutate: func(snapshot *ContextSnapshot) {
				snapshot.Messages[0].TokenCount = 41
			},
		},
		{
			name: "excluded item has no reason",
			mutate: func(snapshot *ContextSnapshot) {
				snapshot.Retrieved[0].ExcludeReason = ""
			},
		},
		{
			name: "source identity is duplicated across collections",
			mutate: func(snapshot *ContextSnapshot) {
				duplicate := snapshot.Messages[0]
				duplicate.ID = "different_display_id"
				snapshot.Events = append(snapshot.Events, duplicate)
			},
		},
		{
			name: "item occurs after anchor",
			mutate: func(snapshot *ContextSnapshot) {
				snapshot.Messages[0].OccurredAt = anchor.Add(time.Nanosecond)
			},
		},
		{
			name: "invalid metadata json",
			mutate: func(snapshot *ContextSnapshot) {
				snapshot.Messages[0].Metadata = json.RawMessage(`{"broken"`)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidate := cloneSnapshot(t, valid)
			tt.mutate(&candidate)
			if err := candidate.Validate(); err == nil {
				t.Fatal("ContextSnapshot.Validate() error = nil, want validation error")
			}
		})
	}

	output := LaneOutput{
		ID: "output_1", EpisodeID: "episode_1", Lane: LaneControl, OutputMode: OutputModeActual,
		ActivationJSON: json.RawMessage(`{}`), RelevanceJSON: json.RawMessage(`{}`),
		JoinDecision: JoinDecisionJoin, TopicRelation: TopicRelationRelated,
		ContextSnapshot: valid, ToolPlanJSON: json.RawMessage(`{}`),
		TokenUsageJSON: json.RawMessage(`{}`), ErrorJSON: json.RawMessage(`{}`),
		ExcludedContext: []ExcludedContextItem{{ContextItem: ContextItem{
			ID: "excluded_duplicate", Source: "lark_message", SourceID: "om_1",
			Kind: "message", Content: "duplicate", ContentHash: "sha256:duplicate",
			Rank: 2, TokenCount: 1, Selected: false, ExcludeReason: "deduplicated",
			OccurredAt: anchor.Add(-time.Minute),
		}}},
	}
	if err := output.Validate(); err == nil {
		t.Fatal("LaneOutput.Validate() error = nil for duplicate selected/excluded identity")
	}
}

func TestDomainValidationRejectsInvalidEnumsJSONIDsTimesAndRanges(t *testing.T) {
	now := time.Date(2026, 7, 29, 10, 0, 0, 0, time.UTC)
	validCohort := Cohort{
		ID: "cohort_1", AppID: "app_1", BotOpenID: "bot_1", ChatIDs: []string{"chat_1"},
		StartAt: now, EndAt: now.Add(time.Hour), Status: CohortStatusCollecting,
		ServingLane: LaneControl, ControlVersion: "control-v1", CandidateVersion: "candidate-v1",
		JudgeConfigJSON: json.RawMessage(`{}`), SamplingPolicyJSON: json.RawMessage(`{}`),
	}
	if err := validCohort.Validate(); err != nil {
		t.Fatalf("valid Cohort.Validate() error = %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*Cohort)
	}{
		{"blank id", func(value *Cohort) { value.ID = " " }},
		{"invalid lane", func(value *Cohort) { value.ServingLane = Lane("other") }},
		{"invalid status", func(value *Cohort) { value.Status = CohortStatus("paused") }},
		{"reversed time range", func(value *Cohort) { value.EndAt = value.StartAt }},
		{"invalid json", func(value *Cohort) { value.JudgeConfigJSON = json.RawMessage(`[]`) }},
		{"negative result version", func(value *Cohort) { value.ResultVersion = -1 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			value := validCohort
			tt.mutate(&value)
			if err := value.Validate(); err == nil {
				t.Fatal("Cohort.Validate() error = nil, want validation error")
			}
		})
	}
}

func TestFeedbackTargetLaneAndMessageAreBothPresentOrBothEmpty(t *testing.T) {
	base := Feedback{
		ID: "feedback_1", EpisodeID: "episode_1", FeedbackEventID: "event_1",
		FeedbackType: FeedbackTypeCorrection, Explicitness: FeedbackExplicit,
		ContentJSON: json.RawMessage(`{}`), AttributionConfidence: 100,
		OccurredAt: time.Date(2026, 7, 29, 10, 0, 0, 0, time.UTC),
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("episode-only Feedback.Validate() error = %v", err)
	}
	for _, feedback := range []Feedback{
		func() Feedback {
			value := base
			value.TargetLane = LaneControl
			return value
		}(),
		func() Feedback {
			value := base
			value.TargetMessageID = "message_1"
			return value
		}(),
	} {
		if err := feedback.Validate(); !errors.Is(err, ErrInvalidContract) {
			t.Fatalf("partially targeted Feedback.Validate() error = %v, want ErrInvalidContract", err)
		}
	}
	targeted := base
	targeted.TargetLane = LaneCandidate
	targeted.TargetMessageID = "message_1"
	if err := targeted.Validate(); err != nil {
		t.Fatalf("fully targeted Feedback.Validate() error = %v", err)
	}
}

func cloneSnapshot(t *testing.T, value ContextSnapshot) ContextSnapshot {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	var cloned ContextSnapshot
	if err := json.Unmarshal(encoded, &cloned); err != nil {
		t.Fatalf("unmarshal snapshot: %v", err)
	}
	return cloned
}
