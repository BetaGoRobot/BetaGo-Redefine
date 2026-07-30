package agentcard

import (
	"context"
	"errors"
	"sync"
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
		return 0, err
	}
	processed := 0
	for _, target := range targets {
		if err := r.processors[processorIndex].Process(
			ctx,
			target.SurfaceID,
			target.Revision,
		); err != nil {
			continue
		}
		processed++
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
	return nil
}
