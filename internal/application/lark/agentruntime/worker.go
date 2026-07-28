package agentruntime

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	appruntime "github.com/BetaGoRobot/BetaGo-Redefine/internal/runtime"
)

const workerDegradedFailureThreshold = 3

type conversationWorkerRuntime interface {
	SubmitDueContinuations(context.Context, int) (int, error)
	ExpireInteractions(context.Context, time.Time, int) (int, error)
}

type projectionWorkerRuntime interface {
	SubmitNextProjection(context.Context) error
}

type ConversationWorkerOptions struct {
	Interval   time.Duration
	MaxBackoff time.Duration
	BatchSize  int
	Now        func() time.Time
	Jitter     func(time.Duration) time.Duration
}

type ProjectionWorkerOptions struct {
	Interval   time.Duration
	MaxBackoff time.Duration
	BatchSize  int
	Jitter     func(time.Duration) time.Duration
}

type workerState struct {
	mu sync.RWMutex

	cancel context.CancelFunc
	done   chan struct{}

	running       bool
	stopping      bool
	iterations    int64
	failures      int64
	lastSubmitted int
	lastExpired   int
	lastSuccessAt time.Time
	lastError     string
	terminalError string
}

type ConversationWorker struct {
	runtime conversationWorkerRuntime
	options ConversationWorkerOptions
	state   workerState
}

func NewConversationWorker(
	runtime conversationWorkerRuntime,
	options ConversationWorkerOptions,
) (*ConversationWorker, error) {
	if isNilRuntimeDependency(runtime) {
		return nil, errors.New("conversation worker runtime is nil")
	}
	if options.Interval <= 0 || options.BatchSize <= 0 || options.BatchSize > 1024 {
		return nil, ErrInvalidRuntimeContract
	}
	normalizeWorkerTiming(&options.MaxBackoff, options.Interval, &options.Jitter)
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	return &ConversationWorker{runtime: runtime, options: options}, nil
}

func (w *ConversationWorker) Name() string                { return "conversation_runtime_worker" }
func (w *ConversationWorker) Critical() bool              { return false }
func (w *ConversationWorker) Init(context.Context) error  { return nil }
func (w *ConversationWorker) Ready(context.Context) error { return nil }

func (w *ConversationWorker) Start(ctx context.Context) error {
	if w == nil {
		return errors.New("conversation worker is nil")
	}
	return w.state.start(ctx, func(loopCtx context.Context) {
		w.loop(loopCtx)
	})
}

func (w *ConversationWorker) Stop(ctx context.Context) error {
	if w == nil {
		return nil
	}
	return w.state.stop(ctx)
}

func (w *ConversationWorker) loop(ctx context.Context) {
	failures := 0
	for {
		err := w.runOnce(ctx)
		if err != nil {
			failures++
		} else {
			failures = 0
		}
		if !waitWorker(ctx, w.nextDelay(failures)) {
			return
		}
	}
}

func (w *ConversationWorker) runOnce(ctx context.Context) error {
	now := w.options.Now().UTC()
	expired, expireErr := w.runtime.ExpireInteractions(ctx, now, w.options.BatchSize)
	submitted, submitErr := w.runtime.SubmitDueContinuations(ctx, w.options.BatchSize)
	err := errors.Join(expireErr, submitErr)
	w.state.record(err, submitted, expired, now)
	return err
}

func (w *ConversationWorker) nextDelay(failures int) time.Duration {
	return workerDelay(w.options.Interval, w.options.MaxBackoff, failures, w.options.Jitter)
}

func (w *ConversationWorker) Stats() map[string]any {
	if w == nil {
		return nil
	}
	return w.state.stats(w.options.BatchSize, w.options.Interval)
}

func (w *ConversationWorker) DynamicHealth() (appruntime.State, string) {
	if w == nil {
		return appruntime.StateDegraded, "conversation worker is nil"
	}
	return w.state.dynamicHealth()
}

type ProjectionWorker struct {
	runtime projectionWorkerRuntime
	options ProjectionWorkerOptions
	state   workerState
}

func NewProjectionWorker(
	runtime projectionWorkerRuntime,
	options ProjectionWorkerOptions,
) (*ProjectionWorker, error) {
	if isNilRuntimeDependency(runtime) {
		return nil, errors.New("projection worker runtime is nil")
	}
	if options.Interval <= 0 || options.BatchSize <= 0 || options.BatchSize > 1024 {
		return nil, ErrInvalidRuntimeContract
	}
	normalizeWorkerTiming(&options.MaxBackoff, options.Interval, &options.Jitter)
	return &ProjectionWorker{runtime: runtime, options: options}, nil
}

func (w *ProjectionWorker) Name() string                { return "conversation_projection_worker" }
func (w *ProjectionWorker) Critical() bool              { return false }
func (w *ProjectionWorker) Init(context.Context) error  { return nil }
func (w *ProjectionWorker) Ready(context.Context) error { return nil }

func (w *ProjectionWorker) Start(ctx context.Context) error {
	if w == nil {
		return errors.New("projection worker is nil")
	}
	return w.state.start(ctx, func(loopCtx context.Context) {
		w.loop(loopCtx)
	})
}

func (w *ProjectionWorker) Stop(ctx context.Context) error {
	if w == nil {
		return nil
	}
	return w.state.stop(ctx)
}

func (w *ProjectionWorker) loop(ctx context.Context) {
	failures := 0
	for {
		err := w.runOnce(ctx)
		if err != nil {
			failures++
		} else {
			failures = 0
		}
		if !waitWorker(ctx, workerDelay(
			w.options.Interval,
			w.options.MaxBackoff,
			failures,
			w.options.Jitter,
		)) {
			return
		}
	}
}

func (w *ProjectionWorker) runOnce(ctx context.Context) error {
	submitted := 0
	for submitted < w.options.BatchSize {
		err := w.runtime.SubmitNextProjection(ctx)
		if errors.Is(err, ErrNotFound) {
			w.state.record(nil, submitted, 0, time.Now().UTC())
			return nil
		}
		if err != nil {
			w.state.record(err, submitted, 0, time.Now().UTC())
			return err
		}
		submitted++
	}
	w.state.record(nil, submitted, 0, time.Now().UTC())
	return nil
}

func (w *ProjectionWorker) Stats() map[string]any {
	if w == nil {
		return nil
	}
	return w.state.stats(w.options.BatchSize, w.options.Interval)
}

func (w *ProjectionWorker) DynamicHealth() (appruntime.State, string) {
	if w == nil {
		return appruntime.StateDegraded, "projection worker is nil"
	}
	return w.state.dynamicHealth()
}

func normalizeWorkerTiming(
	maxBackoff *time.Duration,
	interval time.Duration,
	jitter *func(time.Duration) time.Duration,
) {
	if *maxBackoff <= 0 {
		*maxBackoff = 30 * interval
	}
	if *maxBackoff < interval {
		*maxBackoff = interval
	}
	if *jitter == nil {
		*jitter = func(delay time.Duration) time.Duration {
			if delay <= 10 {
				return delay
			}
			span := delay / 10
			offset := time.Now().UnixNano()%(2*int64(span)+1) - int64(span)
			return delay + time.Duration(offset)
		}
	}
}

func workerDelay(
	interval time.Duration,
	maxBackoff time.Duration,
	failures int,
	jitter func(time.Duration) time.Duration,
) time.Duration {
	delay := interval
	for index := 0; index < failures; index++ {
		if delay >= maxBackoff/2 {
			delay = maxBackoff
			break
		}
		delay *= 2
	}
	if delay > maxBackoff {
		delay = maxBackoff
	}
	jittered := jitter(delay)
	if jittered <= 0 {
		return delay
	}
	if jittered > maxBackoff {
		return maxBackoff
	}
	return jittered
}

func waitWorker(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (s *workerState) start(parent context.Context, loop func(context.Context)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running || s.stopping {
		return nil
	}
	loopCtx, cancel := context.WithCancel(context.WithoutCancel(parent))
	done := make(chan struct{})
	s.cancel = cancel
	s.done = done
	s.running = true
	s.stopping = false
	s.terminalError = ""
	go s.executeLoop(loopCtx, done, loop)
	return nil
}

func (s *workerState) executeLoop(
	ctx context.Context,
	done chan struct{},
	loop func(context.Context),
) {
	var terminalErr error
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				terminalErr = fmt.Errorf("worker panic: %v", recovered)
			}
		}()
		loop(ctx)
	}()
	if terminalErr == nil && ctx.Err() == nil {
		terminalErr = errors.New("worker loop exited unexpectedly")
	}
	s.finish(done, terminalErr)
	close(done)
}

func (s *workerState) stop(ctx context.Context) error {
	s.mu.Lock()
	if !s.running && !s.stopping {
		s.mu.Unlock()
		return nil
	}
	cancel := s.cancel
	done := s.done
	if !s.stopping {
		s.stopping = true
		cancel()
	}
	s.mu.Unlock()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *workerState) finish(done chan struct{}, terminalErr error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done != done {
		return
	}
	s.running = false
	s.stopping = false
	s.cancel = nil
	s.done = nil
	if terminalErr != nil {
		s.failures++
		s.lastError = terminalErr.Error()
		s.terminalError = terminalErr.Error()
	}
}

func (s *workerState) record(
	err error,
	submitted int,
	expired int,
	finishedAt time.Time,
) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.iterations++
	s.lastSubmitted = submitted
	s.lastExpired = expired
	if err != nil {
		s.failures++
		s.lastError = err.Error()
		return
	}
	s.failures = 0
	s.lastError = ""
	s.lastSuccessAt = finishedAt
}

func (s *workerState) stats(batchSize int, interval time.Duration) map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	stats := map[string]any{
		"running":            s.running,
		"stopping":           s.stopping,
		"iterations":         s.iterations,
		"consecutive_errors": s.failures,
		"last_submitted":     s.lastSubmitted,
		"last_expired":       s.lastExpired,
		"batch_size":         batchSize,
		"interval":           interval.String(),
	}
	if !s.lastSuccessAt.IsZero() {
		stats["last_success_at"] = s.lastSuccessAt.UTC().Format(time.RFC3339Nano)
	}
	if s.lastError != "" {
		stats["last_error"] = s.lastError
	}
	if s.terminalError != "" {
		stats["terminal_error"] = s.terminalError
	}
	return stats
}

func (s *workerState) dynamicHealth() (appruntime.State, string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.terminalError != "" {
		return appruntime.StateDegraded, s.terminalError
	}
	if s.failures >= workerDegradedFailureThreshold {
		return appruntime.StateDegraded, fmt.Sprintf(
			"%d consecutive worker errors: %s",
			s.failures,
			s.lastError,
		)
	}
	return appruntime.StateReady, ""
}
