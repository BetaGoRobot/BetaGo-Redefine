package conversationeval

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestCandidateProcessorPersistsAndCompletesCapturedModelError(t *testing.T) {
	input := serviceMessageInput()
	task := CandidateTask{
		ID: "task-1", Cohort: serviceCohort(input),
		Episode: newEpisode(serviceCohort(input), input, nil, input.OccurredAt),
		Message: input, OutputID: "candidate-output",
		ContextSnapshot: fallbackControlContext(input),
		ExcludedContext: []ExcludedContextItem{}, CreatedAt: input.OccurredAt,
	}
	queue := &candidateQueueFake{lease: &CandidateTaskLease{
		Task: task, Status: CandidateTaskRunning, AttemptCount: 1,
		WorkerID: "worker-1", LeaseExpiresAt: input.OccurredAt.Add(time.Minute),
	}}
	repository := &serviceRepositoryFake{}
	service := newServiceForTest(t, repository, &preWindowSourceFake{}, queue)
	modelErr := errors.New("malformed stage")
	runner := &candidateRunnerFake{
		output: serviceLaneOutput(task.Episode, LaneCandidate),
		err:    modelErr,
	}
	processor, err := NewCandidateProcessor(
		queue,
		service,
		func(context.Context, CandidateTask) (CandidateRunner, error) { return runner, nil },
		CandidateProcessorConfig{
			WorkerID: "worker-1", LeaseTTL: time.Minute, RetryDelay: time.Second,
			Now: func() time.Time { return input.OccurredAt.Add(10 * time.Second) },
		},
	)
	if err != nil {
		t.Fatalf("NewCandidateProcessor() error = %v", err)
	}
	if err := processor.ProcessNext(context.Background()); !errors.Is(err, modelErr) {
		t.Fatalf("ProcessNext() error = %v, want model error", err)
	}
	if len(repository.outputs) != 1 || queue.completed != 1 || queue.retried != 0 {
		t.Fatalf("outputs/completed/retried = %d/%d/%d, want 1/1/0",
			len(repository.outputs), queue.completed, queue.retried)
	}
}

func TestCandidateProcessorRetriesFactoryFailure(t *testing.T) {
	input := serviceMessageInput()
	task := CandidateTask{
		ID: "task-1", Cohort: serviceCohort(input),
		Episode: newEpisode(serviceCohort(input), input, nil, input.OccurredAt),
		Message: input, OutputID: "candidate-output",
		ContextSnapshot: fallbackControlContext(input),
		CreatedAt:       input.OccurredAt,
	}
	queue := &candidateQueueFake{lease: &CandidateTaskLease{
		Task: task, Status: CandidateTaskRunning, AttemptCount: 2,
		WorkerID: "worker-1", LeaseExpiresAt: input.OccurredAt.Add(time.Minute),
	}}
	repository := &serviceRepositoryFake{}
	service := newServiceForTest(t, repository, &preWindowSourceFake{}, queue)
	factoryErr := errors.New("model config missing")
	processor, err := NewCandidateProcessor(
		queue,
		service,
		func(context.Context, CandidateTask) (CandidateRunner, error) {
			return nil, factoryErr
		},
		CandidateProcessorConfig{
			WorkerID: "worker-1", LeaseTTL: time.Minute, RetryDelay: time.Second,
			Now: func() time.Time { return input.OccurredAt.Add(10 * time.Second) },
		},
	)
	if err != nil {
		t.Fatalf("NewCandidateProcessor() error = %v", err)
	}
	if err := processor.ProcessNext(context.Background()); !errors.Is(err, factoryErr) {
		t.Fatalf("ProcessNext() error = %v, want factory error", err)
	}
	if queue.retried != 1 || queue.completed != 0 {
		t.Fatalf("retried/completed = %d/%d, want 1/0", queue.retried, queue.completed)
	}
}

func TestCandidateWorkerStopsIdleLoops(t *testing.T) {
	queue := &candidateQueueFake{}
	repository := &serviceRepositoryFake{}
	service := newServiceForTest(t, repository, &preWindowSourceFake{}, queue)
	processor, err := NewCandidateProcessor(
		queue,
		service,
		func(context.Context, CandidateTask) (CandidateRunner, error) {
			return &candidateRunnerFake{}, nil
		},
		CandidateProcessorConfig{
			WorkerID: "worker-1", LeaseTTL: time.Minute,
		},
	)
	if err != nil {
		t.Fatalf("NewCandidateProcessor() error = %v", err)
	}
	worker, err := NewCandidateWorker(processor, CandidateWorkerOptions{
		Workers: 2, Interval: time.Millisecond, MaxBackoff: 2 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewCandidateWorker() error = %v", err)
	}
	if err := worker.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for queue.claims() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := worker.Stop(stopCtx); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if queue.claims() == 0 {
		t.Fatal("worker did not poll queue")
	}
}

type candidateQueueFake struct {
	mu sync.Mutex

	lease      *CandidateTaskLease
	claimCount int
	completed  int
	retried    int
}

func (q *candidateQueueFake) SubmitCandidate(context.Context, CandidateTask) error { return nil }
func (q *candidateQueueFake) ClaimCandidate(
	context.Context,
	CandidateTaskClaim,
) (*CandidateTaskLease, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.claimCount++
	if q.lease == nil {
		return nil, ErrCandidateTaskNotFound
	}
	lease := cloneCaptureValue(*q.lease)
	q.lease = nil
	return &lease, nil
}
func (q *candidateQueueFake) CompleteCandidateTask(
	context.Context,
	CompleteCandidateTaskRequest,
) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.completed++
	return nil
}
func (q *candidateQueueFake) RetryCandidateTask(
	context.Context,
	RetryCandidateTaskRequest,
) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.retried++
	return nil
}
func (q *candidateQueueFake) claims() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.claimCount
}

type candidateRunnerFake struct {
	output LaneOutput
	err    error
}

func (r *candidateRunnerFake) Run(context.Context, CandidateRequest) (LaneOutput, error) {
	return cloneCaptureValue(r.output), r.err
}
