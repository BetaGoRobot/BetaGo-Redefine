package evaluationstore

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/conversationeval"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/evaluationindex"
)

func TestConversationEvaluationEndToEnd(t *testing.T) {
	fixture := newRepositoryFixture(t)
	ctx := context.Background()
	anchorAt := fixture.now
	cohort := fixture.cohort(
		"e2e",
		anchorAt.Add(-time.Hour),
		anchorAt.Add(time.Hour),
	)
	if err := fixture.repo.CreateCohort(ctx, cohort); err != nil {
		t.Fatalf("CreateCohort() error = %v", err)
	}
	preMessages := make([]conversationeval.WindowMessage, 25)
	for index := range preMessages {
		preMessages[index] = conversationeval.WindowMessage{
			EventID:   fmt.Sprintf("pre-event-%02d", index),
			MessageID: fmt.Sprintf("pre-message-%02d", index),
			ChatID:    cohort.ChatIDs[0], TopicID: "topic-e2e",
			SenderOpenID: fmt.Sprintf("pre-sender-%02d", index%3),
			Content:      fmt.Sprintf("pre content %02d", index),
			OccurredAt:   anchorAt.Add(time.Duration(index-25) * time.Minute),
			Position:     conversationeval.WindowPositionPre,
		}
	}
	currentNow := anchorAt.Add(time.Second)
	service, err := conversationeval.NewService(conversationeval.ServiceOptions{
		Repository: fixture.repo,
		PreWindowSource: e2ePreWindowSource{
			messages: preMessages,
		},
		CandidateSubmitter: fixture.repo,
		Now: func() time.Time {
			return currentNow
		},
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	anchor := conversationeval.MessageInput{
		AppID: cohort.AppID, BotOpenID: cohort.BotOpenID,
		ChatID: cohort.ChatIDs[0], RunID: "run-e2e",
		EventID: "anchor-event-e2e", MessageID: "anchor-message-e2e",
		TopicID: "topic-e2e", SenderOpenID: "anchor-sender",
		Content: "明天几点开会？", OccurredAt: anchorAt,
	}
	session, err := service.BeginMessage(ctx, anchor)
	if err != nil {
		t.Fatalf("BeginMessage() error = %v", err)
	}
	if !session.Enabled() {
		t.Fatal("evaluation session is disabled for active cohort")
	}
	contextSnapshot := conversationeval.ContextSnapshot{
		SchemaVersion: conversationeval.SchemaVersion,
		AnchorEventID: anchor.EventID, AnchorAt: anchorAt,
		SystemPrompt: "system", UserPrompt: anchor.Content,
		TokenEstimate: 8, TokenBudget: 128,
		Messages: []conversationeval.ContextItem{{
			ID: "context-e2e", Source: "lark_message", SourceID: "pre-message-24",
			Kind: "message", Content: "明天上午十点", ContentHash: "sha256:e2e",
			Rank: 0, TokenCount: 8, Selected: true,
			OccurredAt: anchorAt.Add(-time.Minute),
		}},
	}
	session.Capture().RecordIntent(ctx, map[string]any{"active": true})
	session.Capture().RecordContext(ctx, contextSnapshot, nil)
	session.Capture().RecordToolPlan(ctx, map[string]any{"action": "reply"})
	session.Capture().RecordOutput(ctx, conversationeval.Output{
		Decision: conversationeval.OutputDecisionReply,
		Reply:    "明天上午十点开会。",
		Latency:  150 * time.Millisecond,
	})
	session.Capture().RecordDelivery(ctx, "delivered-control-message")
	if err := service.CompleteMessage(ctx, session); err != nil {
		t.Fatalf("CompleteMessage() error = %v", err)
	}

	currentNow = anchorAt.Add(2 * time.Second)
	candidateProcessor, err := conversationeval.NewCandidateProcessor(
		fixture.repo,
		service,
		func(context.Context, conversationeval.CandidateTask) (
			conversationeval.CandidateRunner,
			error,
		) {
			return e2eCandidateRunner{now: currentNow}, nil
		},
		conversationeval.CandidateProcessorConfig{
			WorkerID: "e2e-candidate", LeaseTTL: time.Minute,
			Now: func() time.Time {
				return currentNow
			},
		},
	)
	if err != nil {
		t.Fatalf("NewCandidateProcessor() error = %v", err)
	}
	if err := candidateProcessor.ProcessNext(ctx); err != nil {
		t.Fatalf("candidate ProcessNext() error = %v", err)
	}

	for index := 0; index < 10; index++ {
		message := conversationeval.MessageInput{
			AppID: cohort.AppID, BotOpenID: cohort.BotOpenID,
			ChatID:    cohort.ChatIDs[0],
			EventID:   fmt.Sprintf("post-event-%02d", index),
			MessageID: fmt.Sprintf("post-message-%02d", index),
			TopicID:   "topic-e2e", SenderOpenID: fmt.Sprintf("post-sender-%02d", index%2),
			Content:    fmt.Sprintf("post content %02d", index),
			OccurredAt: anchorAt.Add(time.Duration(index+1) * time.Minute),
		}
		if err := service.ObserveWindowMessage(ctx, message); err != nil {
			t.Fatalf("ObserveWindowMessage(%d) error = %v", index, err)
		}
	}
	currentNow = anchorAt.Add(16 * time.Minute)
	if closed, err := service.AdvanceOpenWindows(ctx, anchor.ChatID, currentNow); err != nil {
		t.Fatalf("AdvanceOpenWindows() error = %v", err)
	} else if closed != 1 {
		t.Fatalf("AdvanceOpenWindows() = %d, want 1", closed)
	}

	if err := service.ObserveMessage(ctx, conversationeval.MessageFeedback{
		EventID: "feedback-message-event", MessageID: "feedback-message",
		ChatID: anchor.ChatID, TopicID: anchor.TopicID, ActorOpenID: "reviewer",
		ReplyToMessageID: "delivered-control-message",
		Content:          "不对，应该是十一点", ExplicitCorrection: true,
		OccurredAt: anchorAt.Add(17 * time.Minute),
	}); err != nil {
		t.Fatalf("ObserveMessage(feedback) error = %v", err)
	}
	if err := service.ObserveReaction(ctx, conversationeval.ReactionFeedback{
		EventID: "feedback-reaction-event", ChatID: anchor.ChatID,
		ActorOpenID: "reviewer", TargetMessageID: "delivered-control-message",
		ReactionType: "THUMBSUP",
		OccurredAt:   anchorAt.Add(18 * time.Minute),
	}); err != nil {
		t.Fatalf("ObserveReaction() error = %v", err)
	}

	judge, err := conversationeval.NewJudgeWithCompletion(
		conversationeval.JudgeConfig{
			ModelID: "judge-e2e",
			Now: func() time.Time {
				return anchorAt.Add(19 * time.Minute)
			},
		},
		fixture.repo,
		func(
			context.Context,
			conversationeval.JudgeCompletionRequest,
		) (json.RawMessage, error) {
			return e2eJudgeResult(), nil
		},
	)
	if err != nil {
		t.Fatalf("NewJudgeWithCompletion() error = %v", err)
	}
	judgeProcessor, err := conversationeval.NewJudgeProcessor(
		fixture.repo,
		judge,
		func() time.Time {
			return anchorAt.Add(19 * time.Minute)
		},
	)
	if err != nil {
		t.Fatalf("NewJudgeProcessor() error = %v", err)
	}
	if err := judgeProcessor.ProcessNext(ctx); err != nil {
		t.Fatalf("judge ProcessNext() error = %v", err)
	}

	snapshots, err := fixture.repo.EvaluationSnapshotsAfter(
		ctx,
		evaluationindex.ProjectionCursor{},
		10,
	)
	if err != nil {
		t.Fatalf("EvaluationSnapshotsAfter() error = %v", err)
	}
	if len(snapshots) != 1 {
		t.Fatalf("snapshot count = %d, want 1", len(snapshots))
	}
	snapshot := snapshots[0]
	if len(snapshot.PreMessages) != conversationeval.PreWindowMessageLimit ||
		snapshot.PreMessages[0].MessageID != "pre-message-05" ||
		len(snapshot.PostMessages) != 10 ||
		snapshot.AnchorMessage.MessageID != anchor.MessageID {
		t.Fatalf(
			"window sizes/anchor = %d/%d/%q; first pre = %q",
			len(snapshot.PreMessages),
			len(snapshot.PostMessages),
			snapshot.AnchorMessage.MessageID,
			snapshot.PreMessages[0].MessageID,
		)
	}
	if snapshot.Control.ReplyText != "明天上午十点开会。" ||
		snapshot.Candidate.ReplyText != "我建议先确认参会人。" ||
		len(snapshot.LatestJudgments) != 1 ||
		len(snapshot.FeedbackTypes) != 2 ||
		snapshot.Status != string(conversationeval.EpisodeStatusJudged) {
		t.Fatalf("evaluation snapshot = %#v", snapshot)
	}
}

type e2ePreWindowSource struct {
	messages []conversationeval.WindowMessage
}

func (s e2ePreWindowSource) MessagesBefore(
	context.Context,
	string,
	time.Time,
	int,
) ([]conversationeval.WindowMessage, error) {
	return append([]conversationeval.WindowMessage(nil), s.messages...), nil
}

type e2eCandidateRunner struct {
	now time.Time
}

func (r e2eCandidateRunner) Run(
	_ context.Context,
	request conversationeval.CandidateRequest,
) (conversationeval.LaneOutput, error) {
	return conversationeval.LaneOutput{
		ID: request.OutputID, EpisodeID: request.EpisodeID,
		Lane:            conversationeval.LaneCandidate,
		OutputMode:      conversationeval.OutputModeShadow,
		ActivationJSON:  json.RawMessage(`{"active":true}`),
		RelevanceJSON:   json.RawMessage(`{"score":0.7}`),
		JoinDecision:    conversationeval.JoinDecisionJoin,
		TopicRelation:   conversationeval.TopicRelationRelated,
		ContextSnapshot: request.ContextSnapshot,
		ExcludedContext: request.ExcludedContext,
		ToolPlanJSON:    json.RawMessage(`{"action":"reply"}`),
		ReplyText:       "我建议先确认参会人。", Latency: 200 * time.Millisecond,
		TokenUsageJSON: json.RawMessage(`{"total_tokens":12}`),
		ErrorJSON:      json.RawMessage(`{}`),
		CreatedAt:      r.now, UpdatedAt: r.now,
	}, nil
}

func e2eJudgeResult() json.RawMessage {
	return json.RawMessage(`{
		"winner":"A",
		"scores_a":{
			"participation_timing":8,
			"topic_relation":8,
			"context_correctness":8,
			"response_relevance":8,
			"task_progress":8,
			"factual_tool_consistency":8,
			"group_tone":8,
			"disturbance":8
		},
		"scores_b":{
			"participation_timing":7,
			"topic_relation":7,
			"context_correctness":7,
			"response_relevance":7,
			"task_progress":7,
			"factual_tool_consistency":7,
			"group_tone":7,
			"disturbance":7
		},
		"problem_tags":["time_error"],
		"rationale":"A is more grounded in the captured context.",
		"confidence":82,
		"needs_review":false
	}`)
}
