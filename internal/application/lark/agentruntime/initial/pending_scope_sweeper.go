package initial

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	initialcore "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime/initialcore"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"go.uber.org/zap"
)

const (
	defaultSweepInterval = 5 * time.Second
	defaultSweepCount    = 100
	defaultMaxExecution  = 2
)

// PendingScope carries initial-run flow state.
type PendingScope struct {
	ChatID      string
	ActorOpenID string
}

// SweepStore defines a initial-run flow contract.
type SweepStore interface {
	ListPendingInitialScopes(context.Context, uint64, int64) ([]PendingScope, uint64, error)
	PendingInitialRunCount(context.Context, string, string) (int64, error)
	ActiveExecutionLeaseCount(context.Context, string, string) (int64, error)
	NotifyPendingInitialRun(context.Context, string, string) error
	ClearPendingInitialScopeIfEmpty(context.Context, string, string) error
}

// Sweeper carries initial-run flow state.
type Sweeper struct {
	store      SweepStore
	metrics    *PendingInitialMetrics
	interval   time.Duration
	scanCount  int64
	triggerCh  chan struct{}
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	running    atomic.Bool
	statsMu    sync.Mutex
	ticks      int64
	scanned    int64
	reschedule int64
	busySkip   int64
	cleared    int64
	last       lastActivity
}

// NewPendingScopeSweeper implements initial-run flow behavior.
func NewPendingScopeSweeper(store SweepStore) *Sweeper {
	return NewPendingScopeSweeperWithMetrics(store, nil)
}

// NewPendingScopeSweeperWithMetrics implements initial-run flow behavior.
func NewPendingScopeSweeperWithMetrics(store SweepStore, metrics *PendingInitialMetrics) *Sweeper {
	ctx, cancel := context.WithCancel(context.Background())
	return &Sweeper{
		store:     store,
		metrics:   metrics,
		interval:  defaultSweepInterval,
		scanCount: defaultSweepCount,
		triggerCh: make(chan struct{}, 1),
		ctx:       ctx,
		cancel:    cancel,
	}
}

// Available implements initial-run flow behavior.
func (w *Sweeper) Available() bool {
	return w != nil && w.store != nil
}

// Start launches the resume worker loop in the background.
func (w *Sweeper) Start() {
	if w == nil || !w.Available() {
		return
	}
	if !w.running.CompareAndSwap(false, true) {
		return
	}
	w.wg.Add(1)
	go w.run()
}

// Stop stops the resume worker loop and waits for in-flight polling to exit.
func (w *Sweeper) Stop() {
	if w == nil {
		return
	}
	if !w.running.CompareAndSwap(true, false) {
		return
	}
	w.cancel()
	w.wg.Wait()
}

// Stats implements initial-run flow behavior.
func (w *Sweeper) Stats() map[string]any {
	if w == nil {
		return nil
	}
	w.statsMu.Lock()
	defer w.statsMu.Unlock()
	stats := map[string]any{
		"ticks":         w.ticks,
		"scanned":       w.scanned,
		"rescheduled":   w.reschedule,
		"busy_skipped":  w.busySkip,
		"stale_cleared": w.cleared,
	}
	for key, value := range w.last.stats() {
		stats[key] = value
	}
	return stats
}

func (w *Sweeper) run() {
	defer w.wg.Done()
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-w.ctx.Done():
			return
		case <-w.triggerCh:
			w.sweepOnce(w.ctx)
		case <-ticker.C:
			w.sweepOnce(w.ctx)
		}
	}
}

// Trigger implements initial-run flow behavior.
func (w *Sweeper) Trigger() {
	if w == nil {
		return
	}
	select {
	case w.triggerCh <- struct{}{}:
	default:
	}
}

func (w *Sweeper) sweepOnce(ctx context.Context) {
	if w == nil || w.store == nil {
		return
	}

	w.statsMu.Lock()
	w.ticks++
	w.statsMu.Unlock()
	if w.metrics != nil {
		w.metrics.IncSweepTick()
	}

	var cursor uint64
	for {
		scopes, nextCursor, err := w.store.ListPendingInitialScopes(ctx, cursor, w.scanCount)
		if err != nil {
			if errors.Is(err, context.Canceled) && ctx.Err() != nil {
				return
			}
			w.recordError("", "", err)
			if w.metrics != nil {
				w.metrics.IncSweepErrors()
			}
			logs.L().Ctx(ctx).Warn("agent runtime pending scope sweeper list failed", zap.Error(err))
			return
		}
		for _, scope := range scopes {
			w.handleScope(ctx, scope)
		}
		if nextCursor == 0 {
			return
		}
		cursor = nextCursor
	}
}

func (w *Sweeper) handleScope(ctx context.Context, scope PendingScope) {
	chatID := scope.ChatID
	actorOpenID := scope.ActorOpenID

	count, err := w.store.PendingInitialRunCount(ctx, chatID, actorOpenID)
	if err != nil {
		w.recordError(chatID, actorOpenID, err)
		return
	}

	w.statsMu.Lock()
	w.scanned++
	w.last.record(chatID, actorOpenID, nil)
	w.statsMu.Unlock()
	if w.metrics != nil {
		w.metrics.AddSweepScopesScanned(1)
	}

	action := initialcore.DecideWakeupAction(initialcore.WakeupDecisionInput{
		PendingCount:         count,
		ActiveExecutionCount: activeExecutionCountOrZero(w, ctx, chatID, actorOpenID),
		MaxExecutionPerScope: defaultMaxExecution,
	})

	switch action {
	case initialcore.WakeupActionClear:
		if err := w.store.ClearPendingInitialScopeIfEmpty(ctx, chatID, actorOpenID); err != nil && !(errors.Is(err, context.Canceled) && ctx.Err() != nil) {
			w.recordError(chatID, actorOpenID, err)
			if w.metrics != nil {
				w.metrics.IncSweepErrors()
			}
			return
		}
		w.statsMu.Lock()
		w.cleared++
		w.last.record(chatID, actorOpenID, nil)
		w.statsMu.Unlock()
		if w.metrics != nil {
			w.metrics.IncSweepStaleCleared()
		}
		return
	case initialcore.WakeupActionSkipBusy:
		w.statsMu.Lock()
		w.busySkip++
		w.last.record(chatID, actorOpenID, nil)
		w.statsMu.Unlock()
		if w.metrics != nil {
			w.metrics.IncSweepBusySkipped()
		}
		return
	}

	if err := w.store.NotifyPendingInitialRun(ctx, chatID, actorOpenID); err != nil && !(errors.Is(err, context.Canceled) && ctx.Err() != nil) {
		w.recordError(chatID, actorOpenID, err)
		if w.metrics != nil {
			w.metrics.IncSweepErrors()
		}
		return
	}
	w.statsMu.Lock()
	w.reschedule++
	w.last.record(chatID, actorOpenID, nil)
	w.statsMu.Unlock()
	if w.metrics != nil {
		w.metrics.IncSweepRescheduled()
		w.metrics.IncWakeupEmitted()
	}
}

func (w *Sweeper) recordError(chatID, actorOpenID string, err error) {
	if w == nil || err == nil {
		return
	}
	w.statsMu.Lock()
	defer w.statsMu.Unlock()
	w.last.record(chatID, actorOpenID, err)
}

func activeExecutionCountOrZero(w *Sweeper, ctx context.Context, chatID, actorOpenID string) int64 {
	if w == nil || w.store == nil {
		return 0
	}
	count, err := w.store.ActiveExecutionLeaseCount(ctx, chatID, actorOpenID)
	if err != nil {
		w.recordError(chatID, actorOpenID, err)
		if w.metrics != nil {
			w.metrics.IncSweepErrors()
		}
		return defaultMaxExecution
	}
	return count
}
