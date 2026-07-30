package conversationeval

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/llmusage"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime/model/responses"
)

const (
	candidateStageActivation = "activation"
	candidateStageRelevance  = "relevance"
	candidateStageContext    = "context"
	candidateStageDraft      = "draft"

	defaultCandidateToolRounds = 3
	maxCandidateToolRounds     = 8
	maxCandidateToolsPerRound  = 4

	defaultCandidateContextTokenBudget = 8192
	maxCandidateContextTokenBudget     = 131072
)

type ArkCandidateEngineConfig struct {
	ModelID            string
	PolicyPrompt       string
	ContextTokenBudget int
	Scope              llmusage.Scope
	MaxToolRounds      int
}

type CandidateCompletionRequest struct {
	Stage        string
	CacheScene   string
	ModelID      string
	SystemPrompt string
	UserPrompt   string
	Scope        llmusage.Scope
}

type CandidateJSONCompletion func(
	context.Context,
	CandidateCompletionRequest,
) (json.RawMessage, error)

type arkCandidateStageEngine struct {
	config     ArkCandidateEngineConfig
	completion CandidateJSONCompletion
}

func NewArkCandidateStageEngine(
	config ArkCandidateEngineConfig,
) (CandidateStageEngine, error) {
	return NewArkCandidateStageEngineWithCompletion(config, completeCandidateJSONWithArk)
}

func NewArkCandidateStageEngineWithCompletion(
	config ArkCandidateEngineConfig,
	completion CandidateJSONCompletion,
) (CandidateStageEngine, error) {
	config.ModelID = strings.TrimSpace(config.ModelID)
	if config.ModelID == "" {
		return nil, fmt.Errorf("candidate Ark model id is required")
	}
	if completion == nil {
		return nil, fmt.Errorf("candidate JSON completion is required")
	}
	if config.MaxToolRounds == 0 {
		config.MaxToolRounds = defaultCandidateToolRounds
	}
	if config.MaxToolRounds < 1 || config.MaxToolRounds > maxCandidateToolRounds {
		return nil, fmt.Errorf(
			"candidate max tool rounds must be between 1 and %d",
			maxCandidateToolRounds,
		)
	}
	if config.ContextTokenBudget == 0 {
		config.ContextTokenBudget = defaultCandidateContextTokenBudget
	}
	if config.ContextTokenBudget < 1 ||
		config.ContextTokenBudget > maxCandidateContextTokenBudget {
		return nil, fmt.Errorf(
			"candidate context token budget must be between 1 and %d",
			maxCandidateContextTokenBudget,
		)
	}
	config.PolicyPrompt = strings.TrimSpace(config.PolicyPrompt)
	if config.PolicyPrompt == "" {
		config.PolicyPrompt = defaultCandidatePolicyPrompt
	}
	config.Scope = llmusage.NormalizeScope(config.Scope)
	return &arkCandidateStageEngine{config: config, completion: completion}, nil
}

func completeCandidateJSONWithArk(
	ctx context.Context,
	request CandidateCompletionRequest,
) (json.RawMessage, error) {
	text, err := ark_dal.ResponseTextWithCache(ctx, ark_dal.CachedResponseRequest{
		CacheScene:   request.CacheScene,
		SystemPrompt: request.SystemPrompt,
		UserPrompt:   request.UserPrompt,
		ModelID:      request.ModelID,
		Text: &responses.ResponsesText{
			Format: &responses.TextFormat{Type: responses.TextType_json_object},
		},
		Reasoning: &responses.ResponsesReasoning{
			Effort: responses.ReasoningEffort_minimal,
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

func (e *arkCandidateStageEngine) EvaluateActivation(
	ctx context.Context,
	input CandidateStageInput,
) (CandidateActivation, error) {
	raw, err := e.completeStage(ctx, candidateStageActivation, candidateActivationSystemPrompt, input)
	if err != nil {
		return CandidateActivation{}, err
	}
	var parsed struct {
		State  string `json:"state"`
		Reason string `json:"reason"`
	}
	if err := decodeCandidateStageJSON(raw, &parsed); err != nil {
		return CandidateActivation{}, fmt.Errorf("decode candidate activation: %w", err)
	}
	switch parsed.State {
	case "active", "silent":
	default:
		return CandidateActivation{}, fmt.Errorf("candidate activation state %q is invalid", parsed.State)
	}
	return CandidateActivation{JSON: raw}, nil
}

func (e *arkCandidateStageEngine) EvaluateRelevance(
	ctx context.Context,
	input CandidateStageInput,
	activation CandidateActivation,
) (CandidateRelevance, error) {
	raw, err := e.completeStage(ctx, candidateStageRelevance, candidateRelevanceSystemPrompt, struct {
		Input      CandidateStageInput `json:"input"`
		Activation json.RawMessage     `json:"activation"`
	}{Input: input, Activation: activation.JSON})
	if err != nil {
		return CandidateRelevance{}, err
	}
	var parsed struct {
		JoinDecision  JoinDecision  `json:"join_decision"`
		TopicRelation TopicRelation `json:"topic_relation"`
		Reason        string        `json:"reason"`
	}
	if err := decodeCandidateStageJSON(raw, &parsed); err != nil {
		return CandidateRelevance{}, fmt.Errorf("decode candidate relevance: %w", err)
	}
	if !parsed.JoinDecision.Valid() {
		return CandidateRelevance{}, fmt.Errorf(
			"candidate join decision %q is invalid",
			parsed.JoinDecision,
		)
	}
	if !parsed.TopicRelation.Valid() {
		return CandidateRelevance{}, fmt.Errorf(
			"candidate topic relation %q is invalid",
			parsed.TopicRelation,
		)
	}
	return CandidateRelevance{
		JSON:          raw,
		JoinDecision:  parsed.JoinDecision,
		TopicRelation: parsed.TopicRelation,
	}, nil
}

func (e *arkCandidateStageEngine) ComposeContext(
	ctx context.Context,
	input CandidateStageInput,
	activation CandidateActivation,
	relevance CandidateRelevance,
) (CandidateContext, error) {
	records, choices, degradedSources, err := buildCandidateContextRecords(input)
	if err != nil {
		return CandidateContext{}, err
	}
	raw, err := e.completeStage(ctx, candidateStageContext, candidateContextSystemPrompt, struct {
		EpisodeID    string                   `json:"episode_id"`
		AnchorAt     time.Time                `json:"anchor_at"`
		CurrentInput string                   `json:"current_input"`
		TokenBudget  int                      `json:"token_budget"`
		Candidates   []candidateContextChoice `json:"candidates"`
		Activation   json.RawMessage          `json:"activation"`
		Relevance    json.RawMessage          `json:"relevance"`
	}{
		EpisodeID: input.EpisodeID, AnchorAt: input.AnchorAt,
		CurrentInput: input.ContextSnapshot.CurrentInput,
		TokenBudget:  e.config.ContextTokenBudget, Candidates: choices,
		Activation: activation.JSON, Relevance: relevance.JSON,
	})
	if err != nil {
		return CandidateContext{}, err
	}
	var parsed struct {
		SelectedIDs []string `json:"selected_ids"`
		Reason      string   `json:"reason"`
	}
	if err := decodeCandidateStageJSON(raw, &parsed); err != nil {
		return CandidateContext{}, fmt.Errorf("decode candidate context: %w", err)
	}
	return composeCandidateSelectedContext(
		input,
		records,
		degradedSources,
		parsed.SelectedIDs,
		e.config.PolicyPrompt,
		e.config.ContextTokenBudget,
	)
}

type candidateContextChoice struct {
	ID             string        `json:"id"`
	Bucket         string        `json:"bucket"`
	OriginalBucket ContextBucket `json:"original_bucket,omitempty"`
	TokenCount     int           `json:"token_count"`
	OccurredAt     time.Time     `json:"occurred_at"`
	Content        string        `json:"content"`
}

type candidateContextRecord struct {
	choice         candidateContextChoice
	item           ContextItem
	originalBucket ContextBucket
}

func buildCandidateContextRecords(
	input CandidateStageInput,
) ([]candidateContextRecord, []candidateContextChoice, []string, error) {
	if input.AnchorAt.IsZero() || !input.AnchorAt.Equal(input.ContextSnapshot.AnchorAt) {
		return nil, nil, nil, fmt.Errorf("candidate context anchor does not match snapshot")
	}
	if strings.TrimSpace(input.ContextSnapshot.CurrentInput) == "" {
		return nil, nil, nil, fmt.Errorf("candidate context current input is required")
	}
	records := make([]candidateContextRecord, 0)
	choices := make([]candidateContextChoice, 0)
	degradedSources := append([]string(nil), input.ContextSnapshot.DegradedSources...)
	seenIDs := make(map[string]struct{})
	seenIdentity := make(map[string]struct{})
	appendRecord := func(
		item ContextItem,
		promptBucket string,
		originalBucket ContextBucket,
	) error {
		if !originalBucket.Valid() {
			return fmt.Errorf(
				"candidate context item %q has invalid original bucket %q",
				item.ID,
				originalBucket,
			)
		}
		if item.OccurredAt.IsZero() || item.OccurredAt.After(input.AnchorAt) {
			return fmt.Errorf("candidate context item %q occurred after the anchor", item.ID)
		}
		if err := item.validate(input.AnchorAt); err != nil {
			return fmt.Errorf("candidate context item %q is invalid: %w", item.ID, err)
		}
		if _, exists := seenIDs[item.ID]; exists {
			return fmt.Errorf("candidate context item id %q is duplicate", item.ID)
		}
		seenIDs[item.ID] = struct{}{}
		identity := item.Source + "\x00" + item.SourceID
		if _, exists := seenIdentity[identity]; exists {
			return fmt.Errorf(
				"candidate context identity %q/%q is duplicate",
				item.Source,
				item.SourceID,
			)
		}
		seenIdentity[identity] = struct{}{}
		choice := candidateContextChoice{
			ID: item.ID, Bucket: promptBucket, TokenCount: item.TokenCount,
			OccurredAt: item.OccurredAt, Content: item.Content,
		}
		if promptBucket == "excluded" {
			choice.OriginalBucket = originalBucket
		}
		records = append(records, candidateContextRecord{
			choice: choice, item: cloneCaptureValue(item), originalBucket: originalBucket,
		})
		choices = append(choices, choice)
		return nil
	}
	for _, item := range input.ContextSnapshot.Messages {
		if err := appendRecord(item, string(ContextBucketMessages), ContextBucketMessages); err != nil {
			return nil, nil, nil, err
		}
	}
	for _, item := range input.ContextSnapshot.Retrieved {
		if err := appendRecord(item, string(ContextBucketRetrieved), ContextBucketRetrieved); err != nil {
			return nil, nil, nil, err
		}
	}
	for _, item := range input.ContextSnapshot.Events {
		if err := appendRecord(item, string(ContextBucketEvents), ContextBucketEvents); err != nil {
			return nil, nil, nil, err
		}
	}
	for _, excluded := range input.ExcludedContext {
		item := cloneCaptureValue(excluded.ContextItem)
		originalBucket := excluded.OriginalBucket
		if originalBucket == "" {
			var inferred bool
			originalBucket, inferred = legacyContextBucketFromSource(item.Source)
			if !inferred {
				return nil, nil, nil, fmt.Errorf(
					"legacy excluded context item %q has no safe bucket inference",
					item.ID,
				)
			}
			item.Metadata = markCandidateLegacyBucketInference(item.Metadata)
			if !slices.Contains(degradedSources, "candidate_legacy_bucket_inference") {
				degradedSources = append(degradedSources, "candidate_legacy_bucket_inference")
			}
		}
		if err := appendRecord(item, "excluded", originalBucket); err != nil {
			return nil, nil, nil, err
		}
	}
	return records, choices, degradedSources, nil
}

func composeCandidateSelectedContext(
	input CandidateStageInput,
	records []candidateContextRecord,
	degradedSources []string,
	selectedIDs []string,
	policyPrompt string,
	contextTokenBudget int,
) (CandidateContext, error) {
	selected := make(map[string]struct{}, len(selectedIDs))
	known := make(map[string]candidateContextRecord, len(records))
	for _, record := range records {
		known[record.choice.ID] = record
	}
	selectedTokens := 0
	for _, id := range selectedIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			return CandidateContext{}, fmt.Errorf("candidate context selected id is empty")
		}
		if _, exists := selected[id]; exists {
			return CandidateContext{}, fmt.Errorf("candidate context selected id %q is duplicate", id)
		}
		record, exists := known[id]
		if !exists {
			return CandidateContext{}, fmt.Errorf("candidate context selected id %q is unknown", id)
		}
		selected[id] = struct{}{}
		selectedTokens += record.item.TokenCount
	}
	if selectedTokens > contextTokenBudget {
		return CandidateContext{}, fmt.Errorf(
			"candidate context selection exceeds token budget: %d > %d",
			selectedTokens,
			contextTokenBudget,
		)
	}

	snapshot := cloneCaptureValue(input.ContextSnapshot)
	snapshot.SystemPrompt = strings.TrimSpace(policyPrompt)
	snapshot.TokenBudget = contextTokenBudget
	snapshot.Messages = nil
	snapshot.Retrieved = nil
	snapshot.Events = nil
	snapshot.TokenEstimate = selectedTokens
	snapshot.Truncated = len(records) != len(selected)
	snapshot.DegradedSources = append([]string(nil), degradedSources...)
	excluded := make([]ExcludedContextItem, 0, len(records)-len(selected))
	for _, record := range records {
		item := cloneCaptureValue(record.item)
		if _, isSelected := selected[item.ID]; isSelected {
			item.Selected = true
			item.ExcludeReason = ""
			switch record.originalBucket {
			case ContextBucketMessages:
				snapshot.Messages = append(snapshot.Messages, item)
			case ContextBucketRetrieved:
				snapshot.Retrieved = append(snapshot.Retrieved, item)
			case ContextBucketEvents:
				snapshot.Events = append(snapshot.Events, item)
			default:
				return CandidateContext{}, fmt.Errorf(
					"candidate context item %q has invalid original bucket %q",
					item.ID,
					record.originalBucket,
				)
			}
			continue
		}
		item.Selected = false
		item.ExcludeReason = "candidate_not_selected"
		excluded = append(excluded, ExcludedContextItem{
			ContextItem: item, OriginalBucket: record.originalBucket,
		})
	}
	safeUserPrompt, err := json.Marshal(struct {
		CurrentInput string                        `json:"current_input"`
		Messages     []candidateSelectedPromptItem `json:"messages"`
		Retrieved    []candidateSelectedPromptItem `json:"retrieved"`
		Events       []candidateSelectedPromptItem `json:"events"`
	}{
		CurrentInput: snapshot.CurrentInput,
		Messages:     candidateSelectedPromptItems(snapshot.Messages, ContextBucketMessages),
		Retrieved:    candidateSelectedPromptItems(snapshot.Retrieved, ContextBucketRetrieved),
		Events:       candidateSelectedPromptItems(snapshot.Events, ContextBucketEvents),
	})
	if err != nil {
		return CandidateContext{}, fmt.Errorf("marshal candidate selected context prompt: %w", err)
	}
	snapshot.UserPrompt = string(safeUserPrompt)
	snapshot.TokenEstimate = EstimateTokens(snapshot.SystemPrompt) + EstimateTokens(snapshot.UserPrompt)
	if snapshot.TokenEstimate > snapshot.TokenBudget {
		return CandidateContext{}, fmt.Errorf(
			"candidate rebuilt context exceeds token budget: %d > %d",
			snapshot.TokenEstimate,
			snapshot.TokenBudget,
		)
	}
	if err := snapshot.Validate(); err != nil {
		return CandidateContext{}, fmt.Errorf("candidate selected context: %w", err)
	}
	return CandidateContext{Snapshot: snapshot, Excluded: excluded}, nil
}

type candidateSelectedPromptItem struct {
	ID         string        `json:"id"`
	Bucket     ContextBucket `json:"bucket"`
	Content    string        `json:"content"`
	OccurredAt time.Time     `json:"occurred_at"`
}

func candidateSelectedPromptItems(
	items []ContextItem,
	bucket ContextBucket,
) []candidateSelectedPromptItem {
	result := make([]candidateSelectedPromptItem, 0, len(items))
	for _, item := range items {
		result = append(result, candidateSelectedPromptItem{
			ID: item.ID, Bucket: bucket, Content: item.Content, OccurredAt: item.OccurredAt,
		})
	}
	return result
}

func legacyContextBucketFromSource(source string) (ContextBucket, bool) {
	switch strings.TrimSpace(source) {
	case ContextSourceHistory:
		return ContextBucketMessages, true
	case ContextSourceRetrieved:
		return ContextBucketRetrieved, true
	case ContextSourceEvent:
		return ContextBucketEvents, true
	default:
		return "", false
	}
}

func markCandidateLegacyBucketInference(metadata json.RawMessage) json.RawMessage {
	values := make(map[string]any)
	if len(bytes.TrimSpace(metadata)) != 0 {
		if err := json.Unmarshal(metadata, &values); err != nil {
			values = make(map[string]any)
		}
	}
	values["candidate_bucket_inference"] = "legacy_source"
	encoded, err := json.Marshal(values)
	if err != nil {
		return json.RawMessage(`{"candidate_bucket_inference":"legacy_source"}`)
	}
	return json.RawMessage(encoded)
}

func (e *arkCandidateStageEngine) Draft(
	ctx context.Context,
	input CandidateDraftInput,
) (CandidateDraft, error) {
	if strings.TrimSpace(input.ComposedContext.Snapshot.CurrentInput) == "" {
		return CandidateDraft{}, fmt.Errorf("candidate draft current input is required")
	}
	if input.Tools == nil {
		return CandidateDraft{}, fmt.Errorf("candidate draft shadow tool registry is required")
	}
	observations := make([]ToolObservation, 0)
	rounds := make([]json.RawMessage, 0, e.config.MaxToolRounds+1)
	toolRounds := 0
	for {
		raw, err := e.completeStage(
			ctx,
			candidateStageDraft,
			candidateDraftSystemPrompt+"\n\n# Candidate policy\n"+e.config.PolicyPrompt,
			buildCandidateDraftPrompt(input, observations, rounds),
		)
		if err != nil {
			return CandidateDraft{}, err
		}
		var parsed candidateDraftResponse
		if err := decodeCandidateStageJSON(raw, &parsed); err != nil {
			return CandidateDraft{}, fmt.Errorf("decode candidate draft: %w", err)
		}
		rounds = append(rounds, append(json.RawMessage(nil), raw...))
		if input.Relevance.JoinDecision == JoinDecisionSkip && parsed.Decision != "skip" {
			return CandidateDraft{}, fmt.Errorf(
				"candidate relevance skip requires a skip draft, got %q",
				parsed.Decision,
			)
		}
		switch parsed.Decision {
		case "reply":
			if len(parsed.ToolCalls) != 0 {
				return CandidateDraft{}, fmt.Errorf("candidate reply cannot include tool calls")
			}
			toolPlanJSON, err := encodeCandidateDraftRounds(rounds)
			if err != nil {
				return CandidateDraft{}, err
			}
			return CandidateDraft{
				ReplyText: parsed.Reply, ToolPlanJSON: toolPlanJSON,
				ToolObservations: cloneCaptureValue(observations),
			}, nil
		case "skip":
			if strings.TrimSpace(parsed.Reply) != "" || len(parsed.ToolCalls) != 0 {
				return CandidateDraft{}, fmt.Errorf("candidate skip cannot include reply or tools")
			}
			toolPlanJSON, err := encodeCandidateDraftRounds(rounds)
			if err != nil {
				return CandidateDraft{}, err
			}
			return CandidateDraft{
				ToolPlanJSON: toolPlanJSON, ToolObservations: cloneCaptureValue(observations),
			}, nil
		case "tool":
			if len(parsed.ToolCalls) == 0 {
				return CandidateDraft{}, fmt.Errorf("candidate tool decision requires tool calls")
			}
			if len(parsed.ToolCalls) > maxCandidateToolsPerRound {
				return CandidateDraft{}, fmt.Errorf(
					"candidate draft tool calls per round exceed limit %d",
					maxCandidateToolsPerRound,
				)
			}
			if toolRounds >= e.config.MaxToolRounds {
				return CandidateDraft{}, fmt.Errorf(
					"candidate draft tool round limit %d exceeded",
					e.config.MaxToolRounds,
				)
			}
			for _, call := range parsed.ToolCalls {
				name := strings.TrimSpace(call.Name)
				if name == "" {
					return CandidateDraft{}, fmt.Errorf("candidate draft tool name is required")
				}
				arguments, err := canonicalToolArguments(call.Arguments)
				if err != nil {
					return CandidateDraft{}, fmt.Errorf(
						"candidate draft tool %q arguments: %w",
						name,
						err,
					)
				}
				observation, err := input.Tools.Invoke(
					ctx,
					input.EpisodeID,
					name,
					arguments,
				)
				if err != nil {
					return CandidateDraft{}, fmt.Errorf(
						"candidate draft tool %q: %w",
						name,
						err,
					)
				}
				observations = append(observations, observation)
			}
			toolRounds++
		default:
			return CandidateDraft{}, fmt.Errorf(
				"candidate draft decision %q is invalid",
				parsed.Decision,
			)
		}
	}
}

type candidateDraftResponse struct {
	Decision  string              `json:"decision"`
	Reply     string              `json:"reply"`
	ToolCalls []candidateToolCall `json:"tool_calls"`
}

type candidateToolCall struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type candidateDraftPrompt struct {
	EpisodeID       string                   `json:"episode_id"`
	AnchorAt        time.Time                `json:"anchor_at"`
	CurrentInput    string                   `json:"current_input"`
	Activation      json.RawMessage          `json:"activation"`
	Relevance       json.RawMessage          `json:"relevance"`
	SelectedContext candidateSelectedContext `json:"selected_context"`
	Observations    []ToolObservation        `json:"observations"`
	PriorRounds     []json.RawMessage        `json:"prior_rounds"`
	AvailableTools  []string                 `json:"available_tools"`
}

type candidateSelectedContext struct {
	Messages  []candidateSelectedPromptItem `json:"messages"`
	Retrieved []candidateSelectedPromptItem `json:"retrieved"`
	Events    []candidateSelectedPromptItem `json:"events"`
}

func buildCandidateDraftPrompt(
	input CandidateDraftInput,
	observations []ToolObservation,
	rounds []json.RawMessage,
) candidateDraftPrompt {
	snapshot := input.ComposedContext.Snapshot
	return candidateDraftPrompt{
		EpisodeID: input.EpisodeID, AnchorAt: input.AnchorAt,
		CurrentInput: snapshot.CurrentInput,
		Activation:   append(json.RawMessage(nil), input.Activation.JSON...),
		Relevance:    append(json.RawMessage(nil), input.Relevance.JSON...),
		SelectedContext: candidateSelectedContext{
			Messages: candidateSelectedPromptItems(
				snapshot.Messages,
				ContextBucketMessages,
			),
			Retrieved: candidateSelectedPromptItems(
				snapshot.Retrieved,
				ContextBucketRetrieved,
			),
			Events: candidateSelectedPromptItems(
				snapshot.Events,
				ContextBucketEvents,
			),
		},
		Observations:   cloneCaptureValue(observations),
		PriorRounds:    cloneCaptureValue(rounds),
		AvailableTools: input.Tools.Names(),
	}
}

func encodeCandidateDraftRounds(rounds []json.RawMessage) (json.RawMessage, error) {
	encoded, err := json.Marshal(struct {
		Rounds []json.RawMessage `json:"rounds"`
	}{Rounds: rounds})
	if err != nil {
		return nil, fmt.Errorf("marshal candidate draft rounds: %w", err)
	}
	return json.RawMessage(encoded), nil
}

func (e *arkCandidateStageEngine) completeStage(
	ctx context.Context,
	stage, systemPrompt string,
	payload any,
) (json.RawMessage, error) {
	if e == nil || e.completion == nil {
		return nil, fmt.Errorf("candidate Ark engine is nil")
	}
	userPrompt, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal candidate %s prompt: %w", stage, err)
	}
	source := "conversation_candidate_" + stage
	scope := e.config.Scope
	scope.Source = source
	raw, err := e.completion(ctx, CandidateCompletionRequest{
		Stage:        stage,
		CacheScene:   source,
		ModelID:      e.config.ModelID,
		SystemPrompt: systemPrompt,
		UserPrompt:   string(userPrompt),
		Scope:        scope,
	})
	if err != nil {
		return nil, fmt.Errorf("candidate %s completion: %w", stage, err)
	}
	object, err := candidateObjectJSON("candidate "+stage, raw)
	if err != nil {
		return nil, err
	}
	return object, nil
}

func decodeCandidateStageJSON(raw json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}

const candidateActivationSystemPrompt = `You evaluate whether a group-chat agent is active or silent.
Return one JSON object only: {"state":"active|silent","reason":"..."}.`

const candidateRelevanceSystemPrompt = `You evaluate whether the agent should join the anchored group-chat topic.
Return one JSON object only with join_decision (join|skip), topic_relation
(related|new_topic|unrelated), and reason.`

const candidateContextSystemPrompt = `Select causal context IDs for the Candidate response.
Return one JSON object only: {"selected_ids":["..."],"reason":"..."}.
Never invent IDs and keep the selection within token_budget.`

const candidateDraftSystemPrompt = `Draft the Candidate group-chat action as one JSON object.
decision is reply, skip, or tool. A reply has reply text and no tool_calls.
A skip has empty reply and no tool_calls. A tool decision has structured
tool_calls containing name and object arguments. You may only call a tool whose
exact name appears in available_tools.`

const defaultCandidatePolicyPrompt = `You are a careful group-chat participant.
Only respond when relevant, preserve the current topic, use selected causal
context only, stay concise, and never claim an action or tool result that did
not occur.`
