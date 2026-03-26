package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	uuid "github.com/satori/go.uuid"
	"go.uber.org/zap"
)

var (
	// ErrResumeStateConflict is an exported agent runtime constant.
	ErrResumeStateConflict = errors.New("agent runtime resume state conflict")
	// ErrResumeTokenMismatch is an exported agent runtime constant.
	ErrResumeTokenMismatch = errors.New("agent runtime resume token mismatch")
	// ErrResumeDeferred is an exported agent runtime constant.
	ErrResumeDeferred = errors.New("agent runtime resume deferred")
)

const (
	defaultResumePollTimeout = time.Second
	defaultResumeRunLockTTL  = 30 * time.Second
	defaultResumeRetryDelay  = 200 * time.Millisecond
)

// ResumeSource names a agent runtime type.
type ResumeSource string

const (
	ResumeSourceApproval ResumeSource = "approval"
	ResumeSourceCallback ResumeSource = "callback"
	ResumeSourceSchedule ResumeSource = "schedule"
)

// ResumeEvent carries agent runtime state.
type ResumeEvent struct {
	RunID       string          `json:"run_id"`
	StepID      string          `json:"step_id,omitempty"`
	Revision    int64           `json:"revision"`
	Source      ResumeSource    `json:"source"`
	Token       string          `json:"token,omitempty"`
	Summary     string          `json:"summary,omitempty"`
	PayloadJSON json.RawMessage `json:"payload_json,omitempty"`
	ActorOpenID string          `json:"actor_open_id,omitempty"`
	OccurredAt  time.Time       `json:"occurred_at,omitempty"`
}

type runResumer interface {
	ResumeRun(context.Context, ResumeEvent) (*AgentRun, error)
}

type resumeEnqueuer interface {
	EnqueueResumeEvent(context.Context, ResumeEvent) error
}

// ResumeProcessor defines a agent runtime contract.
type ResumeProcessor interface {
	ProcessResume(context.Context, ResumeEvent) error
}

// ResumeProcessorFunc names a agent runtime type.
type ResumeProcessorFunc func(context.Context, ResumeEvent) error

type resumeQueueStore interface {
	EnqueueResumeEvent(context.Context, ResumeEvent) error
	DequeueResumeEvent(context.Context, time.Duration) (*ResumeEvent, error)
	AcquireRunLock(context.Context, string, string, time.Duration) (bool, error)
	ReleaseRunLock(context.Context, string, string) (bool, error)
}

// ResumeDispatcher carries agent runtime state.
type ResumeDispatcher struct {
	resumer  runResumer
	enqueuer resumeEnqueuer
}

// ResumeWorker carries agent runtime state.
type ResumeWorker struct {
	store       resumeQueueStore
	processor   RunProcessor
	workers     int
	lockOwner   string
	pollTimeout time.Duration
	lockTTL     time.Duration
	retryDelay  time.Duration

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	running atomic.Bool

	statsMu       sync.Mutex
	processed     int64
	skippedLocked int64
	lastRunID     string
	lastError     string
}

// Validate implements agent runtime behavior.
func (e ResumeEvent) Validate() error {
	if strings.TrimSpace(e.RunID) == "" {
		return fmt.Errorf("resume event run_id is required")
	}
	if e.Revision < 0 {
		return fmt.Errorf("resume event revision must be >= 0")
	}
	switch e.Source {
	case ResumeSourceApproval, ResumeSourceCallback, ResumeSourceSchedule:
	default:
		return fmt.Errorf("resume event source is invalid: %q", e.Source)
	}
	if e.Source.requiresToken() && strings.TrimSpace(e.Token) == "" {
		return fmt.Errorf("resume event token is required for source %q", e.Source)
	}
	return nil
}

// WaitingReason implements agent runtime behavior.
func (e ResumeEvent) WaitingReason() WaitingReason {
	switch e.Source {
	case ResumeSourceApproval:
		return WaitingReasonApproval
	case ResumeSourceCallback:
		return WaitingReasonCallback
	case ResumeSourceSchedule:
		return WaitingReasonSchedule
	default:
		return WaitingReasonNone
	}
}

// TriggerType implements agent runtime behavior.
func (e ResumeEvent) TriggerType() TriggerType {
	switch e.Source {
	case ResumeSourceSchedule:
		return TriggerTypeScheduleResume
	case ResumeSourceApproval, ResumeSourceCallback:
		return TriggerTypeCardCallback
	default:
		return TriggerTypeFollowUp
	}
}

// ExternalRef implements agent runtime behavior.
func (e ResumeEvent) ExternalRef() string {
	if stepID := strings.TrimSpace(e.StepID); stepID != "" {
		return stepID
	}
	return string(e.Source)
}

func (s ResumeSource) requiresToken() bool {
	switch s {
	case ResumeSourceApproval, ResumeSourceCallback:
		return true
	default:
		return false
	}
}

// ProcessResume loads the current run projection for a resume event, advances the run state machine, and emits any follow-up reply required by the resumed flow.
func (f ResumeProcessorFunc) ProcessResume(ctx context.Context, event ResumeEvent) error {
	return f(ctx, event)
}

// NewResumeDispatcher builds the resume dispatcher that records resume decisions and enqueues work for asynchronous processing.
func NewResumeDispatcher(resumer runResumer, enqueuer resumeEnqueuer) *ResumeDispatcher {
	return &ResumeDispatcher{
		resumer:  resumer,
		enqueuer: enqueuer,
	}
}

// Dispatch records a resume attempt through the resumer and enqueues the same event for downstream processing when appropriate.
func (d *ResumeDispatcher) Dispatch(ctx context.Context, event ResumeEvent) (*AgentRun, error) {
	if d == nil {
		return nil, fmt.Errorf("resume dispatcher is nil")
	}
	if d.resumer == nil {
		return nil, fmt.Errorf("resume dispatcher resumer is nil")
	}
	if d.enqueuer == nil {
		return nil, fmt.Errorf("resume dispatcher enqueuer is nil")
	}

	run, err := d.resumer.ResumeRun(ctx, event)
	if err != nil {
		return nil, err
	}
	if run == nil && event.Source != ResumeSourceApproval {
		return nil, nil
	}
	if err := d.enqueuer.EnqueueResumeEvent(ctx, event); err != nil {
		return nil, err
	}
	return run, nil
}

// NewResumeWorker constructs the asynchronous worker that drains queued resume events under per-run locks.
func NewResumeWorker(store resumeQueueStore, processor RunProcessor) *ResumeWorker {
	ctx, cancel := context.WithCancel(context.Background())
	return &ResumeWorker{
		store:       store,
		processor:   processor,
		workers:     1,
		lockOwner:   "resume_worker_" + uuid.NewV4().String(),
		pollTimeout: defaultResumePollTimeout,
		lockTTL:     defaultResumeRunLockTTL,
		retryDelay:  defaultResumeRetryDelay,
		ctx:         ctx,
		cancel:      cancel,
	}
}

// Available implements agent runtime behavior.
func (w *ResumeWorker) Available() bool {
	return w != nil && w.store != nil && w.processor != nil
}

func (w *ResumeWorker) WithWorkers(workers int) *ResumeWorker {
	if w == nil {
		return nil
	}
	if workers > 0 {
		w.workers = workers
		return w
	}
	w.workers = 1
	return w
}

// Start launches the resume worker loop in the background.
func (w *ResumeWorker) Start() {
	if w == nil || !w.Available() {
		return
	}
	if !w.running.CompareAndSwap(false, true) {
		return
	}

	workers := w.configuredWorkers()
	w.wg.Add(workers)
	for i := 0; i < workers; i++ {
		go w.run()
	}
}

// Stop stops the resume worker loop and waits for in-flight polling to exit.
func (w *ResumeWorker) Stop() {
	if w == nil {
		return
	}
	if !w.running.CompareAndSwap(true, false) {
		return
	}
	w.cancel()

	w.wg.Wait()
}

// Stats implements agent runtime behavior.
func (w *ResumeWorker) Stats() map[string]any {
	if w == nil {
		return nil
	}

	w.statsMu.Lock()
	defer w.statsMu.Unlock()
	return map[string]any{
		"workers":        int64(w.configuredWorkers()),
		"processed":      w.processed,
		"skipped_locked": w.skippedLocked,
		"last_run_id":    w.lastRunID,
		"last_error":     w.lastError,
	}
}

func (w *ResumeWorker) configuredWorkers() int {
	if w == nil || w.workers <= 0 {
		return 5
	}
	return w.workers
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
		if errors.Is(err, ErrResumeDeferred) || errors.Is(err, ErrRunSlotOccupied) {
			if err := w.store.EnqueueResumeEvent(w.ctx, event); err != nil {
				w.recordError(err)
				logs.L().Ctx(w.ctx).Warn("agent runtime resume worker requeue deferred event failed",
					zap.Error(err),
					zap.String("run_id", event.RunID),
				)
				return
			}
			timer := time.NewTimer(w.retryDelay)
			defer timer.Stop()
			select {
			case <-w.ctx.Done():
			case <-timer.C:
			}
			return
		}
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
