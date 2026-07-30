package agentcardstore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentcard"
)

func TestPatchReconciliationFencesStaleLeaseAndConverges(t *testing.T) {
	fixture := newCardStoreFixture(t)
	begin := fixture.beginRequest("compose-patch")
	if _, err := fixture.repo.BeginCardInteraction(context.Background(), begin); err != nil {
		t.Fatalf("BeginCardInteraction() error = %v", err)
	}
	sentAt := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := fixture.repo.MarkSurfaceSent(
		context.Background(),
		agentcard.MarkSurfaceSentRequest{
			SurfaceID: begin.SurfaceID, ExpectedRevision: begin.Revision,
			MessageID: "om-patch", SourceRef: "delivery:compose-patch",
			SentAt: sentAt,
		},
	); err != nil {
		t.Fatalf("MarkSurfaceSent() error = %v", err)
	}
	submitted, err := fixture.repo.TransitionSurface(
		context.Background(),
		agentcard.TransitionSurfaceRequest{
			SurfaceID: begin.SurfaceID, ExpectedRevision: begin.Revision,
			From: agentcard.SurfaceStatusSent, To: agentcard.SurfaceStatusSubmitted,
			CompiledJSONRedacted: `{"schema":"2.0","state":"submitted"}`,
			ActionID:             "confirm", ActorOpenID: "owner-1",
			SourceRef: "callback:1", OccurredAt: sentAt.Add(time.Second),
		},
	)
	if err != nil {
		t.Fatalf("TransitionSurface() error = %v", err)
	}
	if submitted.PatchStatus != agentcard.PatchStatusPending {
		t.Fatalf("submitted surface = %#v", submitted)
	}

	firstNow := sentAt.Add(2 * time.Second)
	due, err := fixture.repo.ListDuePatches(
		context.Background(),
		firstNow,
		8,
	)
	if err != nil {
		t.Fatalf("ListDuePatches(pending) error = %v", err)
	}
	if len(due) != 1 || due[0].SurfaceID != begin.SurfaceID ||
		due[0].Revision != begin.Revision {
		t.Fatalf("pending due patches = %#v", due)
	}
	first, err := fixture.repo.ClaimPatch(context.Background(), agentcard.ClaimPatchRequest{
		SurfaceID: begin.SurfaceID, ExpectedRevision: begin.Revision,
		WorkerID: "worker-1", LeaseTTL: time.Second, Now: firstNow,
	})
	if err != nil {
		t.Fatalf("ClaimPatch(first) error = %v", err)
	}
	if first.PatchAttemptCount != 1 ||
		first.PatchStatus != agentcard.PatchStatusRunning {
		t.Fatalf("first claim = %#v", first)
	}
	due, err = fixture.repo.ListDuePatches(
		context.Background(),
		firstNow,
		8,
	)
	if err != nil {
		t.Fatalf("ListDuePatches(running) error = %v", err)
	}
	if len(due) != 0 {
		t.Fatalf("unexpired running patches = %#v", due)
	}
	secondNow := firstNow.Add(2 * time.Second)
	due, err = fixture.repo.ListDuePatches(
		context.Background(),
		secondNow,
		8,
	)
	if err != nil {
		t.Fatalf("ListDuePatches(expired) error = %v", err)
	}
	if len(due) != 1 || due[0].SurfaceID != begin.SurfaceID {
		t.Fatalf("expired due patches = %#v", due)
	}
	second, err := fixture.repo.ClaimPatch(context.Background(), agentcard.ClaimPatchRequest{
		SurfaceID: begin.SurfaceID, ExpectedRevision: begin.Revision,
		WorkerID: "worker-2", LeaseTTL: time.Second, Now: secondNow,
	})
	if err != nil {
		t.Fatalf("ClaimPatch(reclaim) error = %v", err)
	}
	if second.PatchAttemptCount != 2 || second.PatchWorkerID != "worker-2" {
		t.Fatalf("second claim = %#v", second)
	}
	if err := fixture.repo.CompletePatch(context.Background(), agentcard.CompletePatchRequest{
		SurfaceID: begin.SurfaceID, ExpectedRevision: begin.Revision,
		WorkerID: "worker-1", AttemptCount: 1, CompletedAt: secondNow,
	}); !errors.Is(err, agentcard.ErrCardConflict) {
		t.Fatalf("stale CompletePatch() error = %v", err)
	}
	retryAt := secondNow.Add(time.Minute)
	if err := fixture.repo.RetryPatch(context.Background(), agentcard.RetryPatchRequest{
		SurfaceID: begin.SurfaceID, ExpectedRevision: begin.Revision,
		WorkerID: "worker-2", AttemptCount: 2, ErrorCode: "lark_unavailable",
		FailedAt: secondNow, RetryAt: retryAt,
	}); err != nil {
		t.Fatalf("RetryPatch() error = %v", err)
	}
	if _, err := fixture.repo.ClaimPatch(context.Background(), agentcard.ClaimPatchRequest{
		SurfaceID: begin.SurfaceID, ExpectedRevision: begin.Revision,
		WorkerID: "worker-3", LeaseTTL: time.Second,
		Now: retryAt.Add(-time.Microsecond),
	}); !errors.Is(err, agentcard.ErrCardNotFound) {
		t.Fatalf("early ClaimPatch() error = %v", err)
	}
	third, err := fixture.repo.ClaimPatch(context.Background(), agentcard.ClaimPatchRequest{
		SurfaceID: begin.SurfaceID, ExpectedRevision: begin.Revision,
		WorkerID: "worker-3", LeaseTTL: time.Second, Now: retryAt,
	})
	if err != nil {
		t.Fatalf("ClaimPatch(retry) error = %v", err)
	}
	if err := fixture.repo.CompletePatch(context.Background(), agentcard.CompletePatchRequest{
		SurfaceID: begin.SurfaceID, ExpectedRevision: begin.Revision,
		WorkerID: "worker-3", AttemptCount: third.PatchAttemptCount,
		CompletedAt: retryAt,
	}); err != nil {
		t.Fatalf("CompletePatch() error = %v", err)
	}
	reloaded, err := fixture.repo.GetByInteraction(context.Background(), agentcard.GetSurfaceRequest{
		RunID: fixture.runID, InteractionID: begin.InteractionID,
	})
	if err != nil {
		t.Fatalf("GetByInteraction() error = %v", err)
	}
	if reloaded.Status != agentcard.SurfaceStatusSubmitted ||
		reloaded.PatchStatus != agentcard.PatchStatusIdle ||
		reloaded.PatchWorkerID != "" {
		t.Fatalf("reconciled surface = %#v", reloaded)
	}
}

func TestTransitionSurfaceRejectsStaleRevisionAndInvalidState(t *testing.T) {
	fixture := newCardStoreFixture(t)
	begin := fixture.beginRequest("compose-transition")
	if _, err := fixture.repo.BeginCardInteraction(context.Background(), begin); err != nil {
		t.Fatalf("BeginCardInteraction() error = %v", err)
	}
	if _, err := fixture.repo.TransitionSurface(
		context.Background(),
		agentcard.TransitionSurfaceRequest{
			SurfaceID: begin.SurfaceID, ExpectedRevision: begin.Revision + 1,
			From: agentcard.SurfaceStatusSent, To: agentcard.SurfaceStatusSubmitted,
			CompiledJSONRedacted: `{"schema":"2.0"}`,
			SourceRef:            "system:stale", OccurredAt: time.Now().UTC(),
		},
	); !errors.Is(err, agentcard.ErrCardConflict) {
		t.Fatalf("stale transition error = %v", err)
	}
}
