package memes

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// ReferenceService retrieves source material. The host model decides freely whether and how to use it.
type ReferenceService struct {
	deps    ReferenceDependencies
	options ReferenceOptions
}

func NewReferenceService(deps ReferenceDependencies, options ReferenceOptions) (*ReferenceService, error) {
	if deps.Model == nil || deps.Retriever == nil {
		return nil, errors.New("query model and retriever are required")
	}
	if options.RequestTimeout < 0 || options.RequestTimeout > 120*time.Second || options.StageTimeout < 0 || options.StageTimeout > 60*time.Second {
		return nil, errors.New("request timeout must be 0..120s and stage timeout 0..60s")
	}
	if options.RequestTimeout == 0 {
		options.RequestTimeout = 30 * time.Second
	}
	if options.StageTimeout == 0 {
		options.StageTimeout = 8 * time.Second
		if options.StageTimeout > options.RequestTimeout {
			options.StageTimeout = options.RequestTimeout
		}
	}
	if options.StageTimeout > options.RequestTimeout {
		return nil, errors.New("stage timeout exceeds request timeout")
	}
	options.Metadata.RequestTimeoutMS = options.RequestTimeout.Milliseconds()
	options.Metadata.StageTimeoutMS = options.StageTimeout.Milliseconds()
	options.Metadata.CandidateBudget = CandidateBudget
	raw, err := json.Marshal(options.Metadata)
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(raw, &options.Metadata); err != nil {
		return nil, err
	}
	return &ReferenceService{deps: deps, options: options}, nil
}

func referenceFailureCode(err error, otherwise string) string {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return ReasonTimeout
	}
	return otherwise
}

func (s *ReferenceService) Retrieve(parent context.Context, request Request) ReferenceResult {
	started := time.Now()
	ctx, cancel := context.WithTimeout(parent, s.options.RequestTimeout)
	defer cancel()
	result := ReferenceResult{Status: ReferenceStatusError, Cards: []UseCard{}, Sources: []SourceFragment{}, Warnings: []Warning{}, Errors: []ReferenceError{}, Metadata: s.options.Metadata}
	result.Metadata.SemanticCalls = 0
	result.Metadata.RepairCalls = 0
	result.Metadata.Calls = nil
	finish := func(status ReferenceStatus, reason string) ReferenceResult {
		if err := ctx.Err(); err != nil {
			status, reason = ReferenceStatusError, ReasonTimeout
			result.Errors = append(result.Errors, ReferenceError{Stage: StageRetrieval, Code: ReasonTimeout, Detail: err.Error()})
		}
		result.Status = status
		result.Reason = reason
		result.Metadata.ElapsedMS = time.Since(started).Milliseconds()
		if status != ReferenceStatusReady {
			result.Cards = []UseCard{}
		}
		return result
	}
	fail := func(stage Stage, code string, err error) ReferenceResult {
		result.Errors = append(result.Errors, ReferenceError{Stage: stage, Code: code, Detail: err.Error()})
		return finish(ReferenceStatusError, code)
	}
	r, sources, err := ValidateRequest(request)
	if err != nil {
		return fail(StageInput, ReasonContractInvalid, err)
	}
	result.Sources = sources
	inputJSON, err := json.Marshal(struct {
		CurrentTurnID string           `json:"current_turn_id"`
		Request       Request          `json:"request"`
		Sources       []SourceFragment `json:"sources"`
	}{r.Dialogue[len(r.Dialogue)-1].TurnID, r, sources})
	if err != nil {
		return fail(StageInput, ReasonContractInvalid, err)
	}
	original := append(json.RawMessage(nil), inputJSON...)
	var analysis QueryAnalysis
	for attempt := 0; attempt < 2; attempt++ {
		if err := ctx.Err(); err != nil {
			return fail(StageQuery, ReasonTimeout, err)
		}
		prompt := SystemPrompt(StageQuery)
		if attempt > 0 {
			prompt += "只修复JSON结构、字段名称、类型和必填项；不引入新事实，不改变原始输入。"
			result.Metadata.RepairCalls++
		} else {
			result.Metadata.SemanticCalls++
		}
		stageCtx, stageCancel := context.WithTimeout(ctx, s.options.StageTimeout)
		callStarted := time.Now()
		response, callErr := s.deps.Model.Complete(stageCtx, CompletionRequest{Stage: StageQuery, SystemPrompt: prompt, InputJSON: inputJSON, Schema: StageSchema(StageQuery), Repair: attempt > 0})
		stageErr := stageCtx.Err()
		stageCancel()
		if callErr == nil && stageErr != nil {
			callErr = stageErr
		}
		response.Metadata.DurationMS = time.Since(callStarted).Milliseconds()
		response.Metadata.PromptHash, _ = Fingerprint(prompt)
		result.Metadata.Calls = append(result.Metadata.Calls, response.Metadata)
		result.Metadata.Model = response.Metadata.Model
		result.Metadata.PromptHash = response.Metadata.PromptHash
		if callErr != nil {
			return fail(StageQuery, referenceFailureCode(callErr, ReasonModelFailed), callErr)
		}
		if err = DecodeStrict(response.JSON, &analysis); err == nil {
			break
		}
		if attempt == 1 {
			return fail(StageQuery, ReasonContractInvalid, err)
		}
		inputJSON, _ = json.Marshal(map[string]any{"original_input": json.RawMessage(original), "invalid_output": string(response.JSON), "structure_error": err.Error()})
	}
	plan, err := CompileQueryPlan(analysis, r, sources)
	result.Plan = plan
	result.Warnings = append(result.Warnings, plan.Warnings...)
	result.Degraded = len(plan.Warnings) > 0
	if err != nil {
		return fail(StageQuery, ReasonContractInvalid, err)
	}
	if len(plan.Queries) == 0 {
		return finish(ReferenceStatusUnresolved, ReasonPlanEmpty)
	}
	if err := ctx.Err(); err != nil {
		return fail(StageRetrieval, ReasonTimeout, err)
	}
	retrievalCtx, retrievalCancel := context.WithTimeout(ctx, s.options.StageTimeout)
	snapshot, searchErr := s.deps.Retriever.Search(retrievalCtx, plan)
	result.Retrieval = snapshot
	retrievalErr := retrievalCtx.Err()
	retrievalCancel()
	if searchErr == nil && retrievalErr != nil {
		searchErr = retrievalErr
	}
	if searchErr != nil && referenceFailureCode(searchErr, ReasonRetrievalFailed) == ReasonTimeout {
		return fail(StageRetrieval, ReasonTimeout, searchErr)
	}
	rawQueries := snapshot.RawQueries
	if rawQueries == nil {
		rawQueries = snapshot.Queries
	}
	if searchErr != nil && len(rawQueries) == 0 {
		return fail(StageRetrieval, ReasonRetrievalFailed, searchErr)
	}
	snapshot, err = FuseRetrieval(plan, rawQueries)
	result.Retrieval = snapshot
	if err != nil {
		return fail(StageRetrieval, ReasonContractInvalid, err)
	}
	result.Warnings = append(result.Warnings, snapshot.Warnings...)
	result.Degraded = snapshot.Degraded || len(plan.Warnings) > 0
	if searchErr != nil {
		result.Degraded = true
		result.Warnings = append(result.Warnings, Warning{Code: ReasonRetrievalFailed, Detail: searchErr.Error()})
		result.Errors = append(result.Errors, ReferenceError{Stage: StageRetrieval, Code: ReasonRetrievalFailed, Detail: searchErr.Error()})
	}
	if len(snapshot.Candidates) == 0 {
		for _, warning := range snapshot.Warnings {
			switch warning.Code {
			case "INVALID_CARD", "CONFLICTING_FULL", "INVALID_HIT", "INVALID_INDEX_HIT":
				return fail(StageRetrieval, ReasonContractInvalid, errors.New("all retrieved references are invalid"))
			}
		}
		if snapshot.SuccessfulQueries == 0 || snapshot.Degraded || searchErr != nil {
			return fail(StageRetrieval, ReasonRetrievalFailed, errors.New("retrieval failed without usable references"))
		}
		return finish(ReferenceStatusEmpty, ReasonNoCandidates)
	}
	result.Cards = snapshot.Candidates
	return finish(ReferenceStatusReady, "")
}
