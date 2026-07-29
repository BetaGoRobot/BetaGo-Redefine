package conversationeval

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

type fakeCandidateStageEngine struct {
	stages            []string
	inputs            []CandidateStageInput
	draftTool         string
	draftArgs         json.RawMessage
	draftReply        string
	activationJSON    json.RawMessage
	relevanceJSON     json.RawMessage
	draftToolPlanJSON json.RawMessage
	tokenUsageJSON    json.RawMessage
	activationErr     error
	relevanceErr      error
	contextErr        error
	draftErr          error
	draftErrAfterTool error
	mutateActivation  bool
	observedContents  []string
}

func (f *fakeCandidateStageEngine) EvaluateActivation(
	_ context.Context,
	input CandidateStageInput,
) (CandidateActivation, error) {
	f.stages = append(f.stages, "activation")
	f.inputs = append(f.inputs, input)
	f.observedContents = append(f.observedContents, input.ContextSnapshot.Messages[0].Content)
	if f.mutateActivation {
		input.ContextSnapshot.Messages[0].Content = "mutated"
		input.ContextSnapshot.Messages[0].Metadata[0] = '['
	}
	value := f.activationJSON
	if len(value) == 0 {
		value = json.RawMessage(`{"state":"active"}`)
	}
	return CandidateActivation{JSON: value}, f.activationErr
}

func (f *fakeCandidateStageEngine) EvaluateRelevance(
	_ context.Context,
	input CandidateStageInput,
	_ CandidateActivation,
) (CandidateRelevance, error) {
	f.stages = append(f.stages, "relevance")
	f.inputs = append(f.inputs, input)
	f.observedContents = append(f.observedContents, input.ContextSnapshot.Messages[0].Content)
	value := f.relevanceJSON
	if len(value) == 0 {
		value = json.RawMessage(`{"score":0.9}`)
	}
	return CandidateRelevance{
		JSON:          value,
		JoinDecision:  JoinDecisionJoin,
		TopicRelation: TopicRelationRelated,
	}, f.relevanceErr
}

func (f *fakeCandidateStageEngine) ComposeContext(
	_ context.Context,
	input CandidateStageInput,
	_ CandidateActivation,
	_ CandidateRelevance,
) (CandidateContext, error) {
	f.stages = append(f.stages, "context")
	f.inputs = append(f.inputs, input)
	f.observedContents = append(f.observedContents, input.ContextSnapshot.Messages[0].Content)
	return CandidateContext{Snapshot: input.ContextSnapshot}, f.contextErr
}

func (f *fakeCandidateStageEngine) Draft(
	ctx context.Context,
	input CandidateDraftInput,
) (CandidateDraft, error) {
	f.stages = append(f.stages, "draft")
	f.inputs = append(f.inputs, input.CandidateStageInput)
	f.observedContents = append(f.observedContents, input.ContextSnapshot.Messages[0].Content)
	if f.draftErr != nil {
		return CandidateDraft{}, f.draftErr
	}
	draft := CandidateDraft{
		ReplyText:      f.draftReply,
		TokenUsageJSON: json.RawMessage(`{"total_tokens":12}`),
		ToolPlanJSON:   f.draftToolPlanJSON,
	}
	if len(f.tokenUsageJSON) != 0 {
		draft.TokenUsageJSON = f.tokenUsageJSON
	}
	if f.draftTool != "" {
		observation, err := input.Tools.Invoke(ctx, input.EpisodeID, f.draftTool, f.draftArgs)
		if err != nil {
			return CandidateDraft{}, err
		}
		draft.ToolObservations = append(draft.ToolObservations, observation)
	}
	if f.draftErrAfterTool != nil {
		return CandidateDraft{}, f.draftErrAfterTool
	}
	return draft, nil
}

func TestCandidateRunnerUsesSameSnapshotInOrderAndReplaysControlObservation(t *testing.T) {
	snapshot := candidateTestSnapshot()
	cache := NewObservationCache()
	registry := NewAnchoredShadowToolRegistry(cache, snapshot.AnchorAt)
	candidateToolCalls := 0
	if err := registry.Register("finance_news_get", func(context.Context, json.RawMessage) (string, error) {
		candidateToolCalls++
		return "candidate-output", nil
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	engine := &fakeCandidateStageEngine{
		draftTool:  "finance_news_get",
		draftArgs:  json.RawMessage(`{"symbol":"AAPL"}`),
		draftReply: "candidate draft",
	}
	runner := NewCandidateRunner(engine, registry)
	request := CandidateRequest{
		OutputID:        "candidate-output-1",
		EpisodeID:       "episode-1",
		AnchorAt:        snapshot.AnchorAt,
		ContextSnapshot: snapshot,
		ControlCapture: CaptureSnapshot{Output: &Output{CapabilityCalls: []ToolTrace{{
			Name: "finance_news_get", Arguments: json.RawMessage(`{"symbol":"AAPL"}`),
			Output: "control-output",
		}}}},
	}

	got, err := runner.Run(context.Background(), request)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !reflect.DeepEqual(engine.stages, []string{"activation", "relevance", "context", "draft"}) {
		t.Fatalf("stage order = %#v", engine.stages)
	}
	for index, input := range engine.inputs {
		if input.EpisodeID != request.EpisodeID ||
			!input.AnchorAt.Equal(request.AnchorAt) ||
			!reflect.DeepEqual(input.ContextSnapshot, request.ContextSnapshot) {
			t.Fatalf("stage[%d] input diverged: %#v", index, input)
		}
	}
	if candidateToolCalls != 0 {
		t.Fatalf("candidate tool calls = %d, want replay before execution", candidateToolCalls)
	}
	if got.Lane != LaneCandidate || got.OutputMode != OutputModeShadow ||
		got.EpisodeID != request.EpisodeID || got.ReplyText != "candidate draft" ||
		!got.ContextSnapshot.AnchorAt.Equal(request.AnchorAt) {
		t.Fatalf("candidate output = %#v", got)
	}
	var toolPlan CandidateToolPlan
	if err := json.Unmarshal(got.ToolPlanJSON, &toolPlan); err != nil {
		t.Fatalf("tool plan json = %s: %v", got.ToolPlanJSON, err)
	}
	if len(toolPlan.Observations) != 1 ||
		!toolPlan.Observations[0].ReplayedFromControl ||
		toolPlan.Observations[0].SourceLane != LaneControl ||
		toolPlan.Observations[0].Output != "control-output" {
		t.Fatalf("tool observations = %#v", toolPlan.Observations)
	}
}

func TestCandidateRunnerKeepsReplayedToolTraceWhenDraftFailsAfterTool(t *testing.T) {
	snapshot := candidateTestSnapshot()
	cache := NewObservationCache()
	registry := NewAnchoredShadowToolRegistry(cache, snapshot.AnchorAt)
	candidateToolCalls := 0
	if err := registry.Register("finance_news_get", func(context.Context, json.RawMessage) (string, error) {
		candidateToolCalls++
		return "candidate-output", nil
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	engine := &fakeCandidateStageEngine{
		draftTool:         "finance_news_get",
		draftArgs:         json.RawMessage(`{"symbol":"AAPL"}`),
		draftErrAfterTool: errors.New("draft model failed after observation"),
	}
	got, err := NewCandidateRunner(engine, registry).Run(context.Background(), CandidateRequest{
		OutputID:        "candidate-output-1",
		EpisodeID:       "episode-1",
		AnchorAt:        snapshot.AnchorAt,
		ContextSnapshot: snapshot,
		ControlCapture: CaptureSnapshot{Output: &Output{CapabilityCalls: []ToolTrace{{
			Name: "finance_news_get", Arguments: json.RawMessage(`{"symbol":"AAPL"}`),
			Output: "control-output",
		}}}},
	})
	if err == nil {
		t.Fatal("Run() draft error = nil")
	}
	if candidateToolCalls != 0 {
		t.Fatalf("candidate tool calls = %d, want replay before execution", candidateToolCalls)
	}
	var toolPlan CandidateToolPlan
	if unmarshalErr := json.Unmarshal(got.ToolPlanJSON, &toolPlan); unmarshalErr != nil {
		t.Fatalf("tool plan json = %s: %v", got.ToolPlanJSON, unmarshalErr)
	}
	if len(toolPlan.Observations) != 1 ||
		!toolPlan.Observations[0].ReplayedFromControl ||
		toolPlan.Observations[0].Output != "control-output" {
		t.Fatalf("tool observations after draft error = %#v", toolPlan.Observations)
	}
	if !strings.Contains(string(got.ErrorJSON), `"stage":"draft"`) {
		t.Fatalf("error json = %s", got.ErrorJSON)
	}
}

func TestCandidateRunnerRejectsInvalidOrNonObjectStageJSON(t *testing.T) {
	tests := []struct {
		name  string
		stage string
		setup func(*fakeCandidateStageEngine)
	}{
		{
			name:  "activation",
			stage: "activation",
			setup: func(engine *fakeCandidateStageEngine) {
				engine.activationJSON = json.RawMessage(`{`)
			},
		},
		{
			name:  "relevance",
			stage: "relevance",
			setup: func(engine *fakeCandidateStageEngine) {
				engine.relevanceJSON = json.RawMessage(`"invalid"`)
			},
		},
		{
			name:  "draft tool plan",
			stage: "draft",
			setup: func(engine *fakeCandidateStageEngine) {
				engine.draftToolPlanJSON = json.RawMessage(`[]`)
			},
		},
		{
			name:  "draft token usage",
			stage: "draft",
			setup: func(engine *fakeCandidateStageEngine) {
				engine.tokenUsageJSON = json.RawMessage(`null`)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := candidateTestSnapshot()
			engine := &fakeCandidateStageEngine{}
			test.setup(engine)
			got, err := NewCandidateRunner(engine, NewAnchoredShadowToolRegistry(nil, snapshot.AnchorAt)).Run(
				context.Background(),
				CandidateRequest{
					OutputID: "candidate-output-1", EpisodeID: "episode-1",
					AnchorAt: snapshot.AnchorAt, ContextSnapshot: snapshot,
				},
			)
			if err == nil {
				t.Fatal("Run() accepted invalid or non-object stage JSON")
			}
			if !strings.Contains(string(got.ErrorJSON), `"stage":"`+test.stage+`"`) {
				t.Fatalf("error json = %s, want stage %q", got.ErrorJSON, test.stage)
			}
		})
	}
}

func TestCandidateRunnerRejectsRegistryOutsideToolAndReturnsErrorOutputWithoutEffects(t *testing.T) {
	snapshot := candidateTestSnapshot()
	registry := NewAnchoredShadowToolRegistry(NewObservationCache(), snapshot.AnchorAt)
	deliveryCalls := 0
	writerCalls := 0
	if err := registry.Register("send_message", func(context.Context, json.RawMessage) (string, error) {
		deliveryCalls++
		return "sent", nil
	}); err == nil {
		t.Fatal("shadow registry accepted fake Lark sender")
	}
	if err := registry.Register("config_set", func(context.Context, json.RawMessage) (string, error) {
		writerCalls++
		return "written", nil
	}); err == nil {
		t.Fatal("shadow registry accepted fake external writer")
	}

	engine := &fakeCandidateStageEngine{
		draftTool: "send_message",
		draftArgs: json.RawMessage(`{"content":"do not send"}`),
	}
	got, err := NewCandidateRunner(engine, registry).Run(context.Background(), CandidateRequest{
		OutputID: "candidate-output-1", EpisodeID: "episode-1",
		AnchorAt: snapshot.AnchorAt, ContextSnapshot: snapshot,
	})
	if err == nil {
		t.Fatal("Run() accepted registry-outside tool request")
	}
	if deliveryCalls != 0 || writerCalls != 0 {
		t.Fatalf("side effects = delivery %d writer %d, want zero", deliveryCalls, writerCalls)
	}
	if got.Lane != LaneCandidate || got.OutputMode != OutputModeShadow ||
		!strings.Contains(string(got.ErrorJSON), `"stage":"draft"`) ||
		!strings.Contains(string(got.ErrorJSON), "send_message") {
		t.Fatalf("candidate error output = %#v", got)
	}
}

func TestRunCandidateIfPresentKeepsServingCompatibleWithoutRunner(t *testing.T) {
	got, err := RunCandidateIfPresent(context.Background(), nil, CandidateRequest{})
	if err != nil || got != nil {
		t.Fatalf("RunCandidateIfPresent(nil) = %#v, %v; want nil, nil", got, err)
	}
}

func TestCandidateRunnerEngineErrorProducesErrorLaneOutput(t *testing.T) {
	snapshot := candidateTestSnapshot()
	engine := &fakeCandidateStageEngine{activationErr: errors.New("activation unavailable")}
	got, err := NewCandidateRunner(engine, NewAnchoredShadowToolRegistry(nil, snapshot.AnchorAt)).Run(
		context.Background(),
		CandidateRequest{
			OutputID: "candidate-output-1", EpisodeID: "episode-1",
			AnchorAt: snapshot.AnchorAt, ContextSnapshot: snapshot,
		},
	)
	if err == nil {
		t.Fatal("Run() engine error = nil")
	}
	if got.JoinDecision != JoinDecisionSkip ||
		!strings.Contains(string(got.ErrorJSON), `"stage":"activation"`) ||
		!strings.Contains(string(got.ErrorJSON), "activation unavailable") {
		t.Fatalf("engine error output = %#v", got)
	}
}

func TestCandidateRunnerRejectsAnchorMismatchBeforeCallingEngine(t *testing.T) {
	snapshot := candidateTestSnapshot()
	engine := &fakeCandidateStageEngine{}
	_, err := NewCandidateRunner(engine, NewAnchoredShadowToolRegistry(nil, snapshot.AnchorAt)).Run(
		context.Background(),
		CandidateRequest{
			OutputID: "candidate-output-1", EpisodeID: "episode-1",
			AnchorAt: snapshot.AnchorAt.Add(time.Millisecond), ContextSnapshot: snapshot,
		},
	)
	if err == nil {
		t.Fatal("Run() accepted anchor mismatch")
	}
	if len(engine.stages) != 0 {
		t.Fatalf("engine stages = %#v, want zero before request validation", engine.stages)
	}
}

func TestCandidateRunnerRejectsUnanchoredShadowRegistryBeforeCallingEngine(t *testing.T) {
	snapshot := candidateTestSnapshot()
	engine := &fakeCandidateStageEngine{}
	got, err := NewCandidateRunner(engine, NewShadowToolRegistry(nil)).Run(
		context.Background(),
		CandidateRequest{
			OutputID: "candidate-output-1", EpisodeID: "episode-1",
			AnchorAt: snapshot.AnchorAt, ContextSnapshot: snapshot,
		},
	)
	if err == nil {
		t.Fatal("Run() accepted unanchored shadow registry")
	}
	if len(engine.stages) != 0 {
		t.Fatalf("engine stages = %#v, want zero before registry validation", engine.stages)
	}
	if !strings.Contains(string(got.ErrorJSON), `"stage":"request"`) {
		t.Fatalf("error json = %s", got.ErrorJSON)
	}
}

func TestCandidateRunnerRejectsMismatchedRegistryAnchorBeforeCallingEngine(t *testing.T) {
	snapshot := candidateTestSnapshot()
	engine := &fakeCandidateStageEngine{}
	got, err := NewCandidateRunner(
		engine,
		NewAnchoredShadowToolRegistry(nil, snapshot.AnchorAt.Add(-time.Second)),
	).Run(
		context.Background(),
		CandidateRequest{
			OutputID: "candidate-output-1", EpisodeID: "episode-1",
			AnchorAt: snapshot.AnchorAt, ContextSnapshot: snapshot,
		},
	)
	if err == nil {
		t.Fatal("Run() accepted mismatched registry anchor")
	}
	if len(engine.stages) != 0 {
		t.Fatalf("engine stages = %#v, want zero before registry validation", engine.stages)
	}
	if !strings.Contains(string(got.ErrorJSON), `"stage":"request"`) ||
		!strings.Contains(string(got.ErrorJSON), "does not match") {
		t.Fatalf("error json = %s", got.ErrorJSON)
	}
}

func TestCandidateRunnerDeepCopiesSnapshotForEveryStage(t *testing.T) {
	snapshot := candidateTestSnapshot()
	engine := &fakeCandidateStageEngine{mutateActivation: true}
	got, err := NewCandidateRunner(engine, NewAnchoredShadowToolRegistry(nil, snapshot.AnchorAt)).Run(
		context.Background(),
		CandidateRequest{
			OutputID: "candidate-output-1", EpisodeID: "episode-1",
			AnchorAt: snapshot.AnchorAt, ContextSnapshot: snapshot,
		},
	)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	want := []string{"pre-window", "pre-window", "pre-window", "pre-window"}
	if !reflect.DeepEqual(engine.observedContents, want) {
		t.Fatalf("stage snapshot contents = %#v, want %#v", engine.observedContents, want)
	}
	if got.ContextSnapshot.Messages[0].Content != "pre-window" ||
		string(got.ContextSnapshot.Messages[0].Metadata) != `{"safe":true}` ||
		snapshot.Messages[0].Content != "pre-window" {
		t.Fatalf("snapshot mutation leaked: output %#v input %#v", got.ContextSnapshot.Messages[0], snapshot.Messages[0])
	}
}

func candidateTestSnapshot() ContextSnapshot {
	anchor := time.Date(2026, 7, 29, 15, 0, 0, 0, time.UTC)
	return ContextSnapshot{
		SchemaVersion: SchemaVersion,
		AnchorEventID: "event-1",
		AnchorAt:      anchor,
		Messages: []ContextItem{{
			ID: "message-1", Source: ContextSourceHistory, SourceID: "om-1",
			Kind: ContextKindMessage, Content: "pre-window", ContentHash: ContentSHA256("pre-window"),
			Rank: 1, TokenCount: 3, Selected: true, OccurredAt: anchor.Add(-time.Minute),
			Metadata: json.RawMessage(`{"safe":true}`),
		}},
		SystemPrompt: "system", UserPrompt: "user", CurrentInput: "current user input",
		TokenEstimate: 3, TokenBudget: 100,
	}
}
