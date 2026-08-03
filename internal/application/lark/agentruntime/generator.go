package agentruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/llmusage"
	"github.com/bytedance/gg/gptr"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime/model/responses"
)

const continuationSystemPrompt = `你是群聊 Agent 的回调续接决策器。
用户批准或取消的 action 已经执行完毕，不得重复执行任何 action 或能力。
结构化 capability outcome 是既成事实，必须据此判断。
群聊回复应简洁、自然；如果已更新的卡片足以表达结果，选择 observe_only。
当前续接不得选择 wait；需要新交互时必须由 capability/Agent Card durable flow 创建。
只输出 JSON：{"decision":"reply|observe_only|wait|close","reply":"文本","reason":"一句话原因"}。`

type TurnDecisionType string

const (
	TurnDecisionReply       TurnDecisionType = "reply"
	TurnDecisionObserveOnly TurnDecisionType = "observe_only"
	TurnDecisionWait        TurnDecisionType = "wait"
	TurnDecisionClose       TurnDecisionType = "close"
)

type TurnDecision struct {
	Decision TurnDecisionType `json:"decision"`
	Reply    string           `json:"reply"`
	Reason   string           `json:"reason"`
}

type ContinuationContext struct {
	RunID            string
	Goal             string
	TriggerMessageID string
	ChatID           string
	ActorOpenID      string
	LatestOutcome    ConversationEvent
	RecentSteps      []*AgentStep
}

type ContinuationGenerator interface {
	Generate(context.Context, ContinuationContext) (TurnDecision, error)
}

type continuationGenerator struct {
	modelID      string
	responseText func(context.Context, ark_dal.CachedResponseRequest, llmusage.Scope) (string, error)
}

type continuationPrompt struct {
	RunID            string                   `json:"run_id"`
	Goal             string                   `json:"goal"`
	TriggerMessageID string                   `json:"trigger_message_id"`
	ChatID           string                   `json:"chat_id"`
	ActorOpenID      string                   `json:"actor_open_id"`
	LatestOutcome    ConversationEvent        `json:"latest_outcome"`
	RecentSteps      []continuationPromptStep `json:"recent_steps"`
}

type continuationPromptStep struct {
	Kind           StepKind   `json:"kind"`
	Status         StepStatus `json:"status"`
	CapabilityName string     `json:"capability_name,omitempty"`
	ExternalRef    string     `json:"external_ref,omitempty"`
}

func NewContinuationGenerator(modelID string) *continuationGenerator {
	return &continuationGenerator{modelID: strings.TrimSpace(modelID), responseText: ark_dal.ResponseTextWithCache}
}

func (g *continuationGenerator) Generate(
	ctx context.Context,
	input ContinuationContext,
) (TurnDecision, error) {
	if g == nil || g.modelID == "" || g.responseText == nil {
		return TurnDecision{}, errors.New("continuation generator is not configured")
	}
	if input.LatestOutcome.Type != EventTypeCapabilityResult {
		return TurnDecision{}, errors.New("latest outcome must be a capability result")
	}
	recent := make([]continuationPromptStep, 0, len(input.RecentSteps))
	for _, step := range input.RecentSteps {
		if step == nil {
			continue
		}
		recent = append(recent, continuationPromptStep{
			Kind: step.Kind, Status: step.Status,
			CapabilityName: step.CapabilityName, ExternalRef: step.ExternalRef,
		})
	}
	prompt, err := json.Marshal(continuationPrompt{
		RunID: input.RunID, Goal: input.Goal, TriggerMessageID: input.TriggerMessageID,
		ChatID: input.ChatID, ActorOpenID: input.ActorOpenID,
		LatestOutcome: input.LatestOutcome, RecentSteps: recent,
	})
	if err != nil {
		return TurnDecision{}, err
	}
	raw, err := g.responseText(ctx, ark_dal.CachedResponseRequest{
		CacheScene: "agent_callback_continuation", SystemPrompt: continuationSystemPrompt,
		UserPrompt: string(prompt), ModelID: g.modelID,
		Text: &responses.ResponsesText{Format: &responses.TextFormat{
			Type: responses.TextType_json_object,
		}},
		Reasoning: &responses.ResponsesReasoning{Effort: responses.ReasoningEffort_minimal},
		Thinking:  &responses.ResponsesThinking{Type: gptr.Of(responses.ThinkingType_disabled)},
	}, llmusage.Scope{
		ChatID: input.ChatID, OpenID: input.ActorOpenID,
		SourceType: llmusage.SourceTypeBackground, Source: "agent_callback_continuation",
		BusinessScene: llmusage.SceneAgentRuntime, BusinessOperation: llmusage.OperationCallbackContinuation,
	})
	if err != nil {
		return TurnDecision{}, err
	}
	return decodeTurnDecision(raw)
}

func decodeTurnDecision(raw string) (TurnDecision, error) {
	if len(raw) == 0 || len(raw) > 16*1024 {
		return TurnDecision{}, errors.New("continuation decision has invalid size")
	}
	decoder := json.NewDecoder(io.LimitReader(bytes.NewBufferString(raw), 16*1024+1))
	decoder.DisallowUnknownFields()
	var decision TurnDecision
	if err := decoder.Decode(&decision); err != nil {
		return TurnDecision{}, fmt.Errorf("decode continuation decision: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return TurnDecision{}, errors.New("continuation decision must be one JSON document")
	}
	decision.Reply = strings.TrimSpace(decision.Reply)
	decision.Reason = strings.TrimSpace(decision.Reason)
	if decision.Reason == "" {
		return TurnDecision{}, errors.New("continuation decision reason is required")
	}
	switch decision.Decision {
	case TurnDecisionReply:
		if decision.Reply == "" {
			return TurnDecision{}, errors.New("reply decision requires reply text")
		}
	case TurnDecisionObserveOnly, TurnDecisionClose:
		if decision.Reply != "" {
			return TurnDecision{}, errors.New("non-reply decision cannot include reply text")
		}
	case TurnDecisionWait:
		return TurnDecision{}, errors.New("wait decision requires a durable wait contract")
	default:
		return TurnDecision{}, errors.New("unknown continuation decision")
	}
	return decision, nil
}
