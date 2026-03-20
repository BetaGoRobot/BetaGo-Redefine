package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type ContinuationProcessor struct {
	coordinator                   *RunCoordinator
	registry                      *CapabilityRegistry
	capabilityReplyTurnExecutor   CapabilityReplyTurnExecutor
	continuationReplyTurnExecutor ContinuationReplyTurnExecutor
	capabilityReplyPlanner        CapabilityReplyPlanner
	initialReplyEmitter           InitialReplyEmitter
	replyEmitter                  ReplyEmitter
	approvalSender                ApprovalSender
}

type continuationObservation struct {
	Source                  ResumeSource    `json:"source"`
	WaitingReason           WaitingReason   `json:"waiting_reason,omitempty"`
	TriggerType             TriggerType     `json:"trigger_type,omitempty"`
	ResumeStepID            string          `json:"resume_step_id,omitempty"`
	ResumeStepExternalRef   string          `json:"resume_step_external_ref,omitempty"`
	PreviousStepKind        StepKind        `json:"previous_step_kind,omitempty"`
	PreviousStepExternalRef string          `json:"previous_step_external_ref,omitempty"`
	PreviousStepTitle       string          `json:"previous_step_title,omitempty"`
	Summary                 string          `json:"summary,omitempty"`
	PayloadJSON             json.RawMessage `json:"payload_json,omitempty"`
	ActorOpenID             string          `json:"actor_open_id,omitempty"`
	OccurredAt              time.Time       `json:"occurred_at"`
}

type capabilityObservation struct {
	CapabilityName string          `json:"capability_name,omitempty"`
	OutputText     string          `json:"output_text,omitempty"`
	OutputJSON     json.RawMessage `json:"output_json,omitempty"`
	ExternalRef    string          `json:"external_ref,omitempty"`
	OccurredAt     time.Time       `json:"occurred_at"`
}

type capabilityReply struct {
	CapabilityName     string              `json:"capability_name,omitempty"`
	ThoughtText        string              `json:"thought_text,omitempty"`
	ReplyText          string              `json:"reply_text,omitempty"`
	Text               string              `json:"text,omitempty"`
	ResponseMessageID  string              `json:"response_message_id,omitempty"`
	ResponseCardID     string              `json:"response_card_id,omitempty"`
	DeliveryMode       ReplyDeliveryMode   `json:"delivery_mode,omitempty"`
	LifecycleState     ReplyLifecycleState `json:"lifecycle_state,omitempty"`
	TargetMessageID    string              `json:"target_message_id,omitempty"`
	TargetCardID       string              `json:"target_card_id,omitempty"`
	TargetStepID       string              `json:"target_step_id,omitempty"`
	PatchedByStepID    string              `json:"patched_by_step_id,omitempty"`
	SupersededByStepID string              `json:"superseded_by_step_id,omitempty"`
}

type continuationPlan struct {
	ObservedAt    time.Time
	Context       continuationContext
	ResultSummary string
	ThoughtText   string
	ReplyText     string
	ObserveStep   AgentStep
	ResumeOutput  []byte
}

type continuationContext struct {
	Source                  ResumeSource
	WaitingReason           WaitingReason
	TriggerType             TriggerType
	ResumeStepID            string
	ResumeStepExternalRef   string
	PreviousStepKind        StepKind
	PreviousStepExternalRef string
	PreviousStepTitle       string
	ResumeSummary           string
	ResumePayloadJSON       json.RawMessage
	LatestReplyMessageID    string
	LatestReplyCardID       string
}

type replyTarget struct {
	MessageID string
	CardID    string
	StepID    string
}

func NewContinuationProcessor(coordinator *RunCoordinator, opts ...ContinuationProcessorOption) *ContinuationProcessor {
	processor := &ContinuationProcessor{coordinator: coordinator}
	for _, opt := range opts {
		if opt != nil {
			opt(processor)
		}
	}
	return processor
}

func WithApprovalSender(sender ApprovalSender) ContinuationProcessorOption {
	return func(p *ContinuationProcessor) {
		if p != nil {
			p.approvalSender = sender
		}
	}
}

func (p *ContinuationProcessor) ProcessResume(ctx context.Context, event ResumeEvent) (err error) {
	if p == nil || p.coordinator == nil {
		return nil
	}
	defer func() {
		if err == nil {
			return
		}
		if failErr := p.failRunIfStillRunning(ctx, strings.TrimSpace(event.RunID), err); failErr != nil {
			err = errors.Join(err, failErr)
		}
	}()
	if err := event.Validate(); err != nil {
		return err
	}

	run, err := p.ensureQueuedRun(ctx, event)
	if err != nil {
		return err
	}
	if run == nil || run.Status.IsTerminal() {
		return nil
	}

	steps, err := p.loadSteps(ctx, run.ID)
	if err != nil {
		return err
	}
	currentStep := findStepByIndex(steps, run.CurrentStepIndex)
	if currentStep != nil && currentStep.Kind == StepKindResume {
		if capabilityStep := findReplayableCapabilityStep(steps, currentStep.Index, event.Source); capabilityStep != nil {
			return p.processCapabilityResume(ctx, run, currentStep, capabilityStep, event)
		}
	}
	if currentStep != nil && currentStep.Kind == StepKindCapabilityCall {
		return p.processCapabilityCall(ctx, run, currentStep, event)
	}

	plan, err := p.plan(run, steps, currentStep, event)
	if err != nil {
		return err
	}
	return p.execute(ctx, run, plan)
}

func (p *ContinuationProcessor) failRunIfStillRunning(ctx context.Context, runID string, cause error) error {
	if p == nil || p.coordinator == nil || strings.TrimSpace(runID) == "" || cause == nil {
		return nil
	}

	run, err := p.coordinator.runRepo.GetByID(ctx, runID)
	if err != nil || run == nil || run.Status != RunStatusRunning {
		return err
	}

	failedAt := time.Now().UTC()
	failedRun, err := p.coordinator.runRepo.UpdateStatus(ctx, run.ID, run.Revision, func(current *AgentRun) error {
		current.Status = RunStatusFailed
		current.ErrorText = strings.TrimSpace(cause.Error())
		current.UpdatedAt = failedAt
		current.FinishedAt = &failedAt
		if current.StartedAt == nil {
			current.StartedAt = &failedAt
		}
		return nil
	})
	if err != nil {
		return err
	}

	return p.clearActiveRunSlot(ctx, failedRun, failedAt)
}

func (p *ContinuationProcessor) loadSteps(ctx context.Context, runID string) ([]*AgentStep, error) {
	return p.coordinator.stepRepo.ListByRun(ctx, runID)
}

func (p *ContinuationProcessor) loadCurrentStep(ctx context.Context, runID string, index int) (*AgentStep, error) {
	steps, err := p.loadSteps(ctx, runID)
	if err != nil {
		return nil, err
	}
	return findStepByIndex(steps, index), nil
}

func (p *ContinuationProcessor) ensureQueuedRun(ctx context.Context, event ResumeEvent) (*AgentRun, error) {
	run, err := p.coordinator.runRepo.GetByID(ctx, strings.TrimSpace(event.RunID))
	if err != nil {
		return nil, err
	}
	if run.Status.IsTerminal() || run.Status == RunStatusQueued || run.Status == RunStatusRunning {
		return run, nil
	}
	if run.Status != waitingRunStatusForResumeSource(event.Source) {
		return nil, fmt.Errorf("%w: run status=%s source=%s", ErrResumeStateConflict, run.Status, event.Source)
	}
	return p.coordinator.ResumeRun(ctx, event)
}

func (p *ContinuationProcessor) plan(run *AgentRun, steps []*AgentStep, currentStep *AgentStep, event ResumeEvent) (continuationPlan, error) {
	observedAt := event.OccurredAt.UTC()
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}

	ctx := buildContinuationContext(run, steps, currentStep, event)
	payload, err := json.Marshal(continuationObservation{
		Source:                  event.Source,
		WaitingReason:           ctx.WaitingReason,
		TriggerType:             ctx.TriggerType,
		ResumeStepID:            ctx.ResumeStepID,
		ResumeStepExternalRef:   ctx.ResumeStepExternalRef,
		PreviousStepKind:        ctx.PreviousStepKind,
		PreviousStepExternalRef: ctx.PreviousStepExternalRef,
		PreviousStepTitle:       ctx.PreviousStepTitle,
		Summary:                 ctx.ResumeSummary,
		PayloadJSON:             append(json.RawMessage(nil), ctx.ResumePayloadJSON...),
		ActorOpenID:             strings.TrimSpace(event.ActorOpenID),
		OccurredAt:              observedAt,
	})
	if err != nil {
		return continuationPlan{}, err
	}

	nextIndex := run.CurrentStepIndex + 1
	if currentStep != nil && currentStep.Index >= 0 {
		nextIndex = currentStep.Index + 1
	}
	replyText := resolveContinuationReplyText(event.Source, ctx)
	return continuationPlan{
		ObservedAt:    observedAt,
		Context:       ctx,
		ResultSummary: resolveContinuationResultSummary(event.Source, ctx),
		ThoughtText:   resolveContinuationThoughtText(run, ctx),
		ReplyText:     replyText,
		ResumeOutput:  payload,
		ObserveStep: AgentStep{
			ID:          newRuntimeID("step"),
			RunID:       run.ID,
			Index:       nextIndex,
			Kind:        StepKindObserve,
			Status:      StepStatusCompleted,
			OutputJSON:  payload,
			ExternalRef: fmt.Sprintf("%s:%s", event.Source, event.ExternalRef()),
			CreatedAt:   observedAt,
			StartedAt:   &observedAt,
			FinishedAt:  &observedAt,
		},
	}, nil
}

func resolveContinuationReplyText(source ResumeSource, ctx continuationContext) string {
	if source == ResumeSourceApproval {
		if title := strings.TrimSpace(ctx.PreviousStepTitle); title != "" {
			if ctx.hasReplyTarget() {
				return fmt.Sprintf("审批「%s」通过了，我已经把原消息更新好了。", title)
			}
			return fmt.Sprintf("审批「%s」通过了，我已经继续处理好了。", title)
		}
	}
	if title := strings.TrimSpace(ctx.PreviousStepTitle); title != "" && ctx.PreviousStepKind == StepKindWait {
		if ctx.hasReplyTarget() {
			switch source {
			case ResumeSourceCallback:
				return fmt.Sprintf("收到回调「%s」了，我已经把原消息更新好了。", title)
			case ResumeSourceSchedule:
				return fmt.Sprintf("定时任务「%s」跑完了，我已经把原消息更新好了。", title)
			}
		}
		switch source {
		case ResumeSourceCallback:
			return fmt.Sprintf("收到回调「%s」了，我已经继续处理好了。", title)
		case ResumeSourceSchedule:
			return fmt.Sprintf("定时任务「%s」跑完了，我已经继续处理好了。", title)
		}
	}
	if ctx.hasReplyTarget() {
		switch source {
		case ResumeSourceCallback:
			return "收到回调了，我已经把原消息更新好了。"
		case ResumeSourceSchedule:
			return "定时任务跑完了，我已经把原消息更新好了。"
		case ResumeSourceApproval:
			return "审批通过了，我已经把原消息更新好了。"
		}
	}
	switch source {
	case ResumeSourceCallback:
		return "收到回调了，我已经继续处理好了。"
	case ResumeSourceSchedule:
		return "定时任务跑完了，我已经继续处理好了。"
	case ResumeSourceApproval:
		return "审批通过了，我已经继续处理好了。"
	default:
		return "我已经继续处理好了。"
	}
}

func buildContinuationContext(run *AgentRun, steps []*AgentStep, currentStep *AgentStep, event ResumeEvent) continuationContext {
	ctx := continuationContext{
		Source:        event.Source,
		WaitingReason: event.WaitingReason(),
		ResumeSummary: strings.TrimSpace(event.Summary),
	}
	if len(event.PayloadJSON) > 0 {
		ctx.ResumePayloadJSON = append(json.RawMessage(nil), event.PayloadJSON...)
	}
	if run != nil {
		if run.WaitingReason != WaitingReasonNone {
			ctx.WaitingReason = run.WaitingReason
		}
		ctx.TriggerType = run.TriggerType
	}

	if currentStep == nil && run != nil {
		currentStep = findStepByIndex(steps, run.CurrentStepIndex)
	}
	if currentStep == nil {
		return ctx
	}

	ctx.ResumeStepID = strings.TrimSpace(currentStep.ID)
	ctx.ResumeStepExternalRef = strings.TrimSpace(currentStep.ExternalRef)
	if previousStep := findPreviousStepBeforeIndex(steps, currentStep.Index); previousStep != nil {
		ctx.PreviousStepKind = previousStep.Kind
		ctx.PreviousStepExternalRef = strings.TrimSpace(previousStep.ExternalRef)
		ctx.PreviousStepTitle = continuationPreviousStepTitle(previousStep)
	}
	if target := findLatestReplyTargetBeforeIndex(steps, currentStep.Index); target.MessageID != "" || target.CardID != "" {
		ctx.LatestReplyMessageID = target.MessageID
		ctx.LatestReplyCardID = target.CardID
	}
	return ctx
}

func resolveContinuationThoughtText(run *AgentRun, ctx continuationContext) string {
	parts := make([]string, 0, 4)
	if label := continuationSourceLabel(ctx.Source); label != "" {
		parts = append(parts, "恢复来源："+label)
	}
	if label := continuationStepKindLabel(ctx.PreviousStepKind); label != "" {
		parts = append(parts, "前置步骤："+label)
	}
	if title := strings.TrimSpace(ctx.PreviousStepTitle); title != "" {
		parts = append(parts, continuationPreviousStepTitleLabel(ctx.PreviousStepKind)+title)
	}
	if summary := strings.TrimSpace(ctx.ResumeSummary); summary != "" {
		parts = append(parts, "恢复摘要："+summary)
	}
	if preview := continuationRunPreview(run); preview != "" {
		parts = append(parts, "请求上下文："+preview)
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "；") + "。"
}

func continuationSourceLabel(source ResumeSource) string {
	switch source {
	case ResumeSourceCallback:
		return "回调"
	case ResumeSourceSchedule:
		return "定时任务"
	case ResumeSourceApproval:
		return "审批"
	default:
		return strings.TrimSpace(string(source))
	}
}

func continuationStepKindLabel(kind StepKind) string {
	switch kind {
	case StepKindWait:
		return "等待"
	case StepKindCapabilityCall:
		return "能力调用"
	case StepKindApprovalRequest:
		return "审批请求"
	case StepKindObserve:
		return "观察"
	case StepKindPlan:
		return "规划"
	default:
		return ""
	}
}

func continuationRunPreview(run *AgentRun) string {
	if run == nil {
		return ""
	}

	preview := strings.TrimSpace(run.Goal)
	if preview == "" {
		preview = strings.TrimSpace(run.InputText)
	}
	preview = strings.Join(strings.Fields(preview), " ")
	if preview == "" {
		return ""
	}

	runes := []rune(preview)
	if len(runes) <= 48 {
		return preview
	}
	return string(runes[:48]) + "..."
}

func continuationPreviousStepTitle(step *AgentStep) string {
	if step == nil {
		return ""
	}
	switch step.Kind {
	case StepKindApprovalRequest:
		state, err := unmarshalApprovalStepState(step.OutputJSON)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(state.Title)
	case StepKindWait:
		var state struct {
			Title string `json:"title,omitempty"`
		}
		if len(step.OutputJSON) == 0 {
			return ""
		}
		if err := json.Unmarshal(step.OutputJSON, &state); err != nil {
			return ""
		}
		return strings.TrimSpace(state.Title)
	default:
		return ""
	}
}

func continuationPreviousStepTitleLabel(kind StepKind) string {
	switch kind {
	case StepKindApprovalRequest:
		return "审批事项："
	case StepKindWait:
		return "等待事项："
	default:
		return "步骤事项："
	}
}

func resolveContinuationResultSummary(source ResumeSource, ctx continuationContext) string {
	if summary := strings.TrimSpace(ctx.ResumeSummary); summary != "" {
		return summary
	}
	summary := fmt.Sprintf("agent runtime continuation processed via %s", source)
	if ctx.PreviousStepKind != "" {
		summary += fmt.Sprintf(" after %s", ctx.PreviousStepKind)
	}
	return summary
}

func (c continuationContext) hasReplyTarget() bool {
	return strings.TrimSpace(c.LatestReplyMessageID) != "" || strings.TrimSpace(c.LatestReplyCardID) != ""
}

func (p *ContinuationProcessor) processCapabilityCall(ctx context.Context, run *AgentRun, step *AgentStep, event ResumeEvent) error {
	if p == nil || p.coordinator == nil {
		return nil
	}
	if step == nil {
		return fmt.Errorf("agent runtime capability step missing: run_id=%s index=%d", run.ID, run.CurrentStepIndex)
	}
	if p.registry == nil {
		return fmt.Errorf("agent runtime capability registry is nil")
	}

	observedAt := event.OccurredAt.UTC()
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}

	input, err := decodeCapabilityCallInput(step.InputJSON)
	if err != nil {
		return err
	}
	input.Request = hydrateCapabilityRequest(run, step, input.Request)

	capability, meta, err := p.lookupCapability(step.CapabilityName, input.Request.Scope)
	if err != nil {
		return err
	}

	currentRun, err := p.moveRunToRunning(ctx, run, observedAt)
	if err != nil {
		return err
	}
	if meta.RequiresApproval || input.Approval != nil {
		return p.requestCapabilityApproval(ctx, currentRun, step, meta, input, observedAt)
	}

	return p.executeCapabilityCall(ctx, currentRun, step, capability, input, meta, step.Index+1, observedAt)
}

func (p *ContinuationProcessor) processCapabilityResume(
	ctx context.Context,
	run *AgentRun,
	resumeStep *AgentStep,
	capabilityStep *AgentStep,
	event ResumeEvent,
) error {
	if resumeStep == nil || capabilityStep == nil {
		return fmt.Errorf("agent runtime capability replay steps missing: run_id=%s", run.ID)
	}

	currentRun, err := p.moveRunToRunning(ctx, run, normalizeObservedAt(event.OccurredAt))
	if err != nil {
		return err
	}

	plan, err := p.plan(currentRun, []*AgentStep{capabilityStep, resumeStep}, resumeStep, event)
	if err != nil {
		return err
	}
	if err := p.completeResumeStep(ctx, currentRun, plan); err != nil {
		return err
	}

	input, err := decodeCapabilityCallInput(capabilityStep.InputJSON)
	if err != nil {
		return err
	}
	input.Request = hydrateCapabilityRequest(currentRun, capabilityStep, input.Request)

	capability, meta, err := p.lookupCapability(capabilityStep.CapabilityName, input.Request.Scope)
	if err != nil {
		return err
	}

	return p.executeCapabilityCall(ctx, currentRun, capabilityStep, capability, input, meta, resumeStep.Index+1, plan.ObservedAt)
}

func (p *ContinuationProcessor) lookupCapability(name string, scope CapabilityScope) (Capability, CapabilityMeta, error) {
	if p == nil || p.registry == nil {
		return nil, CapabilityMeta{}, fmt.Errorf("capability registry is nil")
	}

	name = strings.TrimSpace(name)
	if name == "" {
		return nil, CapabilityMeta{}, fmt.Errorf("capability name is empty")
	}
	if strings.TrimSpace(string(scope)) == "" {
		capability, ok := p.registry.Get(name)
		if !ok {
			return nil, CapabilityMeta{}, fmt.Errorf("capability not found: %s", name)
		}
		return capability, capability.Meta(), nil
	}

	capability, err := p.registry.Lookup(name, scope)
	if err != nil {
		return nil, CapabilityMeta{}, err
	}
	return capability, capability.Meta(), nil
}

func hydrateCapabilityRequest(run *AgentRun, step *AgentStep, req CapabilityRequest) CapabilityRequest {
	if run == nil {
		return req
	}
	if strings.TrimSpace(req.SessionID) == "" {
		req.SessionID = run.SessionID
	}
	if strings.TrimSpace(req.RunID) == "" {
		req.RunID = run.ID
	}
	if step != nil && strings.TrimSpace(req.StepID) == "" {
		req.StepID = step.ID
	}
	if strings.TrimSpace(req.ActorOpenID) == "" {
		req.ActorOpenID = run.ActorOpenID
	}
	return req
}

func (p *ContinuationProcessor) requestCapabilityApproval(
	ctx context.Context,
	run *AgentRun,
	step *AgentStep,
	meta CapabilityMeta,
	input CapabilityCallInput,
	requestedAt time.Time,
) error {
	spec := resolveCapabilityApprovalSpec(step, meta, input, requestedAt)
	request, err := p.coordinator.RequestApproval(ctx, RequestApprovalInput{
		RunID:          run.ID,
		ApprovalType:   spec.Type,
		Title:          spec.Title,
		Summary:        spec.Summary,
		CapabilityName: strings.TrimSpace(step.CapabilityName),
		PayloadJSON:    append([]byte(nil), step.InputJSON...),
		ExpiresAt:      spec.ExpiresAt,
		RequestedAt:    requestedAt,
	})
	if err != nil {
		return err
	}
	if p.approvalSender == nil {
		return nil
	}

	session, err := p.coordinator.sessionRepo.GetByID(ctx, run.SessionID)
	if err != nil {
		return err
	}
	target := ApprovalCardTarget{
		ReplyToMessageID: strings.TrimSpace(run.TriggerMessageID),
		VisibleOpenID:    strings.TrimSpace(run.ActorOpenID),
	}
	if session != nil {
		target.ChatID = strings.TrimSpace(session.ChatID)
	}

	return p.approvalSender.SendApprovalCard(ctx, target, *request)
}

func (p *ContinuationProcessor) executeCapabilityCall(
	ctx context.Context,
	run *AgentRun,
	step *AgentStep,
	capability Capability,
	input CapabilityCallInput,
	meta CapabilityMeta,
	nextIndex int,
	observedAt time.Time,
) error {
	request := input.Request
	activeStep, err := p.startCapabilityStep(ctx, step, observedAt)
	if err != nil {
		return err
	}

	result, execErr := p.invokeCapability(ctx, capability, meta, request)
	if execErr != nil {
		return p.failCapabilityRun(ctx, run, activeStep, execErr, observedAt)
	}

	if _, err := p.coordinator.stepRepo.UpdateStatus(ctx, activeStep.ID, activeStep.Status, func(current *AgentStep) error {
		current.Status = StepStatusCompleted
		current.OutputJSON = encodeCapabilityResult(result)
		current.ExternalRef = strings.TrimSpace(result.ExternalRef)
		current.FinishedAt = &observedAt
		if current.StartedAt == nil {
			current.StartedAt = &observedAt
		}
		return nil
	}); err != nil {
		return err
	}

	observeStep, err := newCapabilityObserveStep(run.ID, nextIndex, step.CapabilityName, result, observedAt)
	if err != nil {
		return err
	}
	if err := p.coordinator.stepRepo.Append(ctx, observeStep); err != nil {
		return err
	}
	if len(input.QueueTail) > 0 {
		return p.continueQueuedCapabilityTail(ctx, run, input.QueueTail, observedAt)
	}

	turnResult, handled, err := p.executeCapabilityReplyTurn(ctx, run, step, input, result, observedAt, nextIndex+1)
	if err != nil {
		return err
	}
	if handled {
		return p.completeCapabilityReplyTurn(ctx, run, input, step, nextIndex, observedAt, result, turnResult)
	}

	replyPlan, err := p.planCapabilityReply(ctx, run, step, input, result)
	if err != nil {
		return err
	}
	plannedRun, planStepIndex, err := p.queueReplyPlanStep(ctx, run, nextIndex, replyPlan.ThoughtText, replyPlan.ReplyText, nil, observedAt)
	if err != nil {
		return err
	}
	replyRefs, err := p.emitCapabilityReply(ctx, plannedRun, replyPlan)
	if err != nil {
		return err
	}

	resultSummary := strings.TrimSpace(replyPlan.ReplyText)
	if resultSummary == "" {
		resultSummary = resolveCapabilityResultSummary(step.CapabilityName, result)
	}
	return p.completeQueuedReplyPlan(ctx, plannedRun, planStepIndex, replyPlan.ThoughtText, replyPlan.ReplyText, replyRefs, resultSummary, observedAt)
}

func (p *ContinuationProcessor) executeCapabilityReplyTurn(
	ctx context.Context,
	run *AgentRun,
	step *AgentStep,
	input CapabilityCallInput,
	result CapabilityResult,
	recordedAt time.Time,
	nextIndex int,
) (CapabilityReplyTurnResult, bool, error) {
	if p == nil || p.capabilityReplyTurnExecutor == nil {
		return CapabilityReplyTurnResult{}, false, nil
	}

	session, err := p.coordinator.sessionRepo.GetByID(ctx, run.SessionID)
	if err != nil {
		return CapabilityReplyTurnResult{}, false, err
	}
	recorder := newRunCapabilityTraceRecorder(p.coordinator, run.ID, nextIndex, recordedAt)
	turnResult, err := p.capabilityReplyTurnExecutor.ExecuteCapabilityReplyTurn(ctx, CapabilityReplyTurnRequest{
		Session:      session,
		Run:          run,
		Step:         step,
		Input:        input,
		Result:       result,
		Recorder:     recorder,
		PlanRecorder: recorder,
	})
	if err != nil {
		return CapabilityReplyTurnResult{}, false, err
	}
	if !turnResult.Executed {
		return CapabilityReplyTurnResult{}, false, nil
	}

	fallback := resolveCapabilityResultSummary(strings.TrimSpace(step.CapabilityName), result)
	turnResult.Plan = normalizeCapabilityReplyPlan(turnResult.Plan, fallback)
	turnResult.PendingCapability = hydrateQueuedCapabilityCall(turnResult.PendingCapability, run, input.Request)
	return turnResult, true, nil
}

func (p *ContinuationProcessor) completeCapabilityReplyTurn(
	ctx context.Context,
	run *AgentRun,
	input CapabilityCallInput,
	step *AgentStep,
	nextIndex int,
	observedAt time.Time,
	result CapabilityResult,
	turnResult CapabilityReplyTurnResult,
) error {
	steps, err := p.coordinator.stepRepo.ListByRun(ctx, run.ID)
	if err != nil {
		return err
	}
	currentIndex := nextAvailableStepIndex(steps, nextIndex)
	for _, call := range turnResult.CapabilityCalls {
		capabilityStep, err := newCompletedCapabilityStep(run.ID, currentIndex, call, observedAt)
		if err != nil {
			return err
		}
		if err := p.coordinator.stepRepo.Append(ctx, capabilityStep); err != nil {
			return err
		}
		currentIndex++

		observeStep, err := newCompletedCapabilityObserveStep(run.ID, currentIndex, call, observedAt)
		if err != nil {
			return err
		}
		if err := p.coordinator.stepRepo.Append(ctx, observeStep); err != nil {
			return err
		}
		currentIndex++
	}

	resultSummary := strings.TrimSpace(turnResult.Plan.ReplyText)
	if resultSummary == "" {
		resultSummary = resolveCapabilityResultSummary(step.CapabilityName, result)
	}

	plannedRun, planStepIndex, err := p.queueReplyPlanStep(ctx, run, currentIndex-1, turnResult.Plan.ThoughtText, turnResult.Plan.ReplyText, turnResult.PendingCapability, observedAt)
	if err != nil {
		return err
	}
	replyRefs, err := p.emitCapabilityReply(ctx, plannedRun, turnResult.Plan)
	if err != nil {
		return err
	}

	if turnResult.PendingCapability != nil {
		return p.continueQueuedReplyPlan(ctx, plannedRun, planStepIndex, turnResult.Plan.ThoughtText, turnResult.Plan.ReplyText, *turnResult.PendingCapability, replyRefs, resultSummary, observedAt)
	}

	return p.completeQueuedReplyPlan(ctx, plannedRun, planStepIndex, turnResult.Plan.ThoughtText, turnResult.Plan.ReplyText, replyRefs, resultSummary, observedAt)
}

func (p *ContinuationProcessor) startCapabilityStep(ctx context.Context, step *AgentStep, startedAt time.Time) (*AgentStep, error) {
	if step == nil {
		return nil, fmt.Errorf("capability step is nil")
	}

	switch step.Status {
	case StepStatusRunning:
		if step.StartedAt == nil {
			step.StartedAt = &startedAt
		}
		return step, nil
	case StepStatusQueued:
		return p.coordinator.stepRepo.UpdateStatus(ctx, step.ID, StepStatusQueued, func(current *AgentStep) error {
			current.Status = StepStatusRunning
			current.StartedAt = &startedAt
			return nil
		})
	default:
		return nil, fmt.Errorf("agent runtime capability step not executable: run_id=%s step_id=%s status=%s", step.RunID, step.ID, step.Status)
	}
}

func (p *ContinuationProcessor) invokeCapability(
	ctx context.Context,
	capability Capability,
	meta CapabilityMeta,
	request CapabilityRequest,
) (CapabilityResult, error) {
	if capability == nil {
		return CapabilityResult{}, fmt.Errorf("capability is nil")
	}
	if meta.DefaultTimeout <= 0 {
		return capability.Execute(ctx, request)
	}

	return capability.Execute(ctx, request)
}

func (p *ContinuationProcessor) failCapabilityRun(
	ctx context.Context,
	run *AgentRun,
	step *AgentStep,
	execErr error,
	failedAt time.Time,
) error {
	if step != nil && step.Status == StepStatusRunning {
		if _, err := p.coordinator.stepRepo.UpdateStatus(ctx, step.ID, StepStatusRunning, func(current *AgentStep) error {
			current.Status = StepStatusFailed
			current.ErrorText = strings.TrimSpace(execErr.Error())
			current.FinishedAt = &failedAt
			return nil
		}); err != nil {
			return err
		}
	}

	failedRun, err := p.coordinator.runRepo.UpdateStatus(ctx, run.ID, run.Revision, func(current *AgentRun) error {
		current.Status = RunStatusFailed
		current.ErrorText = strings.TrimSpace(execErr.Error())
		current.UpdatedAt = failedAt
		current.FinishedAt = &failedAt
		if current.StartedAt == nil {
			current.StartedAt = &failedAt
		}
		return nil
	})
	if err != nil {
		return err
	}

	if clearErr := p.clearActiveRunSlot(ctx, failedRun, failedAt); clearErr != nil {
		return clearErr
	}
	return execErr
}

func newCapabilityObserveStep(runID string, index int, capabilityName string, result CapabilityResult, observedAt time.Time) (*AgentStep, error) {
	output, err := json.Marshal(capabilityObservation{
		CapabilityName: strings.TrimSpace(capabilityName),
		OutputText:     result.OutputText,
		OutputJSON:     json.RawMessage(result.OutputJSON),
		ExternalRef:    strings.TrimSpace(result.ExternalRef),
		OccurredAt:     observedAt,
	})
	if err != nil {
		return nil, err
	}

	return &AgentStep{
		ID:          newRuntimeID("step"),
		RunID:       runID,
		Index:       index,
		Kind:        StepKindObserve,
		Status:      StepStatusCompleted,
		OutputJSON:  output,
		ExternalRef: strings.TrimSpace(result.ExternalRef),
		CreatedAt:   observedAt,
		StartedAt:   &observedAt,
		FinishedAt:  &observedAt,
	}, nil
}

func newCapabilityReplyStep(runID string, index int, capabilityName string, plan CapabilityReplyPlan, refs ReplyEmissionResult, observedAt time.Time) (*AgentStep, error) {
	output, err := json.Marshal(capabilityReply{
		CapabilityName:    strings.TrimSpace(capabilityName),
		ThoughtText:       strings.TrimSpace(plan.ThoughtText),
		ReplyText:         strings.TrimSpace(plan.ReplyText),
		Text:              strings.TrimSpace(plan.ReplyText),
		ResponseMessageID: strings.TrimSpace(refs.MessageID),
		ResponseCardID:    strings.TrimSpace(refs.CardID),
		DeliveryMode:      refs.DeliveryMode,
		LifecycleState:    ReplyLifecycleStateActive,
		TargetMessageID:   strings.TrimSpace(refs.TargetMessageID),
		TargetCardID:      strings.TrimSpace(refs.TargetCardID),
		TargetStepID:      strings.TrimSpace(refs.TargetStepID),
	})
	if err != nil {
		return nil, err
	}

	externalRef := strings.TrimSpace(refs.CardID)
	if externalRef == "" {
		externalRef = strings.TrimSpace(refs.MessageID)
	}

	return &AgentStep{
		ID:          newRuntimeID("step"),
		RunID:       runID,
		Index:       index,
		Kind:        StepKindReply,
		Status:      StepStatusCompleted,
		OutputJSON:  output,
		ExternalRef: externalRef,
		CreatedAt:   observedAt,
		StartedAt:   &observedAt,
		FinishedAt:  &observedAt,
	}, nil
}

func (p *ContinuationProcessor) emitCapabilityReply(ctx context.Context, run *AgentRun, plan CapabilityReplyPlan) (ReplyEmissionResult, error) {
	if p == nil || p.replyEmitter == nil || run == nil {
		return ReplyEmissionResult{}, nil
	}
	session, err := p.coordinator.sessionRepo.GetByID(ctx, run.SessionID)
	if err != nil {
		return ReplyEmissionResult{}, err
	}
	target, err := p.resolveReplyTarget(ctx, run)
	if err != nil {
		return ReplyEmissionResult{}, err
	}
	replyResult, err := p.replyEmitter.EmitReply(ctx, ReplyEmissionRequest{
		Session:         session,
		Run:             run,
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

func (p *ContinuationProcessor) planCapabilityReply(
	ctx context.Context,
	run *AgentRun,
	step *AgentStep,
	input CapabilityCallInput,
	result CapabilityResult,
) (CapabilityReplyPlan, error) {
	fallback := resolveCapabilityResultSummary(strings.TrimSpace(step.CapabilityName), result)
	if p == nil || p.capabilityReplyPlanner == nil {
		return normalizeCapabilityReplyPlan(CapabilityReplyPlan{ReplyText: fallback}, fallback), nil
	}

	session, err := p.coordinator.sessionRepo.GetByID(ctx, run.SessionID)
	if err != nil {
		return CapabilityReplyPlan{}, err
	}
	plan, err := p.capabilityReplyPlanner.PlanCapabilityReply(ctx, CapabilityReplyPlanningRequest{
		Session:        session,
		Run:            run,
		Step:           step,
		CapabilityName: strings.TrimSpace(step.CapabilityName),
		Result:         result,
	})
	if err != nil {
		return CapabilityReplyPlan{}, err
	}
	return normalizeCapabilityReplyPlan(plan, fallback), nil
}

func hydrateQueuedCapabilityCall(call *QueuedCapabilityCall, run *AgentRun, request CapabilityRequest) *QueuedCapabilityCall {
	if call == nil {
		return nil
	}

	copied := *call
	copied.Input = call.Input
	copied.Input.Request = call.Input.Request

	if strings.TrimSpace(copied.Input.Request.SessionID) == "" && run != nil {
		copied.Input.Request.SessionID = run.SessionID
	}
	if strings.TrimSpace(copied.Input.Request.RunID) == "" && run != nil {
		copied.Input.Request.RunID = run.ID
	}
	if strings.TrimSpace(string(copied.Input.Request.Scope)) == "" {
		copied.Input.Request.Scope = request.Scope
	}
	if strings.TrimSpace(copied.Input.Request.ChatID) == "" {
		copied.Input.Request.ChatID = request.ChatID
	}
	if strings.TrimSpace(copied.Input.Request.ActorOpenID) == "" {
		copied.Input.Request.ActorOpenID = coalesceString(request.ActorOpenID, func() string {
			if run == nil {
				return ""
			}
			return run.ActorOpenID
		}())
	}
	if strings.TrimSpace(copied.Input.Request.InputText) == "" {
		copied.Input.Request.InputText = coalesceString(request.InputText, func() string {
			if run == nil {
				return ""
			}
			return run.InputText
		}())
	}
	copied.Input.QueueTail = hydrateQueuedCapabilityQueue(copied.Input.QueueTail, run, request)
	return &copied
}

func hydrateQueuedCapabilityQueue(queue []QueuedCapabilityCall, run *AgentRun, request CapabilityRequest) []QueuedCapabilityCall {
	if len(queue) == 0 {
		return nil
	}
	result := make([]QueuedCapabilityCall, 0, len(queue))
	for _, item := range queue {
		call := hydrateQueuedCapabilityCall(&item, run, request)
		if call == nil {
			continue
		}
		result = append(result, *call)
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func coalesceString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func (p *ContinuationProcessor) executeContinuationReplyTurn(
	ctx context.Context,
	run *AgentRun,
	plan continuationPlan,
) (ContinuationReplyTurnResult, bool, error) {
	if p == nil || p.continuationReplyTurnExecutor == nil {
		return ContinuationReplyTurnResult{}, false, nil
	}

	session, err := p.coordinator.sessionRepo.GetByID(ctx, run.SessionID)
	if err != nil {
		return ContinuationReplyTurnResult{}, false, err
	}
	recorder := newRunCapabilityTraceRecorder(p.coordinator, run.ID, plan.ObserveStep.Index+1, plan.ObservedAt)
	turnResult, err := p.continuationReplyTurnExecutor.ExecuteContinuationReplyTurn(ctx, ContinuationReplyTurnRequest{
		Session:                 session,
		Run:                     run,
		Source:                  plan.Context.Source,
		WaitingReason:           plan.Context.WaitingReason,
		PreviousStepKind:        plan.Context.PreviousStepKind,
		PreviousStepTitle:       plan.Context.PreviousStepTitle,
		PreviousStepExternalRef: plan.Context.PreviousStepExternalRef,
		ResumeSummary:           plan.Context.ResumeSummary,
		ResumePayloadJSON:       append([]byte(nil), plan.Context.ResumePayloadJSON...),
		ThoughtFallback:         plan.ThoughtText,
		ReplyFallback:           plan.ReplyText,
		Recorder:                recorder,
		PlanRecorder:            recorder,
	})
	if err != nil {
		return ContinuationReplyTurnResult{}, false, err
	}
	if !turnResult.Executed {
		return ContinuationReplyTurnResult{}, false, nil
	}

	turnResult.Plan = normalizeCapabilityReplyPlan(turnResult.Plan, plan.ReplyText)
	turnResult.PendingCapability = hydrateQueuedCapabilityCall(turnResult.PendingCapability, run, continuationCapabilityRequest(ContinuationReplyTurnRequest{
		Session: session,
		Run:     run,
	}))
	return turnResult, true, nil
}

func (p *ContinuationProcessor) completeContinuationReplyTurn(
	ctx context.Context,
	run *AgentRun,
	plan continuationPlan,
	turnResult ContinuationReplyTurnResult,
) error {
	steps, err := p.coordinator.stepRepo.ListByRun(ctx, run.ID)
	if err != nil {
		return err
	}
	currentIndex := nextAvailableStepIndex(steps, plan.ObserveStep.Index)
	for _, call := range turnResult.CapabilityCalls {
		capabilityStep, err := newCompletedCapabilityStep(run.ID, currentIndex, call, plan.ObservedAt)
		if err != nil {
			return err
		}
		if err := p.coordinator.stepRepo.Append(ctx, capabilityStep); err != nil {
			return err
		}
		currentIndex++

		observeStep, err := newCompletedCapabilityObserveStep(run.ID, currentIndex, call, plan.ObservedAt)
		if err != nil {
			return err
		}
		if err := p.coordinator.stepRepo.Append(ctx, observeStep); err != nil {
			return err
		}
		currentIndex++
	}

	resultSummary := strings.TrimSpace(turnResult.Plan.ReplyText)
	if resultSummary == "" {
		resultSummary = plan.ResultSummary
	}

	plannedRun, planStepIndex, err := p.queueReplyPlanStep(ctx, run, currentIndex-1, turnResult.Plan.ThoughtText, turnResult.Plan.ReplyText, turnResult.PendingCapability, plan.ObservedAt)
	if err != nil {
		return err
	}
	replyRefs, err := p.emitCapabilityReply(ctx, plannedRun, turnResult.Plan)
	if err != nil {
		return err
	}

	if turnResult.PendingCapability != nil {
		return p.continueQueuedReplyPlan(ctx, plannedRun, planStepIndex, turnResult.Plan.ThoughtText, turnResult.Plan.ReplyText, *turnResult.PendingCapability, replyRefs, resultSummary, plan.ObservedAt)
	}

	return p.completeQueuedReplyPlan(ctx, plannedRun, planStepIndex, turnResult.Plan.ThoughtText, turnResult.Plan.ReplyText, replyRefs, resultSummary, plan.ObservedAt)
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
		return nil
	})
	if err != nil {
		return err
	}
	return p.processQueuedRun(ctx, queuedRun.ID, queuedAt)
}

func mergeQueuedCapabilityQueue(head []QueuedCapabilityCall, tail []QueuedCapabilityCall) []QueuedCapabilityCall {
	if len(head) == 0 && len(tail) == 0 {
		return nil
	}
	merged := make([]QueuedCapabilityCall, 0, len(head)+len(tail))
	for _, item := range head {
		merged = append(merged, cloneQueuedCapabilityCall(item))
	}
	for _, item := range tail {
		merged = append(merged, cloneQueuedCapabilityCall(item))
	}
	return merged
}

func cloneQueuedCapabilityCall(src QueuedCapabilityCall) QueuedCapabilityCall {
	copied := src
	copied.Input = src.Input
	copied.Input.Request = src.Input.Request
	if src.Input.Approval != nil {
		approval := *src.Input.Approval
		copied.Input.Approval = &approval
	}
	if src.Input.Continuation != nil {
		continuation := *src.Input.Continuation
		copied.Input.Continuation = &continuation
	}
	if len(src.Input.Request.PayloadJSON) > 0 {
		copied.Input.Request.PayloadJSON = append([]byte(nil), src.Input.Request.PayloadJSON...)
	}
	if len(src.Input.QueueTail) > 0 {
		copied.Input.QueueTail = mergeQueuedCapabilityQueue(nil, src.Input.QueueTail)
	}
	return copied
}

func resolveCapabilityResultSummary(capabilityName string, result CapabilityResult) string {
	if text := strings.TrimSpace(result.OutputText); text != "" {
		return text
	}
	if raw := strings.TrimSpace(string(result.OutputJSON)); raw != "" {
		return raw
	}
	if name := strings.TrimSpace(capabilityName); name != "" {
		return fmt.Sprintf("capability %s executed", name)
	}
	return "capability executed"
}

func normalizeObservedAt(observedAt time.Time) time.Time {
	if observedAt.IsZero() {
		return time.Now().UTC()
	}
	return observedAt.UTC()
}

func findReplayableCapabilityStep(steps []*AgentStep, currentIndex int, source ResumeSource) *AgentStep {
	if source != ResumeSourceApproval {
		return nil
	}

	for i := len(steps) - 1; i >= 0; i-- {
		step := steps[i]
		if step == nil || step.Index >= currentIndex {
			continue
		}
		if step.Kind != StepKindCapabilityCall {
			continue
		}
		switch step.Status {
		case StepStatusQueued, StepStatusRunning:
			return step
		}
	}
	return nil
}

func (p *ContinuationProcessor) execute(ctx context.Context, run *AgentRun, plan continuationPlan) error {
	currentRun, err := p.moveRunToRunning(ctx, run, plan.ObservedAt)
	if err != nil {
		return err
	}
	if err := p.completeResumeStep(ctx, currentRun, plan); err != nil {
		return err
	}
	if err := p.coordinator.stepRepo.Append(ctx, &plan.ObserveStep); err != nil {
		return err
	}

	turnResult, handled, err := p.executeContinuationReplyTurn(ctx, currentRun, plan)
	if err != nil {
		return err
	}
	if handled {
		return p.completeContinuationReplyTurn(ctx, currentRun, plan, turnResult)
	}

	plannedRun, planStepIndex, err := p.queueReplyPlanStep(ctx, currentRun, plan.ObserveStep.Index, plan.ThoughtText, plan.ReplyText, nil, plan.ObservedAt)
	if err != nil {
		return err
	}
	replyRefs, err := p.emitContinuationReply(ctx, plannedRun, plan)
	if err != nil {
		return err
	}
	return p.completeQueuedReplyPlan(ctx, plannedRun, planStepIndex, plan.ThoughtText, plan.ReplyText, replyRefs, plan.ResultSummary, plan.ObservedAt)
}

func newContinuationReplyStep(runID string, index int, thoughtText, replyText string, refs ReplyEmissionResult, observedAt time.Time) (*AgentStep, error) {
	output, err := json.Marshal(capabilityReply{
		ThoughtText:       strings.TrimSpace(thoughtText),
		ReplyText:         strings.TrimSpace(replyText),
		Text:              strings.TrimSpace(replyText),
		ResponseMessageID: strings.TrimSpace(refs.MessageID),
		ResponseCardID:    strings.TrimSpace(refs.CardID),
		DeliveryMode:      refs.DeliveryMode,
		LifecycleState:    ReplyLifecycleStateActive,
		TargetMessageID:   strings.TrimSpace(refs.TargetMessageID),
		TargetCardID:      strings.TrimSpace(refs.TargetCardID),
		TargetStepID:      strings.TrimSpace(refs.TargetStepID),
	})
	if err != nil {
		return nil, err
	}

	externalRef := strings.TrimSpace(refs.CardID)
	if externalRef == "" {
		externalRef = strings.TrimSpace(refs.MessageID)
	}

	return &AgentStep{
		ID:          newRuntimeID("step"),
		RunID:       runID,
		Index:       index,
		Kind:        StepKindReply,
		Status:      StepStatusCompleted,
		OutputJSON:  output,
		ExternalRef: externalRef,
		CreatedAt:   observedAt,
		StartedAt:   &observedAt,
		FinishedAt:  &observedAt,
	}, nil
}

func (p *ContinuationProcessor) emitContinuationReply(ctx context.Context, run *AgentRun, plan continuationPlan) (ReplyEmissionResult, error) {
	if p == nil || p.replyEmitter == nil || run == nil || strings.TrimSpace(plan.ReplyText) == "" {
		return ReplyEmissionResult{}, nil
	}
	session, err := p.coordinator.sessionRepo.GetByID(ctx, run.SessionID)
	if err != nil {
		return ReplyEmissionResult{}, err
	}
	target, err := p.resolveReplyTarget(ctx, run)
	if err != nil {
		return ReplyEmissionResult{}, err
	}
	replyResult, err := p.replyEmitter.EmitReply(ctx, ReplyEmissionRequest{
		Session:         session,
		Run:             run,
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

func (p *ContinuationProcessor) moveRunToRunning(ctx context.Context, run *AgentRun, startedAt time.Time) (*AgentRun, error) {
	if run.Status == RunStatusRunning {
		if run.StartedAt == nil {
			run.StartedAt = &startedAt
		}
		return run, nil
	}
	return p.coordinator.runRepo.UpdateStatus(ctx, run.ID, run.Revision, func(current *AgentRun) error {
		current.Status = RunStatusRunning
		current.WaitingReason = WaitingReasonNone
		current.WaitingToken = ""
		current.UpdatedAt = startedAt
		if current.StartedAt == nil {
			current.StartedAt = &startedAt
		}
		return nil
	})
}

func (p *ContinuationProcessor) completeResumeStep(ctx context.Context, run *AgentRun, plan continuationPlan) error {
	steps, err := p.coordinator.stepRepo.ListByRun(ctx, run.ID)
	if err != nil {
		return err
	}

	resumeStep := findStepByIndex(steps, run.CurrentStepIndex)
	if resumeStep == nil || resumeStep.Kind != StepKindResume {
		return fmt.Errorf("agent runtime resume step missing: run_id=%s index=%d", run.ID, run.CurrentStepIndex)
	}

	switch resumeStep.Status {
	case StepStatusCompleted:
		return nil
	case StepStatusQueued:
		resumeStep, err = p.coordinator.stepRepo.UpdateStatus(ctx, resumeStep.ID, StepStatusQueued, func(current *AgentStep) error {
			current.Status = StepStatusRunning
			current.StartedAt = &plan.ObservedAt
			return nil
		})
		if err != nil {
			return err
		}
	case StepStatusRunning:
	default:
		return fmt.Errorf("agent runtime resume step not executable: run_id=%s step_id=%s status=%s", run.ID, resumeStep.ID, resumeStep.Status)
	}

	_, err = p.coordinator.stepRepo.UpdateStatus(ctx, resumeStep.ID, resumeStep.Status, func(current *AgentStep) error {
		current.Status = StepStatusCompleted
		current.OutputJSON = plan.ResumeOutput
		current.FinishedAt = &plan.ObservedAt
		current.ExternalRef = resumeStep.ExternalRef
		return nil
	})
	return err
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

	swapped, err := p.coordinator.runtimeStore.SwapActiveChatRun(ctx, session.ChatID, run.ID, "", p.coordinator.activeRunTTL)
	if err != nil {
		return err
	}
	if swapped {
		return nil
	}

	current, err := p.coordinator.runtimeStore.ActiveChatRun(ctx, session.ChatID)
	if err != nil {
		return err
	}
	if current == run.ID {
		return fmt.Errorf("active chat slot still points to completed run: chat_id=%s run_id=%s", session.ChatID, run.ID)
	}
	return nil
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

func findLatestReplyTargetBeforeIndex(steps []*AgentStep, index int) replyTarget {
	target := findLatestReplyTargetBeforeIndexWithFilter(steps, index, func(step *AgentStep) bool {
		return replyLifecycleState(step) != ReplyLifecycleStateSuperseded
	})
	if target.MessageID != "" || target.CardID != "" {
		return target
	}
	return findLatestReplyTargetBeforeIndexWithFilter(steps, index, nil)
}

func (p *ContinuationProcessor) resolveReplyTarget(ctx context.Context, run *AgentRun) (replyTarget, error) {
	if p == nil || p.coordinator == nil || run == nil {
		return replyTarget{}, nil
	}

	steps, err := p.coordinator.stepRepo.ListByRun(ctx, run.ID)
	if err != nil {
		return replyTarget{}, err
	}
	target := findLatestReplyTargetWithFilter(steps, func(step *AgentStep) bool {
		return replyLifecycleState(step) != ReplyLifecycleStateSuperseded
	})
	if target.MessageID != "" || target.CardID != "" {
		return target, nil
	}
	return findLatestReplyTargetWithFilter(steps, nil), nil
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
	target.MessageID = strings.TrimSpace(result.CompatibleReplyMessageID)
	return target
}

func decodeReplyLikeTarget(step *AgentStep) replyTarget {
	if step == nil {
		return replyTarget{}
	}
	switch step.Kind {
	case StepKindReply:
		return decodeReplyTarget(step)
	case StepKindCapabilityCall:
		return decodeCapabilityCompatibleReplyTarget(step)
	default:
		return replyTarget{}
	}
}

func (p *ContinuationProcessor) linkSupersededReplyStep(ctx context.Context, refs ReplyEmissionResult, nextStepID string) error {
	if p == nil || p.coordinator == nil {
		return nil
	}
	targetStepID := strings.TrimSpace(refs.TargetStepID)
	nextStepID = strings.TrimSpace(nextStepID)
	if targetStepID == "" || nextStepID == "" || targetStepID == nextStepID {
		return nil
	}

	_, err := p.coordinator.stepRepo.UpdateStatus(ctx, targetStepID, StepStatusCompleted, func(current *AgentStep) error {
		output := map[string]any{}
		if len(current.OutputJSON) > 0 {
			if unmarshalErr := json.Unmarshal(current.OutputJSON, &output); unmarshalErr != nil {
				return unmarshalErr
			}
		}
		output["lifecycle_state"] = string(ReplyLifecycleStateSuperseded)
		if refs.DeliveryMode == ReplyDeliveryModePatch {
			output["patched_by_step_id"] = nextStepID
		} else {
			output["superseded_by_step_id"] = nextStepID
		}
		raw, marshalErr := json.Marshal(output)
		if marshalErr != nil {
			return marshalErr
		}
		current.OutputJSON = raw
		return nil
	})
	return err
}
