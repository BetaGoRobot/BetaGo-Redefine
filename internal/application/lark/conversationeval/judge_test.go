package conversationeval

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/llmusage"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime/model/responses"
)

func TestJudgePromptIsDeterministicallyBlind(t *testing.T) {
	input := judgeTestInput()
	first, firstOrder, err := BuildJudgePrompt(input)
	if err != nil {
		t.Fatalf("BuildJudgePrompt() error = %v", err)
	}
	second, secondOrder, err := BuildJudgePrompt(input)
	if err != nil {
		t.Fatalf("BuildJudgePrompt(second) error = %v", err)
	}
	if firstOrder != secondOrder || first.UserPrompt != second.UserPrompt {
		t.Fatalf("judge prompt/order is not deterministic: %#v / %#v", firstOrder, secondOrder)
	}
	if !firstOrder.Valid() || firstOrder.A == firstOrder.B {
		t.Fatalf("blind order = %#v", firstOrder)
	}
	prompt := strings.ToLower(first.SystemPrompt + "\n" + first.UserPrompt)
	if strings.Contains(prompt, "control") || strings.Contains(prompt, "candidate") {
		t.Fatalf("blind prompt leaks lane labels: %s", prompt)
	}
	if !strings.Contains(first.UserPrompt, `"action":"skip"`) ||
		!strings.Contains(first.UserPrompt, `"action":"reply"`) ||
		strings.Contains(first.UserPrompt, `"reply_text":""`) {
		t.Fatalf("skip/reply representation is ambiguous: %s", first.UserPrompt)
	}
	if !strings.Contains(first.UserPrompt, `"scope":"serving_outcome_only"`) {
		t.Fatalf("post-window scope is missing: %s", first.UserPrompt)
	}

	orders := map[BlindOrder]struct{}{firstOrder: {}}
	for version := int64(2); version <= 32 && len(orders) == 1; version++ {
		input.Version = version
		input.PreviousJudgmentID = "previous-judgment"
		_, order, buildErr := BuildJudgePrompt(input)
		if buildErr != nil {
			t.Fatalf("BuildJudgePrompt(version %d) error = %v", version, buildErr)
		}
		orders[order] = struct{}{}
	}
	if len(orders) != 2 {
		t.Fatal("episode/version randomization never produced both blind orientations")
	}
}

func TestJudgeEvaluatesStrictResultAndAppendsVersion(t *testing.T) {
	input := judgeTestInput()
	input.Version = 2
	input.PreviousJudgmentID = "judgment-prior"
	store := &judgeStoreFake{}
	var completionRequest JudgeCompletionRequest
	judge, err := NewJudgeWithCompletion(
		JudgeConfig{
			ModelID: "judge-model",
			Scope: llmusage.Scope{
				ChatID: "chat-1", OpenID: "reviewer",
				SourceType: llmusage.SourceTypeUser, Source: "wrong-source",
			},
			Now: func() time.Time {
				return input.Episode.AnchorAt.Add(time.Hour)
			},
		},
		store,
		func(_ context.Context, request JudgeCompletionRequest) (json.RawMessage, error) {
			completionRequest = request
			return validJudgeResultJSON("A"), nil
		},
	)
	if err != nil {
		t.Fatalf("NewJudgeWithCompletion() error = %v", err)
	}

	got, err := judge.Evaluate(context.Background(), input)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	_, order, err := BuildJudgePrompt(input)
	if err != nil {
		t.Fatalf("BuildJudgePrompt() error = %v", err)
	}
	wantWinner := JudgmentWinnerControl
	if order.A == LaneCandidate {
		wantWinner = JudgmentWinnerCandidate
	}
	if got.Winner != wantWinner || got.Version != 2 ||
		got.SupersedesID != input.PreviousJudgmentID ||
		got.Source != JudgmentSourceConversationJudge ||
		got.EvaluatorID != "judge-model" {
		t.Fatalf("judgment = %#v", got)
	}
	if len(store.judgments) != 1 || store.judgments[0].ID != got.ID {
		t.Fatalf("stored judgments = %#v", store.judgments)
	}
	if completionRequest.Scope.Source != "conversation_evaluation_judge" ||
		completionRequest.Scope.SourceType != llmusage.SourceTypeBackground {
		t.Fatalf("judge scope = %#v", completionRequest.Scope)
	}
	var scores map[string]DimensionScore
	if err := json.Unmarshal(got.ScoresJSON, &scores); err != nil {
		t.Fatalf("decode scores: %v", err)
	}
	if len(scores) != 2 {
		t.Fatalf("scores = %#v", scores)
	}
}

func TestJudgeRejectsMalformedOutputWithoutWriting(t *testing.T) {
	tests := []struct {
		name string
		raw  json.RawMessage
	}{
		{name: "not json", raw: json.RawMessage(`not-json`)},
		{name: "unknown field", raw: append(validJudgeResultJSON("tie"), []byte(` `)...)},
		{
			name: "score out of range",
			raw: json.RawMessage(`{
				"winner":"tie",
				"scores_a":{"participation_timing":11,"topic_relation":8,"context_correctness":8,"response_relevance":8,"task_progress":8,"factual_tool_consistency":8,"group_tone":8,"disturbance":8},
				"scores_b":{"participation_timing":8,"topic_relation":8,"context_correctness":8,"response_relevance":8,"task_progress":8,"factual_tool_consistency":8,"group_tone":8,"disturbance":8},
				"problem_tags":[],"rationale":"bad range","confidence":80,"needs_review":false
			}`),
		},
	}
	unknown := map[string]any{}
	if err := json.Unmarshal(validJudgeResultJSON("tie"), &unknown); err != nil {
		t.Fatalf("decode valid result fixture: %v", err)
	}
	unknown["unexpected"] = true
	tests[1].raw, _ = json.Marshal(unknown)

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &judgeStoreFake{}
			judge, err := NewJudgeWithCompletion(
				JudgeConfig{ModelID: "judge-model"},
				store,
				func(context.Context, JudgeCompletionRequest) (json.RawMessage, error) {
					return test.raw, nil
				},
			)
			if err != nil {
				t.Fatalf("NewJudgeWithCompletion() error = %v", err)
			}
			if _, err := judge.Evaluate(context.Background(), judgeTestInput()); err == nil {
				t.Fatal("Evaluate() error = nil")
			}
			if len(store.judgments) != 0 {
				t.Fatalf("stored malformed judgment = %#v", store.judgments)
			}
		})
	}
}

func TestJudgeProductionConstructorUsesStrictArkJSONSchema(t *testing.T) {
	if _, err := NewJudge(JudgeConfig{}, &judgeStoreFake{}); err == nil {
		t.Fatal("NewJudge() accepted empty model id")
	}
	built, err := NewJudge(JudgeConfig{ModelID: "judge-model"}, &judgeStoreFake{})
	if err != nil {
		t.Fatalf("NewJudge() error = %v", err)
	}
	if reflect.ValueOf(built.completion).Pointer() !=
		reflect.ValueOf(JudgeJSONCompletion(completeJudgeJSONWithArk)).Pointer() {
		t.Fatal("production constructor did not select the Ark JSON completion wrapper")
	}
	format := judgeResponseFormat()
	if format.Type != responses.TextType_json_schema ||
		format.Strict == nil || !*format.Strict ||
		format.Schema == nil || !json.Valid(format.Schema.Value) ||
		format.Name != "conversation_evaluation_judge" {
		t.Fatalf("judge response format = %#v", format)
	}
	if strings.Contains(string(format.Schema.Value), `"$ref"`) ||
		strings.Contains(string(format.Schema.Value), `"uniqueItems"`) {
		t.Fatalf("judge schema uses unsupported strict-mode features: %s", format.Schema.Value)
	}
}

type judgeStoreFake struct {
	judgments []Judgment
}

func (f *judgeStoreFake) AppendJudgment(_ context.Context, judgment Judgment) error {
	f.judgments = append(f.judgments, cloneCaptureValue(judgment))
	return nil
}

func judgeTestInput() JudgeInput {
	message := serviceMessageInput()
	episode := newEpisode(serviceCohort(message), message, nil, message.OccurredAt)
	postEnd := message.OccurredAt.Add(10 * time.Minute)
	episode.PostWindowEnd = &postEnd
	episode.Status = EpisodeStatusReadyForJudge
	control := serviceLaneOutput(episode, LaneControl)
	control.JoinDecision = JoinDecisionJoin
	control.TopicRelation = TopicRelationRelated
	control.ReplyText = "明天上午十点开会。"
	control.ToolPlanJSON = json.RawMessage(
		`{"source":"control","delivery_message_id":"actual-message","nested":{"lane":"candidate"}}`,
	)
	candidate := serviceLaneOutput(episode, LaneCandidate)
	candidate.JoinDecision = JoinDecisionSkip
	candidate.ReplyText = ""
	candidate.ToolPlanJSON = json.RawMessage(`{"plan":{"source":"candidate"}}`)
	pre := message.WindowMessage(WindowPositionAnchor)
	pre.Sequence = 0
	post := message.WindowMessage(WindowPositionPost)
	post.EventID = "event-post"
	post.MessageID = "message-post"
	post.Content = "这个时间不对"
	post.OccurredAt = message.OccurredAt.Add(time.Minute)
	post.Sequence = 0
	return JudgeInput{
		Episode: episode, Version: 1,
		Messages:      []WindowMessage{pre, post},
		ControlOutput: control, CandidateOutput: candidate,
		Feedback: []Feedback{{
			ID: "feedback-1", EpisodeID: episode.ID, FeedbackEventID: "feedback-event",
			FeedbackType: FeedbackTypeCorrection, Explicitness: FeedbackExplicit,
			ContentJSON:           json.RawMessage(`{"text":"这个时间不对"}`),
			AttributionConfidence: 90, OccurredAt: post.OccurredAt,
		}},
	}
}

func validJudgeResultJSON(winner string) json.RawMessage {
	return json.RawMessage(`{
		"winner":"` + winner + `",
		"scores_a":{"participation_timing":9,"topic_relation":8,"context_correctness":8,"response_relevance":9,"task_progress":8,"factual_tool_consistency":8,"group_tone":9,"disturbance":8},
		"scores_b":{"participation_timing":7,"topic_relation":7,"context_correctness":7,"response_relevance":7,"task_progress":6,"factual_tool_consistency":7,"group_tone":8,"disturbance":9},
		"problem_tags":["timing"],
		"rationale":"A better advances the conversation.",
		"confidence":88,
		"needs_review":false
	}`)
}
