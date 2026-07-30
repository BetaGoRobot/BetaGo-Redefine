package conversationeval

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

var ErrJudgeInputNotFound = errors.New("evaluation judge input not found")

type JudgeInputSource interface {
	NextJudgeInput(context.Context, time.Time) (*JudgeInput, error)
}

type JudgeProcessor struct {
	source JudgeInputSource
	judge  *Judge
	now    func() time.Time
}

func NewJudgeProcessor(
	source JudgeInputSource,
	judge *Judge,
	now func() time.Time,
) (*JudgeProcessor, error) {
	if source == nil || judge == nil {
		return nil, fmt.Errorf("%w: judge processor dependency is nil", ErrEvaluationUnavailable)
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &JudgeProcessor{source: source, judge: judge, now: now}, nil
}

func (p *JudgeProcessor) ProcessNext(ctx context.Context) error {
	input, err := p.source.NextJudgeInput(ctx, p.now())
	if err != nil {
		return err
	}
	if input == nil {
		return ErrJudgeInputNotFound
	}
	_, err = p.judge.Evaluate(ctx, *input)
	return err
}

type JudgeWorkerOptions struct {
	Workers    int
	Interval   time.Duration
	MaxBackoff time.Duration
}

type JudgeWorker struct {
	processor *JudgeProcessor
	options   JudgeWorkerOptions

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}

	iterations atomic.Int64
	failures   atomic.Int64
	lastError  atomic.Pointer[string]
}

func NewJudgeWorker(
	processor *JudgeProcessor,
	options JudgeWorkerOptions,
) (*JudgeWorker, error) {
	if processor == nil {
		return nil, fmt.Errorf("%w: judge processor is nil", ErrEvaluationUnavailable)
	}
	if options.Workers <= 0 {
		options.Workers = 1
	}
	if options.Workers > 8 {
		return nil, contractError("judge worker count must not exceed 8")
	}
	if options.Interval <= 0 {
		options.Interval = time.Second
	}
	if options.MaxBackoff < options.Interval {
		options.MaxBackoff = 30 * options.Interval
	}
	return &JudgeWorker{processor: processor, options: options}, nil
}

func (w *JudgeWorker) Name() string                { return "conversation_evaluation_judge" }
func (w *JudgeWorker) Critical() bool              { return false }
func (w *JudgeWorker) Init(context.Context) error  { return nil }
func (w *JudgeWorker) Ready(context.Context) error { return nil }

func (w *JudgeWorker) Start(ctx context.Context) error {
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

func (w *JudgeWorker) Stop(ctx context.Context) error {
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

func (w *JudgeWorker) run(ctx context.Context, done chan struct{}) {
	defer close(done)
	var group sync.WaitGroup
	group.Add(w.options.Workers)
	for range w.options.Workers {
		go w.runLoop(ctx, &group)
	}
	group.Wait()
}

func (w *JudgeWorker) runLoop(ctx context.Context, group *sync.WaitGroup) {
	defer group.Done()
	w.loop(ctx)
}

func (w *JudgeWorker) loop(ctx context.Context) {
	consecutiveFailures := 0
	for {
		err := w.processor.ProcessNext(ctx)
		w.iterations.Add(1)
		delay := time.Duration(0)
		switch {
		case err == nil:
			consecutiveFailures = 0
		case errors.Is(err, ErrJudgeInputNotFound):
			consecutiveFailures = 0
			delay = w.options.Interval
		default:
			consecutiveFailures++
			w.failures.Add(1)
			message := err.Error()
			w.lastError.Store(&message)
			delay = candidateWorkerBackoff(
				w.options.Interval,
				w.options.MaxBackoff,
				consecutiveFailures,
			)
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

func (w *JudgeWorker) Stats() map[string]any {
	stats := map[string]any{
		"workers":    w.options.Workers,
		"interval":   w.options.Interval.String(),
		"iterations": w.iterations.Load(),
		"failures":   w.failures.Load(),
	}
	if value := w.lastError.Load(); value != nil {
		stats["last_error"] = *value
	}
	return stats
}
