package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	approvaldef "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime/approval"
	capdef "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime/capability"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/runtimecontext"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"go.uber.org/zap"
)

const (
	defaultExecutionLeaseTTL           = 3 * 60 * time.Second
	defaultExecutionLeaseRenewInterval = 15 * time.Second
)

type executionLeaseStore interface {
	AcquireExecutionLease(context.Context, string, string, string, time.Duration, int64) (bool, error)
	RenewExecutionLease(context.Context, string, string, string, time.Duration) (bool, error)
	ReleaseExecutionLease(context.Context, string, string, string) (bool, error)
}

// ContinuationProcessor is the runtime execution engine that resumes queued,
// approval-gated, and callback-driven runs. It owns capability execution,
// continuation reply planning, reply emission, and initial-run processing.
type ContinuationProcessor struct {
	coordinator                   *RunCoordinator
	registry                      *CapabilityRegistry
	capabilityReplyTurnExecutor   CapabilityReplyTurnExecutor
	continuationReplyTurnExecutor ContinuationReplyTurnExecutor
	capabilityReplyPlanner        CapabilityReplyPlanner
	initialReplyExecutorFactory   InitialReplyExecutorFactory
	initialReplyEmitter           InitialReplyEmitter
	replyEmitter                  ReplyEmitter
	approvalSender                ApprovalSender
	runLeasePolicy                RunLeasePolicy
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

// NewContinuationProcessor constructs a continuation processor with default reply, approval, and continuation executors wired around the provided coordinator.
func NewContinuationProcessor(coordinator *RunCoordinator, opts ...ContinuationProcessorOption) *ContinuationProcessor {
	processor := &ContinuationProcessor{
		coordinator:    coordinator,
		runLeasePolicy: DefaultRunLeasePolicy(),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(processor)
		}
	}
	return processor
}

// WithApprovalSender injects the approval card sender used when continuation
// logic needs to surface a pending approval request.
func WithApprovalSender(sender ApprovalSender) ContinuationProcessorOption {
	return func(p *ContinuationProcessor) {
		if p != nil {
			p.approvalSender = sender
		}
	}
}

// ProcessResume loads the current run projection for a resume event, advances the run state machine, and emits any follow-up reply required by the resumed flow.
func (p *ContinuationProcessor) ProcessResume(ctx context.Context, event ResumeEvent) (err error) {
	if p == nil || p.coordinator == nil {
		return nil
	}
	defer func() {
		if err == nil || errors.Is(err, ErrResumeDeferred) {
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
	session, err := p.coordinator.sessionRepo.GetByID(ctx, run.SessionID)
	if err != nil {
		return err
	}
	if err := p.withExecutionLease(ctx, sessionChatID(session), run.ActorOpenID, executionLeaseHolderForResume(event), func() error {
		return p.processResumedRun(ctx, run, event)
	}); err != nil {
		return err
	}
	return nil
}

// processResumedRun is the durable resume lane:
// 1. rebuild the run projection
// 2. choose capability-replay vs normal continuation
// 3. emit the next reply / queue follow-up work
func (p *ContinuationProcessor) processResumedRun(ctx context.Context, run *AgentRun, event ResumeEvent) error {
	if p == nil || p.coordinator == nil || run == nil {
		return nil
	}
	deferred, err := p.shouldDeferApprovalResume(ctx, run, event)
	if err != nil {
		return err
	}
	if deferred {
		return ErrResumeDeferred
	}
	projection, err := p.loadProjection(ctx, run)
	if err != nil {
		return err
	}
	ctx, err = p.withAgenticReplyTargetState(ctx, projection)
	if err != nil {
		return err
	}
	currentStep := projection.CurrentStep()
	if currentStep != nil && currentStep.Kind == StepKindResume {
		if capabilityStep := projection.ReplayableCapabilityStepBefore(currentStep.Index, event.Source); capabilityStep != nil {
			return p.processCapabilityResume(ctx, run, currentStep, capabilityStep, event)
		}
	}
	if currentStep != nil && currentStep.Kind == StepKindCapabilityCall {
		return p.processCapabilityCall(ctx, run, currentStep, event)
	}

	plan, err := p.buildContinuationPlan(run, projection.steps, currentStep, event)
	if err != nil {
		return err
	}
	return p.executeContinuationPlan(ctx, run, plan)
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
		clearRunExecutionLiveness(current)
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

func (p *ContinuationProcessor) loadProjection(ctx context.Context, run *AgentRun) (RunProjection, error) {
	if p == nil || p.coordinator == nil || run == nil {
		return RunProjection{}, nil
	}
	steps, err := p.loadSteps(ctx, run.ID)
	if err != nil {
		return RunProjection{}, err
	}
	return NewRunProjection(run, steps), nil
}

func (p *ContinuationProcessor) loadCurrentStep(ctx context.Context, runID string, index int) (*AgentStep, error) {
	steps, err := p.loadSteps(ctx, runID)
	if err != nil {
		return nil, err
	}
	return findStepByIndex(steps, index), nil
}

func (p *ContinuationProcessor) withAgenticReplyTargetState(ctx context.Context, projection RunProjection) (context.Context, error) {
	state := runtimecontext.AgenticReplyTargetStateFromContext(ctx)
	if state == nil {
		state = runtimecontext.NewAgenticReplyTargetState()
		ctx = runtimecontext.WithAgenticReplyTargetState(ctx, state)
	}
	if p == nil || p.coordinator == nil || projection.run == nil {
		return ctx, nil
	}

	root := projection.RootReplyTarget()
	runtimecontext.SeedRootAgenticReplyTarget(ctx, root.MessageID, root.CardID)

	if current, ok := runtimecontext.ActiveAgenticReplyTarget(ctx); !ok || (current.MessageID == "" && current.CardID == "") {
		active := projection.LatestReplyTarget()
		runtimecontext.RecordActiveAgenticReplyTarget(ctx, active.MessageID, active.CardID)
	}
	return ctx, nil
}

func (p *ContinuationProcessor) shouldDeferApprovalResume(ctx context.Context, run *AgentRun, event ResumeEvent) (bool, error) {
	if p == nil || p.coordinator == nil || run == nil || event.Source != ResumeSourceApproval {
		return false, nil
	}

	stepID := strings.TrimSpace(event.StepID)
	token := strings.TrimSpace(event.Token)
	if stepID == "" && token == "" {
		return false, nil
	}

	steps, err := p.coordinator.stepRepo.ListByRun(ctx, run.ID)
	if err != nil {
		return false, err
	}
	for _, step := range steps {
		if step == nil || step.Kind != StepKindApprovalRequest {
			continue
		}
		if stepID == "" || strings.TrimSpace(step.ID) == stepID {
			return false, nil
		}
	}

	reservation, err := p.coordinator.loadApprovalReservation(ctx, stepID, token)
	if err != nil {
		return false, err
	}
	return reservation != nil, nil
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

func (p *ContinuationProcessor) buildContinuationPlan(run *AgentRun, steps []*AgentStep, currentStep *AgentStep, event ResumeEvent) (continuationPlan, error) {
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
	return NewRunProjection(run, steps).ContinuationContext(currentStep, event)
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
		request, err := approvaldef.DecodeApprovalRequest("", "", 0, "", step.OutputJSON)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(request.Title)
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

	input, err := capdef.DecodeCallInput(step.InputJSON)
	if err != nil {
		return err
	}
	input.Request = hydrateCapabilityRequest(run, step, input.Request)

	capability, meta, err := p.lookupCapability(step.CapabilityName, input.Request.Scope)
	if err != nil {
		return err
	}

	return p.withRunExecutionHeartbeat(ctx, run, observedAt, func(currentRun *AgentRun) error {
		if meta.RequiresApproval || input.Approval != nil {
			return p.requestCapabilityApproval(ctx, currentRun, step, meta, input, observedAt)
		}
		return p.executeCapabilityCall(ctx, currentRun, step, capability, input, meta, step.Index+1, observedAt)
	})
}

func (p *ContinuationProcessor) withExecutionLease(ctx context.Context, chatID, actorOpenID, holder string, run func() error) error {
	if run == nil {
		return nil
	}
	chatID = strings.TrimSpace(chatID)
	actorOpenID = strings.TrimSpace(actorOpenID)
	holder = strings.TrimSpace(holder)
	store := p.executionLeaseStore()
	if store == nil || chatID == "" || actorOpenID == "" || holder == "" {
		return run()
	}
	acquired, err := store.AcquireExecutionLease(ctx, chatID, actorOpenID, holder, defaultExecutionLeaseTTL, DefaultMaxExecutionLeasesPerActorChat)
	if err != nil {
		return err
	}
	if !acquired {
		return ErrRunSlotOccupied
	}

	ctx = withExecutionWorkerID(ctx, holder)
	stopRenew := make(chan struct{})
	doneRenew := make(chan struct{})
	go p.renewExecutionLease(ctx, store, chatID, actorOpenID, holder, stopRenew, doneRenew)
	defer func() {
		close(stopRenew)
		<-doneRenew
		if _, err := store.ReleaseExecutionLease(ctx, chatID, actorOpenID, holder); err != nil && !errors.Is(err, context.Canceled) {
			logs.L().Ctx(ctx).Warn("agent runtime execution lease release failed",
				zap.Error(err),
				zap.String("chat_id", chatID),
				zap.String("actor_open_id", actorOpenID),
				zap.String("holder", holder),
			)
		}
	}()
	return run()
}

func (p *ContinuationProcessor) renewExecutionLease(
	ctx context.Context,
	store executionLeaseStore,
	chatID, actorOpenID, holder string,
	stop <-chan struct{},
	done chan<- struct{},
) {
	defer close(done)
	ticker := time.NewTicker(defaultExecutionLeaseRenewInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			renewed, err := store.RenewExecutionLease(ctx, chatID, actorOpenID, holder, defaultExecutionLeaseTTL)
			if err != nil && !errors.Is(err, context.Canceled) {
				logs.L().Ctx(ctx).Warn("agent runtime execution lease renew failed",
					zap.Error(err),
					zap.String("chat_id", chatID),
					zap.String("actor_open_id", actorOpenID),
					zap.String("holder", holder),
				)
				continue
			}
			if !renewed {
				logs.L().Ctx(ctx).Warn("agent runtime execution lease missing during renew",
					zap.String("chat_id", chatID),
					zap.String("actor_open_id", actorOpenID),
					zap.String("holder", holder),
				)
			}
		}
	}
}

func (p *ContinuationProcessor) executionLeaseStore() executionLeaseStore {
	if p == nil || p.coordinator == nil || p.coordinator.runtimeStore == nil {
		return nil
	}
	store, _ := p.coordinator.runtimeStore.(executionLeaseStore)
	return store
}

func (p *ContinuationProcessor) normalizedRunLeasePolicy() RunLeasePolicy {
	if p == nil {
		return DefaultRunLeasePolicy()
	}
	return p.runLeasePolicy.Normalize()
}

func (p *ContinuationProcessor) withRunExecutionHeartbeat(
	ctx context.Context,
	run *AgentRun,
	startedAt time.Time,
	execute func(*AgentRun) error,
) error {
	if execute == nil {
		return nil
	}
	if p == nil || p.coordinator == nil || run == nil {
		return execute(run)
	}

	workerID := executionWorkerIDFromContext(ctx)
	currentRun, err := p.coordinator.startRunExecution(ctx, run, workerID, startedAt, p.normalizedRunLeasePolicy())
	if err != nil {
		return err
	}
	if workerID == "" {
		return execute(currentRun)
	}

	stopRenew := make(chan struct{})
	doneRenew := make(chan struct{})
	go p.renewRunExecutionHeartbeat(ctx, currentRun.ID, workerID, stopRenew, doneRenew)
	defer func() {
		close(stopRenew)
		<-doneRenew
	}()
	return execute(currentRun)
}

func (p *ContinuationProcessor) renewRunExecutionHeartbeat(
	ctx context.Context,
	runID string,
	workerID string,
	stop <-chan struct{},
	done chan<- struct{},
) {
	defer close(done)
	policy := p.normalizedRunLeasePolicy()
	ticker := time.NewTicker(policy.HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			updated, err := p.coordinator.refreshRunExecutionLiveness(ctx, runID, workerID, time.Now().UTC(), policy)
			if err != nil && !errors.Is(err, context.Canceled) {
				logs.L().Ctx(ctx).Warn("agent runtime run heartbeat renew failed",
					zap.Error(err),
					zap.String("run_id", runID),
					zap.String("worker_id", workerID),
				)
				continue
			}
			if updated == nil {
				return
			}
		}
	}
}

func executionLeaseHolderForInitial(input InitialRunInput) string {
	if triggerMessageID := strings.TrimSpace(input.Start.TriggerMessageID); triggerMessageID != "" {
		return "initial:" + triggerMessageID
	}
	if event := strings.TrimSpace(input.Start.TriggerEventID); event != "" {
		return "initial_event:" + event
	}
	return ""
}

func executionLeaseHolderForResume(event ResumeEvent) string {
	if runID := strings.TrimSpace(event.RunID); runID != "" {
		return "resume:" + runID
	}
	return ""
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
	return p.withRunExecutionHeartbeat(ctx, run, normalizeObservedAt(event.OccurredAt), func(currentRun *AgentRun) error {
		plan, err := p.buildContinuationPlan(currentRun, []*AgentStep{capabilityStep, resumeStep}, resumeStep, event)
		if err != nil {
			return err
		}
		if err := p.completeResumeStep(ctx, currentRun, plan); err != nil {
			return err
		}

		input, err := capdef.DecodeCallInput(capabilityStep.InputJSON)
		if err != nil {
			return err
		}
		input.Request = hydrateCapabilityRequest(currentRun, capabilityStep, input.Request)

		capability, meta, err := p.lookupCapability(capabilityStep.CapabilityName, input.Request.Scope)
		if err != nil {
			return err
		}

		return p.executeCapabilityCall(ctx, currentRun, capabilityStep, capability, input, meta, resumeStep.Index+1, plan.ObservedAt)
	})
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
	if strings.TrimSpace(spec.ReservationStepID) != "" || strings.TrimSpace(spec.ReservationToken) != "" {
		request, decision, err := p.coordinator.ActivateReservedApproval(ctx, ActivateReservedApprovalInput{
			RunID:       run.ID,
			StepID:      spec.ReservationStepID,
			Token:       spec.ReservationToken,
			RequestedAt: requestedAt,
		})
		switch {
		case err == nil:
			if decision == nil || request == nil {
				return nil
			}
			event := ResumeEvent{
				RunID:       request.RunID,
				StepID:      request.StepID,
				Revision:    request.Revision,
				Source:      ResumeSourceApproval,
				Token:       request.Token,
				ActorOpenID: decision.ActorOpenID,
				OccurredAt:  decision.OccurredAt,
			}
			switch decision.Outcome {
			case ApprovalReservationDecisionApproved:
				// The approval click path has already enqueued the async resume event.
				// Stop here and let that queued resume own the post-approval continuation,
				// otherwise the current execution path and the resume worker can both
				// continue the same capability and emit duplicate side effects.
				return nil
			case ApprovalReservationDecisionRejected:
				_, err := p.coordinator.RejectApproval(ctx, event)
				return err
			default:
				return fmt.Errorf("unsupported approval reservation decision outcome: %q", decision.Outcome)
			}
		case errors.Is(err, ErrApprovalReservationNotFound):
		default:
			return err
		}
	}
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
	if root, ok := runtimecontext.RootAgenticReplyTarget(ctx); ok && strings.TrimSpace(root.MessageID) != "" {
		target.ReplyToMessageID = strings.TrimSpace(root.MessageID)
		target.ReplyInThread = true
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
		current.OutputJSON = capdef.EncodeResult(result)
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
		Session:  session,
		Run:      run,
		Step:     step,
		Input:    input,
		Result:   result,
		Recorder: recorder,
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
		clearRunExecutionLiveness(current)
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
		ChatID:         session.ChatID,
		OpenID:         run.ActorOpenID,
		InputText:      run.InputText,
		CapabilityName: strings.TrimSpace(step.CapabilityName),
		Result:         result,
	})
	if err != nil {
		return CapabilityReplyPlan{}, err
	}
	return normalizeCapabilityReplyPlan(plan, fallback), nil
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

// executeContinuationPlan owns the normal continuation lane after replay checks:
// complete the durable resume step, run one continuation turn, then persist the
// emitted reply as either a completed run or a continued queued capability.
func (p *ContinuationProcessor) executeContinuationPlan(ctx context.Context, run *AgentRun, plan continuationPlan) error {
	return p.withRunExecutionHeartbeat(ctx, run, plan.ObservedAt, func(currentRun *AgentRun) error {
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

		return p.finalizeContinuationPlan(ctx, currentRun, plan)
	})
}

func (p *ContinuationProcessor) finalizeContinuationPlan(ctx context.Context, run *AgentRun, plan continuationPlan) error {
	plannedRun, planStepIndex, err := p.queueReplyPlanStep(ctx, run, plan.ObserveStep.Index, plan.ThoughtText, plan.ReplyText, nil, plan.ObservedAt)
	if err != nil {
		return err
	}
	replyRefs, err := p.emitContinuationReply(ctx, plannedRun, plan)
	if err != nil {
		return err
	}
	return p.completeQueuedReplyPlan(ctx, plannedRun, planStepIndex, plan.ThoughtText, plan.ReplyText, replyRefs, plan.ResultSummary, plan.ObservedAt)
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

func (p *ContinuationProcessor) resolveRootReplyTarget(ctx context.Context, run *AgentRun) (replyTarget, error) {
	if p == nil || p.coordinator == nil || run == nil {
		return replyTarget{}, nil
	}

	steps, err := p.coordinator.stepRepo.ListByRun(ctx, run.ID)
	if err != nil {
		return replyTarget{}, err
	}
	return NewRunProjection(run, steps).RootReplyTarget(), nil
}

func (p *ContinuationProcessor) resolveFollowUpReplyTarget(ctx context.Context, run *AgentRun) (replyTarget, bool, error) {
	root, err := p.resolveRootReplyTarget(ctx, run)
	if err != nil {
		return replyTarget{}, false, err
	}
	if ctxRoot, ok := runtimecontext.RootAgenticReplyTarget(ctx); ok {
		if strings.TrimSpace(root.MessageID) == "" {
			root.MessageID = strings.TrimSpace(ctxRoot.MessageID)
		}
		if strings.TrimSpace(root.CardID) == "" {
			root.CardID = strings.TrimSpace(ctxRoot.CardID)
		}
	}
	if strings.TrimSpace(root.MessageID) != "" {
		return root, true, nil
	}

	target, err := p.resolveReplyTarget(ctx, run)
	if err != nil {
		return replyTarget{}, false, err
	}
	return target, false, nil
}

func (p *ContinuationProcessor) resolveModelReplyTarget(ctx context.Context, run *AgentRun) (replyTarget, error) {
	root, err := p.resolveRootReplyTarget(ctx, run)
	if err != nil {
		return replyTarget{}, err
	}
	if ctxRoot, ok := runtimecontext.RootAgenticReplyTarget(ctx); ok {
		if strings.TrimSpace(root.MessageID) == "" {
			root.MessageID = strings.TrimSpace(ctxRoot.MessageID)
		}
		if strings.TrimSpace(root.CardID) == "" {
			root.CardID = strings.TrimSpace(ctxRoot.CardID)
		}
	}
	if strings.TrimSpace(root.MessageID) != "" || strings.TrimSpace(root.CardID) != "" {
		return root, nil
	}
	return p.resolveLatestModelReplyTarget(ctx, run)
}

func (p *ContinuationProcessor) resolveReplyTarget(ctx context.Context, run *AgentRun) (replyTarget, error) {
	if p == nil || p.coordinator == nil || run == nil {
		return replyTarget{}, nil
	}

	steps, err := p.coordinator.stepRepo.ListByRun(ctx, run.ID)
	if err != nil {
		return replyTarget{}, err
	}
	return NewRunProjection(run, steps).LatestReplyTarget(), nil
}

func (p *ContinuationProcessor) resolveLatestModelReplyTarget(ctx context.Context, run *AgentRun) (replyTarget, error) {
	if p == nil || p.coordinator == nil || run == nil {
		return replyTarget{}, nil
	}

	steps, err := p.coordinator.stepRepo.ListByRun(ctx, run.ID)
	if err != nil {
		return replyTarget{}, err
	}
	return NewRunProjection(run, steps).LatestModelReplyTarget(), nil
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
