package agentcard

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type PatchWorkerOptions struct {
	Store      PatchStore
	Client     SurfaceClient
	WorkerID   string
	LeaseTTL   time.Duration
	RetryDelay time.Duration
	Now        func() time.Time
}

type PatchWorker struct {
	store      PatchStore
	client     SurfaceClient
	workerID   string
	leaseTTL   time.Duration
	retryDelay time.Duration
	now        func() time.Time
}

func NewPatchWorker(options PatchWorkerOptions) (*PatchWorker, error) {
	if options.Store == nil || options.Client == nil {
		return nil, errors.New("patch store and surface client are required")
	}
	if strings.TrimSpace(options.WorkerID) == "" {
		return nil, errors.New("patch worker id is required")
	}
	if options.LeaseTTL <= 0 {
		options.LeaseTTL = 30 * time.Second
	}
	if options.RetryDelay <= 0 {
		options.RetryDelay = 5 * time.Second
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &PatchWorker{
		store: options.Store, client: options.Client,
		workerID: options.WorkerID, leaseTTL: options.LeaseTTL,
		retryDelay: options.RetryDelay, now: options.Now,
	}, nil
}

func (w *PatchWorker) Process(
	ctx context.Context,
	surfaceID string,
	expectedRevision int64,
) error {
	if w == nil {
		return errors.New("patch worker is nil")
	}
	now := w.now().UTC()
	claimed, err := w.store.ClaimPatch(ctx, ClaimPatchRequest{
		SurfaceID: surfaceID, ExpectedRevision: expectedRevision,
		WorkerID: w.workerID, LeaseTTL: w.leaseTTL, Now: now,
	})
	if err != nil {
		return err
	}
	compiled := json.RawMessage(claimed.CompiledJSONRedacted)
	if claimed.MessageID == "" || !json.Valid(compiled) ||
		jsonDocumentContainsToken(compiled) {
		_ = w.store.RetryPatch(ctx, RetryPatchRequest{
			SurfaceID: claimed.ID, ExpectedRevision: claimed.Revision,
			WorkerID: w.workerID, AttemptCount: claimed.PatchAttemptCount,
			ErrorCode: "invalid_patch_artifact", FailedAt: now,
			RetryAt: now.Add(w.retryDelay),
		})
		return ErrSurfacePatchPending
	}
	if err := w.client.PatchCard(
		ctx,
		claimed.MessageID,
		append(json.RawMessage(nil), compiled...),
	); err != nil {
		if retryErr := w.store.RetryPatch(ctx, RetryPatchRequest{
			SurfaceID: claimed.ID, ExpectedRevision: claimed.Revision,
			WorkerID: w.workerID, AttemptCount: claimed.PatchAttemptCount,
			ErrorCode: "patch_transport_failed", FailedAt: now,
			RetryAt: now.Add(w.retryDelay),
		}); retryErr != nil {
			return retryErr
		}
		return ErrSurfacePatchPending
	}
	return w.store.CompletePatch(ctx, CompletePatchRequest{
		SurfaceID: claimed.ID, ExpectedRevision: claimed.Revision,
		WorkerID: w.workerID, AttemptCount: claimed.PatchAttemptCount,
		CompletedAt: w.now().UTC(),
	})
}
