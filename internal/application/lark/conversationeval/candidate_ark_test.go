package conversationeval

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/llmusage"
)

func TestArkCandidateProductionConstructorUsesArkCompletionAndRequiresModel(t *testing.T) {
	if _, err := NewArkCandidateStageEngine(ArkCandidateEngineConfig{}); err == nil {
		t.Fatal("NewArkCandidateStageEngine() accepted empty model id")
	}

	built, err := NewArkCandidateStageEngine(ArkCandidateEngineConfig{
		ModelID: "candidate-model",
		Scope: llmusage.Scope{
			SourceType: llmusage.SourceTypeBackground,
			Source:     "conversation_candidate",
		},
	})
	if err != nil {
		t.Fatalf("NewArkCandidateStageEngine() error = %v", err)
	}
	engine, ok := built.(*arkCandidateStageEngine)
	if !ok {
		t.Fatalf("production engine type = %T", built)
	}
	if reflect.ValueOf(engine.completion).Pointer() !=
		reflect.ValueOf(CandidateJSONCompletion(completeCandidateJSONWithArk)).Pointer() {
		t.Fatal("production constructor did not select the Ark JSON completion wrapper")
	}
}

func TestCandidateCachedResponseRequestUsesCentralizedPrefixCacheEligibility(t *testing.T) {
	req := candidateCachedResponseRequest(CandidateCompletionRequest{
		CacheScene: "conversation_candidate_activation",
		ModelID:    "candidate-model", SystemPrompt: "system", UserPrompt: "user",
	})
	if req.DisablePrefixCache {
		t.Fatal("candidate Ark request should let ResponseTextWithCache check token length")
	}
}

func TestArkCandidateContextPreservesOriginalBucketsAndBudget(t *testing.T) {
	input := candidateContextTestInput()
	var contextPrompt string
	engine := newCandidateTestArkEngine(t, func(
		_ context.Context,
		request CandidateCompletionRequest,
	) (json.RawMessage, error) {
		if request.Stage != candidateStageContext {
			t.Fatalf("stage = %q, want context", request.Stage)
		}
		contextPrompt = request.UserPrompt
		return json.RawMessage(
			`{"selected_ids":["message-1","event-1","excluded-1"],"reason":"best evidence"}`,
		), nil
	})

	got, err := engine.ComposeContext(
		context.Background(),
		input,
		CandidateActivation{JSON: json.RawMessage(`{"state":"active"}`)},
		CandidateRelevance{
			JSON:         json.RawMessage(`{"join_decision":"join","topic_relation":"related"}`),
			JoinDecision: JoinDecisionJoin, TopicRelation: TopicRelationRelated,
		},
	)
	if err != nil {
		t.Fatalf("ComposeContext() error = %v", err)
	}
	if !strings.Contains(contextPrompt, `"bucket":"messages"`) ||
		!strings.Contains(contextPrompt, `"bucket":"retrieved"`) ||
		!strings.Contains(contextPrompt, `"bucket":"events"`) ||
		!strings.Contains(contextPrompt, `"bucket":"excluded"`) ||
		!strings.Contains(contextPrompt, `"current_input":"current question"`) {
		t.Fatalf("context prompt lost bucket identity: %s", contextPrompt)
	}
	if len(got.Snapshot.Messages) != 1 || got.Snapshot.Messages[0].ID != "message-1" ||
		len(got.Snapshot.Retrieved) != 1 || got.Snapshot.Retrieved[0].ID != "excluded-1" ||
		len(got.Snapshot.Events) != 1 || got.Snapshot.Events[0].ID != "event-1" {
		t.Fatalf("rebuilt buckets = messages %#v retrieved %#v events %#v",
			got.Snapshot.Messages, got.Snapshot.Retrieved, got.Snapshot.Events)
	}
	wantEstimate := EstimateTokens(got.Snapshot.SystemPrompt) + EstimateTokens(got.Snapshot.UserPrompt)
	if got.Snapshot.TokenEstimate != wantEstimate ||
		got.Snapshot.TokenBudget != defaultCandidateContextTokenBudget {
		t.Fatalf("rebuilt token accounting = %d/%d", got.Snapshot.TokenEstimate, got.Snapshot.TokenBudget)
	}
	if got.Snapshot.SystemPrompt != defaultCandidatePolicyPrompt {
		t.Fatalf("rebuilt candidate policy = %q", got.Snapshot.SystemPrompt)
	}
	if strings.Contains(got.Snapshot.UserPrompt, "must-not-leak") ||
		!strings.Contains(got.Snapshot.UserPrompt, "current question") ||
		!strings.Contains(got.Snapshot.UserPrompt, "excluded-1 content") {
		t.Fatalf("rebuilt safe user prompt = %q", got.Snapshot.UserPrompt)
	}
	if len(got.Excluded) != 1 ||
		got.Excluded[0].ID != "retrieved-1" ||
		got.Excluded[0].OriginalBucket != ContextBucketRetrieved ||
		got.Excluded[0].ExcludeReason != "candidate_not_selected" {
		t.Fatalf("rebuilt excluded = %#v", got.Excluded)
	}
	if err := got.Snapshot.Validate(); err != nil {
		t.Fatalf("rebuilt snapshot Validate() error = %v", err)
	}
}

func TestArkCandidateContextRejectsInvalidSelections(t *testing.T) {
	tests := []struct {
		name      string
		response  json.RawMessage
		mutate    func(*CandidateStageInput)
		wantErr   string
		wantCalls int
	}{
		{
			name: "unknown id", response: json.RawMessage(`{"selected_ids":["unknown"]}`),
			wantErr: "unknown", wantCalls: 1,
		},
		{
			name: "duplicate id", response: json.RawMessage(`{"selected_ids":["message-1","message-1"]}`),
			wantErr: "duplicate", wantCalls: 1,
		},
		{
			name: "budget overflow", response: json.RawMessage(`{"selected_ids":["message-1","retrieved-1","event-1","excluded-1"]}`),
			wantErr: "budget", wantCalls: 1,
		},
		{
			name:     "post anchor candidate",
			response: json.RawMessage(`{"selected_ids":["message-1"]}`),
			mutate: func(input *CandidateStageInput) {
				input.ContextSnapshot.Messages[0].OccurredAt = input.AnchorAt.Add(time.Millisecond)
			},
			wantErr: "after the anchor", wantCalls: 0,
		},
		{
			name:     "missing current input",
			response: json.RawMessage(`{"selected_ids":[]}`),
			mutate: func(input *CandidateStageInput) {
				input.ContextSnapshot.CurrentInput = ""
			},
			wantErr: "current input", wantCalls: 0,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := candidateContextTestInput()
			if test.mutate != nil {
				test.mutate(&input)
			}
			calls := 0
			contextBudget := 0
			if test.name == "budget overflow" {
				contextBudget = 9
			}
			engine := newCandidateTestArkEngineWithBudget(t, contextBudget, func(
				_ context.Context,
				request CandidateCompletionRequest,
			) (json.RawMessage, error) {
				calls++
				return test.response, nil
			})
			_, err := engine.ComposeContext(
				context.Background(),
				input,
				CandidateActivation{JSON: json.RawMessage(`{"state":"active"}`)},
				CandidateRelevance{
					JSON:         json.RawMessage(`{"join_decision":"join","topic_relation":"related"}`),
					JoinDecision: JoinDecisionJoin, TopicRelation: TopicRelationRelated,
				},
			)
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("ComposeContext() error = %v, want %q", err, test.wantErr)
			}
			if calls != test.wantCalls {
				t.Fatalf("completion calls = %d, want %d", calls, test.wantCalls)
			}
		})
	}
}

func TestArkCandidateUsesOwnContextBudgetForEarlyControlSkipSnapshot(t *testing.T) {
	snapshot := candidateTestSnapshot()
	snapshot.Messages = nil
	snapshot.Retrieved = nil
	snapshot.Events = nil
	snapshot.SystemPrompt = ""
	snapshot.UserPrompt = ""
	snapshot.TokenEstimate = 0
	snapshot.TokenBudget = 0
	snapshot.CurrentInput = "high-value skip disagreement"
	engine := newCandidateTestArkEngine(t, func(
		_ context.Context,
		request CandidateCompletionRequest,
	) (json.RawMessage, error) {
		switch request.Stage {
		case candidateStageActivation:
			return json.RawMessage(`{"state":"active","reason":"candidate disagrees"}`), nil
		case candidateStageRelevance:
			return json.RawMessage(`{"join_decision":"join","topic_relation":"related","reason":"answerable"}`), nil
		case candidateStageContext:
			return json.RawMessage(`{"selected_ids":[],"reason":"current input is enough"}`), nil
		case candidateStageDraft:
			return json.RawMessage(`{"decision":"reply","reply":"candidate answer","tool_calls":[]}`), nil
		default:
			t.Fatalf("unexpected stage %q", request.Stage)
			return nil, nil
		}
	})
	got, err := NewCandidateRunner(
		engine,
		NewAnchoredShadowToolRegistry(nil, snapshot.AnchorAt),
	).Run(context.Background(), CandidateRequest{
		OutputID: "candidate-output-1", EpisodeID: "episode-1",
		AnchorAt: snapshot.AnchorAt, ContextSnapshot: snapshot,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got.ReplyText != "candidate answer" ||
		got.ContextSnapshot.TokenBudget != defaultCandidateContextTokenBudget ||
		got.ContextSnapshot.TokenEstimate <= 0 {
		t.Fatalf("early skip candidate output = %#v", got)
	}
}

func TestArkCandidateContextMarksLegacyBucketInference(t *testing.T) {
	input := candidateContextTestInput()
	input.ExcludedContext[0].OriginalBucket = ""
	engine := newCandidateTestArkEngine(t, func(
		_ context.Context,
		request CandidateCompletionRequest,
	) (json.RawMessage, error) {
		return json.RawMessage(`{"selected_ids":["excluded-1"]}`), nil
	})

	got, err := engine.ComposeContext(
		context.Background(),
		input,
		CandidateActivation{JSON: json.RawMessage(`{"state":"active"}`)},
		CandidateRelevance{
			JSON:         json.RawMessage(`{"join_decision":"join","topic_relation":"related"}`),
			JoinDecision: JoinDecisionJoin, TopicRelation: TopicRelationRelated,
		},
	)
	if err != nil {
		t.Fatalf("ComposeContext() error = %v", err)
	}
	if len(got.Snapshot.Retrieved) != 1 ||
		!strings.Contains(string(got.Snapshot.Retrieved[0].Metadata), "legacy_source") ||
		!slices.Contains(got.Snapshot.DegradedSources, "candidate_legacy_bucket_inference") {
		t.Fatalf("legacy inferred context = %#v", got.Snapshot)
	}
}

func TestArkCandidateDraftUsesOnlySelectedContextAndFeedsBackShadowObservations(t *testing.T) {
	input := candidateDraftTestInput()
	var prompts []string
	engine := newCandidateTestArkEngineWithRounds(t, 1, func(
		_ context.Context,
		request CandidateCompletionRequest,
	) (json.RawMessage, error) {
		prompts = append(prompts, request.UserPrompt)
		if strings.Contains(request.UserPrompt, "must-not-leak") {
			t.Fatalf("draft prompt leaked unselected/control context: %s", request.UserPrompt)
		}
		if len(prompts) == 1 {
			if !strings.Contains(request.UserPrompt, "current question") ||
				!strings.Contains(request.UserPrompt, "promoted-selected") ||
				!strings.Contains(request.UserPrompt, `"available_tools":["finance_news_get"]`) ||
				strings.Contains(request.UserPrompt, "send_message") ||
				!strings.Contains(request.SystemPrompt, "candidate-policy-sentinel") {
				t.Fatalf("initial draft prompt missing selected context: %s", request.UserPrompt)
			}
			return json.RawMessage(
				`{"decision":"tool","reply":"","tool_calls":[{"name":"finance_news_get","arguments":{"symbol":"AAPL"}}]}`,
			), nil
		}
		if !strings.Contains(request.UserPrompt, "fresh finance output") {
			t.Fatalf("follow-up prompt missing observation: %s", request.UserPrompt)
		}
		return json.RawMessage(
			`{"decision":"reply","reply":"final candidate","tool_calls":[]}`,
		), nil
	})
	registry := NewAnchoredShadowToolRegistry(NewObservationCache(), input.AnchorAt)
	calls := 0
	if err := registry.Register("finance_news_get", func(
		context.Context,
		json.RawMessage,
	) (string, error) {
		calls++
		return "fresh finance output", nil
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	input.Tools = registry

	got, err := engine.Draft(context.Background(), input)
	if err != nil {
		t.Fatalf("Draft() error = %v", err)
	}
	if got.ReplyText != "final candidate" || calls != 1 || len(prompts) != 2 ||
		len(got.ToolObservations) != 1 ||
		got.ToolObservations[0].ToolName != "finance_news_get" {
		t.Fatalf("draft = %#v, calls %d prompts %d", got, calls, len(prompts))
	}
}

func TestArkCandidateDraftSkipHasEmptyReplyAndNoTools(t *testing.T) {
	input := candidateDraftTestInput()
	engine := newCandidateTestArkEngineWithRounds(t, 1, func(
		_ context.Context,
		request CandidateCompletionRequest,
	) (json.RawMessage, error) {
		return json.RawMessage(`{"decision":"skip","reply":"","tool_calls":[]}`), nil
	})
	registry := NewAnchoredShadowToolRegistry(NewObservationCache(), input.AnchorAt)
	calls := 0
	if err := registry.Register("finance_news_get", func(
		context.Context,
		json.RawMessage,
	) (string, error) {
		calls++
		return "must not run", nil
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	input.Tools = registry

	got, err := engine.Draft(context.Background(), input)
	if err != nil {
		t.Fatalf("Draft() error = %v", err)
	}
	if got.ReplyText != "" || calls != 0 || len(got.ToolObservations) != 0 {
		t.Fatalf("skip draft = %#v, tool calls %d", got, calls)
	}
}

func TestArkCandidateDraftRejectsUnsafeToolWithoutFallback(t *testing.T) {
	input := candidateDraftTestInput()
	engine := newCandidateTestArkEngineWithRounds(t, 1, func(
		_ context.Context,
		request CandidateCompletionRequest,
	) (json.RawMessage, error) {
		return json.RawMessage(
			`{"decision":"tool","reply":"","tool_calls":[{"name":"send_message","arguments":{"content":"never"}}]}`,
		), nil
	})
	registry := NewAnchoredShadowToolRegistry(NewObservationCache(), input.AnchorAt)
	safeCalls := 0
	if err := registry.Register("finance_news_get", func(
		context.Context,
		json.RawMessage,
	) (string, error) {
		safeCalls++
		return "safe", nil
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	input.Tools = registry

	if _, err := engine.Draft(context.Background(), input); err == nil ||
		!strings.Contains(err.Error(), "send_message") {
		t.Fatalf("Draft() unsafe tool error = %v", err)
	}
	if safeCalls != 0 {
		t.Fatalf("safe fallback calls = %d, want zero", safeCalls)
	}
}

func TestArkCandidateDraftRelevanceSkipRejectsReplyOrToolWithoutInvocation(t *testing.T) {
	responses := []json.RawMessage{
		json.RawMessage(`{"decision":"reply","reply":"must not reply","tool_calls":[]}`),
		json.RawMessage(`{"decision":"tool","reply":"","tool_calls":[{"name":"finance_news_get","arguments":{}}]}`),
	}
	for _, response := range responses {
		t.Run(string(response), func(t *testing.T) {
			input := candidateDraftTestInput()
			input.Relevance.JoinDecision = JoinDecisionSkip
			input.Relevance.TopicRelation = TopicRelationUnrelated
			input.Relevance.JSON = json.RawMessage(
				`{"join_decision":"skip","topic_relation":"unrelated"}`,
			)
			engine := newCandidateTestArkEngineWithRounds(t, 1, func(
				_ context.Context,
				request CandidateCompletionRequest,
			) (json.RawMessage, error) {
				return response, nil
			})
			registry := NewAnchoredShadowToolRegistry(NewObservationCache(), input.AnchorAt)
			calls := 0
			if err := registry.Register("finance_news_get", func(
				context.Context,
				json.RawMessage,
			) (string, error) {
				calls++
				return "must not run", nil
			}); err != nil {
				t.Fatalf("Register() error = %v", err)
			}
			input.Tools = registry

			if _, err := engine.Draft(context.Background(), input); err == nil ||
				!strings.Contains(err.Error(), "requires a skip") {
				t.Fatalf("Draft() relevance skip error = %v", err)
			}
			if calls != 0 {
				t.Fatalf("tool calls = %d, want zero", calls)
			}
		})
	}
}

func TestArkCandidateDraftRejectsNonObjectToolArguments(t *testing.T) {
	for _, arguments := range []string{`[]`, `null`, `"AAPL"`} {
		t.Run(arguments, func(t *testing.T) {
			input := candidateDraftTestInput()
			engine := newCandidateTestArkEngineWithRounds(t, 1, func(
				_ context.Context,
				request CandidateCompletionRequest,
			) (json.RawMessage, error) {
				return json.RawMessage(
					`{"decision":"tool","reply":"","tool_calls":[{"name":"finance_news_get","arguments":` +
						arguments + `}]}`,
				), nil
			})
			registry := NewAnchoredShadowToolRegistry(NewObservationCache(), input.AnchorAt)
			calls := 0
			if err := registry.Register("finance_news_get", func(
				context.Context,
				json.RawMessage,
			) (string, error) {
				calls++
				return "must not run", nil
			}); err != nil {
				t.Fatalf("Register() error = %v", err)
			}
			input.Tools = registry

			if _, err := engine.Draft(context.Background(), input); err == nil ||
				!strings.Contains(err.Error(), "arguments") {
				t.Fatalf("Draft() non-object arguments error = %v", err)
			}
			if calls != 0 {
				t.Fatalf("tool calls = %d, want zero", calls)
			}
		})
	}
}

func TestArkCandidateDraftCapsToolCallsPerRoundBeforeInvocation(t *testing.T) {
	input := candidateDraftTestInput()
	engine := newCandidateTestArkEngineWithRounds(t, 2, func(
		_ context.Context,
		request CandidateCompletionRequest,
	) (json.RawMessage, error) {
		return json.RawMessage(`{
			"decision":"tool",
			"reply":"",
			"tool_calls":[
				{"name":"finance_news_get","arguments":{"n":1}},
				{"name":"finance_news_get","arguments":{"n":2}},
				{"name":"finance_news_get","arguments":{"n":3}},
				{"name":"finance_news_get","arguments":{"n":4}},
				{"name":"finance_news_get","arguments":{"n":5}}
			]
		}`), nil
	})
	registry := NewAnchoredShadowToolRegistry(NewObservationCache(), input.AnchorAt)
	calls := 0
	if err := registry.Register("finance_news_get", func(
		context.Context,
		json.RawMessage,
	) (string, error) {
		calls++
		return "must not run", nil
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	input.Tools = registry

	if _, err := engine.Draft(context.Background(), input); err == nil ||
		!strings.Contains(err.Error(), "per round") {
		t.Fatalf("Draft() per-round cap error = %v", err)
	}
	if calls != 0 {
		t.Fatalf("tool calls = %d, want zero", calls)
	}
}

func TestArkCandidateRoundCapErrorRetainsCompletedObservation(t *testing.T) {
	snapshot := candidateTestSnapshot()
	draftCalls := 0
	engine := newCandidateTestArkEngineWithRounds(t, 1, func(
		_ context.Context,
		request CandidateCompletionRequest,
	) (json.RawMessage, error) {
		switch request.Stage {
		case candidateStageActivation:
			return json.RawMessage(`{"state":"active","reason":"test"}`), nil
		case candidateStageRelevance:
			return json.RawMessage(`{"join_decision":"join","topic_relation":"related","reason":"test"}`), nil
		case candidateStageContext:
			return json.RawMessage(`{"selected_ids":["message-1"],"reason":"test"}`), nil
		case candidateStageDraft:
			draftCalls++
			return json.RawMessage(
				`{"decision":"tool","reply":"","tool_calls":[{"name":"finance_news_get","arguments":{"symbol":"AAPL"}}]}`,
			), nil
		default:
			t.Fatalf("unexpected stage %q", request.Stage)
			return nil, nil
		}
	})
	registry := NewAnchoredShadowToolRegistry(NewObservationCache(), snapshot.AnchorAt)
	toolCalls := 0
	if err := registry.Register("finance_news_get", func(
		context.Context,
		json.RawMessage,
	) (string, error) {
		toolCalls++
		return "observed once", nil
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	got, err := NewCandidateRunner(engine, registry).Run(context.Background(), CandidateRequest{
		OutputID: "candidate-output-1", EpisodeID: "episode-1",
		AnchorAt: snapshot.AnchorAt, ContextSnapshot: snapshot,
	})
	if err == nil || !strings.Contains(err.Error(), "round") {
		t.Fatalf("Run() round cap error = %v", err)
	}
	var plan CandidateToolPlan
	if unmarshalErr := json.Unmarshal(got.ToolPlanJSON, &plan); unmarshalErr != nil {
		t.Fatalf("ToolPlanJSON = %s: %v", got.ToolPlanJSON, unmarshalErr)
	}
	if draftCalls != 2 || toolCalls != 1 || len(plan.Observations) != 1 ||
		plan.Observations[0].Output != "observed once" {
		t.Fatalf("round cap output = draft calls %d tool calls %d plan %#v", draftCalls, toolCalls, plan)
	}
}

func TestCandidateRunnerAggregatesArkStageUsageAndKeepsDraftFallback(t *testing.T) {
	snapshot := candidateTestSnapshot()
	engine := newCandidateTestArkEngineWithRounds(t, 1, func(
		ctx context.Context,
		request CandidateCompletionRequest,
	) (json.RawMessage, error) {
		if err := llmusage.RecordUsage(ctx, llmusage.Record{
			PromptTokens: 2, CompletionTokens: 3, TotalTokens: 5,
		}); err != nil {
			t.Fatalf("RecordUsage() error = %v", err)
		}
		switch request.Stage {
		case candidateStageActivation:
			return json.RawMessage(`{"state":"active","reason":"test"}`), nil
		case candidateStageRelevance:
			return json.RawMessage(`{"join_decision":"join","topic_relation":"related","reason":"test"}`), nil
		case candidateStageContext:
			return json.RawMessage(`{"selected_ids":["message-1"],"reason":"test"}`), nil
		case candidateStageDraft:
			return json.RawMessage(`{"decision":"reply","reply":"done","tool_calls":[]}`), nil
		default:
			t.Fatalf("unexpected stage %q", request.Stage)
			return nil, nil
		}
	})
	registry := NewAnchoredShadowToolRegistry(NewObservationCache(), snapshot.AnchorAt)
	got, err := NewCandidateRunner(engine, registry).Run(context.Background(), CandidateRequest{
		OutputID: "candidate-output-1", EpisodeID: "episode-1",
		AnchorAt: snapshot.AnchorAt, ContextSnapshot: snapshot,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	var usage TokenUsage
	if err := json.Unmarshal(got.TokenUsageJSON, &usage); err != nil {
		t.Fatalf("TokenUsageJSON = %s: %v", got.TokenUsageJSON, err)
	}
	if usage.PromptTokens != 8 || usage.CompletionTokens != 12 ||
		usage.TotalTokens != 20 || usage.Records != 4 {
		t.Fatalf("aggregated usage = %#v", usage)
	}

	fallbackEngine := &fakeCandidateStageEngine{}
	fallback, err := NewCandidateRunner(
		fallbackEngine,
		NewAnchoredShadowToolRegistry(nil, snapshot.AnchorAt),
	).Run(context.Background(), CandidateRequest{
		OutputID: "candidate-output-2", EpisodeID: "episode-2",
		AnchorAt: snapshot.AnchorAt, ContextSnapshot: snapshot,
	})
	if err != nil {
		t.Fatalf("fallback Run() error = %v", err)
	}
	var fallbackUsage TokenUsage
	if err := json.Unmarshal(fallback.TokenUsageJSON, &fallbackUsage); err != nil {
		t.Fatalf("fallback TokenUsageJSON = %s: %v", fallback.TokenUsageJSON, err)
	}
	if fallbackUsage.TotalTokens != 12 {
		t.Fatalf("fallback usage = %#v", fallbackUsage)
	}
}

func candidateDraftTestInput() CandidateDraftInput {
	input := candidateContextTestInput()
	input.ContextSnapshot.Messages[0].Content = "must-not-leak-unselected"
	input.ContextSnapshot.UserPrompt = "must-not-leak-control-user-prompt"
	selected := input.ExcludedContext[0].ContextItem
	selected.Content = "promoted-selected"
	selected.ContentHash = ContentSHA256(selected.Content)
	selected.Selected = true
	selected.ExcludeReason = ""
	snapshot := input.ContextSnapshot
	snapshot.Messages = nil
	snapshot.Retrieved = []ContextItem{selected}
	snapshot.Events = nil
	snapshot.UserPrompt = "must-not-leak-rebuilt-control-prompt"
	return CandidateDraftInput{
		CandidateStageInput: input,
		Activation: CandidateActivation{
			JSON: json.RawMessage(`{"state":"active"}`),
		},
		Relevance: CandidateRelevance{
			JSON:         json.RawMessage(`{"join_decision":"join","topic_relation":"related"}`),
			JoinDecision: JoinDecisionJoin, TopicRelation: TopicRelationRelated,
		},
		ComposedContext: CandidateContext{
			Snapshot: snapshot,
			Excluded: input.ExcludedContext,
		},
	}
}

func newCandidateTestArkEngineWithRounds(
	t *testing.T,
	rounds int,
	completion CandidateJSONCompletion,
) CandidateStageEngine {
	t.Helper()
	engine, err := NewArkCandidateStageEngineWithCompletion(
		ArkCandidateEngineConfig{
			ModelID:      "candidate-model",
			PolicyPrompt: "candidate-policy-sentinel",
			Scope: llmusage.Scope{
				SourceType: llmusage.SourceTypeBackground,
				Source:     "candidate-test",
			},
			MaxToolRounds: rounds,
		},
		completion,
	)
	if err != nil {
		t.Fatalf("NewArkCandidateStageEngineWithCompletion() error = %v", err)
	}
	return engine
}

func candidateContextTestInput() CandidateStageInput {
	anchor := time.Date(2026, 7, 29, 15, 0, 0, 123000000, time.UTC)
	item := func(
		id, source, kind string,
		tokens int,
	) ContextItem {
		return ContextItem{
			ID: id, Source: source, SourceID: id, Kind: kind,
			Content: id + " content", ContentHash: ContentSHA256(id + " content"),
			Rank: 1, TokenCount: tokens, Selected: true, OccurredAt: anchor.Add(-time.Minute),
			Metadata: json.RawMessage(`{}`),
		}
	}
	message := item("message-1", ContextSourceHistory, ContextKindMessage, 2)
	retrieved := item("retrieved-1", ContextSourceRetrieved, ContextKindChunk, 3)
	event := item("event-1", ContextSourceEvent, ContextKindEvent, 1)
	excludedItem := item("excluded-1", ContextSourceRetrieved, ContextKindChunk, 4)
	excludedItem.Selected = false
	excludedItem.ExcludeReason = "control_budget"
	return CandidateStageInput{
		EpisodeID: "episode-1",
		AnchorAt:  anchor,
		ContextSnapshot: ContextSnapshot{
			SchemaVersion: SchemaVersion, AnchorEventID: "anchor-1", AnchorAt: anchor,
			Messages: []ContextItem{message}, Retrieved: []ContextItem{retrieved},
			Events: []ContextItem{event}, SystemPrompt: "system",
			UserPrompt: "must-not-leak-control-user-prompt", CurrentInput: "current question",
			TokenEstimate: 6, TokenBudget: 1000,
		},
		ExcludedContext: []ExcludedContextItem{{
			ContextItem: excludedItem, OriginalBucket: ContextBucketRetrieved,
		}},
	}
}

func newCandidateTestArkEngine(
	t *testing.T,
	completion CandidateJSONCompletion,
) CandidateStageEngine {
	return newCandidateTestArkEngineWithBudget(t, 0, completion)
}

func newCandidateTestArkEngineWithBudget(
	t *testing.T,
	contextTokenBudget int,
	completion CandidateJSONCompletion,
) CandidateStageEngine {
	t.Helper()
	engine, err := NewArkCandidateStageEngineWithCompletion(
		ArkCandidateEngineConfig{
			ModelID:            "candidate-model",
			ContextTokenBudget: contextTokenBudget,
			Scope: llmusage.Scope{
				SourceType: llmusage.SourceTypeBackground,
				Source:     "candidate-test",
			},
			MaxToolRounds: 2,
		},
		completion,
	)
	if err != nil {
		t.Fatalf("NewArkCandidateStageEngineWithCompletion() error = %v", err)
	}
	return engine
}

func TestArkCandidateRunsFourRealStagesWithModelSceneAndScope(t *testing.T) {
	var requests []CandidateCompletionRequest
	completion := func(
		_ context.Context,
		request CandidateCompletionRequest,
	) (json.RawMessage, error) {
		requests = append(requests, request)
		switch request.Stage {
		case candidateStageActivation:
			return json.RawMessage(`{"state":"active","reason":"direct request"}`), nil
		case candidateStageRelevance:
			return json.RawMessage(`{"join_decision":"join","topic_relation":"related","reason":"on topic"}`), nil
		case candidateStageContext:
			return json.RawMessage(`{"selected_ids":["message-1"],"reason":"causal"}`), nil
		case candidateStageDraft:
			return json.RawMessage(`{"decision":"reply","reply":"candidate reply","tool_calls":[]}`), nil
		default:
			t.Fatalf("unexpected completion stage %q", request.Stage)
			return nil, nil
		}
	}
	built, err := NewArkCandidateStageEngineWithCompletion(
		ArkCandidateEngineConfig{
			ModelID: "candidate-model",
			Scope: llmusage.Scope{
				ChatID:     "oc_chat",
				SourceType: llmusage.SourceTypeBackground,
				Source:     "base",
			},
			MaxToolRounds: 2,
		},
		completion,
	)
	if err != nil {
		t.Fatalf("NewArkCandidateStageEngineWithCompletion() error = %v", err)
	}

	snapshot := candidateTestSnapshot()
	input := CandidateStageInput{
		EpisodeID:       "episode-1",
		AnchorAt:        snapshot.AnchorAt,
		ContextSnapshot: snapshot,
	}
	activation, err := built.EvaluateActivation(context.Background(), input)
	if err != nil {
		t.Fatalf("EvaluateActivation() error = %v", err)
	}
	relevance, err := built.EvaluateRelevance(context.Background(), input, activation)
	if err != nil {
		t.Fatalf("EvaluateRelevance() error = %v", err)
	}
	composed, err := built.ComposeContext(context.Background(), input, activation, relevance)
	if err != nil {
		t.Fatalf("ComposeContext() error = %v", err)
	}
	draft, err := built.Draft(context.Background(), CandidateDraftInput{
		CandidateStageInput: input,
		Activation:          activation,
		Relevance:           relevance,
		ComposedContext:     composed,
		Tools:               NewAnchoredShadowToolRegistry(nil, snapshot.AnchorAt),
	})
	if err != nil {
		t.Fatalf("Draft() error = %v", err)
	}
	if relevance.JoinDecision != JoinDecisionJoin ||
		relevance.TopicRelation != TopicRelationRelated ||
		draft.ReplyText != "candidate reply" {
		t.Fatalf("stage results = relevance %#v draft %#v", relevance, draft)
	}

	wantStages := []string{
		candidateStageActivation,
		candidateStageRelevance,
		candidateStageContext,
		candidateStageDraft,
	}
	if len(requests) != len(wantStages) {
		t.Fatalf("completion requests = %d, want %d", len(requests), len(wantStages))
	}
	for index, request := range requests {
		stage := wantStages[index]
		if request.Stage != stage ||
			request.ModelID != "candidate-model" ||
			request.CacheScene != "conversation_candidate_"+stage ||
			request.Scope.Source != "conversation_candidate_"+stage ||
			request.Scope.SourceType != llmusage.SourceTypeBackground ||
			!strings.Contains(request.UserPrompt, "episode-1") {
			t.Fatalf("request[%d] = %#v", index, request)
		}
	}
}
