package intentmeta

import (
	"encoding/json"
	"strings"

	"github.com/volcengine/volcengine-go-sdk/service/arkruntime/model/responses"
)

// IntentType 定义意图类型
type IntentType string

const (
	IntentTypeQuestion IntentType = "question"
	IntentTypeChat     IntentType = "chat"
	IntentTypeShare    IntentType = "share"
	IntentTypeCommand  IntentType = "command"
	IntentTypeIgnore   IntentType = "ignore"
)

// SuggestAction 建议动作
type SuggestAction string

const (
	SuggestActionChat   SuggestAction = "chat"
	SuggestActionReact  SuggestAction = "react"
	SuggestActionRepeat SuggestAction = "repeat"
	SuggestActionIgnore SuggestAction = "ignore"
)

type InteractionMode string

const (
	InteractionModeStandard InteractionMode = "standard"
	InteractionModeAgentic  InteractionMode = "agentic"
)

func (m InteractionMode) Normalize() InteractionMode {
	switch m {
	case InteractionModeAgentic:
		return InteractionModeAgentic
	default:
		return InteractionModeStandard
	}
}

// IntentAnalysis 意图分析结果
type IntentAnalysis struct {
	IntentType      IntentType                     `json:"intent_type"`
	NeedReply       bool                           `json:"need_reply"`
	ReplyConfidence int                            `json:"reply_confidence"`
	Reason          string                         `json:"reason"`
	SuggestAction   SuggestAction                  `json:"suggest_action"`
	InteractionMode InteractionMode                `json:"interaction_mode"`
	ReasoningEffort responses.ReasoningEffort_Enum `json:"reasoning_effort"`
}

// UnmarshalJSON accepts both enum strings and enum numbers for reasoning effort.
func (a *IntentAnalysis) UnmarshalJSON(data []byte) error {
	var raw struct {
		IntentType      IntentType      `json:"intent_type"`
		NeedReply       bool            `json:"need_reply"`
		ReplyConfidence int             `json:"reply_confidence"`
		Reason          string          `json:"reason"`
		SuggestAction   SuggestAction   `json:"suggest_action"`
		InteractionMode InteractionMode `json:"interaction_mode"`
		ReasoningEffort json.RawMessage `json:"reasoning_effort"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	*a = IntentAnalysis{
		IntentType:      raw.IntentType,
		NeedReply:       raw.NeedReply,
		ReplyConfidence: raw.ReplyConfidence,
		Reason:          raw.Reason,
		SuggestAction:   raw.SuggestAction,
		InteractionMode: raw.InteractionMode,
		ReasoningEffort: parseReasoningEffort(raw.ReasoningEffort),
	}
	return nil
}

// DefaultReasoningEffort returns the mode-based fallback effort when the model does not provide one.
func DefaultReasoningEffort(mode InteractionMode) responses.ReasoningEffort_Enum {
	if mode.Normalize() == InteractionModeAgentic {
		return responses.ReasoningEffort_medium
	}
	return responses.ReasoningEffort_minimal
}

// NormalizeReasoningEffort keeps only supported reasoning effort values.
func NormalizeReasoningEffort(
	effort responses.ReasoningEffort_Enum,
	mode InteractionMode,
) responses.ReasoningEffort_Enum {
	switch effort {
	case responses.ReasoningEffort_minimal,
		responses.ReasoningEffort_low,
		responses.ReasoningEffort_medium,
		responses.ReasoningEffort_high:
		return effort
	default:
		return DefaultReasoningEffort(mode)
	}
}

// Sanitize validates the intent payload and fills conservative defaults.
func (a *IntentAnalysis) Sanitize() {
	switch a.IntentType {
	case IntentTypeQuestion, IntentTypeChat, IntentTypeShare, IntentTypeCommand, IntentTypeIgnore:
	default:
		a.IntentType = IntentTypeChat
	}

	switch a.SuggestAction {
	case SuggestActionChat, SuggestActionReact, SuggestActionRepeat, SuggestActionIgnore:
	default:
		a.SuggestAction = SuggestActionIgnore
	}

	a.InteractionMode = a.InteractionMode.Normalize()
	a.ReasoningEffort = NormalizeReasoningEffort(a.ReasoningEffort, a.InteractionMode)

	if a.ReplyConfidence < 0 {
		a.ReplyConfidence = 0
	}
	if a.ReplyConfidence > 100 {
		a.ReplyConfidence = 100
	}

	if a.IntentType == IntentTypeQuestion {
		a.NeedReply = true
	} else if a.IntentType == IntentTypeIgnore {
		a.NeedReply = false
	}
}

func parseReasoningEffort(raw json.RawMessage) responses.ReasoningEffort_Enum {
	if len(raw) == 0 || string(raw) == "null" {
		return responses.ReasoningEffort_unspecified
	}

	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return parseReasoningEffortText(text)
	}

	var effort responses.ReasoningEffort_Enum
	if err := json.Unmarshal(raw, &effort); err == nil {
		return effort
	}

	var numeric int32
	if err := json.Unmarshal(raw, &numeric); err == nil {
		return responses.ReasoningEffort_Enum(numeric)
	}
	return responses.ReasoningEffort_unspecified
}

func parseReasoningEffortText(text string) responses.ReasoningEffort_Enum {
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "minimal":
		return responses.ReasoningEffort_minimal
	case "low":
		return responses.ReasoningEffort_low
	case "medium":
		return responses.ReasoningEffort_medium
	case "high":
		return responses.ReasoningEffort_high
	default:
		return responses.ReasoningEffort_unspecified
	}
}
