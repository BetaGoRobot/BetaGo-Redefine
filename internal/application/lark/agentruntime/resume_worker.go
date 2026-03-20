package agentruntime

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	uuid "github.com/satori/go.uuid"
	"go.uber.org/zap"
)

const (
	defaultResumePollTimeout = time.Second
	defaultResumeRunLockTTL  = 30 * time.Second
	defaultResumeRetryDelay  = 200 * time.Millisecond
)

type ResumeProcessor interface {
	ProcessResume(context.Context, ResumeEvent) error
}

type ResumeProcessorFunc func(context.Context, ResumeEvent) error

func (f ResumeProcessorFunc) ProcessResume(ctx context.Context, event ResumeEvent) error {
	return f(ctx, event)
}

type resumeQueueStore interface {
	EnqueueResumeEvent(context.Context, ResumeEvent) error
	DequeueResumeEvent(context.Context, time.Duration) (*ResumeEvent, error)
	AcquireRunLock(context.Context, string, string, time.Duration) (bool, error)
	ReleaseRunLock(context.Context, string, string) (bool, error)
}

type ResumeWorker struct {
	store       resumeQueueStore
	processor   RunProcessor
	lockOwner   string
	pollTimeout time.Duration
	lockTTL     time.Duration
	retryDelay  time.Duration

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu      sync.Mutex
	running bool

	statsMu       sync.Mutex
	processed     int64
	skippedLocked int64
	lastRunID     string
	lastError     string
}

func NewResumeWorker(store resumeQueueStore, processor RunProcessor) *ResumeWorker {
	ctx, cancel := context.WithCancel(context.Background())
	return &ResumeWorker{
		store:       store,
		processor:   processor,
		lockOwner:   "resume_worker_" + uuid.NewV4().String(),
		pollTimeout: defaultResumePollTimeout,
		lockTTL:     defaultResumeRunLockTTL,
		retryDelay:  defaultResumeRetryDelay,
		ctx:         ctx,
		cancel:      cancel,
	}
}

func (w *ResumeWorker) Available() bool {
	return w != nil && w.store != nil && w.processor != nil
}

func (w *ResumeWorker) Start() {
	if w == nil || !w.Available() {
		return
	}

	w.mu.Lock()
	if w.running {
		w.mu.Unlock()
		return
	}
	w.running = true
	w.mu.Unlock()

	w.wg.Add(1)
	go w.run()
}

func (w *ResumeWorker) Stop() {
	if w == nil {
		return
	}

	w.mu.Lock()
	if !w.running {
		w.mu.Unlock()
		return
	}
	w.running = false
	w.cancel()
	w.mu.Unlock()

	w.wg.Wait()
}

func (w *ResumeWorker) Stats() map[string]any {
	if w == nil {
		return nil
	}

	w.statsMu.Lock()
	defer w.statsMu.Unlock()
	return map[string]any{
		"processed":      w.processed,
		"skipped_locked": w.skippedLocked,
		"last_run_id":    w.lastRunID,
		"last_error":     w.lastError,
	}
}

func (w *ResumeWorker) run() {
	defer w.wg.Done()

	for {
		select {
		case <-w.ctx.Done():
			return
		default:
		}

		event, err := w.store.DequeueResumeEvent(w.ctx, w.pollTimeout)
		if err != nil {
			if errors.Is(err, context.Canceled) && w.ctx.Err() != nil {
				return
			}
			w.recordError(err)
			logs.L().Ctx(w.ctx).Warn("agent runtime resume worker dequeue failed", zap.Error(err))
			continue
		}
		if event == nil {
			continue
		}
		w.handleEvent(*event)
	}
}

func (w *ResumeWorker) handleEvent(event ResumeEvent) {
	if err := event.Validate(); err != nil {
		w.recordError(err)
		return
	}

	acquired, err := w.store.AcquireRunLock(w.ctx, event.RunID, w.lockOwner, w.lockTTL)
	if err != nil {
		w.recordError(err)
		logs.L().Ctx(w.ctx).Warn("agent runtime resume worker acquire lock failed",
			zap.Error(err),
			zap.String("run_id", event.RunID),
		)
		return
	}
	if !acquired {
		if err := w.store.EnqueueResumeEvent(w.ctx, event); err != nil {
			w.recordError(err)
			logs.L().Ctx(w.ctx).Warn("agent runtime resume worker requeue failed",
				zap.Error(err),
				zap.String("run_id", event.RunID),
			)
			return
		}
		w.recordSkipped(event.RunID)
		timer := time.NewTimer(w.retryDelay)
		defer timer.Stop()
		select {
		case <-w.ctx.Done():
		case <-timer.C:
		}
		return
	}

	defer func() {
		if _, err := w.store.ReleaseRunLock(w.ctx, event.RunID, w.lockOwner); err != nil && !errors.Is(err, context.Canceled) {
			w.recordError(err)
			logs.L().Ctx(w.ctx).Warn("agent runtime resume worker release lock failed",
				zap.Error(err),
				zap.String("run_id", event.RunID),
			)
		}
	}()

	if err := w.processor.ProcessRun(w.ctx, RunProcessorInput{Resume: &event}); err != nil {
		w.recordError(err)
		logs.L().Ctx(w.ctx).Warn("agent runtime resume worker process failed",
			zap.Error(err),
			zap.String("run_id", event.RunID),
		)
		return
	}

	w.recordProcessed(event.RunID)
}

func (w *ResumeWorker) recordProcessed(runID string) {
	w.statsMu.Lock()
	defer w.statsMu.Unlock()
	w.processed++
	w.lastRunID = runID
	w.lastError = ""
}

func (w *ResumeWorker) recordSkipped(runID string) {
	w.statsMu.Lock()
	defer w.statsMu.Unlock()
	w.skippedLocked++
	w.lastRunID = runID
}

func (w *ResumeWorker) recordError(err error) {
	if w == nil || err == nil {
		return
	}
	w.statsMu.Lock()
	defer w.statsMu.Unlock()
	w.lastError = err.Error()
}
