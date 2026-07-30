package evaluationindex

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

type ProjectionCursor struct {
	UpdatedAt time.Time
	EpisodeID string
}

type ProjectionSource interface {
	EvaluationSnapshotsAfter(
		context.Context,
		ProjectionCursor,
		int,
	) ([]EvaluationSnapshot, error)
}

type ProjectionProcessor struct {
	source ProjectionSource
	store  *Store
	limit  int

	mu     sync.Mutex
	cursor ProjectionCursor
}

func NewProjectionProcessor(
	source ProjectionSource,
	store *Store,
	limit int,
) (*ProjectionProcessor, error) {
	if source == nil || store == nil {
		return nil, fmt.Errorf("evaluation projection dependency is nil")
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		return nil, fmt.Errorf("evaluation projection batch must not exceed 1000")
	}
	return &ProjectionProcessor{source: source, store: store, limit: limit}, nil
}

func (p *ProjectionProcessor) ProcessNext(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	snapshots, err := p.source.EvaluationSnapshotsAfter(ctx, p.cursor, p.limit)
	if err != nil {
		return err
	}
	for _, snapshot := range snapshots {
		if err := p.store.Upsert(ctx, snapshot); err != nil {
			return err
		}
		p.cursor = ProjectionCursor{
			UpdatedAt: snapshot.UpdatedAt,
			EpisodeID: snapshot.EpisodeID,
		}
	}
	return nil
}

func (p *ProjectionProcessor) Cursor() ProjectionCursor {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cursor
}

type ProjectionWorkerOptions struct {
	Interval   time.Duration
	MaxBackoff time.Duration
}

type ProjectionWorker struct {
	processor *ProjectionProcessor
	options   ProjectionWorkerOptions

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}

	iterations atomic.Int64
	failures   atomic.Int64
	lastError  atomic.Pointer[string]
}

func NewProjectionWorker(
	processor *ProjectionProcessor,
	options ProjectionWorkerOptions,
) (*ProjectionWorker, error) {
	if processor == nil {
		return nil, fmt.Errorf("evaluation projection processor is nil")
	}
	if options.Interval <= 0 {
		options.Interval = 30 * time.Second
	}
	if options.MaxBackoff < options.Interval {
		options.MaxBackoff = 10 * options.Interval
	}
	return &ProjectionWorker{processor: processor, options: options}, nil
}

func (w *ProjectionWorker) Name() string                { return "conversation_evaluation_projection" }
func (w *ProjectionWorker) Critical() bool              { return false }
func (w *ProjectionWorker) Init(context.Context) error  { return nil }
func (w *ProjectionWorker) Ready(context.Context) error { return nil }

func (w *ProjectionWorker) Start(ctx context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cancel != nil {
		return nil
	}
	loopCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	done := make(chan struct{})
	w.cancel = cancel
	w.done = done
	go w.loop(loopCtx, done)
	return nil
}

func (w *ProjectionWorker) Stop(ctx context.Context) error {
	w.mu.Lock()
	cancel := w.cancel
	done := w.done
	if cancel == nil {
		w.mu.Unlock()
		return nil
	}
	cancel()
	w.mu.Unlock()
	select {
	case <-done:
		w.mu.Lock()
		if w.done == done {
			w.cancel = nil
			w.done = nil
		}
		w.mu.Unlock()
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (w *ProjectionWorker) loop(ctx context.Context, done chan struct{}) {
	defer close(done)
	failures := 0
	for {
		err := w.processor.ProcessNext(ctx)
		w.iterations.Add(1)
		delay := w.options.Interval
		if err != nil {
			failures++
			w.failures.Add(1)
			message := err.Error()
			w.lastError.Store(&message)
			delay = projectionBackoff(w.options.Interval, w.options.MaxBackoff, failures)
		} else {
			failures = 0
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func projectionBackoff(interval, maximum time.Duration, failures int) time.Duration {
	delay := interval
	for index := 1; index < failures && delay < maximum; index++ {
		delay *= 2
		if delay >= maximum {
			return maximum
		}
	}
	return delay
}

func (w *ProjectionWorker) Stats() map[string]any {
	cursor := w.processor.Cursor()
	stats := map[string]any{
		"interval":   w.options.Interval.String(),
		"iterations": w.iterations.Load(),
		"failures":   w.failures.Load(),
		"cursor_id":  cursor.EpisodeID,
	}
	if !cursor.UpdatedAt.IsZero() {
		stats["cursor_updated_at"] = cursor.UpdatedAt
	}
	if value := w.lastError.Load(); value != nil {
		stats["last_error"] = *value
	}
	return stats
}

func (w *ProjectionWorker) Cursor() ProjectionCursor {
	if w == nil || w.processor == nil {
		return ProjectionCursor{}
	}
	return w.processor.Cursor()
}
