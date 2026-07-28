package agentruntime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

type ConversationExecutor interface {
	Submit(context.Context, string, func(context.Context) error) error
}

type ContinuationRunner interface {
	ProcessRun(context.Context, string) error
}

// ContinuationCatalog is deliberately read-only. Claiming a continuation step
// remains inside ContinuationRunner, after an executor slot has been accepted.
type ContinuationCatalog interface {
	FindRunChatID(context.Context, string) (string, error)
	ListDueContinuationRunIDs(context.Context, int) ([]string, error)
}

type ScheduleInteractionResolver interface {
	Resolve(context.Context, ScheduleInteractionRequest) (ScheduleInteractionOutcome, error)
}

type ProjectionSubmitter interface {
	SubmitNext(context.Context) error
}

type RuntimeOptions struct {
	ConversationExecutor        ConversationExecutor
	CallbackContinuationEnabled func(context.Context, string) bool
}

type RuntimeDependencies struct {
	InteractionStarter InteractionStarter
	ScheduleResolver   ScheduleInteractionResolver
	EnabledProcessor   ContinuationRunner
	DisabledProcessor  ContinuationRunner
	Catalog            ContinuationCatalog
	Projector          ProjectionSubmitter
	Expirer            InteractionExpirer
}

type Runtime struct {
	executor                    ConversationExecutor
	callbackContinuationEnabled func(context.Context, string) bool

	mu   sync.RWMutex
	deps *RuntimeDependencies

	inFlight sync.Map
}

func NewRuntime(opts RuntimeOptions) (*Runtime, error) {
	if isNilRuntimeDependency(opts.ConversationExecutor) {
		return nil, errors.New("conversation executor is nil")
	}
	if opts.CallbackContinuationEnabled == nil {
		return nil, errors.New("callback continuation gate is nil")
	}
	return &Runtime{
		executor:                    opts.ConversationExecutor,
		callbackContinuationEnabled: opts.CallbackContinuationEnabled,
	}, nil
}

// Bind installs production dependencies after infrastructure initialization.
// It is one-shot so request handlers never observe a partially replaced graph.
func (r *Runtime) Bind(deps RuntimeDependencies) error {
	if r == nil {
		return errors.New("conversation runtime is nil")
	}
	if isNilRuntimeDependency(deps.InteractionStarter) ||
		isNilRuntimeDependency(deps.ScheduleResolver) ||
		isNilRuntimeDependency(deps.EnabledProcessor) ||
		isNilRuntimeDependency(deps.DisabledProcessor) ||
		isNilRuntimeDependency(deps.Catalog) ||
		isNilRuntimeDependency(deps.Projector) ||
		isNilRuntimeDependency(deps.Expirer) {
		return errors.New("conversation runtime dependencies are incomplete")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.deps != nil {
		return errors.New("conversation runtime is already bound")
	}
	copied := deps
	r.deps = &copied
	return nil
}

func (r *Runtime) StartScheduleEdit(
	ctx context.Context,
	req StartScheduleEditRequest,
) (*RuntimeEnvelope, error) {
	deps, err := r.dependencies()
	if err != nil {
		return nil, err
	}
	return deps.InteractionStarter.StartScheduleEdit(ctx, req)
}

func (r *Runtime) Resolve(
	ctx context.Context,
	req ScheduleInteractionRequest,
) (ScheduleInteractionOutcome, error) {
	deps, err := r.dependencies()
	if err != nil {
		return ScheduleInteractionOutcome{}, err
	}
	return deps.ScheduleResolver.Resolve(ctx, req)
}

func (r *Runtime) SubmitRun(ctx context.Context, runID string) error {
	_, err := r.submitRun(ctx, runID)
	return err
}

func (r *Runtime) submitRun(ctx context.Context, runID string) (bool, error) {
	if strings.TrimSpace(runID) != runID || runID == "" {
		return false, ErrInvalidRuntimeContract
	}
	deps, err := r.dependencies()
	if err != nil {
		return false, err
	}
	if _, loaded := r.inFlight.LoadOrStore(runID, struct{}{}); loaded {
		return false, nil
	}
	err = r.executor.Submit(ctx, "conversation-run:"+runID, func(taskCtx context.Context) error {
		defer r.inFlight.Delete(runID)
		chatID, err := deps.Catalog.FindRunChatID(taskCtx, runID)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				return nil
			}
			return err
		}
		runner := deps.DisabledProcessor
		if r.callbackContinuationEnabled(taskCtx, chatID) {
			runner = deps.EnabledProcessor
		}
		return runner.ProcessRun(taskCtx, runID)
	})
	if err != nil {
		r.inFlight.Delete(runID)
		return false, err
	}
	return true, nil
}

func (r *Runtime) SubmitDueContinuations(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		return 0, ErrInvalidRuntimeContract
	}
	deps, err := r.dependencies()
	if err != nil {
		return 0, err
	}
	runIDs, err := deps.Catalog.ListDueContinuationRunIDs(ctx, limit)
	if err != nil {
		return 0, err
	}
	submitted := 0
	for _, runID := range runIDs {
		accepted, submitErr := r.submitRun(ctx, runID)
		if submitErr != nil {
			// In particular, bounded executor backpressure stops this polling
			// round. No step has been claimed, so PostgreSQL remains retryable.
			return submitted, submitErr
		}
		if accepted {
			submitted++
		}
	}
	return submitted, nil
}

func (r *Runtime) SubmitNextProjection(ctx context.Context) error {
	deps, err := r.dependencies()
	if err != nil {
		return err
	}
	err = deps.Projector.SubmitNext(ctx)
	return err
}

func (r *Runtime) ExpireInteractions(ctx context.Context, now time.Time, limit int) (int, error) {
	deps, err := r.dependencies()
	if err != nil {
		return 0, err
	}
	return deps.Expirer.ExpireScheduleEditInteractions(ctx, now, limit)
}

func (r *Runtime) dependencies() (*RuntimeDependencies, error) {
	if r == nil {
		return nil, errors.New("conversation runtime is nil")
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.deps == nil {
		return nil, errors.New("conversation runtime is not bound")
	}
	return r.deps, nil
}

func (r *Runtime) String() string {
	if _, err := r.dependencies(); err != nil {
		return "conversation-runtime(unbound)"
	}
	return fmt.Sprintf("conversation-runtime(%p)", r)
}
