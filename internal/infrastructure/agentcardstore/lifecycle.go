package agentcardstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentcard"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var _ agentcard.DeliveryStore = (*Repository)(nil)

func (r *Repository) MarkSurfaceSent(
	ctx context.Context,
	request agentcard.MarkSurfaceSentRequest,
) (*agentcard.CardSurface, error) {
	if strings.TrimSpace(request.SurfaceID) == "" ||
		request.ExpectedRevision <= 0 ||
		strings.TrimSpace(request.MessageID) == "" ||
		strings.TrimSpace(request.SourceRef) == "" ||
		request.SentAt.IsZero() {
		return nil, errors.New("invalid mark-surface-sent request")
	}
	var stored model.AgentCardSurface
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&stored, "id = ?", request.SurfaceID).Error; err != nil {
			return mapStoreNotFound(err)
		}
		if stored.Revision != request.ExpectedRevision {
			return agentcard.ErrCardConflict
		}
		if stored.Status == string(agentcard.SurfaceStatusSent) {
			if stored.MessageID == request.MessageID &&
				stored.LastSourceRef == request.SourceRef {
				return nil
			}
			return agentcard.ErrCardConflict
		}
		if stored.Status != string(agentcard.SurfaceStatusDraft) ||
			stored.MessageID != "" {
			return agentcard.ErrCardConflict
		}
		now := request.SentAt.UTC()
		result := tx.Model(&model.AgentCardSurface{}).
			Where(
				"id = ? AND revision = ? AND status = ?",
				request.SurfaceID,
				request.ExpectedRevision,
				string(agentcard.SurfaceStatusDraft),
			).
			Updates(map[string]any{
				"status":     string(agentcard.SurfaceStatusSent),
				"message_id": request.MessageID, "last_source_ref": request.SourceRef,
				"patch_status":    string(agentcard.PatchStatusIdle),
				"patch_worker_id": "", "patch_lease_expires_at": nil,
				"last_error": "", "updated_at": now,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return agentcard.ErrCardConflict
		}
		stored.Status = string(agentcard.SurfaceStatusSent)
		stored.MessageID = request.MessageID
		stored.LastSourceRef = request.SourceRef
		stored.PatchStatus = string(agentcard.PatchStatusIdle)
		stored.PatchWorkerID = ""
		stored.PatchLeaseExpiresAt = time.Time{}
		stored.LastError = ""
		stored.UpdatedAt = now
		return nil
	})
	if err != nil {
		return nil, err
	}
	return toApplicationSurface(&stored), nil
}

func (r *Repository) MarkSurfaceSendUncertain(
	ctx context.Context,
	request agentcard.MarkSurfaceSendUncertainRequest,
) (*agentcard.CardSurface, error) {
	if strings.TrimSpace(request.SurfaceID) == "" ||
		request.ExpectedRevision <= 0 ||
		strings.TrimSpace(request.SourceRef) == "" ||
		strings.TrimSpace(request.ErrorCode) == "" ||
		request.ObservedAt.IsZero() {
		return nil, errors.New("invalid uncertain-delivery request")
	}
	var stored model.AgentCardSurface
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&stored, "id = ?", request.SurfaceID).Error; err != nil {
			return mapStoreNotFound(err)
		}
		if stored.Revision != request.ExpectedRevision ||
			stored.Status != string(agentcard.SurfaceStatusDraft) {
			return agentcard.ErrCardConflict
		}
		if stored.LastSourceRef == request.SourceRef &&
			stored.LastError == request.ErrorCode &&
			stored.PatchStatus == string(agentcard.PatchStatusPending) {
			return nil
		}
		now := request.ObservedAt.UTC()
		result := tx.Model(&model.AgentCardSurface{}).
			Where(
				"id = ? AND revision = ? AND status = ?",
				request.SurfaceID,
				request.ExpectedRevision,
				string(agentcard.SurfaceStatusDraft),
			).
			Updates(map[string]any{
				"last_source_ref": request.SourceRef,
				"last_error":      request.ErrorCode,
				"patch_status":    string(agentcard.PatchStatusPending),
				"next_patch_at":   now, "updated_at": now,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return agentcard.ErrCardConflict
		}
		stored.LastSourceRef = request.SourceRef
		stored.LastError = request.ErrorCode
		stored.PatchStatus = string(agentcard.PatchStatusPending)
		stored.NextPatchAt = now
		stored.UpdatedAt = now
		return nil
	})
	if err != nil {
		return nil, err
	}
	return toApplicationSurface(&stored), nil
}

func (r *Repository) MarkSurfaceSendFailed(
	ctx context.Context,
	request agentcard.MarkSurfaceSendFailedRequest,
) (*agentcard.CardSurface, error) {
	if strings.TrimSpace(request.SurfaceID) == "" ||
		request.ExpectedRevision <= 0 ||
		strings.TrimSpace(request.SourceRef) == "" ||
		strings.TrimSpace(request.ErrorCode) == "" ||
		request.FailedAt.IsZero() {
		return nil, errors.New("invalid failed-delivery request")
	}
	var stored model.AgentCardSurface
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&stored, "id = ?", request.SurfaceID).Error; err != nil {
			return mapStoreNotFound(err)
		}
		if stored.Revision != request.ExpectedRevision {
			return agentcard.ErrCardConflict
		}
		if stored.Status == string(agentcard.SurfaceStatusFailed) {
			if stored.LastSourceRef == request.SourceRef &&
				stored.LastError == request.ErrorCode {
				return nil
			}
			return agentcard.ErrCardConflict
		}
		if stored.Status != string(agentcard.SurfaceStatusDraft) {
			return agentcard.ErrCardConflict
		}
		var run model.AgentRun
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&run, "id = ?", stored.RunID).Error; err != nil {
			return mapStoreNotFound(err)
		}
		expectedStatus, expectedReason := cardWaitingState(stored.InteractionKind)
		if run.Revision != request.ExpectedRevision ||
			run.Status != string(expectedStatus) ||
			run.WaitingReason != string(expectedReason) ||
			run.WaitingToken == "" {
			return agentcard.ErrCardConflict
		}
		now := request.FailedAt.UTC()
		_, continuation, err := createDeliveryFailureRepair(
			tx,
			&run,
			&stored,
			request,
			now,
		)
		if err != nil {
			return err
		}
		result := tx.Model(&model.AgentCardSurface{}).
			Where(
				"id = ? AND revision = ? AND status = ?",
				stored.ID,
				request.ExpectedRevision,
				string(agentcard.SurfaceStatusDraft),
			).
			Updates(map[string]any{
				"status":    string(agentcard.SurfaceStatusFailed),
				"failed_at": now, "last_source_ref": request.SourceRef,
				"last_error":   request.ErrorCode,
				"patch_status": string(agentcard.PatchStatusIdle),
				"updated_at":   now,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return agentcard.ErrCardConflict
		}
		result = tx.Model(&model.AgentRun{}).
			Where("id = ? AND revision = ?", run.ID, request.ExpectedRevision).
			Updates(map[string]any{
				"status":         string(agentruntime.RunStatusQueued),
				"waiting_reason": "", "waiting_token": "",
				"revision":           request.ExpectedRevision + 1,
				"current_step_index": continuation.Index,
				"result_summary":     "agent card delivery failed; repair queued",
				"updated_at":         now, "last_relevant_at": now,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return agentcard.ErrCardConflict
		}
		stored.Status = string(agentcard.SurfaceStatusFailed)
		stored.FailedAt = now
		stored.LastSourceRef = request.SourceRef
		stored.LastError = request.ErrorCode
		stored.PatchStatus = string(agentcard.PatchStatusIdle)
		stored.UpdatedAt = now
		return nil
	})
	if err != nil {
		return nil, err
	}
	return toApplicationSurface(&stored), nil
}

func createDeliveryFailureRepair(
	tx *gorm.DB,
	run *model.AgentRun,
	surface *model.AgentCardSurface,
	request agentcard.MarkSurfaceSendFailedRequest,
	now time.Time,
) (*model.AgentStep, *model.AgentStep, error) {
	index, err := nextCardStepIndex(tx, run)
	if err != nil {
		return nil, nil, err
	}
	dedupeBase := "card_delivery:" + surface.InteractionID + ":" +
		strconvFormatInt(surface.Revision)
	eventPayload, err := json.Marshal(map[string]any{
		"version": 1, "event_type": "agent_card_delivery_failed",
		"surface_id": surface.ID, "run_id": surface.RunID,
		"interaction_id": surface.InteractionID, "revision": surface.Revision,
		"source_ref": request.SourceRef, "error_code": request.ErrorCode,
		"occurred_at": now,
	})
	if err != nil {
		return nil, nil, err
	}
	event := &model.AgentStep{
		ID:       stableCardStepID(run.ID, dedupeBase+":event"),
		TenantID: run.TenantID, RunID: run.ID,
		Index: index, Kind: string(agentruntime.StepKindObserve),
		Status:    string(agentruntime.StepStatusCompleted),
		InputJSON: string(eventPayload), OutputJSON: "{}",
		ExternalRef: surface.InteractionID, StartedAt: now, FinishedAt: now,
		CreatedAt: now, DedupeKey: dedupeBase + ":event",
	}
	if err := tx.Create(event).Error; err != nil {
		return nil, nil, err
	}
	var source model.AgentProjectionOutbox
	if err := tx.Where("step_id = ?", surface.WaitStepID).
		First(&source).Error; err != nil {
		return nil, nil, err
	}
	projectionPayload, err := json.Marshal(map[string]any{
		"schema_version": "1", "event_id": event.ID,
		"event_type": "agent_card_delivery_failed", "run_id": run.ID,
		"step_id": event.ID, "source_step_id": surface.WaitStepID,
		"status": "failed", "occurred_at": now,
		"structured_payload": map[string]any{
			"surface_id": surface.ID, "interaction_id": surface.InteractionID,
			"revision": surface.Revision, "error_code": request.ErrorCode,
		},
	})
	if err != nil {
		return nil, nil, err
	}
	documentID := strings.TrimSuffix(source.DocumentID, ":"+surface.WaitStepID)
	if err := insertCardProjectionOutbox(tx, event.ID, agentruntime.ProjectionDocument{
		IndexAlias: source.IndexAlias, DocumentID: documentID,
		Payload: projectionPayload,
	}, now); err != nil {
		return nil, nil, err
	}
	continuationInput, err := json.Marshal(map[string]any{
		"version": 1, "source_step_id": event.ID,
	})
	if err != nil {
		return nil, nil, err
	}
	continuation := &model.AgentStep{
		ID:       stableCardStepID(run.ID, dedupeBase+":continuation"),
		TenantID: run.TenantID, RunID: run.ID, Index: index + 1,
		Kind:      string(agentruntime.StepKindDecide),
		Status:    string(agentruntime.StepStatusQueued),
		InputJSON: string(continuationInput), OutputJSON: "{}",
		CreatedAt: now, DedupeKey: dedupeBase + ":continuation",
	}
	if err := tx.Create(continuation).Error; err != nil {
		return nil, nil, err
	}
	return event, continuation, nil
}

func stableCardStepID(runID, dedupeKey string) string {
	sum := sha256.Sum256([]byte(runID + "\x00" + dedupeKey))
	return "step_card_" + hex.EncodeToString(sum[:])
}

func strconvFormatInt(value int64) string {
	// Kept local so lifecycle dedupe construction stays allocation-light and
	// cannot accidentally use locale-sensitive formatting.
	return strconv.FormatInt(value, 10)
}
