package conversationeval

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

type CandidateRunnerFactory func(context.Context, CandidateTask) (CandidateRunner, error)

type CandidateProcessorConfig struct {
	WorkerID   string
	LeaseTTL   time.Duration
	RetryDelay time.Duration
	Now        func() time.Time
}

type CandidateProcessor struct {
	queue   CandidateTaskQueue
	service *Service
	factory CandidateRunnerFactory
	config  CandidateProcessorConfig
}

func NewCandidateProcessor(
	queue CandidateTaskQueue,
	service *Service,
	factory CandidateRunnerFactory,
	config CandidateProcessorConfig,
) (*CandidateProcessor, error) {
	if queue == nil || service == nil || factory == nil {
		return nil, fmt.Errorf("%w: candidate processor dependency is nil", ErrEvaluationUnavailable)
	}
	if config.WorkerID == "" || config.LeaseTTL <= 0 {
		return nil, contractError("candidate processor requires worker_id and positive lease_ttl")
	}
	if config.RetryDelay <= 0 {
		config.RetryDelay = 5 * time.Second
	}
	if config.Now == nil {
		config.Now = func() time.Time { return time.Now().UTC() }
	}
	return &CandidateProcessor{
		queue: queue, service: service, factory: factory, config: config,
	}, nil
}

func (p *CandidateProcessor) ProcessNext(ctx context.Context) error {
	now := p.config.Now()
	lease, err := p.queue.ClaimCandidate(ctx, CandidateTaskClaim{
		WorkerID: p.config.WorkerID, LeaseTTL: p.config.LeaseTTL, Now: now,
	})
	if err != nil {
		return err
	}
	retry := func(processErr error) error {
		failedAt := p.config.Now()
		retryErr := p.queue.RetryCandidateTask(ctx, RetryCandidateTaskRequest{
			TaskID: lease.Task.ID, WorkerID: lease.WorkerID,
			AttemptCount: lease.AttemptCount, ErrorText: processErr.Error(),
			FailedAt: failedAt, RetryAt: failedAt.Add(p.config.RetryDelay),
		})
		return errors.Join(processErr, retryErr)
	}

	runner, err := p.factory(ctx, lease.Task)
	if err != nil {
		return retry(fmt.Errorf("build candidate runner: %w", err))
	}
	output, runErr := runner.Run(ctx, CandidateRequest{
		OutputID: lease.Task.OutputID, EpisodeID: lease.Task.Episode.ID,
		AnchorAt:        lease.Task.Episode.AnchorAt,
		ContextSnapshot: cloneCaptureValue(lease.Task.ContextSnapshot),
		ExcludedContext: cloneCaptureValue(lease.Task.ExcludedContext),
		ControlCapture:  cloneCaptureValue(lease.Task.ControlCapture),
	})
	if lease.Task.Episode.ServingLane == LaneCandidate {
		output.OutputMode = OutputModeActual
	}
	if err := p.service.CompleteCandidate(ctx, lease.Task.Episode.ID, output); err != nil {
		return retry(fmt.Errorf("persist candidate output: %w", err))
	}
	finishedAt := p.config.Now()
	if err := p.queue.CompleteCandidateTask(ctx, CompleteCandidateTaskRequest{
		TaskID: lease.Task.ID, WorkerID: lease.WorkerID,
		AttemptCount: lease.AttemptCount, FinishedAt: finishedAt,
	}); err != nil {
		return err
	}
	// A model/stage error is itself a successfully captured evaluation result.
	// It is intentionally not retried into a different counterfactual answer.
	return runErr
}

type CandidateWorkerOptions struct {
	Workers             int
	Interval            time.Duration
	MaxBackoff          time.Duration
	WindowSweepInterval time.Duration
	WindowSweepBatch    int
	Now                 func() time.Time
}

type CandidateWorker struct {
	processor *CandidateProcessor
	options   CandidateWorkerOptions

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}

	iterations atomic.Int64
	failures   atomic.Int64
	lastError  atomic.Pointer[string]
}

func NewCandidateWorker(
	processor *CandidateProcessor,
	options CandidateWorkerOptions,
) (*CandidateWorker, error) {
	if processor == nil {
		return nil, fmt.Errorf("%w: candidate processor is nil", ErrEvaluationUnavailable)
	}
	if options.Workers <= 0 {
		options.Workers = 2
	}
	if options.Workers > 32 {
		return nil, contractError("candidate worker count must not exceed 32")
	}
	if options.Interval <= 0 {
		options.Interval = time.Second
	}
	if options.MaxBackoff < options.Interval {
		options.MaxBackoff = 30 * options.Interval
	}
	if options.WindowSweepInterval <= 0 {
		options.WindowSweepInterval = 5 * time.Second
	}
	if options.WindowSweepBatch <= 0 {
		options.WindowSweepBatch = 256
	}
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	return &CandidateWorker{processor: processor, options: options}, nil
}

func (w *CandidateWorker) Name() string                { return "conversation_evaluation_worker" }
func (w *CandidateWorker) Critical() bool              { return false }
func (w *CandidateWorker) Init(context.Context) error  { return nil }
func (w *CandidateWorker) Ready(context.Context) error { return nil }

func (w *CandidateWorker) Start(ctx context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cancel != nil {
		return nil
	}
	loopCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	done := make(chan struct{})
	w.cancel = cancel
	w.done = done
	go w.run(loopCtx, done)
	return nil
}

func (w *CandidateWorker) Stop(ctx context.Context) error {
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

func (w *CandidateWorker) run(ctx context.Context, done chan struct{}) {
	defer close(done)
	var group sync.WaitGroup
	group.Add(w.options.Workers + 1)
	for range w.options.Workers {
		go func() {
			defer group.Done()
			w.loop(ctx)
		}()
	}
	go func() {
		defer group.Done()
		w.windowLoop(ctx)
	}()
	group.Wait()
}

func (w *CandidateWorker) loop(ctx context.Context) {
	failures := 0
	for {
		err := w.processor.ProcessNext(ctx)
		w.iterations.Add(1)
		delay := time.Duration(0)
		switch {
		case err == nil:
			failures = 0
		case errors.Is(err, ErrCandidateTaskNotFound):
			failures = 0
			delay = w.options.Interval
		default:
			failures++
			w.failures.Add(1)
			message := err.Error()
			w.lastError.Store(&message)
			delay = candidateWorkerBackoff(w.options.Interval, w.options.MaxBackoff, failures)
		}
		if delay == 0 {
			continue
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

func (w *CandidateWorker) windowLoop(ctx context.Context) {
	ticker := time.NewTicker(w.options.WindowSweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := w.processor.service.AdvanceAllOpenWindows(
				ctx,
				w.options.Now(),
				w.options.WindowSweepBatch,
			); err != nil {
				w.failures.Add(1)
				message := err.Error()
				w.lastError.Store(&message)
			}
		}
	}
}

func candidateWorkerBackoff(interval, maximum time.Duration, failures int) time.Duration {
	delay := interval
	for index := 1; index < failures && delay < maximum; index++ {
		delay *= 2
		if delay >= maximum {
			return maximum
		}
	}
	return delay
}

func (w *CandidateWorker) Stats() map[string]any {
	stats := map[string]any{
		"workers": w.options.Workers, "interval": w.options.Interval.String(),
		"window_sweep_interval": w.options.WindowSweepInterval.String(),
		"iterations":            w.iterations.Load(), "failures": w.failures.Load(),
	}
	if value := w.lastError.Load(); value != nil {
		stats["last_error"] = *value
	}
	return stats
}
