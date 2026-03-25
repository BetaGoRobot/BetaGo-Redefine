package agentruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func coalesceString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// RunProjection carries agent runtime state.
type RunProjection struct {
	run   *AgentRun
	steps []*AgentStep
}

// NewRunProjection implements agent runtime behavior.
func NewRunProjection(run *AgentRun, steps []*AgentStep) RunProjection {
	return RunProjection{
		run:   run,
		steps: steps,
	}
}

// CurrentStep implements agent runtime behavior.
func (p RunProjection) CurrentStep() *AgentStep {
	if p.run == nil {
		return nil
	}
	return findStepByIndex(p.steps, p.run.CurrentStepIndex)
}

// PreviousStepBefore implements agent runtime behavior.
func (p RunProjection) PreviousStepBefore(index int) *AgentStep {
	return findPreviousStepBeforeIndex(p.steps, index)
}

// ReplayableCapabilityStepBefore implements agent runtime behavior.
func (p RunProjection) ReplayableCapabilityStepBefore(currentIndex int, source ResumeSource) *AgentStep {
	if source != ResumeSourceApproval {
		return nil
	}
	for i := len(p.steps) - 1; i >= 0; i-- {
		step := p.steps[i]
		if step == nil || step.Index >= currentIndex || step.Kind != StepKindCapabilityCall {
			continue
		}
		switch step.Status {
		case StepStatusQueued, StepStatusRunning:
			return step
		}
	}
	return nil
}

// RootReplyTarget implements agent runtime behavior.
func (p RunProjection) RootReplyTarget() replyTarget {
	target := findRootReplyTargetWithFilter(p.steps, func(step *AgentStep) bool {
		return replyLifecycleState(step) != ReplyLifecycleStateSuperseded
	})
	if target.MessageID != "" || target.CardID != "" {
		return target
	}
	return findRootReplyTargetWithFilter(p.steps, nil)
}

// LatestReplyTarget implements agent runtime behavior.
func (p RunProjection) LatestReplyTarget() replyTarget {
	target := findLatestReplyTargetWithFilter(p.steps, func(step *AgentStep) bool {
		return replyLifecycleState(step) != ReplyLifecycleStateSuperseded
	})
	if target.MessageID != "" || target.CardID != "" {
		return target
	}
	return findLatestReplyTargetWithFilter(p.steps, nil)
}

// LatestReplyTargetBefore implements agent runtime behavior.
func (p RunProjection) LatestReplyTargetBefore(index int) replyTarget {
	target := findLatestReplyTargetBeforeIndexWithFilter(p.steps, index, func(step *AgentStep) bool {
		return replyLifecycleState(step) != ReplyLifecycleStateSuperseded
	})
	if target.MessageID != "" || target.CardID != "" {
		return target
	}
	return findLatestReplyTargetBeforeIndexWithFilter(p.steps, index, nil)
}

// LatestModelReplyTarget implements agent runtime behavior.
func (p RunProjection) LatestModelReplyTarget() replyTarget {
	target := findLatestModelReplyTargetWithFilter(p.steps, func(step *AgentStep) bool {
		return replyLifecycleState(step) != ReplyLifecycleStateSuperseded
	})
	if target.MessageID != "" || target.CardID != "" {
		return target
	}
	return findLatestModelReplyTargetWithFilter(p.steps, nil)
}

// ContinuationContext implements agent runtime behavior.
func (p RunProjection) ContinuationContext(currentStep *AgentStep, event ResumeEvent) continuationContext {
	ctx := continuationContext{
		Source:        event.Source,
		WaitingReason: event.WaitingReason(),
		ResumeSummary: strings.TrimSpace(event.Summary),
	}
	if len(event.PayloadJSON) > 0 {
		ctx.ResumePayloadJSON = append(json.RawMessage(nil), event.PayloadJSON...)
	}
	if p.run != nil {
		if p.run.WaitingReason != WaitingReasonNone {
			ctx.WaitingReason = p.run.WaitingReason
		}
		ctx.TriggerType = p.run.TriggerType
	}

	if currentStep == nil {
		currentStep = p.CurrentStep()
	}
	if currentStep == nil {
		return ctx
	}

	ctx.ResumeStepID = strings.TrimSpace(currentStep.ID)
	ctx.ResumeStepExternalRef = strings.TrimSpace(currentStep.ExternalRef)
	if previousStep := p.PreviousStepBefore(currentStep.Index); previousStep != nil {
		ctx.PreviousStepKind = previousStep.Kind
		ctx.PreviousStepExternalRef = strings.TrimSpace(previousStep.ExternalRef)
		ctx.PreviousStepTitle = continuationPreviousStepTitle(previousStep)
	}
	if target := p.LatestReplyTargetBefore(currentStep.Index); target.MessageID != "" || target.CardID != "" {
		ctx.LatestReplyMessageID = target.MessageID
		ctx.LatestReplyCardID = target.CardID
	}
	return ctx
}

func findStepByIndex(steps []*AgentStep, index int) *AgentStep {
	for _, step := range steps {
		if step != nil && step.Index == index {
			return step
		}
	}
	return nil
}

func findPreviousStepBeforeIndex(steps []*AgentStep, index int) *AgentStep {
	bestIndex := -1
	var best *AgentStep
	for _, step := range steps {
		if step == nil || step.Index >= index {
			continue
		}
		if step.Index > bestIndex {
			bestIndex = step.Index
			best = step
		}
	}
	return best
}

func findRootReplyTargetWithFilter(steps []*AgentStep, filter func(*AgentStep) bool) replyTarget {
	for i := 0; i < len(steps); i++ {
		step := steps[i]
		if step == nil || step.Kind != StepKindReply {
			continue
		}
		if filter != nil && !filter(step) {
			continue
		}
		target := decodeReplyTarget(step)
		if target.MessageID != "" || target.CardID != "" {
			target.StepID = strings.TrimSpace(step.ID)
			return target
		}
	}
	return replyTarget{}
}

func findLatestReplyTargetWithFilter(steps []*AgentStep, filter func(*AgentStep) bool) replyTarget {
	for i := len(steps) - 1; i >= 0; i-- {
		step := steps[i]
		if step == nil || (step.Kind != StepKindReply && step.Kind != StepKindCapabilityCall) {
			continue
		}
		if filter != nil && !filter(step) {
			continue
		}
		target := decodeReplyLikeTarget(step)
		if target.MessageID != "" || target.CardID != "" {
			target.StepID = strings.TrimSpace(step.ID)
			return target
		}
	}
	return replyTarget{}
}

func findLatestReplyTargetBeforeIndexWithFilter(steps []*AgentStep, index int, filter func(*AgentStep) bool) replyTarget {
	for i := len(steps) - 1; i >= 0; i-- {
		step := steps[i]
		if step == nil || step.Index >= index || (step.Kind != StepKindReply && step.Kind != StepKindCapabilityCall) {
			continue
		}
		if filter != nil && !filter(step) {
			continue
		}
		target := decodeReplyLikeTarget(step)
		if target.MessageID != "" || target.CardID != "" {
			target.StepID = strings.TrimSpace(step.ID)
			return target
		}
	}
	return replyTarget{}
}

func findLatestModelReplyTargetWithFilter(steps []*AgentStep, filter func(*AgentStep) bool) replyTarget {
	for i := len(steps) - 1; i >= 0; i-- {
		step := steps[i]
		if step == nil || step.Kind != StepKindReply {
			continue
		}
		if filter != nil && !filter(step) {
			continue
		}
		target := decodeReplyTarget(step)
		if target.MessageID != "" || target.CardID != "" {
			target.StepID = strings.TrimSpace(step.ID)
			return target
		}
	}
	return replyTarget{}
}

func replyLifecycleState(step *AgentStep) ReplyLifecycleState {
	if step == nil || len(step.OutputJSON) == 0 {
		return ""
	}
	var state struct {
		LifecycleState ReplyLifecycleState `json:"lifecycle_state,omitempty"`
	}
	if err := json.Unmarshal(step.OutputJSON, &state); err != nil {
		return ""
	}
	return state.LifecycleState
}

func decodeReplyTarget(step *AgentStep) replyTarget {
	target := replyTarget{}
	if step == nil {
		return target
	}

	reply := capabilityReply{}
	if err := json.Unmarshal(step.OutputJSON, &reply); err == nil {
		target.MessageID = strings.TrimSpace(reply.ResponseMessageID)
		target.CardID = strings.TrimSpace(reply.ResponseCardID)
	}
	if target.MessageID == "" && target.CardID == "" {
		target.MessageID = strings.TrimSpace(step.ExternalRef)
	}
	return target
}

func decodeCapabilityCompatibleReplyTarget(step *AgentStep) replyTarget {
	target := replyTarget{}
	if step == nil || len(step.OutputJSON) == 0 {
		return target
	}

	var result struct {
		CompatibleReplyMessageID string `json:"compatible_reply_message_id,omitempty"`
		CompatibleReplyKind      string `json:"compatible_reply_kind,omitempty"`
	}
	if err := json.Unmarshal(step.OutputJSON, &result); err != nil {
		return target
	}
	if strings.TrimSpace(result.CompatibleReplyKind) == "" {
		return target
	}
	target.MessageID = strings.TrimSpace(result.CompatibleReplyMessageID)
	return target
}

func decodeReplyLikeTarget(step *AgentStep) replyTarget {
	if target := decodeReplyTarget(step); target.MessageID != "" || target.CardID != "" {
		return target
	}
	return decodeCapabilityCompatibleReplyTarget(step)
}

func (p *ContinuationProcessor) emitCapabilityReply(ctx context.Context, run *AgentRun, plan CapabilityReplyPlan) (ReplyEmissionResult, error) {
	if p == nil || p.replyEmitter == nil || run == nil {
		return ReplyEmissionResult{}, nil
	}
	session, err := p.coordinator.sessionRepo.GetByID(ctx, run.SessionID)
	if err != nil {
		return ReplyEmissionResult{}, err
	}
	target, err := p.resolveModelReplyTarget(ctx, run)
	if err != nil {
		return ReplyEmissionResult{}, err
	}
	replyResult, err := p.replyEmitter.EmitReply(ctx, ReplyEmissionRequest{
		Session:         session,
		Run:             run,
		OutputKind:      AgenticOutputKindModelReply,
		MentionOpenID:   strings.TrimSpace(run.ActorOpenID),
		ThoughtText:     strings.TrimSpace(plan.ThoughtText),
		ReplyText:       strings.TrimSpace(plan.ReplyText),
		TargetMessageID: target.MessageID,
		TargetCardID:    target.CardID,
	})
	if err != nil {
		return ReplyEmissionResult{}, err
	}
	replyResult.TargetStepID = target.StepID
	return replyResult, nil
}

func (p *ContinuationProcessor) emitContinuationReply(ctx context.Context, run *AgentRun, plan continuationPlan) (ReplyEmissionResult, error) {
	if p == nil || p.replyEmitter == nil || run == nil || strings.TrimSpace(plan.ReplyText) == "" {
		return ReplyEmissionResult{}, nil
	}
	session, err := p.coordinator.sessionRepo.GetByID(ctx, run.SessionID)
	if err != nil {
		return ReplyEmissionResult{}, err
	}
	target, err := p.resolveModelReplyTarget(ctx, run)
	if err != nil {
		return ReplyEmissionResult{}, err
	}
	replyResult, err := p.replyEmitter.EmitReply(ctx, ReplyEmissionRequest{
		Session:         session,
		Run:             run,
		OutputKind:      AgenticOutputKindModelReply,
		MentionOpenID:   strings.TrimSpace(run.ActorOpenID),
		ThoughtText:     strings.TrimSpace(plan.ThoughtText),
		ReplyText:       strings.TrimSpace(plan.ReplyText),
		TargetMessageID: target.MessageID,
		TargetCardID:    target.CardID,
	})
	if err != nil {
		return ReplyEmissionResult{}, err
	}
	replyResult.TargetStepID = target.StepID
	return replyResult, nil
}

func (p *ContinuationProcessor) continueQueuedCapabilityTail(
	ctx context.Context,
	run *AgentRun,
	queue []QueuedCapabilityCall,
	queuedAt time.Time,
) error {
	if p == nil || p.coordinator == nil || run == nil || len(queue) == 0 {
		return nil
	}

	nextCall := queue[0]
	if len(queue) > 1 {
		nextCall.Input.QueueTail = mergeQueuedCapabilityQueue(nextCall.Input.QueueTail, queue[1:])
	}

	steps, err := p.coordinator.stepRepo.ListByRun(ctx, run.ID)
	if err != nil {
		return err
	}
	nextStepIndex := nextAvailableStepIndex(steps, run.CurrentStepIndex)
	queuedStep, err := newQueuedCapabilityStep(run.ID, nextStepIndex, nextCall, queuedAt)
	if err != nil {
		return err
	}
	if err := p.coordinator.stepRepo.Append(ctx, queuedStep); err != nil {
		return err
	}

	queuedRun, err := p.coordinator.runRepo.UpdateStatus(ctx, run.ID, run.Revision, func(current *AgentRun) error {
		current.Status = RunStatusQueued
		current.CurrentStepIndex = queuedStep.Index
		current.UpdatedAt = queuedAt
		if current.StartedAt == nil {
			current.StartedAt = &queuedAt
		}
		seedQueuedRunLiveness(current, queuedAt, DefaultRunLeasePolicy())
		return nil
	})
	if err != nil {
		return err
	}
	return p.processQueuedRun(ctx, queuedRun.ID, queuedAt)
}

func (p *ContinuationProcessor) moveRunToRunning(ctx context.Context, run *AgentRun, startedAt time.Time) (*AgentRun, error) {
	if p == nil || p.coordinator == nil {
		return run, nil
	}
	return p.coordinator.moveRunToRunning(ctx, run, startedAt)
}

func (p *ContinuationProcessor) queueReplyPlanStep(
	ctx context.Context,
	run *AgentRun,
	fromStepIndex int,
	thoughtText string,
	replyText string,
	pending *QueuedCapabilityCall,
	plannedAt time.Time,
) (*AgentRun, int, error) {
	if p == nil || p.coordinator == nil {
		return run, 0, nil
	}
	if run == nil {
		return nil, 0, fmt.Errorf("agent runtime run is nil")
	}

	plannedRun, err := p.coordinator.QueuePlanStep(ctx, QueuePlanStepInput{
		RunID:             run.ID,
		Revision:          run.Revision,
		FromStepIndex:     fromStepIndex,
		ThoughtText:       strings.TrimSpace(thoughtText),
		ReplyText:         strings.TrimSpace(replyText),
		PendingCapability: buildPlanPendingCapability(pending),
		PlannedAt:         plannedAt,
	})
	if err != nil {
		return nil, 0, err
	}
	if plannedRun == nil {
		return run, run.CurrentStepIndex, nil
	}
	return plannedRun, plannedRun.CurrentStepIndex, nil
}

func (p *ContinuationProcessor) completeQueuedReplyPlan(
	ctx context.Context,
	run *AgentRun,
	planStepIndex int,
	thoughtText string,
	replyText string,
	replyRefs ReplyEmissionResult,
	resultSummary string,
	completedAt time.Time,
) error {
	if p == nil || p.coordinator == nil {
		return nil
	}
	if run == nil {
		return fmt.Errorf("agent runtime run is nil")
	}

	completedRun, err := p.coordinator.CompleteRunWithReply(ctx, CompleteRunWithReplyInput{
		RunID:             run.ID,
		Revision:          run.Revision,
		ThoughtText:       strings.TrimSpace(thoughtText),
		ReplyText:         strings.TrimSpace(replyText),
		ResponseMessageID: strings.TrimSpace(replyRefs.MessageID),
		ResponseCardID:    strings.TrimSpace(replyRefs.CardID),
		DeliveryMode:      replyRefs.DeliveryMode,
		TargetMessageID:   strings.TrimSpace(replyRefs.TargetMessageID),
		TargetCardID:      strings.TrimSpace(replyRefs.TargetCardID),
		TargetStepID:      strings.TrimSpace(replyRefs.TargetStepID),
		CompletedAt:       completedAt,
	})
	if err != nil {
		return err
	}
	completedRun, err = p.overrideRunResultSummary(ctx, completedRun, resultSummary, completedAt)
	if err != nil {
		return err
	}
	if err := p.linkReplyPlanStep(ctx, completedRun.ID, planStepIndex+1, replyRefs); err != nil {
		return err
	}
	return nil
}

func (p *ContinuationProcessor) continueQueuedReplyPlan(
	ctx context.Context,
	run *AgentRun,
	planStepIndex int,
	thoughtText string,
	replyText string,
	queuedCapability QueuedCapabilityCall,
	replyRefs ReplyEmissionResult,
	resultSummary string,
	continuedAt time.Time,
) error {
	if p == nil || p.coordinator == nil {
		return nil
	}
	if run == nil {
		return fmt.Errorf("agent runtime run is nil")
	}

	continuedRun, err := p.coordinator.ContinueRunWithReply(ctx, ContinueRunWithReplyInput{
		RunID:             run.ID,
		Revision:          run.Revision,
		ThoughtText:       strings.TrimSpace(thoughtText),
		ReplyText:         strings.TrimSpace(replyText),
		ResponseMessageID: strings.TrimSpace(replyRefs.MessageID),
		ResponseCardID:    strings.TrimSpace(replyRefs.CardID),
		DeliveryMode:      replyRefs.DeliveryMode,
		TargetMessageID:   strings.TrimSpace(replyRefs.TargetMessageID),
		TargetCardID:      strings.TrimSpace(replyRefs.TargetCardID),
		TargetStepID:      strings.TrimSpace(replyRefs.TargetStepID),
		QueuedCapability:  &queuedCapability,
		ContinuedAt:       continuedAt,
	})
	if err != nil {
		return err
	}
	continuedRun, err = p.overrideRunResultSummary(ctx, continuedRun, resultSummary, continuedAt)
	if err != nil {
		return err
	}
	return p.linkReplyPlanStep(ctx, continuedRun.ID, planStepIndex+1, replyRefs)
}

func (p *ContinuationProcessor) overrideRunResultSummary(ctx context.Context, run *AgentRun, resultSummary string, updatedAt time.Time) (*AgentRun, error) {
	if p == nil || p.coordinator == nil || run == nil {
		return run, nil
	}
	resultSummary = strings.TrimSpace(resultSummary)
	if resultSummary == "" || strings.TrimSpace(run.ResultSummary) == resultSummary {
		return run, nil
	}
	return p.coordinator.runRepo.UpdateStatus(ctx, run.ID, run.Revision, func(current *AgentRun) error {
		current.ResultSummary = resultSummary
		current.UpdatedAt = updatedAt
		return nil
	})
}

func (p *ContinuationProcessor) linkReplyPlanStep(ctx context.Context, runID string, replyStepIndex int, refs ReplyEmissionResult) error {
	if p == nil || p.coordinator == nil {
		return nil
	}
	if strings.TrimSpace(refs.TargetStepID) == "" {
		return nil
	}

	steps, err := p.coordinator.stepRepo.ListByRun(ctx, strings.TrimSpace(runID))
	if err != nil {
		return err
	}
	replyStep := findStepByIndex(steps, replyStepIndex)
	if replyStep == nil {
		return nil
	}
	return p.linkSupersededReplyStep(ctx, refs, replyStep.ID)
}

func (p *ContinuationProcessor) clearActiveRunSlot(ctx context.Context, run *AgentRun, updatedAt time.Time) error {
	if p == nil || p.coordinator == nil || run == nil || strings.TrimSpace(run.SessionID) == "" {
		return nil
	}

	session, err := p.coordinator.sessionRepo.GetByID(ctx, run.SessionID)
	if err != nil {
		return err
	}
	if session != nil && session.ActiveRunID == run.ID {
		if err := p.coordinator.sessionRepo.SetActiveRun(ctx, session.ID, "", "", "", updatedAt); err != nil {
			return err
		}
	}

	if p.coordinator.runtimeStore == nil || session == nil || strings.TrimSpace(session.ChatID) == "" {
		return nil
	}

	swapped, err := p.coordinator.runtimeStore.SwapActiveActorChatRun(ctx, session.ChatID, run.ActorOpenID, run.ID, "", p.coordinator.activeRunTTL)
	if err != nil {
		return err
	}
	if swapped {
		_ = p.coordinator.runtimeStore.NotifyPendingInitialRun(ctx, session.ChatID, run.ActorOpenID)
		TriggerPendingScopeSweep()
		return nil
	}

	current, err := p.coordinator.runtimeStore.ActiveActorChatRun(ctx, session.ChatID, run.ActorOpenID)
	if err != nil {
		return err
	}
	if current == run.ID {
		return fmt.Errorf("active actor chat slot still points to completed run: chat_id=%s actor_open_id=%s run_id=%s", session.ChatID, run.ActorOpenID, run.ID)
	}
	_ = p.coordinator.runtimeStore.NotifyPendingInitialRun(ctx, session.ChatID, run.ActorOpenID)
	TriggerPendingScopeSweep()
	return nil
}
