package memes

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

type GroundedReferenceService struct {
	base                 *ReferenceService
	selector             *KnowledgeSelector
	knowledgeFingerprint string
	visibility           ReferenceVisibility
}

func (s *GroundedReferenceService) WithVisibility(policy ReferenceVisibility) *GroundedReferenceService {
	copy := *s
	copy.visibility = policy
	return &copy
}

// PreparedKnowledge owns a detached, validated immutable snapshot. Reusing it
// avoids hashing the complete corpus in each request's first-phase budget.
type PreparedKnowledge struct {
	selector    *KnowledgeSelector
	fingerprint string
}

func PrepareKnowledge(snapshot KnowledgeSnapshot) (*PreparedKnowledge, error) {
	selector, err := NewKnowledgeSelector(snapshot)
	if err != nil {
		return nil, err
	}
	return &PreparedKnowledge{selector: selector, fingerprint: snapshot.Fingerprint}, nil
}
func NewGroundedReferenceService(deps ReferenceDependencies, options ReferenceOptions, snapshot KnowledgeSnapshot) (*GroundedReferenceService, error) {
	prepared, err := PrepareKnowledge(snapshot)
	if err != nil {
		return nil, err
	}
	return NewGroundedReferenceServiceWithKnowledge(deps, options, prepared)
}
func NewGroundedReferenceServiceWithKnowledge(deps ReferenceDependencies, options ReferenceOptions, prepared *PreparedKnowledge) (*GroundedReferenceService, error) {
	base, err := NewReferenceService(deps, options)
	if err != nil {
		return nil, err
	}
	if prepared == nil {
		return nil, errors.New("knowledge unavailable")
	}
	return &GroundedReferenceService{base: base, selector: prepared.selector, knowledgeFingerprint: prepared.fingerprint}, nil
}
func (s *GroundedReferenceService) Retrieve(parent context.Context, request Request) ReferenceResult {
	started := time.Now()
	ctx, cancel := context.WithTimeout(parent, s.base.options.RequestTimeout)
	defer cancel()
	result := ReferenceResult{Status: ReferenceStatusError, Cards: []UseCard{}, Warnings: []Warning{}, Errors: []ReferenceError{}, Metadata: s.base.options.Metadata}
	var selection KnowledgeSelection
	finish := func(status ReferenceStatus, reason string) ReferenceResult {
		if s.visibility != nil && result.ContextV2 != nil {
			current, err := s.visibility.CheckKnowledge(ctx, selection)
			cards, cardErr := s.visibility.FilterUseCards(ctx, result.Cards)
			if err != nil || cardErr != nil {
				result.ContextV2.PriorKnowledge = nil
				result.ContextV2.Interpretations = nil
				result.Cards = nil
				result.ContextV2.Stages.Knowledge = "policy_unavailable"
				status = ReferenceStatusError
				reason = "SOURCE_POLICY_UNAVAILABLE"
				result.Errors = append(result.Errors, ReferenceError{Stage: StageInput, Code: reason, Detail: "current source visibility unavailable"})
			} else {
				RestrictGroundedContext(result.ContextV2, current)
				result.Cards = cards
				if status == ReferenceStatusReady && len(cards) == 0 {
					status = ReferenceStatusEmpty
					reason = ReasonNoCandidates
				}
			}
		}
		result.Status = status
		result.Reason = reason
		result.Metadata.ElapsedMS = time.Since(started).Milliseconds()
		result.Degraded = len(result.Warnings) > 0 || len(result.Errors) > 0
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
	knowledgeStarted := time.Now()
	knowledgeCtx, knowledgeStop := context.WithTimeout(ctx, 500*time.Millisecond)
	defer knowledgeStop()
	selection, err = s.selector.Select(r)
	if err != nil {
		return fail(StageInput, ReasonContractInvalid, err)
	}
	if s.visibility != nil {
		selection, err = s.visibility.CheckKnowledge(knowledgeCtx, selection)
		if err != nil {
			return fail(StageInput, "SOURCE_POLICY_UNAVAILABLE", errors.New("current source visibility unavailable"))
		}
	}
	result.ContextV2 = GroundedKnowledgeContext(selection)
	prior := result.ContextV2
	prior.BindKnowledgeSnapshot(s.knowledgeFingerprint)
	prior.KnowledgeMS = time.Since(knowledgeStarted).Milliseconds()
	for _, source := range sources {
		prior.SourceIDs = append(prior.SourceIDs, source.SourceID)
	}
	knowledgeErr := knowledgeCtx.Err()
	knowledgeStop()
	if time.Since(knowledgeStarted) > 500*time.Millisecond || knowledgeErr != nil {
		return fail(StageInput, "KNOWLEDGE_TIMEOUT", context.DeadlineExceeded)
	}
	if err = ctx.Err(); err != nil {
		return fail(StageInput, ReasonTimeout, err)
	}
	// Only knowledge actually packed into the model input may be referenced.
	provided := map[string]bool{}
	for _, entry := range prior.PriorKnowledge {
		provided[entry.ID] = true
	}
	filtered := selection.Entries[:0:0]
	for _, entry := range selection.Entries {
		if provided[entry.ID] {
			filtered = append(filtered, entry)
		}
	}
	selection.Entries = filtered
	input, err := json.Marshal(struct {
		CurrentTurnID    string           `json:"current_turn_id"`
		Request          Request          `json:"request"`
		Sources          []SourceFragment `json:"sources"`
		Context          *GroundedContext `json:"knowledge_context"`
		RemainingQueries int              `json:"remaining_queries"`
	}{r.Dialogue[len(r.Dialogue)-1].TurnID, r, sources, prior, 3})
	if err != nil {
		return fail(StageInput, ReasonContractInvalid, err)
	}
	stage, stop := s.stageContext(ctx)
	callStarted := time.Now()
	result.Metadata.SemanticCalls = 1
	response, err := s.base.deps.Model.Complete(stage, CompletionRequest{Stage: StageQuery, SystemPrompt: GroundedPlannerPrompt, InputJSON: input, Schema: GroundedSchema()})
	stageErr := stage.Err()
	stop()
	response.Metadata.DurationMS = time.Since(callStarted).Milliseconds()
	response.Metadata.PromptHash, _ = Fingerprint(GroundedPlannerPrompt)
	result.Metadata.Calls = []CallMetadata{response.Metadata}
	result.Metadata.Model = response.Metadata.Model
	result.Metadata.PromptHash = response.Metadata.PromptHash
	if err == nil {
		err = stageErr
	}
	if err != nil {
		prior.Stages.Planner = "error"
		return fail(StageQuery, referenceFailureCode(err, ReasonModelFailed), err)
	}
	analysis, err := decodeGroundedAnalysis(response.JSON)
	if err != nil {
		prior.Stages.Planner = "invalid"
		return fail(StageQuery, ReasonContractInvalid, err)
	}
	plan, hypotheses, warnings := CompileGroundedPlan(analysis, r, sources, selection)
	plan.Grounded.KnowledgeFingerprint = s.knowledgeFingerprint
	result.Plan = plan
	result.Warnings = warnings
	prior.Interpretations = hypotheses
	prior.Stages.Planner = "ready"
	for len(prior.Interpretations) > 0 {
		raw, _ := json.Marshal(prior.Interpretations)
		if len(raw) <= 4*1024 {
			break
		}
		prior.Interpretations = prior.Interpretations[:len(prior.Interpretations)-1]
		prior.OmittedInterpretations++
	}
	if len(plan.Queries) == 0 {
		return finish(ReferenceStatusUnresolved, ReasonPlanEmpty)
	}
	searchCtx, searchStop := s.stageContext(ctx)
	snapshot, searchErr := s.base.deps.Retriever.Search(searchCtx, plan)
	searchCtxErr := searchCtx.Err()
	searchStop()
	if searchErr == nil {
		searchErr = searchCtxErr
	}
	if searchErr != nil {
		prior.Stages.Expansion = "error"
		return fail(StageRetrieval, referenceFailureCode(searchErr, ReasonRetrievalFailed), searchErr)
	}
	raw := snapshot.RawQueries
	if raw == nil {
		raw = snapshot.Queries
	}
	snapshot, err = FuseRetrieval(plan, raw)
	if err != nil {
		prior.Stages.Expansion = "invalid"
		return fail(StageRetrieval, ReasonContractInvalid, err)
	}
	result.Retrieval = snapshot
	result.Warnings = append(result.Warnings, snapshot.Warnings...)
	if len(snapshot.Candidates) == 0 {
		if snapshot.SuccessfulQueries == 0 {
			prior.Stages.Expansion = "error"
			return fail(StageRetrieval, ReasonRetrievalFailed, errors.New("no successful expansion queries"))
		}
		prior.Stages.Expansion = "empty"
		return finish(ReferenceStatusEmpty, ReasonNoCandidates)
	}
	prior.Stages.Expansion = "ready"
	result.Cards = snapshot.Candidates
	return finish(ReferenceStatusReady, "")
}

// Leave time inside the existing total deadline for the final current-source
// check. A planner/search timeout must not consume that check's entire budget.
func (s *GroundedReferenceService) stageContext(parent context.Context) (context.Context, context.CancelFunc) {
	budget := s.base.options.StageTimeout
	if deadline, ok := parent.Deadline(); ok {
		remaining := time.Until(deadline) - 200*time.Millisecond
		if remaining < budget {
			budget = remaining
		}
	}
	return context.WithTimeout(parent, budget)
}
