package conversationeval

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type BlindOrder struct {
	A Lane
	B Lane
}

func (o BlindOrder) Valid() bool {
	return o.A.Valid() && o.B.Valid() && o.A != o.B
}

type JudgePrompt struct {
	SystemPrompt string
	UserPrompt   string
}

type judgePromptPayload struct {
	EpisodeID            string                  `json:"episode_id"`
	Version              int64                   `json:"version"`
	ConversationBefore   []judgeMessage          `json:"conversation_before"`
	AlternativeA         judgeAlternative        `json:"alternative_a"`
	AlternativeB         judgeAlternative        `json:"alternative_b"`
	ObservedAfterServing judgeServingOutcomeOnly `json:"observed_after_serving"`
}

type judgeMessage struct {
	MessageID        string    `json:"message_id"`
	SenderOpenID     string    `json:"sender_open_id,omitempty"`
	ReplyToMessageID string    `json:"reply_to_message_id,omitempty"`
	Content          string    `json:"content"`
	OccurredAt       time.Time `json:"occurred_at"`
}

type judgeAlternative struct {
	Action          string             `json:"action"`
	ReplyText       string             `json:"reply_text,omitempty"`
	TopicRelation   TopicRelation      `json:"topic_relation"`
	SelectedContext []judgeContextItem `json:"selected_context"`
	ExcludedContext []judgeContextItem `json:"excluded_context"`
	ToolEvidence    json.RawMessage    `json:"tool_evidence"`
	HasError        bool               `json:"has_error"`
}

type judgeContextItem struct {
	Kind       string    `json:"kind"`
	Content    string    `json:"content"`
	OccurredAt time.Time `json:"occurred_at"`
	Reason     string    `json:"reason,omitempty"`
}

type judgeServingOutcomeOnly struct {
	Scope    string          `json:"scope"`
	Notice   string          `json:"notice"`
	Messages []judgeMessage  `json:"messages"`
	Feedback []judgeFeedback `json:"feedback"`
}

type judgeFeedback struct {
	Type                  FeedbackType         `json:"type"`
	Explicitness          FeedbackExplicitness `json:"explicitness"`
	Content               json.RawMessage      `json:"content"`
	AttributionConfidence int                  `json:"attribution_confidence"`
	OccurredAt            time.Time            `json:"occurred_at"`
}

func BuildJudgePrompt(input JudgeInput) (JudgePrompt, BlindOrder, error) {
	if err := input.Validate(); err != nil {
		return JudgePrompt{}, BlindOrder{}, err
	}
	order := blindOrder(input.Episode.ID, input.Version)
	outputs := map[Lane]LaneOutput{
		LaneControl: input.ControlOutput, LaneCandidate: input.CandidateOutput,
	}
	before := make([]judgeMessage, 0, len(input.Messages))
	after := make([]judgeMessage, 0, len(input.Messages))
	for _, message := range input.Messages {
		item := judgeMessage{
			MessageID: message.MessageID, SenderOpenID: message.SenderOpenID,
			ReplyToMessageID: message.ReplyToMessageID,
			Content:          message.Content, OccurredAt: message.OccurredAt,
		}
		if message.Position == WindowPositionPost {
			after = append(after, item)
		} else {
			before = append(before, item)
		}
	}
	feedback := make([]judgeFeedback, 0, len(input.Feedback))
	for _, item := range input.Feedback {
		feedback = append(feedback, judgeFeedback{
			Type: item.FeedbackType, Explicitness: item.Explicitness,
			Content:               cloneCaptureValue(item.ContentJSON),
			AttributionConfidence: item.AttributionConfidence,
			OccurredAt:            item.OccurredAt,
		})
	}
	payload := judgePromptPayload{
		EpisodeID: input.Episode.ID, Version: input.Version,
		ConversationBefore: before,
		AlternativeA:       buildJudgeAlternative(outputs[order.A]),
		AlternativeB:       buildJudgeAlternative(outputs[order.B]),
		ObservedAfterServing: judgeServingOutcomeOnly{
			Scope: "serving_outcome_only",
			Notice: "These events followed the behavior actually delivered to the chat. " +
				"They are observational outcome evidence only; do not infer which alternative was delivered " +
				"and do not treat them as counterfactual outcomes for both alternatives.",
			Messages: after,
			Feedback: feedback,
		},
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return JudgePrompt{}, BlindOrder{}, fmt.Errorf("marshal judge prompt: %w", err)
	}
	return JudgePrompt{
		SystemPrompt: judgeSystemPrompt,
		UserPrompt:   string(encoded),
	}, order, nil
}

func blindOrder(episodeID string, version int64) BlindOrder {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d", episodeID, version)))
	if sum[0]&1 == 0 {
		return BlindOrder{A: LaneControl, B: LaneCandidate}
	}
	return BlindOrder{A: LaneCandidate, B: LaneControl}
}

func buildJudgeAlternative(output LaneOutput) judgeAlternative {
	action := "skip"
	reply := ""
	if output.JoinDecision == JoinDecisionJoin {
		action = "reply"
		reply = output.ReplyText
	}
	selected := make([]judgeContextItem, 0,
		len(output.ContextSnapshot.Messages)+
			len(output.ContextSnapshot.Retrieved)+
			len(output.ContextSnapshot.Events),
	)
	for _, bucket := range [][]ContextItem{
		output.ContextSnapshot.Messages,
		output.ContextSnapshot.Retrieved,
		output.ContextSnapshot.Events,
	} {
		for _, item := range bucket {
			selected = append(selected, judgeContextItem{
				Kind: item.Kind, Content: item.Content, OccurredAt: item.OccurredAt,
			})
		}
	}
	excluded := make([]judgeContextItem, 0, len(output.ExcludedContext))
	for _, item := range output.ExcludedContext {
		excluded = append(excluded, judgeContextItem{
			Kind: item.Kind, Content: item.Content, OccurredAt: item.OccurredAt,
			Reason: item.ExcludeReason,
		})
	}
	toolEvidence := redactJudgeToolEvidence(output.ToolPlanJSON)
	return judgeAlternative{
		Action: action, ReplyText: reply, TopicRelation: output.TopicRelation,
		SelectedContext: selected, ExcludedContext: excluded,
		ToolEvidence: toolEvidence, HasError: hasJudgeLaneError(output.ErrorJSON),
	}
}

func redactJudgeToolEvidence(raw json.RawMessage) json.RawMessage {
	var value any
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil {
		return json.RawMessage(`{}`)
	}
	value = redactJudgeValue(value)
	encoded, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return json.RawMessage(encoded)
}

func redactJudgeValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		clean := make(map[string]any, len(typed))
		for key, item := range typed {
			switch strings.ToLower(strings.TrimSpace(key)) {
			case "delivery_message_id", "lane", "output_mode", "source":
				continue
			default:
				clean[key] = redactJudgeValue(item)
			}
		}
		return clean
	case []any:
		clean := make([]any, len(typed))
		for index, item := range typed {
			clean[index] = redactJudgeValue(item)
		}
		return clean
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case string(LaneControl), string(LaneCandidate):
			return "redacted_alternative"
		default:
			return typed
		}
	default:
		return value
	}
}

func hasJudgeLaneError(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var object map[string]json.RawMessage
	return json.Unmarshal(raw, &object) != nil || len(object) != 0
}

const judgeSystemPrompt = `You are a blind pairwise evaluator of two group-chat agent alternatives labeled A and B.
Evaluate each alternative independently on eight integer dimensions from 0 to 10:
participation_timing, topic_relation, context_correctness, response_relevance,
task_progress, factual_tool_consistency, group_tone, and disturbance.
For disturbance, 10 means appropriately non-disruptive and 0 means severely disruptive.
A skip is a real behavior and has no reply text. Never invent a reply for a skip.
The observed_after_serving section is explicitly serving-outcome-only evidence.
Do not infer which alternative was delivered and do not assign that outcome to both alternatives.
Return exactly one JSON object matching the required schema, with winner A, B, or tie.`
