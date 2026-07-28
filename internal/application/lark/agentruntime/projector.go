package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

type ProjectionWriter interface {
	Upsert(context.Context, string, string, json.RawMessage) error
}

type ProjectionExecutor interface {
	Submit(context.Context, string, func(context.Context) error) error
}

type ProjectorConfig struct {
	WorkerID string
	LeaseTTL time.Duration
	Now      func() time.Time
}

type Projector struct {
	store    ProjectionOutboxStore
	writer   ProjectionWriter
	executor ProjectionExecutor
	config   ProjectorConfig
}

func NewProjector(
	store ProjectionOutboxStore,
	writer ProjectionWriter,
	executor ProjectionExecutor,
	config ProjectorConfig,
) *Projector {
	return &Projector{store: store, writer: writer, executor: executor, config: config}
}

func (p *Projector) SubmitNext(ctx context.Context) error {
	if p == nil || p.store == nil || p.writer == nil || p.executor == nil ||
		p.config.WorkerID == "" || p.config.LeaseTTL <= 0 || p.config.Now == nil {
		return ErrInvalidRuntimeContract
	}
	claimTime := p.config.Now()
	outbox, err := p.store.ClaimProjection(ctx, ProjectionClaim{
		WorkerID: p.config.WorkerID,
		LeaseTTL: p.config.LeaseTTL,
		Now:      claimTime,
	})
	if err != nil {
		return err
	}
	if outbox == nil {
		return ErrInvalidRuntimeContract
	}
	var taskRan atomic.Bool
	err = p.executor.Submit(ctx, "conversation-event-projection:"+outbox.ID, func(taskCtx context.Context) error {
		taskRan.Store(true)
		return p.project(taskCtx, outbox)
	})
	if err == nil || taskRan.Load() {
		return err
	}
	releaseErr := p.store.RetryProjection(ctx, RetryProjectionRequest{
		OutboxID: outbox.ID, WorkerID: outbox.WorkerID, AttemptCount: outbox.AttemptCount,
		ErrorText: boundedProjectionError(err), FailedAt: claimTime, RetryAt: claimTime,
	})
	if releaseErr != nil {
		return errors.Join(err, releaseErr)
	}
	return err
}

func (p *Projector) project(ctx context.Context, outbox *ProjectionOutbox) error {
	var err error
	projection := ProjectionDocument{
		IndexAlias: outbox.IndexAlias,
		DocumentID: outbox.DocumentID,
		Payload:    outbox.Payload,
	}
	if outbox.ID == "" || projection.Validate() != nil {
		err = ErrInvalidRuntimeContract
	} else {
		err = p.writer.Upsert(ctx, outbox.IndexAlias, outbox.DocumentID, outbox.Payload)
	}
	finishedAt := p.config.Now()
	if err != nil {
		boundedErr := boundedProjectionError(err)
		retryErr := p.store.RetryProjection(ctx, RetryProjectionRequest{
			OutboxID: outbox.ID, WorkerID: outbox.WorkerID, AttemptCount: outbox.AttemptCount,
			ErrorText: boundedErr, FailedAt: finishedAt,
			RetryAt: finishedAt.Add(projectionRetryDelay(outbox.AttemptCount)),
		})
		if retryErr != nil {
			return errors.Join(newProjectionFailure(err), retryErr)
		}
		if errors.Is(err, ErrInvalidRuntimeContract) {
			return ErrInvalidRuntimeContract
		}
		return projectionFailure{cause: err, message: boundedErr}
	}
	return p.store.CompleteProjection(ctx, CompleteProjectionRequest{
		OutboxID: outbox.ID, WorkerID: outbox.WorkerID,
		AttemptCount: outbox.AttemptCount, FinishedAt: finishedAt,
	})
}

func projectionRetryDelay(attempt int32) time.Duration {
	switch attempt {
	case 1:
		return 5 * time.Second
	case 2:
		return 30 * time.Second
	case 3:
		return 2 * time.Minute
	case 4:
		return 10 * time.Minute
	default:
		return 30 * time.Minute
	}
}

func boundedProjectionError(err error) string {
	text := fmt.Sprint(err)
	if len(text) <= MaxProjectionErrorBytes {
		return text
	}
	text = text[:MaxProjectionErrorBytes]
	for !utf8.ValidString(text) {
		text = text[:len(text)-1]
	}
	return text
}

type projectionFailure struct {
	cause   error
	message string
}

func newProjectionFailure(err error) projectionFailure {
	return projectionFailure{cause: err, message: boundedProjectionError(err)}
}

func (e projectionFailure) Error() string {
	return e.message
}

func (e projectionFailure) Unwrap() error {
	return e.cause
}
