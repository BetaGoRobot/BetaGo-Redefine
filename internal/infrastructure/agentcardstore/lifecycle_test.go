package agentcardstore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentcard"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
)

func TestMarkSurfaceSentIsIdempotentAndFenced(t *testing.T) {
	fixture := newCardStoreFixture(t)
	request := fixture.beginRequest("compose-sent")
	if _, err := fixture.repo.BeginCardInteraction(context.Background(), request); err != nil {
		t.Fatalf("BeginCardInteraction() error = %v", err)
	}
	mark := agentcard.MarkSurfaceSentRequest{
		SurfaceID: request.SurfaceID, ExpectedRevision: request.Revision,
		MessageID: "om-card", SourceRef: "delivery:compose-sent",
		SentAt: time.Now().UTC(),
	}
	sent, err := fixture.repo.MarkSurfaceSent(context.Background(), mark)
	if err != nil {
		t.Fatalf("MarkSurfaceSent() error = %v", err)
	}
	if sent.Status != agentcard.SurfaceStatusSent ||
		sent.MessageID != mark.MessageID {
		t.Fatalf("sent surface = %#v", sent)
	}
	if _, err := fixture.repo.MarkSurfaceSent(
		context.Background(),
		mark,
	); err != nil {
		t.Fatalf("MarkSurfaceSent(replay) error = %v", err)
	}
	collision := mark
	collision.MessageID = "om-other"
	if _, err := fixture.repo.MarkSurfaceSent(
		context.Background(),
		collision,
	); !errors.Is(err, agentcard.ErrCardConflict) {
		t.Fatalf("MarkSurfaceSent(collision) error = %v", err)
	}
	stale := mark
	stale.ExpectedRevision++
	if _, err := fixture.repo.MarkSurfaceSent(
		context.Background(),
		stale,
	); !errors.Is(err, agentcard.ErrCardConflict) {
		t.Fatalf("MarkSurfaceSent(stale) error = %v", err)
	}
}

func TestMarkSurfaceSendFailedReleasesRunAndQueuesRepair(t *testing.T) {
	fixture := newCardStoreFixture(t)
	request := fixture.beginRequest("compose-failed")
	if _, err := fixture.repo.BeginCardInteraction(context.Background(), request); err != nil {
		t.Fatalf("BeginCardInteraction() error = %v", err)
	}
	failedAt := time.Now().UTC().Truncate(time.Microsecond)
	surface, err := fixture.repo.MarkSurfaceSendFailed(
		context.Background(),
		agentcard.MarkSurfaceSendFailedRequest{
			SurfaceID: request.SurfaceID, ExpectedRevision: request.Revision,
			SourceRef: "delivery:compose-failed", ErrorCode: "delivery_rejected",
			FailedAt: failedAt,
		},
	)
	if err != nil {
		t.Fatalf("MarkSurfaceSendFailed() error = %v", err)
	}
	if surface.Status != agentcard.SurfaceStatusFailed ||
		surface.LastError != "delivery_rejected" {
		t.Fatalf("failed surface = %#v", surface)
	}
	var run model.AgentRun
	if err := fixture.db.First(&run, "id = ?", fixture.runID).Error; err != nil {
		t.Fatalf("load run: %v", err)
	}
	if run.Status != string(agentruntime.RunStatusQueued) ||
		run.WaitingReason != "" || run.WaitingToken != "" ||
		run.Revision != request.Revision+1 {
		t.Fatalf("released run = %#v", run)
	}
	var repairCount int64
	if err := fixture.db.Model(&model.AgentStep{}).
		Where(
			"run_id = ? AND kind = ? AND status = ?",
			fixture.runID,
			string(agentruntime.StepKindDecide),
			string(agentruntime.StepStatusQueued),
		).
		Count(&repairCount).Error; err != nil {
		t.Fatalf("count repair continuation: %v", err)
	}
	if repairCount != 1 {
		t.Fatalf("repair continuation count = %d, want 1", repairCount)
	}
}

func TestMarkSurfaceSendUncertainKeepsRunWaitingForReceiptReconciliation(t *testing.T) {
	fixture := newCardStoreFixture(t)
	request := fixture.beginRequest("compose-uncertain")
	if _, err := fixture.repo.BeginCardInteraction(context.Background(), request); err != nil {
		t.Fatalf("BeginCardInteraction() error = %v", err)
	}
	surface, err := fixture.repo.MarkSurfaceSendUncertain(
		context.Background(),
		agentcard.MarkSurfaceSendUncertainRequest{
			SurfaceID: request.SurfaceID, ExpectedRevision: request.Revision,
			SourceRef: "delivery:compose-uncertain",
			ErrorCode: "ambiguous_transport", ObservedAt: time.Now().UTC(),
		},
	)
	if err != nil {
		t.Fatalf("MarkSurfaceSendUncertain() error = %v", err)
	}
	if surface.Status != agentcard.SurfaceStatusDraft ||
		surface.PatchStatus != agentcard.PatchStatusPending {
		t.Fatalf("uncertain surface = %#v", surface)
	}
	var run model.AgentRun
	if err := fixture.db.First(&run, "id = ?", fixture.runID).Error; err != nil {
		t.Fatalf("load run: %v", err)
	}
	if run.Status != string(agentruntime.RunStatusWaitingCallback) ||
		run.WaitingToken == "" || run.Revision != request.Revision {
		t.Fatalf("ambiguous delivery changed waiting run: %#v", run)
	}
}
