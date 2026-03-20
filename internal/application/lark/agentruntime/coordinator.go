package agentruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	uuid "github.com/satori/go.uuid"
)

const defaultActiveRunTTL = 30 * time.Minute

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
	NextCancelGeneration(ctx context.Context, runID string) (int64, error)
}

type RunCoordinator struct {
	sessionRepo  sessionRepository
	runRepo      runRepository
	stepRepo     stepRepository
	runtimeStore coordinationStore
	identity     botidentity.Identity
	activeRunTTL time.Duration
}

func NewRunCoordinator(sessionRepo sessionRepository, runRepo runRepository, stepRepo stepRepository, runtimeStore coordinationStore, identity botidentity.Identity) *RunCoordinator {
	return &RunCoordinator{
		sessionRepo:  sessionRepo,
		runRepo:      runRepo,
		stepRepo:     stepRepo,
		runtimeStore: runtimeStore,
		identity:     identity,
		activeRunTTL: defaultActiveRunTTL,
	}
}

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
	if supersedeRunID == "" {
		supersedeRunID, err = c.currentActiveRunID(ctx, session, req.ChatID)
		if err != nil {
			return nil, err
		}
	}
	if supersedeRunID != "" {
		if err := c.CancelRun(ctx, supersedeRunID, "superseded"); err != nil {
			return nil, err
		}
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

	if c.runtimeStore != nil && strings.TrimSpace(req.ChatID) != "" {
		swapped, err := c.runtimeStore.SwapActiveChatRun(ctx, req.ChatID, "", run.ID, c.activeRunTTL)
		if err != nil {
			return nil, err
		}
		if !swapped {
			current, err := c.runtimeStore.ActiveChatRun(ctx, req.ChatID)
			if err != nil {
				return nil, err
			}
			if current != run.ID {
				return nil, fmt.Errorf("active chat slot already occupied: chat_id=%s run_id=%s", req.ChatID, current)
			}
		}
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
	if err := c.refreshActiveRunSlot(ctx, coalesceChatID(strings.TrimSpace(req.ChatID), sessionChatID(session)), updated.ID); err != nil {
		return nil, err
	}

	return updated, nil
}

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
		return nil
	}); err != nil {
		return err
	}

	return c.clearCancelledRunState(ctx, run.ID, run.SessionID, now)
}

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
	if err := validateRequestApprovalInput(input, requestedAt); err != nil {
		return nil, err
	}

	run, err := c.runRepo.GetByID(ctx, strings.TrimSpace(input.RunID))
	if err != nil {
		return nil, err
	}
	token := newRuntimeID("approval")
	stepID := newRuntimeID("step")

	updated, err := c.runRepo.UpdateStatus(ctx, run.ID, run.Revision, func(current *AgentRun) error {
		if current.Status != RunStatusRunning {
			return fmt.Errorf("%w: run status=%s", ErrApprovalStateConflict, current.Status)
		}
		current.Status = RunStatusWaitingApproval
		current.WaitingReason = WaitingReasonApproval
		current.WaitingToken = token
		current.CurrentStepIndex++
		current.UpdatedAt = requestedAt
		return nil
	})
	if err != nil {
		return nil, err
	}

	request := approvalRequestFromInput(input, stepID, token, updated.Revision, requestedAt)
	if err := request.Validate(requestedAt); err != nil {
		return nil, err
	}

	stepInput, err := marshalApprovalStepState(request)
	if err != nil {
		return nil, err
	}
	step := &AgentStep{
		ID:          stepID,
		RunID:       updated.ID,
		Index:       updated.CurrentStepIndex,
		Kind:        StepKindApprovalRequest,
		Status:      StepStatusCompleted,
		InputJSON:   stepInput,
		ExternalRef: token,
		CreatedAt:   requestedAt,
		StartedAt:   &requestedAt,
		FinishedAt:  &requestedAt,
	}
	if err := c.stepRepo.Append(ctx, step); err != nil {
		return nil, err
	}

	return &request, nil
}

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
	if err := c.validateApprovalResume(ctx, run, event, now); err != nil {
		return nil, err
	}

	updated, err := c.runRepo.UpdateStatus(ctx, strings.TrimSpace(event.RunID), event.Revision, func(current *AgentRun) error {
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
	if err := c.clearCancelledRunState(ctx, updated.ID, updated.SessionID, now); err != nil {
		return nil, err
	}
	return updated, nil
}

func (c *RunCoordinator) LoadApprovalRequest(ctx context.Context, runID, stepID string) (*ApprovalRequest, error) {
	if c == nil {
		return nil, fmt.Errorf("run coordinator is nil")
	}

	run, err := c.runRepo.GetByID(ctx, strings.TrimSpace(runID))
	if err != nil {
		return nil, err
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
		return nil, fmt.Errorf("%w: approval step not found", ErrApprovalStateConflict)
	}

	state, err := unmarshalApprovalStepState(approvalStep.InputJSON)
	if err != nil {
		return nil, fmt.Errorf("decode approval step state: %w", err)
	}
	return &ApprovalRequest{
		RunID:          run.ID,
		StepID:         approvalStep.ID,
		Revision:       run.Revision,
		ApprovalType:   state.ApprovalType,
		Title:          state.Title,
		Summary:        state.Summary,
		CapabilityName: state.CapabilityName,
		Token:          approvalStep.ExternalRef,
		RequestedAt:    state.RequestedAt,
		ExpiresAt:      state.ExpiresAt,
	}, nil
}

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
		if err := c.validateApprovalResume(ctx, run, event, now); err != nil {
			return nil, err
		}
	}

	updated, err := c.runRepo.UpdateStatus(ctx, strings.TrimSpace(event.RunID), event.Revision, func(current *AgentRun) error {
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

	state, err := unmarshalApprovalStepState(currentStep.InputJSON)
	if err != nil {
		return fmt.Errorf("decode approval step state: %w", err)
	}
	if !state.ExpiresAt.IsZero() && !state.ExpiresAt.UTC().After(now.UTC()) {
		return ErrApprovalExpired
	}
	return nil
}

func (c *RunCoordinator) clearCancelledRunState(ctx context.Context, runID, sessionID string, now time.Time) error {
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
		if session != nil && strings.TrimSpace(session.ChatID) != "" {
			swapped, err := c.runtimeStore.SwapActiveChatRun(ctx, session.ChatID, runID, "", c.activeRunTTL)
			if err != nil {
				return err
			}
			if !swapped {
				current, err := c.runtimeStore.ActiveChatRun(ctx, session.ChatID)
				if err != nil {
					return err
				}
				if current == runID {
					return fmt.Errorf("active chat slot still points to cancelled run: chat_id=%s run_id=%s", session.ChatID, runID)
				}
			}
		}
	}
	return nil
}

func (c *RunCoordinator) currentActiveRunID(ctx context.Context, session *AgentSession, chatID string) (string, error) {
	if c.runtimeStore != nil && strings.TrimSpace(chatID) != "" {
		activeRunID, err := c.runtimeStore.ActiveChatRun(ctx, chatID)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(activeRunID) != "" {
			return activeRunID, nil
		}
	}
	if session == nil {
		return "", nil
	}
	return strings.TrimSpace(session.ActiveRunID), nil
}

func (c *RunCoordinator) refreshActiveRunSlot(ctx context.Context, chatID, runID string) error {
	if c == nil || c.runtimeStore == nil || strings.TrimSpace(chatID) == "" || strings.TrimSpace(runID) == "" {
		return nil
	}

	swapped, err := c.runtimeStore.SwapActiveChatRun(ctx, chatID, runID, runID, c.activeRunTTL)
	if err != nil {
		return err
	}
	if swapped {
		return nil
	}

	current, err := c.runtimeStore.ActiveChatRun(ctx, chatID)
	if err != nil {
		return err
	}
	current = strings.TrimSpace(current)
	switch current {
	case runID:
		return nil
	case "":
		swapped, err = c.runtimeStore.SwapActiveChatRun(ctx, chatID, "", runID, c.activeRunTTL)
		if err != nil {
			return err
		}
		if swapped {
			return nil
		}
		current, err = c.runtimeStore.ActiveChatRun(ctx, chatID)
		if err != nil {
			return err
		}
		if strings.TrimSpace(current) == runID {
			return nil
		}
	}

	return fmt.Errorf("active chat slot mismatch during attach: chat_id=%s current=%s expected=%s", chatID, current, runID)
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

func (c *RunCoordinator) ActiveRunSnapshot(ctx context.Context, chatID string) (*ActiveRunSnapshot, error) {
	if c == nil {
		return nil, nil
	}
	runID, err := c.currentActiveRunID(ctx, nil, chatID)
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
