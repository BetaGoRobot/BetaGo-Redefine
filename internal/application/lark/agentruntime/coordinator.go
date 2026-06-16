package agentruntime

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"
)

var ErrNotFound = errors.New("agent runtime record not found")

type ScopeType string

const (
	ScopeTypeChat   ScopeType = "chat"
	ScopeTypeThread ScopeType = "thread"
)

type Store interface {
	GetOrCreateSession(context.Context, *AgentSession) (*AgentSession, error)
	FindRunBySessionAndTriggerMessage(context.Context, string, string) (*AgentRun, error)
	CreateRun(context.Context, *AgentRun) error
	UpdateSessionActiveRun(context.Context, string, string, string, string) (*AgentSession, error)
	CreateStep(context.Context, *AgentStep) error
}

type RunCoordinator struct {
	store Store
}

type StartRunRequest struct {
	AppID            string
	BotOpenID        string
	ChatID           string
	ScopeType        ScopeType
	ScopeID          string
	TriggerType      TriggerType
	TriggerMessageID string
	TriggerEventID   string
	ActorOpenID      string
	ParentRunID      string
	Goal             string
	InputText        string
}

type StartRunResult struct {
	Session *AgentSession
	Run     *AgentRun
}

func NewRunCoordinator(store Store) *RunCoordinator {
	return &RunCoordinator{store: store}
}

func (c *RunCoordinator) StartShadowRun(ctx context.Context, req StartRunRequest) (*StartRunResult, error) {
	if c == nil || c.store == nil {
		return nil, errors.New("agent runtime store is nil")
	}
	session, err := c.store.GetOrCreateSession(ctx, newSession(req))
	if err != nil {
		return nil, err
	}
	if req.TriggerMessageID != "" {
		existing, findErr := c.store.FindRunBySessionAndTriggerMessage(ctx, session.ID, req.TriggerMessageID)
		if findErr == nil {
			return &StartRunResult{Session: session, Run: existing}, nil
		}
		if !errors.Is(findErr, ErrNotFound) {
			return nil, findErr
		}
	}

	run := NewRun(NewRunRequest{
		SessionID:        session.ID,
		TriggerType:      req.TriggerType,
		TriggerMessageID: req.TriggerMessageID,
		TriggerEventID:   req.TriggerEventID,
		ActorOpenID:      req.ActorOpenID,
		ParentRunID:      req.ParentRunID,
		Goal:             req.Goal,
		InputText:        req.InputText,
	})
	if err := c.store.CreateRun(ctx, run); err != nil {
		return nil, err
	}
	step := NewStep(NewStepRequest{
		RunID:          run.ID,
		Index:          0,
		Kind:           StepKindPlan,
		CapabilityName: "shadow",
		InputJSON:      "{}",
	})
	step.Status = StepStatusCompleted
	step.StartedAt = step.CreatedAt
	step.FinishedAt = time.Now()
	if err := c.store.CreateStep(ctx, step); err != nil {
		return nil, err
	}
	session, err = c.store.UpdateSessionActiveRun(ctx, session.ID, run.ID, req.TriggerMessageID, req.ActorOpenID)
	if err != nil {
		return nil, err
	}
	return &StartRunResult{Session: session, Run: run}, nil
}

func newSession(req StartRunRequest) *AgentSession {
	scopeType := req.ScopeType
	if scopeType == "" {
		scopeType = ScopeTypeChat
	}
	scopeID := req.ScopeID
	if scopeID == "" {
		scopeID = req.ChatID
	}
	now := time.Now()
	return &AgentSession{
		ID:              deterministicSessionID(req.AppID, req.BotOpenID, req.ChatID, string(scopeType), scopeID),
		AppID:           req.AppID,
		BotOpenID:       req.BotOpenID,
		ChatID:          req.ChatID,
		ScopeType:       string(scopeType),
		ScopeID:         scopeID,
		Status:          "active",
		LastMessageID:   req.TriggerMessageID,
		LastActorOpenID: req.ActorOpenID,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
}

func deterministicSessionID(appID, botOpenID, chatID, scopeType, scopeID string) string {
	raw := fmt.Sprintf("%s:%s:%s:%s:%s", appID, botOpenID, chatID, scopeType, scopeID)
	sum := sha256.Sum256([]byte(raw))
	return fmt.Sprintf("session_%x", sum[:16])
}

func cloneSession(src *AgentSession) *AgentSession {
	if src == nil {
		return nil
	}
	cloned := *src
	return &cloned
}

func cloneRun(src *AgentRun) *AgentRun {
	if src == nil {
		return nil
	}
	cloned := *src
	return &cloned
}

func cloneStep(src *AgentStep) *AgentStep {
	if src == nil {
		return nil
	}
	cloned := *src
	return &cloned
}
