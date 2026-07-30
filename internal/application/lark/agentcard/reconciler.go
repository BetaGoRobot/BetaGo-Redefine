package agentcard

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

type PatchProcessor interface {
	Process(context.Context, string, int64) error
}

type PatchReconcilerOptions struct {
	Catalog    PatchCatalog
	Processors []PatchProcessor
	BatchSize  int
	Interval   time.Duration
	Now        func() time.Time
}

type PatchReconciler struct {
	catalog    PatchCatalog
	processors []PatchProcessor
	batchSize  int
	interval   time.Duration
	now        func() time.Time

	mu     sync.Mutex
	cancel context.CancelFunc
	wg     sync.WaitGroup

	running       atomic.Bool
	scanned       atomic.Uint64
	completed     atomic.Uint64
	skipped       atomic.Uint64
	failed        atomic.Uint64
	statusMu      sync.RWMutex
	lastError     string
	lastSuccessAt time.Time
	lastFailureAt time.Time
}

func NewPatchReconciler(
	options PatchReconcilerOptions,
) (*PatchReconciler, error) {
	if options.Catalog == nil || len(options.Processors) == 0 {
		return nil, errors.New("patch catalog and processors are required")
	}
	for _, processor := range options.Processors {
		if processor == nil {
			return nil, errors.New("patch processor is required")
		}
	}
	if options.BatchSize <= 0 {
		options.BatchSize = 32
	}
	if options.Interval <= 0 {
		options.Interval = time.Second
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &PatchReconciler{
		catalog:    options.Catalog,
		processors: append([]PatchProcessor(nil), options.Processors...),
		batchSize:  options.BatchSize, interval: options.Interval,
		now: options.Now,
	}, nil
}

func (r *PatchReconciler) ReconcileOnce(
	ctx context.Context,
	processorIndex int,
) (int, error) {
	if r == nil || processorIndex < 0 ||
		processorIndex >= len(r.processors) {
		return 0, errors.New("invalid patch reconciler processor")
	}
	targets, err := r.catalog.ListDuePatches(
		ctx,
		r.now().UTC(),
		r.batchSize,
	)
	if err != nil {
		r.recordFailure(err)
		return 0, err
	}
	r.scanned.Add(uint64(len(targets)))
	processed := 0
	hadFailure := false
	for _, target := range targets {
		if err := r.processors[processorIndex].Process(
			ctx,
			target.SurfaceID,
			target.Revision,
		); err != nil {
			if errors.Is(err, ErrCardNotFound) ||
				errors.Is(err, ErrCardConflict) {
				r.skipped.Add(1)
				continue
			}
			hadFailure = true
			r.recordFailure(err)
			continue
		}
		processed++
		r.completed.Add(1)
	}
	if !hadFailure {
		r.recordSuccess()
	}
	return processed, nil
}

func (r *PatchReconciler) Start(ctx context.Context) error {
	if r == nil {
		return errors.New("patch reconciler is nil")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cancel != nil {
		return errors.New("patch reconciler is already started")
	}
	workerCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	r.cancel = cancel
	r.running.Store(true)
	for index := range r.processors {
		r.wg.Add(1)
		go r.run(workerCtx, index)
	}
	return nil
}

func (r *PatchReconciler) run(ctx context.Context, processorIndex int) {
	defer r.wg.Done()
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		_, _ = r.ReconcileOnce(ctx, processorIndex)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (r *PatchReconciler) Stop(context.Context) error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	cancel := r.cancel
	r.cancel = nil
	r.mu.Unlock()
	if cancel != nil {
		cancel()
		r.wg.Wait()
	}
	r.running.Store(false)
	return nil
}

func (r *PatchReconciler) recordFailure(err error) {
	if r == nil || err == nil {
		return
	}
	message := err.Error()
	if len(message) > 1024 {
		message = message[:1024]
	}
	r.failed.Add(1)
	r.statusMu.Lock()
	r.lastError = message
	r.lastFailureAt = r.now().UTC()
	r.statusMu.Unlock()
}

func (r *PatchReconciler) recordSuccess() {
	if r == nil {
		return
	}
	r.statusMu.Lock()
	r.lastError = ""
	r.lastSuccessAt = r.now().UTC()
	r.statusMu.Unlock()
}

func (r *PatchReconciler) Stats() map[string]any {
	if r == nil {
		return map[string]any{"running": false}
	}
	r.statusMu.RLock()
	lastError := r.lastError
	lastSuccessAt := r.lastSuccessAt
	lastFailureAt := r.lastFailureAt
	r.statusMu.RUnlock()
	return map[string]any{
		"running": r.running.Load(), "workers": len(r.processors),
		"scanned": r.scanned.Load(), "completed": r.completed.Load(),
		"skipped": r.skipped.Load(), "failed": r.failed.Load(),
		"last_error": lastError, "last_success_at": lastSuccessAt,
		"last_failure_at": lastFailureAt,
	}
}

func (r *PatchReconciler) Health() (bool, string) {
	if r == nil {
		return false, "agent card patch reconciler unavailable"
	}
	r.statusMu.RLock()
	defer r.statusMu.RUnlock()
	if r.lastError != "" {
		return false, r.lastError
	}
	return true, ""
}
