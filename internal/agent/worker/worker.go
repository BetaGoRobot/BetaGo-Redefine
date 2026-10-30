// Package worker advances durable, independent indexing and interpretation jobs.
// It never receives Lark ingress or owns business decisions.
package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/pgstore"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/telemetry"
)

type Index interface {
	UpsertMessage(context.Context, domain.Message) error
	DeleteMessage(context.Context, domain.Scope, string) error
	UpsertContext(context.Context, domain.Context, []float32) error
	DeleteContext(context.Context, domain.Scope, string) error
}
type Memory interface {
	Retain(context.Context, domain.Context) error
	DeleteDocument(context.Context, domain.Scope, string) error
}
type MediaProcessor interface {
	Process(context.Context, domain.Message) (domain.MediaArtifact, error)
}
type Config struct {
	Store       *pgstore.Store
	Index       Index
	Memory      Memory
	Media       MediaProcessor
	Embedder    domain.Embedder
	Owner       string
	Lease, Poll time.Duration
}
type Worker struct{ c Config }

var ErrNotReady = errors.New("durable pipeline prerequisite not ready")
var pipelineKinds = []domain.JobKind{domain.JobIndexMessage, domain.JobCleanup, domain.JobMedia, domain.JobContexts, domain.JobEmbedContext, domain.JobMemory}

func New(c Config) (*Worker, error) {
	if c.Store == nil || c.Index == nil || c.Owner == "" {
		return nil, fmt.Errorf("%w: worker dependencies", domain.ErrInvalid)
	}
	if c.Lease == 0 {
		c.Lease = 2 * time.Minute
	}
	if c.Poll == 0 {
		c.Poll = 250 * time.Millisecond
	}
	if c.Lease < 30*time.Millisecond || c.Lease > 24*time.Hour || c.Poll <= 0 || c.Poll > time.Minute {
		return nil, domain.ErrInvalid
	}
	return &Worker{c}, nil
}

// Run dedicates a polling lane to literal indexing so long model/media calls in
// the derived lane cannot delay new canonical messages.
func (w *Worker) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); w.loop(ctx, []domain.JobKind{domain.JobIndexMessage}) }()
	w.loop(ctx, pipelineKinds[1:])
	<-done
	return ctx.Err()
}
func (w *Worker) loop(ctx context.Context, kinds []domain.JobKind) {
	for ctx.Err() == nil {
		err := w.Step(ctx, kinds...)
		if errors.Is(err, domain.ErrNotFound) || err != nil {
			timer := time.NewTimer(w.c.Poll)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}
}
func (w *Worker) Step(ctx context.Context, kinds ...domain.JobKind) (result error) {
	if len(kinds) == 0 {
		kinds = pipelineKinds
	}
	claimStart := time.Now()
	j, err := w.c.Store.ClaimJob(ctx, kinds, w.c.Owner, w.c.Lease)
	if err != nil {
		if !errors.Is(err, domain.ErrNotFound) && ctx.Err() == nil {
			failureCtx, failureSpan := telemetry.Start(ctx, "worker.queue.claim")
			telemetry.Error(failureCtx, "worker.queue.claim.failed", telemetry.Wrap("queue.claim", "database_claim_failed", err), "claim_duration_ms", time.Since(claimStart).Milliseconds())
			telemetry.End(failureCtx, failureSpan, telemetry.Wrap("queue.claim", "database_claim_failed", err))
		}
		return err
	}
	ctx = telemetry.JobContext(ctx, j)
	var payload struct {
		ContextID string `json:"context_id"`
	}
	_ = json.Unmarshal(j.Payload, &payload)
	if j.Kind == domain.JobIndexMessage || ((j.Kind == domain.JobContexts || j.Kind == domain.JobMemory) && payload.ContextID == "") {
		ctx = telemetry.Quiet(ctx)
	}
	ctx = telemetry.Context(ctx, "context_id", payload.ContextID)
	ctx, span := telemetry.Start(ctx, "worker.job."+string(j.Kind))
	defer func() { telemetry.End(ctx, span, result) }()
	jobCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(w.c.Lease / 3)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-jobCtx.Done():
				return
			case <-ticker.C:
				if _, e := w.c.Store.RenewJob(jobCtx, j, w.c.Lease); e != nil {
					if jobCtx.Err() == nil {
						telemetry.Error(jobCtx, "worker.job.lease_lost", telemetry.Wrap("worker.lease.renew", "lease_renew_failed", e))
					}
					cancel()
					return
				}
			}
		}
	}()
	err = w.handle(jobCtx, j)
	close(stop)
	<-stopped
	if errors.Is(err, ErrNotReady) {
		ctx = telemetry.Context(ctx, "outcome", "deferred")
		telemetry.Info(ctx, "worker.prerequisite.waiting", "retry_after_ms", 1000)
		return w.c.Store.DeferJob(ctx, j, time.Second)
	}
	if err == nil || errors.Is(err, domain.ErrStaleSource) {
		if err != nil {
			ctx = telemetry.Context(ctx, "outcome", "stale")
			telemetry.Warn(ctx, "worker.source.stale")
		}
		return w.c.Store.CompleteJob(ctx, j)
	}
	telemetry.Error(ctx, "worker.job.retry", telemetry.Wrap("worker."+string(j.Kind), "pipeline_stage_failed", err))
	// Keep only controlled diagnostics in the durable job. Transport/model/parser
	// error text is never persisted because it may contain external input.
	if retryErr := w.c.Store.RetryJob(ctx, j, errors.New("pipeline stage failed")); retryErr != nil {
		return retryErr
	}
	return err
}
func (w *Worker) handle(ctx context.Context, j domain.Job) error {
	switch j.Kind {
	case domain.JobIndexMessage:
		return w.indexMessage(ctx, j)
	case domain.JobMedia:
		return w.media(ctx, j)
	case domain.JobContexts:
		return w.contexts(ctx, j)
	case domain.JobEmbedContext:
		return w.embed(ctx, j)
	case domain.JobMemory:
		return w.memory(ctx, j)
	case domain.JobCleanup:
		return w.cleanup(ctx, j)
	}
	return domain.ErrInvalid
}
func (w *Worker) current(ctx context.Context, j domain.Job) (domain.Message, error) {
	if err := w.c.Store.CheckClaim(ctx, j); err != nil {
		return domain.Message{}, err
	}
	m, err := w.c.Store.Message(ctx, j.Scope, j.Source.MessageID)
	if err != nil {
		return m, err
	}
	if m.Source.Version != j.Source.Version {
		return m, domain.ErrStaleSource
	}
	return m, nil
}
func (w *Worker) valid(ctx context.Context, j domain.Job, refs []domain.SourceRef) error {
	if err := w.c.Store.CheckClaim(ctx, j); err != nil {
		return err
	}
	ok, err := w.c.Store.ValidSources(ctx, domain.Query{Scope: j.Scope, Anchor: time.Now().UTC(), Mode: domain.QueryDiscussion, Limit: 1}, refs)
	if err != nil {
		return err
	}
	if !ok {
		// A current fact slightly ahead of this machine's clock is a prerequisite
		// wait, not a superseded fact. Keep its durable job until it becomes eligible.
		future := false
		now := time.Now().UTC()
		for _, ref := range refs {
			m, e := w.c.Store.Message(ctx, ref.Scope, ref.MessageID)
			if e != nil {
				return e
			}
			if m.Deleted || m.Source.Version != ref.Version || m.Source.AuthorID != ref.AuthorID || !m.Source.OccurredAt.Equal(ref.OccurredAt) {
				return domain.ErrStaleSource
			}
			future = future || m.UpdatedAt.After(now) || ref.OccurredAt.After(now)
		}
		if future {
			return ErrNotReady
		}
		return domain.ErrStaleSource
	}
	return nil
}
