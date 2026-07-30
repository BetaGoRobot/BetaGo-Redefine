package conversationeval

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/llmusage"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime/model/responses"
)

const judgeSource = "conversation_evaluation_judge"

type DimensionScore struct {
	ParticipationTiming    int `json:"participation_timing"`
	TopicRelation          int `json:"topic_relation"`
	ContextCorrectness     int `json:"context_correctness"`
	ResponseRelevance      int `json:"response_relevance"`
	TaskProgress           int `json:"task_progress"`
	FactualToolConsistency int `json:"factual_tool_consistency"`
	GroupTone              int `json:"group_tone"`
	Disturbance            int `json:"disturbance"`
}

func (s DimensionScore) Validate() error {
	for name, score := range map[string]int{
		"participation_timing":     s.ParticipationTiming,
		"topic_relation":           s.TopicRelation,
		"context_correctness":      s.ContextCorrectness,
		"response_relevance":       s.ResponseRelevance,
		"task_progress":            s.TaskProgress,
		"factual_tool_consistency": s.FactualToolConsistency,
		"group_tone":               s.GroupTone,
		"disturbance":              s.Disturbance,
	} {
		if score < 0 || score > 10 {
			return contractError("judge score %s must be between 0 and 10", name)
		}
	}
	return nil
}

type JudgeResult struct {
	Winner      string         `json:"winner"`
	ScoresA     DimensionScore `json:"scores_a"`
	ScoresB     DimensionScore `json:"scores_b"`
	ProblemTags []string       `json:"problem_tags"`
	Rationale   string         `json:"rationale"`
	Confidence  int            `json:"confidence"`
	NeedsReview bool           `json:"needs_review"`
}

func (r JudgeResult) Validate() error {
	switch r.Winner {
	case "A", "B", "tie":
	default:
		return contractError("judge winner %q is invalid", r.Winner)
	}
	if err := r.ScoresA.Validate(); err != nil {
		return err
	}
	if err := r.ScoresB.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(r.Rationale) == "" {
		return contractError("judge rationale must not be empty")
	}
	if r.Confidence < 0 || r.Confidence > 100 {
		return contractError("judge confidence must be between 0 and 100")
	}
	seen := make(map[string]struct{}, len(r.ProblemTags))
	for _, tag := range r.ProblemTags {
		if err := validateID("judge problem_tag", tag); err != nil {
			return err
		}
		if _, exists := seen[tag]; exists {
			return contractError("judge problem tag %q is duplicated", tag)
		}
		seen[tag] = struct{}{}
	}
	return nil
}

type JudgeInput struct {
	Episode            Episode
	Version            int64
	PreviousJudgmentID string
	Messages           []WindowMessage
	ControlOutput      LaneOutput
	CandidateOutput    LaneOutput
	Feedback           []Feedback
}

func (i JudgeInput) Validate() error {
	if err := i.Episode.Validate(); err != nil {
		return err
	}
	if i.Episode.Status != EpisodeStatusReadyForJudge &&
		i.Episode.Status != EpisodeStatusJudged {
		return contractError("episode %q is not judgeable", i.Episode.ID)
	}
	if i.Version <= 0 {
		return contractError("judge version must be positive")
	}
	if i.Version == 1 && i.PreviousJudgmentID != "" {
		return contractError("first judge input must not supersede a judgment")
	}
	if i.Version > 1 {
		if err := validateID("previous_judgment_id", i.PreviousJudgmentID); err != nil {
			return err
		}
	}
	for lane, output := range map[Lane]LaneOutput{
		LaneControl: i.ControlOutput, LaneCandidate: i.CandidateOutput,
	} {
		if err := output.Validate(); err != nil {
			return fmt.Errorf("judge %s output: %w", lane, err)
		}
		if output.EpisodeID != i.Episode.ID || output.Lane != lane {
			return contractError("judge %s output does not match episode", lane)
		}
	}
	for _, message := range i.Messages {
		if err := message.Validate(); err != nil {
			return err
		}
		if message.ChatID != i.Episode.ChatID {
			return contractError("judge message chat does not match episode")
		}
	}
	for _, feedback := range i.Feedback {
		if err := feedback.Validate(); err != nil {
			return err
		}
		if feedback.EpisodeID != i.Episode.ID {
			return contractError("judge feedback does not match episode")
		}
	}
	return nil
}

type JudgeStore interface {
	AppendJudgment(context.Context, Judgment) error
}

type JudgeConfig struct {
	ModelID string
	Scope   llmusage.Scope
	Now     func() time.Time
}

type JudgeCompletionRequest struct {
	ModelID      string
	SystemPrompt string
	UserPrompt   string
	Scope        llmusage.Scope
}

type JudgeJSONCompletion func(
	context.Context,
	JudgeCompletionRequest,
) (json.RawMessage, error)

type Judge struct {
	config     JudgeConfig
	store      JudgeStore
	completion JudgeJSONCompletion
}

func NewJudge(config JudgeConfig, store JudgeStore) (*Judge, error) {
	return NewJudgeWithCompletion(config, store, completeJudgeJSONWithArk)
}

func NewJudgeWithCompletion(
	config JudgeConfig,
	store JudgeStore,
	completion JudgeJSONCompletion,
) (*Judge, error) {
	config.ModelID = strings.TrimSpace(config.ModelID)
	if config.ModelID == "" {
		return nil, fmt.Errorf("judge Ark model id is required")
	}
	if store == nil {
		return nil, fmt.Errorf("judge store is required")
	}
	if completion == nil {
		return nil, fmt.Errorf("judge JSON completion is required")
	}
	if config.Now == nil {
		config.Now = func() time.Time { return time.Now().UTC() }
	}
	config.Scope = llmusage.NormalizeScope(config.Scope)
	config.Scope.SourceType = llmusage.SourceTypeBackground
	config.Scope.Source = judgeSource
	return &Judge{config: config, store: store, completion: completion}, nil
}

func (j *Judge) Evaluate(ctx context.Context, input JudgeInput) (Judgment, error) {
	if j == nil || j.completion == nil || j.store == nil {
		return Judgment{}, ErrEvaluationUnavailable
	}
	prompt, order, err := BuildJudgePrompt(input)
	if err != nil {
		return Judgment{}, err
	}
	raw, err := j.completion(ctx, JudgeCompletionRequest{
		ModelID: j.config.ModelID, SystemPrompt: prompt.SystemPrompt,
		UserPrompt: prompt.UserPrompt, Scope: j.config.Scope,
	})
	if err != nil {
		return Judgment{}, fmt.Errorf("judge completion: %w", err)
	}
	result, err := decodeJudgeResult(raw)
	if err != nil {
		return Judgment{}, fmt.Errorf("decode judge result: %w", err)
	}
	winner := JudgmentWinnerTie
	switch result.Winner {
	case "A":
		winner = judgmentWinnerForLane(order.A)
	case "B":
		winner = judgmentWinnerForLane(order.B)
	}
	scoresByLane := map[string]DimensionScore{
		string(order.A): result.ScoresA,
		string(order.B): result.ScoresB,
	}
	scoresJSON, err := json.Marshal(scoresByLane)
	if err != nil {
		return Judgment{}, fmt.Errorf("marshal judge scores: %w", err)
	}
	createdAt := j.config.Now()
	judgment := Judgment{
		ID: evaluationID(
			"judgment",
			input.Episode.ID,
			string(JudgmentSourceConversationJudge),
			fmt.Sprintf("%d", input.Version),
		),
		EpisodeID: input.Episode.ID, Version: input.Version,
		Source: JudgmentSourceConversationJudge, EvaluatorID: j.config.ModelID,
		Winner: winner, ScoresJSON: scoresJSON,
		ProblemTags: cloneCaptureValue(result.ProblemTags),
		Rationale:   strings.TrimSpace(result.Rationale), Confidence: result.Confidence,
		NeedsReview: result.NeedsReview, SupersedesID: input.PreviousJudgmentID,
		CreatedAt: createdAt,
	}
	if err := judgment.Validate(); err != nil {
		return Judgment{}, err
	}
	if err := j.store.AppendJudgment(ctx, judgment); err != nil {
		return Judgment{}, fmt.Errorf("append judge result: %w", err)
	}
	return judgment, nil
}

func judgmentWinnerForLane(lane Lane) JudgmentWinner {
	if lane == LaneCandidate {
		return JudgmentWinnerCandidate
	}
	return JudgmentWinnerControl
}

func decodeJudgeResult(raw json.RawMessage) (JudgeResult, error) {
	var result JudgeResult
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return JudgeResult{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return JudgeResult{}, fmt.Errorf("multiple JSON values")
		}
		return JudgeResult{}, err
	}
	if err := result.Validate(); err != nil {
		return JudgeResult{}, err
	}
	return result, nil
}

func completeJudgeJSONWithArk(
	ctx context.Context,
	request JudgeCompletionRequest,
) (json.RawMessage, error) {
	text, err := ark_dal.ResponseTextWithCache(ctx, ark_dal.CachedResponseRequest{
		CacheScene: judgeSource, SystemPrompt: request.SystemPrompt,
		UserPrompt: request.UserPrompt, ModelID: request.ModelID,
		Text: &responses.ResponsesText{Format: judgeResponseFormat()},
		Reasoning: &responses.ResponsesReasoning{
			Effort: responses.ReasoningEffort_medium,
		},
		Thinking: &responses.ResponsesThinking{
			Type: responses.ThinkingType_disabled.Enum(),
		},
	}, request.Scope)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(text), nil
}

func judgeResponseFormat() *responses.TextFormat {
	strict := true
	return &responses.TextFormat{
		Type:   responses.TextType_json_schema,
		Schema: &responses.Bytes{Value: judgeResultSchema()},
		Name:   judgeSource, Description: new(
			"Blind pairwise conversation quality judgment",
		),
		Strict: &strict,
	}
}

func judgeResultSchema() []byte {
	scoreFields := []string{
		"participation_timing", "topic_relation", "context_correctness",
		"response_relevance", "task_progress", "factual_tool_consistency",
		"group_tone", "disturbance",
	}
	scoreProperties := make(map[string]any, len(scoreFields))
	for _, field := range scoreFields {
		scoreProperties[field] = map[string]any{
			"type": "integer", "minimum": 0, "maximum": 10,
		}
	}
	scoreSchema := func() map[string]any {
		return map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             append([]string(nil), scoreFields...),
			"properties":           scoreProperties,
		}
	}
	schema := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required": []string{
			"winner", "scores_a", "scores_b", "problem_tags",
			"rationale", "confidence", "needs_review",
		},
		"properties": map[string]any{
			"winner": map[string]any{
				"type": "string", "enum": []string{"A", "B", "tie"},
			},
			"scores_a": scoreSchema(),
			"scores_b": scoreSchema(),
			"problem_tags": map[string]any{
				"type": "array", "items": map[string]any{"type": "string"},
			},
			"rationale": map[string]any{
				"type": "string", "minLength": 1,
			},
			"confidence": map[string]any{
				"type": "integer", "minimum": 0, "maximum": 100,
			},
			"needs_review": map[string]any{"type": "boolean"},
		},
	}
	encoded, err := json.Marshal(schema)
	if err != nil {
		panic("marshal static judge JSON schema: " + err.Error())
	}
	return encoded
}
