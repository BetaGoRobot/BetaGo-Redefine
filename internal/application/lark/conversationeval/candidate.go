package conversationeval

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type CandidateRequest struct {
	OutputID        string
	EpisodeID       string
	AnchorAt        time.Time
	ContextSnapshot ContextSnapshot
	ExcludedContext []ExcludedContextItem
	ControlCapture  CaptureSnapshot
}

type CandidateStageInput struct {
	EpisodeID       string
	AnchorAt        time.Time
	ContextSnapshot ContextSnapshot
	ExcludedContext []ExcludedContextItem
}

type CandidateActivation struct {
	JSON json.RawMessage
}

type CandidateRelevance struct {
	JSON          json.RawMessage
	JoinDecision  JoinDecision
	TopicRelation TopicRelation
}

type CandidateContext struct {
	Snapshot ContextSnapshot
	Excluded []ExcludedContextItem
}

type CandidateDraftInput struct {
	CandidateStageInput
	Activation      CandidateActivation
	Relevance       CandidateRelevance
	ComposedContext CandidateContext
	Tools           *ShadowToolRegistry
}

type CandidateDraft struct {
	ReplyText        string
	ToolPlanJSON     json.RawMessage
	ToolObservations []ToolObservation
	TokenUsageJSON   json.RawMessage
}

type CandidateToolPlan struct {
	Plan         json.RawMessage   `json:"plan,omitempty"`
	Observations []ToolObservation `json:"observations"`
}

type CandidateStageEngine interface {
	EvaluateActivation(context.Context, CandidateStageInput) (CandidateActivation, error)
	EvaluateRelevance(context.Context, CandidateStageInput, CandidateActivation) (CandidateRelevance, error)
	ComposeContext(context.Context, CandidateStageInput, CandidateActivation, CandidateRelevance) (CandidateContext, error)
	Draft(context.Context, CandidateDraftInput) (CandidateDraft, error)
}

type CandidateRunner interface {
	Run(context.Context, CandidateRequest) (LaneOutput, error)
}

type candidateRunner struct {
	engine CandidateStageEngine
	tools  *ShadowToolRegistry
	now    func() time.Time
}

func NewCandidateRunner(
	engine CandidateStageEngine,
	tools *ShadowToolRegistry,
) CandidateRunner {
	return &candidateRunner{engine: engine, tools: tools, now: time.Now}
}

func RunCandidateIfPresent(
	ctx context.Context,
	runner CandidateRunner,
	request CandidateRequest,
) (*LaneOutput, error) {
	if runner == nil {
		return nil, nil
	}
	output, err := runner.Run(ctx, request)
	return &output, err
}

func (r *candidateRunner) Run(
	ctx context.Context,
	request CandidateRequest,
) (LaneOutput, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, invocationRecorder := withShadowInvocationRecorder(ctx)
	startedAt := time.Now()
	if r != nil && r.now != nil {
		startedAt = r.now()
	}
	originalSnapshot := cloneCaptureValue(request.ContextSnapshot)
	originalExcluded := cloneCaptureValue(request.ExcludedContext)
	output := newCandidateLaneOutput(request, originalSnapshot, originalExcluded, startedAt)

	fail := func(stage string, err error) (LaneOutput, error) {
		finishedAt := time.Now()
		if r != nil && r.now != nil {
			finishedAt = r.now()
		}
		output.Latency = finishedAt.Sub(startedAt)
		if output.Latency < 0 {
			output.Latency = 0
		}
		output.UpdatedAt = finishedAt
		output.ErrorJSON = candidateErrorJSON(stage, err)
		output.JoinDecision = JoinDecisionSkip
		output.TopicRelation = TopicRelationUnrelated
		if observations := invocationRecorder.Snapshot(); len(observations) != 0 {
			if toolPlanJSON, marshalErr := candidateToolPlanJSON(nil, observations); marshalErr == nil {
				output.ToolPlanJSON = toolPlanJSON
			}
		}
		return output, err
	}

	if err := validateCandidateRequest(request, originalSnapshot); err != nil {
		return fail("request", err)
	}
	if r == nil || r.engine == nil {
		return fail("request", fmt.Errorf("candidate stage engine is nil"))
	}
	if r.tools == nil {
		return fail("request", fmt.Errorf("candidate shadow tool registry is nil"))
	}
	if err := r.tools.validateAnchor(request.AnchorAt); err != nil {
		return fail("request", err)
	}
	if err := r.tools.cache.RecordControlSnapshot(ctx, request.EpisodeID, request.ControlCapture); err != nil {
		return fail("observation_cache", err)
	}

	stageInput := func() CandidateStageInput {
		return CandidateStageInput{
			EpisodeID:       request.EpisodeID,
			AnchorAt:        request.AnchorAt,
			ContextSnapshot: cloneCaptureValue(originalSnapshot),
			ExcludedContext: cloneCaptureValue(originalExcluded),
		}
	}

	activation, err := r.engine.EvaluateActivation(ctx, stageInput())
	if err != nil {
		return fail("activation", err)
	}
	activationJSON, err := candidateObjectJSON("activation", activation.JSON)
	if err != nil {
		return fail("activation", err)
	}
	output.ActivationJSON = activationJSON

	relevance, err := r.engine.EvaluateRelevance(
		ctx,
		stageInput(),
		cloneCaptureValue(activation),
	)
	if err != nil {
		return fail("relevance", err)
	}
	relevanceJSON, err := candidateObjectJSON("relevance", relevance.JSON)
	if err != nil {
		return fail("relevance", err)
	}
	output.RelevanceJSON = relevanceJSON
	output.JoinDecision = relevance.JoinDecision
	output.TopicRelation = relevance.TopicRelation

	composed, err := r.engine.ComposeContext(
		ctx,
		stageInput(),
		cloneCaptureValue(activation),
		cloneCaptureValue(relevance),
	)
	if err != nil {
		return fail("context", err)
	}
	if err := validateCandidateComposedContext(composed, request.AnchorAt); err != nil {
		return fail("context", err)
	}
	output.ContextSnapshot = cloneCaptureValue(composed.Snapshot)
	output.ExcludedContext = cloneCaptureValue(composed.Excluded)

	draft, err := r.engine.Draft(ctx, CandidateDraftInput{
		CandidateStageInput: stageInput(),
		Activation:          cloneCaptureValue(activation),
		Relevance:           cloneCaptureValue(relevance),
		ComposedContext:     cloneCaptureValue(composed),
		Tools:               r.tools,
	})
	if err != nil {
		return fail("draft", err)
	}
	toolPlanJSON, err := candidateObjectJSON("draft tool plan", draft.ToolPlanJSON)
	if err != nil {
		return fail("draft", err)
	}
	tokenUsageJSON, err := candidateObjectJSON("draft token usage", draft.TokenUsageJSON)
	if err != nil {
		return fail("draft", err)
	}
	observations := invocationRecorder.Snapshot()
	if len(observations) == 0 {
		observations = cloneCaptureValue(draft.ToolObservations)
	}
	encodedToolPlan, err := candidateToolPlanJSON(toolPlanJSON, observations)
	if err != nil {
		return fail("draft", err)
	}
	output.ReplyText = draft.ReplyText
	output.ToolPlanJSON = encodedToolPlan
	output.TokenUsageJSON = tokenUsageJSON

	finishedAt := time.Now()
	if r.now != nil {
		finishedAt = r.now()
	}
	output.Latency = finishedAt.Sub(startedAt)
	if output.Latency < 0 {
		output.Latency = 0
	}
	output.UpdatedAt = finishedAt
	if err := output.Validate(); err != nil {
		return fail("output", err)
	}
	return output, nil
}

func validateCandidateRequest(request CandidateRequest, snapshot ContextSnapshot) error {
	if strings.TrimSpace(request.OutputID) == "" {
		return fmt.Errorf("candidate output id is required")
	}
	if strings.TrimSpace(request.EpisodeID) == "" {
		return fmt.Errorf("candidate episode id is required")
	}
	if request.AnchorAt.IsZero() || !request.AnchorAt.Equal(snapshot.AnchorAt) {
		return fmt.Errorf("candidate anchor must equal context snapshot anchor")
	}
	if err := snapshot.Validate(); err != nil {
		return fmt.Errorf("candidate context snapshot: %w", err)
	}
	return nil
}

func validateCandidateComposedContext(value CandidateContext, anchor time.Time) error {
	if !value.Snapshot.AnchorAt.Equal(anchor) {
		return fmt.Errorf("candidate composed context anchor changed")
	}
	if err := value.Snapshot.Validate(); err != nil {
		return fmt.Errorf("candidate composed context: %w", err)
	}
	return nil
}

func newCandidateLaneOutput(
	request CandidateRequest,
	snapshot ContextSnapshot,
	excluded []ExcludedContextItem,
	startedAt time.Time,
) LaneOutput {
	return LaneOutput{
		ID:              strings.TrimSpace(request.OutputID),
		EpisodeID:       strings.TrimSpace(request.EpisodeID),
		Lane:            LaneCandidate,
		OutputMode:      OutputModeShadow,
		ActivationJSON:  json.RawMessage(`{}`),
		RelevanceJSON:   json.RawMessage(`{}`),
		JoinDecision:    JoinDecisionSkip,
		TopicRelation:   TopicRelationUnrelated,
		ContextSnapshot: snapshot,
		ExcludedContext: excluded,
		ToolPlanJSON:    json.RawMessage(`{}`),
		TokenUsageJSON:  json.RawMessage(`{}`),
		ErrorJSON:       json.RawMessage(`{}`),
		CreatedAt:       startedAt,
		UpdatedAt:       startedAt,
	}
}

func candidateToolPlanJSON(
	toolPlanJSON json.RawMessage,
	observations []ToolObservation,
) (json.RawMessage, error) {
	normalizedPlan, err := candidateObjectJSON("candidate tool plan", toolPlanJSON)
	if err != nil {
		return nil, err
	}
	plan := CandidateToolPlan{
		Plan:         normalizedPlan,
		Observations: cloneCaptureValue(observations),
	}
	encoded, err := json.Marshal(plan)
	if err != nil {
		return nil, fmt.Errorf("marshal candidate tool plan: %w", err)
	}
	return json.RawMessage(encoded), nil
}

func candidateErrorJSON(stage string, err error) json.RawMessage {
	message := ""
	if err != nil {
		message = err.Error()
	}
	encoded, marshalErr := json.Marshal(map[string]string{
		"stage":   strings.TrimSpace(stage),
		"message": message,
	})
	if marshalErr != nil {
		return json.RawMessage(`{"stage":"unknown","message":"marshal error"}`)
	}
	return json.RawMessage(encoded)
}

func candidateObjectJSON(field string, value json.RawMessage) (json.RawMessage, error) {
	if len(bytes.TrimSpace(value)) == 0 {
		return json.RawMessage(`{}`), nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(value, &object); err != nil || object == nil {
		if err == nil {
			err = fmt.Errorf("value is not a JSON object")
		}
		return nil, fmt.Errorf("%s must be a JSON object: %w", strings.TrimSpace(field), err)
	}
	return append(json.RawMessage(nil), value...), nil
}
