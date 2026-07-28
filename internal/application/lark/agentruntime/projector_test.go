package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestProjectorUpsertsExactDocumentAndCompletes(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	store := &projectionStoreFake{claimed: projectionOutboxFixture(1, now)}
	writer := &projectionWriterFake{}
	executor := &projectionExecutorFake{}
	projector := NewProjector(store, writer, executor, ProjectorConfig{
		WorkerID: "projector-1", LeaseTTL: time.Minute, Now: func() time.Time { return now },
	})

	if err := projector.SubmitNext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if writer.index != "agent_conversation_events" || writer.documentID != "step-1" {
		t.Fatalf("upsert target = %q/%q", writer.index, writer.documentID)
	}
	if !writer.hasDeadline || writer.deadlineRemaining <= 0 || writer.deadlineRemaining > time.Minute {
		t.Fatalf("writer deadline remaining = %v, present=%v", writer.deadlineRemaining, writer.hasDeadline)
	}
	if store.renewed.OutboxID != "outbox-1" || !store.renewed.Now.Equal(now) ||
		store.renewed.LeaseTTL != time.Minute {
		t.Fatalf("renewed = %#v", store.renewed)
	}
	if string(writer.payload) != `{"event_id":"step-1"}` {
		t.Fatalf("payload = %s", writer.payload)
	}
	if store.completed.OutboxID != "outbox-1" ||
		store.completed.WorkerID != "projector-1" || store.completed.AttemptCount != 1 {
		t.Fatalf("completed = %#v", store.completed)
	}
	if store.retried.OutboxID != "" {
		t.Fatalf("unexpected retry = %#v", store.retried)
	}
}

func TestProjectorDoesNotWriteWhenLeaseExpiredInExecutorQueue(t *testing.T) {
	claimTime := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	taskTime := claimTime
	store := &projectionStoreFake{claimed: projectionOutboxFixture(1, claimTime)}
	writer := &projectionWriterFake{}
	executor := &projectionExecutorFake{beforeTask: func() {
		taskTime = claimTime.Add(time.Minute)
	}}
	projector := NewProjector(store, writer, executor, ProjectorConfig{
		WorkerID: "projector-1", LeaseTTL: time.Minute, Now: func() time.Time { return taskTime },
	})
	err := projector.SubmitNext(context.Background())
	if !errors.Is(err, ErrProjectionLeaseLost) {
		t.Fatalf("SubmitNext() error = %v, want lease lost", err)
	}
	if writer.calls != 0 {
		t.Fatalf("writer called %d times after queued lease expired", writer.calls)
	}
	if store.completed.OutboxID != "" || store.retried.OutboxID != "" {
		t.Fatalf("expired owner finalized row: completed=%#v retried=%#v", store.completed, store.retried)
	}
}

func TestRenewProjectionLeaseRequestValidate(t *testing.T) {
	now := time.Now().UTC()
	valid := RenewProjectionLeaseRequest{
		OutboxID: "outbox-1", WorkerID: "worker-1", AttemptCount: 1,
		LeaseTTL: time.Minute, Now: now,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	invalid := valid
	invalid.AttemptCount = 0
	if !errors.Is(invalid.Validate(), ErrInvalidRuntimeContract) {
		t.Fatalf("invalid Validate() error = %v", invalid.Validate())
	}
}

func TestProjectorRetryBackoffByAttempt(t *testing.T) {
	cases := []struct {
		attempt int32
		delay   time.Duration
	}{
		{attempt: 1, delay: 5 * time.Second},
		{attempt: 2, delay: 30 * time.Second},
		{attempt: 3, delay: 2 * time.Minute},
		{attempt: 4, delay: 10 * time.Minute},
		{attempt: 5, delay: 30 * time.Minute},
		{attempt: 20, delay: 30 * time.Minute},
	}
	for _, tc := range cases {
		t.Run(tc.delay.String(), func(t *testing.T) {
			now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
			store := &projectionStoreFake{claimed: projectionOutboxFixture(tc.attempt, now)}
			writer := &projectionWriterFake{err: errors.New("temporary opensearch failure")}
			projector := NewProjector(store, writer, &projectionExecutorFake{}, ProjectorConfig{
				WorkerID: "projector-1", LeaseTTL: time.Minute, Now: func() time.Time { return now },
			})
			if err := projector.SubmitNext(context.Background()); err == nil ||
				err.Error() != "temporary opensearch failure" {
				t.Fatalf("SubmitNext() error = %v", err)
			}
			if store.retried.OutboxID != "outbox-1" ||
				!store.retried.FailedAt.Equal(now) ||
				!store.retried.RetryAt.Equal(now.Add(tc.delay)) {
				t.Fatalf("retry = %#v", store.retried)
			}
		})
	}
}

func TestProjectorSubmitFailureImmediatelyReleasesClaim(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	submitErr := errors.New("executor queue full")
	store := &projectionStoreFake{claimed: projectionOutboxFixture(1, now)}
	projector := NewProjector(
		store, &projectionWriterFake{}, &projectionExecutorFake{submitErr: submitErr},
		ProjectorConfig{
			WorkerID: "projector-1", LeaseTTL: time.Minute, Now: func() time.Time { return now },
		},
	)
	if err := projector.SubmitNext(context.Background()); !errors.Is(err, submitErr) {
		t.Fatalf("SubmitNext() error = %v", err)
	}
	if !store.retried.FailedAt.Equal(now) || !store.retried.RetryAt.Equal(now) {
		t.Fatalf("submit failure did not release immediately: %#v", store.retried)
	}
}

func TestProjectorReplayUsesSameDocumentID(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	store := &projectionStoreFake{claimed: projectionOutboxFixture(1, now)}
	writer := &projectionWriterFake{}
	projector := NewProjector(store, writer, &projectionExecutorFake{}, ProjectorConfig{
		WorkerID: "projector-1", LeaseTTL: time.Minute, Now: func() time.Time { return now },
	})
	if err := projector.SubmitNext(context.Background()); err != nil {
		t.Fatal(err)
	}
	store.claimed = projectionOutboxFixture(2, now)
	if err := projector.SubmitNext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(writer.documentIDs) != 2 ||
		writer.documentIDs[0] != "step-1" || writer.documentIDs[1] != "step-1" {
		t.Fatalf("document IDs = %#v", writer.documentIDs)
	}
}

func TestProjectorMalformedClaimIsFencedBackToRetry(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	claimed := projectionOutboxFixture(1, now)
	claimed.Payload = json.RawMessage(`{"broken":`)
	store := &projectionStoreFake{claimed: claimed}
	projector := NewProjector(store, &projectionWriterFake{}, &projectionExecutorFake{}, ProjectorConfig{
		WorkerID: "projector-1", LeaseTTL: time.Minute, Now: func() time.Time { return now },
	})
	if err := projector.SubmitNext(context.Background()); !errors.Is(err, ErrInvalidRuntimeContract) {
		t.Fatalf("SubmitNext() error = %v", err)
	}
	if store.retried.OutboxID != claimed.ID ||
		!store.retried.RetryAt.Equal(now.Add(5*time.Second)) {
		t.Fatalf("malformed claim left running: %#v", store.retried)
	}
}

func TestProjectorRejectsNonObjectJSONAndNilClaimWithoutPanicking(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	claimed := projectionOutboxFixture(1, now)
	claimed.Payload = json.RawMessage(`["not-an-event-object"]`)
	store := &projectionStoreFake{claimed: claimed}
	projector := NewProjector(store, &projectionWriterFake{}, &projectionExecutorFake{}, ProjectorConfig{
		WorkerID: "projector-1", LeaseTTL: time.Minute, Now: func() time.Time { return now },
	})
	if err := projector.SubmitNext(context.Background()); !errors.Is(err, ErrInvalidRuntimeContract) {
		t.Fatalf("array SubmitNext() error = %v", err)
	}
	if store.retried.OutboxID != claimed.ID {
		t.Fatalf("array claim was not retried: %#v", store.retried)
	}

	store = &projectionStoreFake{}
	projector = NewProjector(store, &projectionWriterFake{}, &projectionExecutorFake{}, ProjectorConfig{
		WorkerID: "projector-1", LeaseTTL: time.Minute, Now: func() time.Time { return now },
	})
	if err := projector.SubmitNext(context.Background()); !errors.Is(err, ErrInvalidRuntimeContract) {
		t.Fatalf("nil claim SubmitNext() error = %v", err)
	}
}

func TestRetryProjectionContractBoundsPersistedError(t *testing.T) {
	now := time.Now().UTC()
	req := RetryProjectionRequest{
		OutboxID: "outbox-1", WorkerID: "worker-1", AttemptCount: 1,
		ErrorText: string(make([]byte, MaxProjectionErrorBytes+1)),
		FailedAt:  now, RetryAt: now.Add(time.Minute),
	}
	if !errors.Is(req.Validate(), ErrInvalidRuntimeContract) {
		t.Fatalf("oversized RetryProjectionRequest.Validate() error = %v", req.Validate())
	}
}

type projectionStoreFake struct {
	claimed   *ProjectionOutbox
	claimErr  error
	renewed   RenewProjectionLeaseRequest
	completed CompleteProjectionRequest
	retried   RetryProjectionRequest
}

func (f *projectionStoreFake) ClaimProjection(context.Context, ProjectionClaim) (*ProjectionOutbox, error) {
	return f.claimed, f.claimErr
}

func (f *projectionStoreFake) RenewProjectionLease(
	_ context.Context,
	req RenewProjectionLeaseRequest,
) error {
	f.renewed = req
	if f.claimed == nil || !f.claimed.LeaseExpiresAt.After(req.Now) ||
		f.claimed.ID != req.OutboxID || f.claimed.WorkerID != req.WorkerID ||
		f.claimed.AttemptCount != req.AttemptCount {
		return ErrProjectionLeaseLost
	}
	f.claimed.LeaseExpiresAt = req.Now.Add(req.LeaseTTL)
	return nil
}

func (f *projectionStoreFake) CompleteProjection(_ context.Context, req CompleteProjectionRequest) error {
	f.completed = req
	return nil
}

func (f *projectionStoreFake) RetryProjection(_ context.Context, req RetryProjectionRequest) error {
	f.retried = req
	return nil
}

type projectionWriterFake struct {
	index             string
	documentID        string
	documentIDs       []string
	payload           json.RawMessage
	err               error
	calls             int
	hasDeadline       bool
	deadlineRemaining time.Duration
}

func (f *projectionWriterFake) Upsert(
	ctx context.Context,
	index string,
	documentID string,
	payload json.RawMessage,
) error {
	f.calls++
	deadline, ok := ctx.Deadline()
	f.hasDeadline = ok
	if ok {
		f.deadlineRemaining = time.Until(deadline)
	}
	f.index = index
	f.documentID = documentID
	f.documentIDs = append(f.documentIDs, documentID)
	f.payload = append(json.RawMessage(nil), payload...)
	return f.err
}

type projectionExecutorFake struct {
	submitErr  error
	beforeTask func()
}

func (f *projectionExecutorFake) Submit(
	ctx context.Context,
	_ string,
	task func(context.Context) error,
) error {
	if f.submitErr != nil {
		return f.submitErr
	}
	if f.beforeTask != nil {
		f.beforeTask()
	}
	return task(ctx)
}

func projectionOutboxFixture(attempt int32, now time.Time) *ProjectionOutbox {
	return &ProjectionOutbox{
		ID: "outbox-1", StepID: "step-1",
		IndexAlias: "agent_conversation_events", DocumentID: "step-1",
		Payload: json.RawMessage(`{"event_id":"step-1"}`),
		Status:  ProjectionStatusRunning, AttemptCount: attempt,
		WorkerID: "projector-1", LeaseExpiresAt: now.Add(time.Minute),
	}
}
