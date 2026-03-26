package initialcore

import (
	"context"
	"errors"
	"time"
)

// Store defines a initial-run core logic contract.
type Store interface {
	DequeuePendingInitialScope(context.Context, time.Duration) (string, string, error)
	ConsumePendingInitialRun(context.Context, string, string) ([]byte, int64, error)
	PrependPendingInitialRun(context.Context, string, string, []byte) error
	NotifyPendingInitialRun(context.Context, string, string) error
	ClearPendingInitialScopeIfEmpty(context.Context, string, string) error
	AcquirePendingInitialScopeLock(context.Context, string, string, string, time.Duration) (bool, error)
	ReleasePendingInitialScopeLock(context.Context, string, string, string) (bool, error)
}

// Scope carries initial-run core logic state.
type Scope struct {
	ChatID      string
	ActorOpenID string
}

// WorkerCallbacks carries initial-run core logic state.
type WorkerCallbacks[T any] struct {
	Decode       func([]byte) (T, error)
	Validate     func(T) error
	ApplyContext func(context.Context, T) context.Context
	MarkStarted  func(context.Context, T) error
	Process      func(context.Context, T) error
	RequestedAt  func(T) time.Time
	Retryable    func(error) bool
	Now          func() time.Time

	OnWakeupConsumed func()
	OnProcessed      func(Scope, time.Duration)
	OnLockSkipped    func(Scope)
	OnError          func(Scope, error)
	OnRequeued       func(Scope)
	OnScopeCleared   func(Scope)
}

// Worker carries initial-run core logic state.
type Worker[T any] struct {
	store       Store
	callbacks   WorkerCallbacks[T]
	lockOwner   string
	pollTimeout time.Duration
	lockTTL     time.Duration
	retryDelay  time.Duration

	ctx    context.Context
	cancel context.CancelFunc
}

// NewWorker implements initial-run core logic behavior.
func NewWorker[T any](store Store, callbacks WorkerCallbacks[T], lockOwner string, pollTimeout, lockTTL, retryDelay time.Duration) *Worker[T] {
	ctx, cancel := context.WithCancel(context.Background())
	return &Worker[T]{
		store:       store,
		callbacks:   callbacks,
		lockOwner:   lockOwner,
		pollTimeout: pollTimeout,
		lockTTL:     lockTTL,
		retryDelay:  retryDelay,
		ctx:         ctx,
		cancel:      cancel,
	}
}

// Available implements initial-run core logic behavior.
func (w *Worker[T]) Available() bool {
	return w != nil && w.store != nil && w.callbacks.Process != nil
}

// Run implements initial-run core logic behavior.
func (w *Worker[T]) Run() {
	if w == nil || !w.Available() {
		return
	}
	for {
		select {
		case <-w.ctx.Done():
			return
		default:
		}

		chatID, actorOpenID, err := w.store.DequeuePendingInitialScope(w.ctx, w.pollTimeout)
		if err != nil {
			if errors.Is(err, context.Canceled) && w.ctx.Err() != nil {
				return
			}
			w.recordError(Scope{}, err)
			continue
		}
		if chatID == "" || actorOpenID == "" {
			continue
		}
		scope := Scope{ChatID: chatID, ActorOpenID: actorOpenID}
		if w.callbacks.OnWakeupConsumed != nil {
			w.callbacks.OnWakeupConsumed()
		}
		w.handleScope(scope)
	}
}

// Stop stops the resume worker loop and waits for in-flight polling to exit.
func (w *Worker[T]) Stop() {
	if w == nil || w.cancel == nil {
		return
	}
	w.cancel()
}

func (w *Worker[T]) handleScope(scope Scope) {
	var shouldRetry bool

	acquired, err := w.store.AcquirePendingInitialScopeLock(w.ctx, scope.ChatID, scope.ActorOpenID, w.lockOwner, w.lockTTL)
	if err != nil {
		w.recordError(scope, err)
		return
	}
	if !acquired {
		if w.callbacks.OnLockSkipped != nil {
			w.callbacks.OnLockSkipped(scope)
		}
		return
	}

	defer func() {
		if _, err := w.store.ReleasePendingInitialScopeLock(w.ctx, scope.ChatID, scope.ActorOpenID, w.lockOwner); err != nil && !errors.Is(err, context.Canceled) {
			w.recordError(scope, err)
		}
		if shouldRetry {
			if err := w.store.NotifyPendingInitialRun(w.ctx, scope.ChatID, scope.ActorOpenID); err != nil && !errors.Is(err, context.Canceled) {
				w.recordError(scope, err)
			}
		}
	}()

	raw, remaining, err := w.store.ConsumePendingInitialRun(w.ctx, scope.ChatID, scope.ActorOpenID)
	if err != nil {
		w.recordError(scope, err)
		return
	}
	if len(raw) == 0 {
		w.clearScopeIfEmpty(scope)
		return
	}

	item, err := w.callbacks.Decode(raw)
	if err != nil {
		if remaining == 0 {
			w.clearScopeIfEmpty(scope)
		}
		w.recordError(scope, err)
		return
	}
	if w.callbacks.Validate != nil {
		if err := w.callbacks.Validate(item); err != nil {
			if remaining == 0 {
				w.clearScopeIfEmpty(scope)
			}
			w.recordError(scope, err)
			return
		}
	}

	runCtx := w.ctx
	if w.callbacks.ApplyContext != nil {
		runCtx = w.callbacks.ApplyContext(runCtx, item)
	}
	if w.callbacks.MarkStarted != nil {
		if err := w.callbacks.MarkStarted(runCtx, item); err != nil {
			w.recordError(scope, err)
		}
	}

	startedAt := w.now()
	waitDuration := time.Duration(0)
	if w.callbacks.RequestedAt != nil {
		if requestedAt := w.callbacks.RequestedAt(item); !requestedAt.IsZero() {
			waitDuration = startedAt.Sub(requestedAt)
			if waitDuration < 0 {
				waitDuration = 0
			}
		}
	}

	if err := w.callbacks.Process(runCtx, item); err != nil {
		if prependErr := w.store.PrependPendingInitialRun(w.ctx, scope.ChatID, scope.ActorOpenID, raw); prependErr != nil {
			w.recordError(scope, prependErr)
			return
		}
		if w.callbacks.OnRequeued != nil {
			w.callbacks.OnRequeued(scope)
		}
		if w.callbacks.Retryable != nil && w.callbacks.Retryable(err) {
			timer := time.NewTimer(w.retryDelay)
			defer timer.Stop()
			select {
			case <-w.ctx.Done():
			case <-timer.C:
			}
			shouldRetry = true
			if w.callbacks.OnLockSkipped != nil {
				w.callbacks.OnLockSkipped(scope)
			}
			return
		}
		w.recordError(scope, err)
		return
	}

	if remaining == 0 {
		w.clearScopeIfEmpty(scope)
	}
	if w.callbacks.OnProcessed != nil {
		w.callbacks.OnProcessed(scope, waitDuration)
	}
}

func (w *Worker[T]) clearScopeIfEmpty(scope Scope) {
	if err := w.store.ClearPendingInitialScopeIfEmpty(w.ctx, scope.ChatID, scope.ActorOpenID); err != nil && !errors.Is(err, context.Canceled) {
		w.recordError(scope, err)
		return
	}
	if w.callbacks.OnScopeCleared != nil {
		w.callbacks.OnScopeCleared(scope)
	}
}

func (w *Worker[T]) recordError(scope Scope, err error) {
	if w.callbacks.OnError != nil {
		w.callbacks.OnError(scope, err)
	}
}

func (w *Worker[T]) now() time.Time {
	if w.callbacks.Now != nil {
		return w.callbacks.Now()
	}
	return time.Now()
}
