package agentruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	approvaldef "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime/approval"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	uuid "github.com/satori/go.uuid"
)

const defaultActiveRunTTL = 30 * time.Minute

// StartShadowRunRequest describes the observed message that should create a new
// runtime run or attach to an existing one.
type StartShadowRunRequest struct {
	ChatID           string      `json:"chat_id"`
	ActorOpenID      string      `json:"actor_open_id,omitempty"`
	TriggerType      TriggerType `json:"trigger_type"`
	TriggerMessageID string      `json:"trigger_message_id,omitempty"`
	TriggerEventID   string      `json:"trigger_event_id,omitempty"`
	AttachToRunID    string      `json:"attach_to_run_id,omitempty"`
	SupersedeRunID   string      `json:"supersede_run_id,omitempty"`
	InputText        string      `json:"input_text,omitempty"`
	Goal             string      `json:"goal,omitempty"`
	Now              time.Time   `json:"now"`
}

// ShadowRunStarter is implemented by components that can create the initial run
// record for an observed message.
type ShadowRunStarter interface {
	StartShadowRun(context.Context, StartShadowRunRequest) (*AgentRun, error)
}

type sessionRepository interface {
	FindOrCreateChatSession(ctx context.Context, appID, botOpenID, chatID string) (*AgentSession, error)
	GetByID(ctx context.Context, id string) (*AgentSession, error)
	SetActiveRun(ctx context.Context, sessionID, runID, lastMessageID, lastActorOpenID string, updatedAt time.Time) error
}

type runRepository interface {
	Create(ctx context.Context, run *AgentRun) error
	GetByID(ctx context.Context, id string) (*AgentRun, error)
	FindByTriggerMessage(ctx context.Context, sessionID, triggerMessageID string) (*AgentRun, error)
	FindLatestActiveBySessionActor(ctx context.Context, sessionID, actorOpenID string) (*AgentRun, error)
	CountActiveBySessionActor(ctx context.Context, sessionID, actorOpenID string) (int64, error)
	UpdateStatus(ctx context.Context, runID string, fromRevision int64, mutate func(*AgentRun) error) (*AgentRun, error)
}

type stepRepository interface {
	Append(ctx context.Context, step *AgentStep) error
	ListByRun(ctx context.Context, runID string) ([]*AgentStep, error)
	UpdateStatus(ctx context.Context, stepID string, fromStatus StepStatus, mutate func(*AgentStep) error) (*AgentStep, error)
}

type coordinationStore interface {
	ActiveChatRun(ctx context.Context, chatID string) (string, error)
	SwapActiveChatRun(ctx context.Context, chatID, expectedRunID, newRunID string, ttl time.Duration) (bool, error)
	ActiveActorChatRun(ctx context.Context, chatID, actorOpenID string) (string, error)
	SwapActiveActorChatRun(ctx context.Context, chatID, actorOpenID, expectedRunID, newRunID string, ttl time.Duration) (bool, error)
	NextCancelGeneration(ctx context.Context, runID string) (int64, error)
	NotifyPendingInitialRun(ctx context.Context, chatID, actorOpenID string) error
	SaveApprovalReservation(ctx context.Context, stepID, token string, payload []byte, ttl time.Duration) error
	LoadApprovalReservation(ctx context.Context, stepID, token string) ([]byte, error)
	RecordApprovalReservationDecision(ctx context.Context, stepID, token string, decisionPayload []byte) ([]byte, error)
	ConsumeApprovalReservation(ctx context.Context, stepID, token string) ([]byte, error)
}

// RunCoordinator is the persistence-facing runtime coordinator. It owns session
// lookup, run creation, status transitions, approval lifecycle persistence, and
// active-slot bookkeeping in the coordination store.
type RunCoordinator struct {
	sessionRepo               sessionRepository
	runRepo                   runRepository
	stepRepo                  stepRepository
	runtimeStore              coordinationStore
	identity                  botidentity.Identity
	activeRunTTL              time.Duration
	maxActiveRunsPerActorChat int64
}

// NewRunCoordinator constructs the central runtime coordinator that owns run/session persistence, approval transitions, and slot bookkeeping.
func NewRunCoordinator(sessionRepo sessionRepository, runRepo runRepository, stepRepo stepRepository, runtimeStore coordinationStore, identity botidentity.Identity) *RunCoordinator {
	return &RunCoordinator{
		sessionRepo:               sessionRepo,
		runRepo:                   runRepo,
		stepRepo:                  stepRepo,
		runtimeStore:              runtimeStore,
		identity:                  identity,
		activeRunTTL:              defaultActiveRunTTL,
		maxActiveRunsPerActorChat: DefaultMaxActiveRunsPerActorChat,
	}
}

// StartShadowRun creates or reuses a queued runtime run for an observed message and seeds the initial decide step that will drive later processing.
func (c *RunCoordinator) StartShadowRun(ctx context.Context, req StartShadowRunRequest) (*AgentRun, error) {
	if c == nil {
		return nil, fmt.Errorf("run coordinator is nil")
	}
	if req.Now.IsZero() {
		req.Now = time.Now().UTC()
	}

	if attachRunID := strings.TrimSpace(req.AttachToRunID); attachRunID != "" {
		return c.attachShadowRun(ctx, attachRunID, req)
	}

	session, err := c.sessionRepo.FindOrCreateChatSession(ctx, c.identity.AppID, c.identity.BotOpenID, req.ChatID)
	if err != nil {
		return nil, err
	}

	if existing, err := c.runRepo.FindByTriggerMessage(ctx, session.ID, req.TriggerMessageID); err != nil {
		return nil, err
	} else if existing != nil {
		return existing, nil
	}

	supersedeRunID := strings.TrimSpace(req.SupersedeRunID)
	if supersedeRunID != "" {
		if err := c.CancelRun(ctx, supersedeRunID, "superseded"); err != nil {
			return nil, err
		}
	}
	if err := c.ensureActiveRunCapacity(ctx, session, req.ActorOpenID); err != nil {
		return nil, err
	}

	run := &AgentRun{
		ID:               newRuntimeID("run"),
		SessionID:        session.ID,
		TriggerType:      req.TriggerType,
		TriggerMessageID: strings.TrimSpace(req.TriggerMessageID),
		TriggerEventID:   strings.TrimSpace(req.TriggerEventID),
		ActorOpenID:      strings.TrimSpace(req.ActorOpenID),
		Status:           RunStatusQueued,
		Goal:             strings.TrimSpace(req.Goal),
		InputText:        strings.TrimSpace(req.InputText),
		CurrentStepIndex: 0,
		Revision:         0,
		CreatedAt:        req.Now,
		UpdatedAt:        req.Now,
	}
	seedQueuedRunLiveness(run, req.Now, DefaultRunLeasePolicy())
	if err := c.runRepo.Create(ctx, run); err != nil {
		if existing, findErr := c.runRepo.FindByTriggerMessage(ctx, session.ID, req.TriggerMessageID); findErr == nil && existing != nil {
			return existing, nil
		}
		return nil, err
	}

	step := &AgentStep{
		ID:        newRuntimeID("step"),
		RunID:     run.ID,
		Index:     0,
		Kind:      StepKindDecide,
		Status:    StepStatusQueued,
		CreatedAt: req.Now,
	}
	if err := c.stepRepo.Append(ctx, step); err != nil {
		return nil, err
	}

	if err := c.sessionRepo.SetActiveRun(ctx, session.ID, run.ID, req.TriggerMessageID, req.ActorOpenID, req.Now); err != nil {
		return nil, err
	}

	return run, nil
}

type attachedDecideStepInput struct {
	TriggerType      TriggerType `json:"trigger_type,omitempty"`
	TriggerMessageID string      `json:"trigger_message_id,omitempty"`
	TriggerEventID   string      `json:"trigger_event_id,omitempty"`
	ActorOpenID      string      `json:"actor_open_id,omitempty"`
	InputText        string      `json:"input_text,omitempty"`
}

func (c *RunCoordinator) attachShadowRun(ctx context.Context, runID string, req StartShadowRunRequest) (*AgentRun, error) {
	run, err := c.runRepo.GetByID(ctx, runID)
	if err != nil {
		return nil, err
	}
	if run == nil {
		return nil, fmt.Errorf("attach target run not found: %s", runID)
	}
	if run.Status.IsTerminal() {
		return run, nil
	}

	session, err := c.sessionRepo.GetByID(ctx, run.SessionID)
	if err != nil {
		return nil, err
	}
	if session != nil {
		reqChatID := strings.TrimSpace(req.ChatID)
		if reqChatID != "" && strings.TrimSpace(session.ChatID) != "" && reqChatID != strings.TrimSpace(session.ChatID) {
			return nil, fmt.Errorf("attach target session chat mismatch: want=%s got=%s", session.ChatID, reqChatID)
		}
		if strings.TrimSpace(req.TriggerMessageID) != "" &&
			strings.TrimSpace(session.ActiveRunID) == strings.TrimSpace(run.ID) &&
			strings.TrimSpace(session.LastMessageID) == strings.TrimSpace(req.TriggerMessageID) {
			return run, nil
		}
	}

	nextIndex := run.CurrentStepIndex + 1
	inputJSON, err := json.Marshal(attachedDecideStepInput{
		TriggerType:      req.TriggerType,
		TriggerMessageID: strings.TrimSpace(req.TriggerMessageID),
		TriggerEventID:   strings.TrimSpace(req.TriggerEventID),
		ActorOpenID:      strings.TrimSpace(req.ActorOpenID),
		InputText:        strings.TrimSpace(req.InputText),
	})
	if err != nil {
		return nil, err
	}

	updated, err := c.runRepo.UpdateStatus(ctx, run.ID, run.Revision, func(current *AgentRun) error {
		if current.Status.IsTerminal() {
			return fmt.Errorf("attach target run is terminal: %s", current.Status)
		}
		current.Status = RunStatusQueued
		current.CurrentStepIndex = nextIndex
		current.WaitingReason = WaitingReasonNone
		current.WaitingToken = ""
		current.InputText = strings.TrimSpace(req.InputText)
		if actorOpenID := strings.TrimSpace(req.ActorOpenID); actorOpenID != "" {
			current.ActorOpenID = actorOpenID
		}
		current.ErrorText = ""
		current.UpdatedAt = req.Now
		seedQueuedRunLiveness(current, req.Now, DefaultRunLeasePolicy())
		return nil
	})
	if err != nil {
		return nil, err
	}

	if err := c.stepRepo.Append(ctx, &AgentStep{
		ID:        newRuntimeID("step"),
		RunID:     updated.ID,
		Index:     nextIndex,
		Kind:      StepKindDecide,
		Status:    StepStatusQueued,
		InputJSON: inputJSON,
		CreatedAt: req.Now,
	}); err != nil {
		return nil, err
	}

	if session != nil {
		if err := c.sessionRepo.SetActiveRun(ctx, session.ID, updated.ID, req.TriggerMessageID, req.ActorOpenID, req.Now); err != nil {
			return nil, err
		}
	}

	return updated, nil
}

// CancelRun cancels an existing run, clears any active slot state, and preserves a terminal cancellation reason for later inspection.
func (c *RunCoordinator) CancelRun(ctx context.Context, runID, reason string) error {
	if c == nil || strings.TrimSpace(runID) == "" {
		return nil
	}

	run, err := c.runRepo.GetByID(ctx, runID)
	if err != nil {
		return err
	}
	if run.Status.IsTerminal() {
		return nil
	}

	now := time.Now().UTC()
	if _, err := c.runRepo.UpdateStatus(ctx, run.ID, run.Revision, func(current *AgentRun) error {
		current.Status = RunStatusCancelled
		current.WaitingReason = WaitingReasonNone
		current.WaitingToken = ""
		current.ErrorText = strings.TrimSpace(reason)
		current.FinishedAt = &now
		current.UpdatedAt = now
		clearRunExecutionLiveness(current)
		return nil
	}); err != nil {
		return err
	}

	return c.clearCancelledRunState(ctx, run.ID, run.SessionID, run.ActorOpenID, now)
}

// RequestApproval persists an approval-request step, moves the run into waiting-approval state, and returns the rendered approval request payload.
func (c *RunCoordinator) RequestApproval(ctx context.Context, input RequestApprovalInput) (*ApprovalRequest, error) {
	if c == nil {
		return nil, fmt.Errorf("run coordinator is nil")
	}

	requestedAt := input.RequestedAt
	if requestedAt.IsZero() {
		requestedAt = time.Now().UTC()
	} else {
		requestedAt = requestedAt.UTC()
	}
	if err := input.Validate(requestedAt); err != nil {
		return nil, err
	}

	run, err := c.runRepo.GetByID(ctx, strings.TrimSpace(input.RunID))
	if err != nil {
		return nil, err
	}
	token := newRuntimeID("approval")
	stepID := newRuntimeID("step")

	updated, err := c.moveRunToWaitingApproval(ctx, run, run.Revision, token, requestedAt)
	if err != nil {
		return nil, err
	}

	request := approvaldef.BuildApprovalRequest(input, stepID, token, updated.Revision, requestedAt)
	if err := request.Validate(requestedAt); err != nil {
		return nil, err
	}

	step, err := newCompletedApprovalStep(updated.ID, stepID, updated.CurrentStepIndex, token, request, requestedAt)
	if err != nil {
		return nil, err
	}
	if err := c.stepRepo.Append(ctx, step); err != nil {
		return nil, err
	}

	return &request, nil
}

// RejectApproval records an approval rejection and cancels the waiting run so no further continuation work is scheduled.
func (c *RunCoordinator) RejectApproval(ctx context.Context, event ResumeEvent) (*AgentRun, error) {
	if c == nil {
		return nil, fmt.Errorf("run coordinator is nil")
	}
	if err := event.Validate(); err != nil {
		return nil, err
	}
	if event.Source != ResumeSourceApproval {
		return nil, fmt.Errorf("%w: source=%s", ErrApprovalStateConflict, event.Source)
	}

	now := event.OccurredAt
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}

	run, err := c.runRepo.GetByID(ctx, strings.TrimSpace(event.RunID))
	if err != nil {
		return nil, err
	}
	if run != nil && !run.Status.IsTerminal() && run.Status != RunStatusWaitingApproval {
		recorded, err := c.recordReservedApprovalDecision(ctx, run, event, ApprovalReservationDecisionRejected, now)
		if err != nil {
			return nil, err
		}
		if recorded != nil {
			return nil, nil
		}
	}
	if err := c.validateApprovalResume(ctx, run, event, now); err != nil {
		return nil, err
	}

	updated, err := c.runRepo.UpdateStatus(ctx, strings.TrimSpace(event.RunID), c.resumeRevisionForEvent(run, event), func(current *AgentRun) error {
		if current.Status.IsTerminal() {
			return fmt.Errorf("%w: run status=%s", ErrApprovalStateConflict, current.Status)
		}
		if current.Status != RunStatusWaitingApproval {
			return fmt.Errorf("%w: run status=%s", ErrApprovalStateConflict, current.Status)
		}
		if current.WaitingReason != WaitingReasonApproval {
			return fmt.Errorf("%w: waiting reason=%s", ErrApprovalStateConflict, current.WaitingReason)
		}
		if strings.TrimSpace(current.WaitingToken) == "" {
			return fmt.Errorf("%w: waiting token missing", ErrApprovalStateConflict)
		}
		if strings.TrimSpace(event.Token) != strings.TrimSpace(current.WaitingToken) {
			return ErrResumeTokenMismatch
		}

		current.Status = RunStatusCancelled
		current.WaitingReason = WaitingReasonNone
		current.WaitingToken = ""
		current.ErrorText = "approval_rejected"
		current.UpdatedAt = now
		current.FinishedAt = &now
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := c.clearCancelledRunState(ctx, updated.ID, updated.SessionID, updated.ActorOpenID, now); err != nil {
		return nil, err
	}
	return updated, nil
}

// LoadApprovalRequest implements agent runtime behavior.
func (c *RunCoordinator) LoadApprovalRequest(ctx context.Context, runID, stepID string) (*ApprovalRequest, error) {
	if c == nil {
		return nil, fmt.Errorf("run coordinator is nil")
	}

	run, err := c.runRepo.GetByID(ctx, strings.TrimSpace(runID))
	if err != nil {
		return nil, err
	}
	if run == nil {
		return nil, fmt.Errorf("%w: approval run not found", ErrApprovalStateConflict)
	}
	steps, err := c.stepRepo.ListByRun(ctx, run.ID)
	if err != nil {
		return nil, err
	}

	var approvalStep *AgentStep
	for _, step := range steps {
		if step == nil || step.Kind != StepKindApprovalRequest {
			continue
		}
		if strings.TrimSpace(stepID) == "" || step.ID == strings.TrimSpace(stepID) {
			approvalStep = step
			break
		}
	}
	if approvalStep == nil {
		reservation, err := c.loadApprovalReservation(ctx, strings.TrimSpace(stepID), "")
		if err != nil {
			return nil, err
		}
		if reservation == nil {
			return nil, fmt.Errorf("%w: approval step not found", ErrApprovalStateConflict)
		}
		request := reservation.ApprovalRequest(run.Revision)
		return &request, nil
	}

	request, err := approvaldef.DecodeApprovalRequest(run.ID, approvalStep.ID, run.Revision, approvalStep.ExternalRef, approvalStep.InputJSON)
	if err != nil {
		return nil, fmt.Errorf("decode approval step state: %w", err)
	}
	return &request, nil
}

// ResumeRun validates a resume event, updates the waiting run back into queued execution, and returns the run when execution should continue immediately.
func (c *RunCoordinator) ResumeRun(ctx context.Context, event ResumeEvent) (*AgentRun, error) {
	if c == nil {
		return nil, fmt.Errorf("run coordinator is nil")
	}
	if err := event.Validate(); err != nil {
		return nil, err
	}

	now := event.OccurredAt
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}

	run, err := c.runRepo.GetByID(ctx, strings.TrimSpace(event.RunID))
	if err != nil {
		return nil, err
	}
	if event.Source == ResumeSourceApproval {
		if run != nil && !run.Status.IsTerminal() && run.Status != RunStatusWaitingApproval {
			recorded, err := c.recordReservedApprovalDecision(ctx, run, event, ApprovalReservationDecisionApproved, now)
			if err != nil {
				return nil, err
			}
			if recorded != nil {
				return nil, nil
			}
		}
		if err := c.validateApprovalResume(ctx, run, event, now); err != nil {
			return nil, err
		}
	}

	updated, err := c.runRepo.UpdateStatus(ctx, strings.TrimSpace(event.RunID), c.resumeRevisionForEvent(run, event), func(current *AgentRun) error {
		if current.Status.IsTerminal() {
			return fmt.Errorf("%w: run status=%s", ErrResumeStateConflict, current.Status)
		}
		expectedStatus := waitingRunStatusForResumeSource(event.Source)
		if current.Status != expectedStatus {
			return fmt.Errorf("%w: run status=%s source=%s", ErrResumeStateConflict, current.Status, event.Source)
		}
		if current.WaitingReason != event.WaitingReason() {
			return fmt.Errorf("%w: waiting reason=%s source=%s", ErrResumeStateConflict, current.WaitingReason, event.Source)
		}
		expectedToken := strings.TrimSpace(current.WaitingToken)
		if event.Source.requiresToken() {
			if expectedToken == "" {
				return fmt.Errorf("%w: waiting token missing for source=%s", ErrResumeStateConflict, event.Source)
			}
			if strings.TrimSpace(event.Token) != expectedToken {
				return ErrResumeTokenMismatch
			}
		} else if expectedToken != "" && strings.TrimSpace(event.Token) != expectedToken {
			return ErrResumeTokenMismatch
		}

		current.Status = RunStatusQueued
		current.WaitingReason = WaitingReasonNone
		current.WaitingToken = ""
		current.CurrentStepIndex++
		current.UpdatedAt = now
		seedQueuedRunLiveness(current, now, DefaultRunLeasePolicy())
		return nil
	})
	if err != nil {
		return nil, err
	}

	step := &AgentStep{
		ID:          newRuntimeID("step"),
		RunID:       updated.ID,
		Index:       updated.CurrentStepIndex,
		Kind:        StepKindResume,
		Status:      StepStatusQueued,
		ExternalRef: event.ExternalRef(),
		CreatedAt:   now,
	}
	if err := c.stepRepo.Append(ctx, step); err != nil {
		return nil, err
	}

	return updated, nil
}

func (c *RunCoordinator) validateApprovalResume(ctx context.Context, run *AgentRun, event ResumeEvent, now time.Time) error {
	if c == nil || run == nil {
		return nil
	}

	steps, err := c.stepRepo.ListByRun(ctx, run.ID)
	if err != nil {
		return err
	}
	currentStep := findStepByIndex(steps, run.CurrentStepIndex)
	if currentStep == nil || currentStep.Kind != StepKindApprovalRequest {
		return fmt.Errorf("%w: approval step missing for run=%s", ErrResumeStateConflict, run.ID)
	}
	if stepID := strings.TrimSpace(event.StepID); stepID != "" && currentStep.ID != stepID {
		return fmt.Errorf("%w: approval step_id=%s event_step_id=%s", ErrResumeStateConflict, currentStep.ID, stepID)
	}

	request, err := approvaldef.DecodeApprovalRequest(run.ID, currentStep.ID, run.Revision, currentStep.ExternalRef, currentStep.InputJSON)
	if err != nil {
		return fmt.Errorf("decode approval step state: %w", err)
	}
	if !request.ExpiresAt.IsZero() && !request.ExpiresAt.UTC().After(now.UTC()) {
		return ErrApprovalExpired
	}
	return nil
}

func (c *RunCoordinator) resumeRevisionForEvent(run *AgentRun, event ResumeEvent) int64 {
	if event.Source == ResumeSourceApproval && run != nil {
		return run.Revision
	}
	return event.Revision
}

func (c *RunCoordinator) loadApprovalReservation(ctx context.Context, stepID, token string) (*ApprovalReservation, error) {
	if c == nil || c.runtimeStore == nil {
		return nil, nil
	}
	raw, err := c.runtimeStore.LoadApprovalReservation(ctx, strings.TrimSpace(stepID), strings.TrimSpace(token))
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, nil
	}
	reservation, err := approvaldef.DecodeApprovalReservation(raw)
	if err != nil {
		return nil, fmt.Errorf("decode approval reservation: %w", err)
	}
	return &reservation, nil
}

func (c *RunCoordinator) recordReservedApprovalDecision(
	ctx context.Context,
	run *AgentRun,
	event ResumeEvent,
	outcome ApprovalReservationDecisionOutcome,
	now time.Time,
) (*ApprovalReservation, error) {
	if c == nil || c.runtimeStore == nil || run == nil || event.Source != ResumeSourceApproval {
		return nil, nil
	}

	reservation, err := c.loadApprovalReservation(ctx, strings.TrimSpace(event.StepID), strings.TrimSpace(event.Token))
	if err != nil {
		return nil, err
	}
	if reservation == nil {
		return nil, nil
	}
	if reservation.RunID != strings.TrimSpace(run.ID) {
		return nil, fmt.Errorf("%w: approval reservation run mismatch", ErrApprovalStateConflict)
	}
	if err := reservation.Validate(now); err != nil {
		return nil, err
	}

	decision := approvaldef.NewApprovalReservationDecision(outcome, strings.TrimSpace(event.ActorOpenID), now)
	if err := decision.Validate(); err != nil {
		return nil, err
	}
	rawDecision, err := json.Marshal(decision)
	if err != nil {
		return nil, err
	}
	raw, err := c.runtimeStore.RecordApprovalReservationDecision(ctx, strings.TrimSpace(event.StepID), strings.TrimSpace(event.Token), rawDecision)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, nil
	}
	recorded, err := approvaldef.DecodeApprovalReservation(raw)
	if err != nil {
		return nil, fmt.Errorf("decode recorded approval reservation: %w", err)
	}
	if recorded.Decision != nil && recorded.Decision.Outcome != outcome {
		return nil, fmt.Errorf("%w: approval reservation already decided as %s", ErrApprovalStateConflict, recorded.Decision.Outcome)
	}
	return &recorded, nil
}

func (c *RunCoordinator) clearCancelledRunState(ctx context.Context, runID, sessionID, actorOpenID string, now time.Time) error {
	if c == nil {
		return nil
	}

	session, err := c.sessionRepo.GetByID(ctx, sessionID)
	if err != nil {
		return err
	}
	if session != nil && session.ActiveRunID == runID {
		if err := c.sessionRepo.SetActiveRun(ctx, session.ID, "", "", "", now); err != nil {
			return err
		}
	}

	if c.runtimeStore != nil {
		if _, err := c.runtimeStore.NextCancelGeneration(ctx, runID); err != nil {
			return err
		}
		if session != nil && strings.TrimSpace(session.ChatID) != "" && strings.TrimSpace(actorOpenID) != "" {
			swapped, err := c.runtimeStore.SwapActiveActorChatRun(ctx, session.ChatID, actorOpenID, runID, "", c.activeRunTTL)
			if err != nil {
				return err
			}
			if !swapped {
				current, err := c.runtimeStore.ActiveActorChatRun(ctx, session.ChatID, actorOpenID)
				if err != nil {
					return err
				}
				if current == runID {
					return fmt.Errorf("active actor chat slot still points to cancelled run: chat_id=%s actor_open_id=%s run_id=%s", session.ChatID, actorOpenID, runID)
				}
			}
			_ = c.runtimeStore.NotifyPendingInitialRun(ctx, session.ChatID, actorOpenID)
			TriggerPendingScopeSweep()
		}
	}
	return nil
}

func (c *RunCoordinator) currentActiveRunID(ctx context.Context, session *AgentSession, chatID, actorOpenID string) (string, error) {
	if session == nil || strings.TrimSpace(actorOpenID) == "" {
		return "", nil
	}
	activeRun, err := c.runRepo.FindLatestActiveBySessionActor(ctx, session.ID, actorOpenID)
	if err != nil || activeRun == nil {
		return "", err
	}
	return strings.TrimSpace(activeRun.ID), nil
}

func sessionChatID(session *AgentSession) string {
	if session == nil {
		return ""
	}
	return strings.TrimSpace(session.ChatID)
}

func coalesceChatID(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// ActiveRunSnapshot implements agent runtime behavior.
func (c *RunCoordinator) ActiveRunSnapshot(ctx context.Context, chatID, actorOpenID string) (*ActiveRunSnapshot, error) {
	if c == nil {
		return nil, nil
	}
	runID, err := c.currentActiveRunID(ctx, nil, chatID, actorOpenID)
	if err != nil {
		return nil, err
	}
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return nil, nil
	}
	run, err := c.runRepo.GetByID(ctx, runID)
	if err != nil {
		return nil, err
	}
	if run == nil || run.Status.IsTerminal() {
		return nil, nil
	}
	return &ActiveRunSnapshot{
		ID:           run.ID,
		ActorOpenID:  strings.TrimSpace(run.ActorOpenID),
		Status:       run.Status,
		LastActiveAt: run.UpdatedAt,
	}, nil
}

func (c *RunCoordinator) ensureActiveRunCapacity(ctx context.Context, session *AgentSession, actorOpenID string) error {
	if c == nil || c.runRepo == nil || session == nil {
		return nil
	}
	actorOpenID = strings.TrimSpace(actorOpenID)
	if actorOpenID == "" || c.maxActiveRunsPerActorChat <= 0 {
		return nil
	}
	count, err := c.runRepo.CountActiveBySessionActor(ctx, session.ID, actorOpenID)
	if err != nil {
		return err
	}
	if count >= c.maxActiveRunsPerActorChat {
		return ErrActiveRunLimitExceeded
	}
	return nil
}

func newRuntimeID(prefix string) string {
	return prefix + "_" + uuid.NewV4().String()
}

func waitingRunStatusForResumeSource(source ResumeSource) RunStatus {
	switch source {
	case ResumeSourceApproval:
		return RunStatusWaitingApproval
	case ResumeSourceCallback:
		return RunStatusWaitingCallback
	case ResumeSourceSchedule:
		return RunStatusWaitingSchedule
	default:
		return RunStatusQueued
	}
}
