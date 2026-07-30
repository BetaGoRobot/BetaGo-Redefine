package agentcardstore

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentcard"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	_ agentcard.LifecycleStore = (*Repository)(nil)
	_ agentcard.PatchStore     = (*Repository)(nil)
	_ agentcard.PatchCatalog   = (*Repository)(nil)
)

func (r *Repository) ListDuePatches(
	ctx context.Context,
	now time.Time,
	limit int,
) ([]agentcard.PatchTarget, error) {
	if r == nil || r.db == nil || now.IsZero() || limit <= 0 {
		return nil, errors.New("invalid due patch query")
	}
	var rows []struct {
		ID       string
		Revision int64
	}
	result := r.db.WithContext(ctx).
		Model(&model.AgentCardSurface{}).
		Select("id", "revision").
		Where("message_id <> ''").
		Where(
			`(
				(patch_status IN ? AND next_patch_at <= ?)
				OR
				(patch_status = ? AND patch_lease_expires_at <= ?)
			)`,
			[]string{
				string(agentcard.PatchStatusPending),
				string(agentcard.PatchStatusFailed),
			},
			now.UTC(),
			string(agentcard.PatchStatusRunning),
			now.UTC(),
		).
		Order("next_patch_at ASC").
		Order("id ASC").
		Limit(limit).
		Scan(&rows)
	if result.Error != nil {
		return nil, result.Error
	}
	targets := make([]agentcard.PatchTarget, 0, len(rows))
	for _, row := range rows {
		targets = append(targets, agentcard.PatchTarget{
			SurfaceID: row.ID,
			Revision:  row.Revision,
		})
	}
	return targets, nil
}

func (r *Repository) TransitionSurface(
	ctx context.Context,
	request agentcard.TransitionSurfaceRequest,
) (*agentcard.CardSurface, error) {
	if strings.TrimSpace(request.SurfaceID) == "" ||
		request.ExpectedRevision <= 0 ||
		!validSurfaceTransition(request.From, request.To) ||
		!json.Valid([]byte(request.CompiledJSONRedacted)) ||
		jsonContainsToken([]byte(request.CompiledJSONRedacted)) ||
		strings.TrimSpace(request.SourceRef) == "" ||
		request.OccurredAt.IsZero() {
		return nil, errors.New("invalid surface transition request")
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
		if stored.Status == string(request.To) {
			if stored.LastSourceRef == request.SourceRef &&
				stored.LastActionID == request.ActionID &&
				equalJSON(
					[]byte(stored.CompiledJSONRedacted),
					[]byte(request.CompiledJSONRedacted),
				) {
				return nil
			}
			return agentcard.ErrCardConflict
		}
		if stored.Status != string(request.From) || stored.MessageID == "" {
			return agentcard.ErrCardConflict
		}
		now := request.OccurredAt.UTC()
		updates := map[string]any{
			"status":                 string(request.To),
			"compiled_json_redacted": request.CompiledJSONRedacted,
			"last_action_id":         request.ActionID,
			"last_source_ref":        request.SourceRef,
			"patch_status":           string(agentcard.PatchStatusPending),
			"next_patch_at":          now, "patch_worker_id": "",
			"patch_lease_expires_at": nil, "last_error": "",
			"updated_at": now,
		}
		switch request.To {
		case agentcard.SurfaceStatusSubmitted:
			updates["submitted_at"] = now
		case agentcard.SurfaceStatusProcessing:
			updates["processing_at"] = now
		case agentcard.SurfaceStatusResolved:
			updates["resolved_at"] = now
		case agentcard.SurfaceStatusCancelled:
			updates["cancelled_at"] = now
		case agentcard.SurfaceStatusFailed:
			updates["failed_at"] = now
		}
		result := tx.Model(&model.AgentCardSurface{}).
			Where(
				"id = ? AND revision = ? AND status = ?",
				request.SurfaceID,
				request.ExpectedRevision,
				string(request.From),
			).
			Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return agentcard.ErrCardConflict
		}
		if err := tx.First(&stored, "id = ?", request.SurfaceID).Error; err != nil {
			return mapStoreNotFound(err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return toApplicationSurface(&stored), nil
}

func (r *Repository) ClaimPatch(
	ctx context.Context,
	request agentcard.ClaimPatchRequest,
) (*agentcard.CardSurface, error) {
	if strings.TrimSpace(request.SurfaceID) == "" ||
		request.ExpectedRevision <= 0 ||
		strings.TrimSpace(request.WorkerID) == "" ||
		request.LeaseTTL <= 0 || request.Now.IsZero() {
		return nil, errors.New("invalid patch claim request")
	}
	var claimed model.AgentCardSurface
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Clauses(clause.Locking{
			Strength: "UPDATE",
			Options:  "SKIP LOCKED",
		}).
			Where(
				"id = ? AND revision = ? AND message_id <> ''",
				request.SurfaceID,
				request.ExpectedRevision,
			).
			Where(
				`(
					(patch_status IN ? AND next_patch_at <= ?)
					OR
					(patch_status = ? AND patch_lease_expires_at <= ?)
				)`,
				[]string{
					string(agentcard.PatchStatusPending),
					string(agentcard.PatchStatusFailed),
				},
				request.Now,
				string(agentcard.PatchStatusRunning),
				request.Now,
			).
			First(&claimed)
		if result.Error != nil {
			return mapStoreNotFound(result.Error)
		}
		attempt := claimed.PatchAttemptCount + 1
		leaseExpiresAt := request.Now.Add(request.LeaseTTL)
		result = tx.Model(&model.AgentCardSurface{}).
			Where(
				"id = ? AND revision = ? AND patch_attempt_count = ?",
				claimed.ID,
				request.ExpectedRevision,
				claimed.PatchAttemptCount,
			).
			Updates(map[string]any{
				"patch_status":           string(agentcard.PatchStatusRunning),
				"patch_attempt_count":    attempt,
				"patch_worker_id":        request.WorkerID,
				"patch_lease_expires_at": leaseExpiresAt,
				"updated_at":             request.Now,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return agentcard.ErrCardConflict
		}
		claimed.PatchStatus = string(agentcard.PatchStatusRunning)
		claimed.PatchAttemptCount = attempt
		claimed.PatchWorkerID = request.WorkerID
		claimed.PatchLeaseExpiresAt = leaseExpiresAt
		claimed.UpdatedAt = request.Now
		return nil
	})
	if err != nil {
		return nil, err
	}
	return toApplicationSurface(&claimed), nil
}

func (r *Repository) CompletePatch(
	ctx context.Context,
	request agentcard.CompletePatchRequest,
) error {
	if strings.TrimSpace(request.SurfaceID) == "" ||
		request.ExpectedRevision <= 0 ||
		strings.TrimSpace(request.WorkerID) == "" ||
		request.AttemptCount <= 0 || request.CompletedAt.IsZero() {
		return errors.New("invalid complete-patch request")
	}
	result := r.db.WithContext(ctx).Model(&model.AgentCardSurface{}).
		Where(
			`id = ? AND revision = ? AND patch_status = ?
			 AND patch_worker_id = ? AND patch_attempt_count = ?`,
			request.SurfaceID,
			request.ExpectedRevision,
			string(agentcard.PatchStatusRunning),
			request.WorkerID,
			request.AttemptCount,
		).
		Updates(map[string]any{
			"patch_status":    string(agentcard.PatchStatusIdle),
			"patch_worker_id": "", "patch_lease_expires_at": nil,
			"last_error": "", "updated_at": request.CompletedAt.UTC(),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return agentcard.ErrCardConflict
	}
	return nil
}

func (r *Repository) RetryPatch(
	ctx context.Context,
	request agentcard.RetryPatchRequest,
) error {
	if strings.TrimSpace(request.SurfaceID) == "" ||
		request.ExpectedRevision <= 0 ||
		strings.TrimSpace(request.WorkerID) == "" ||
		request.AttemptCount <= 0 ||
		strings.TrimSpace(request.ErrorCode) == "" ||
		request.FailedAt.IsZero() || request.RetryAt.IsZero() ||
		request.RetryAt.Before(request.FailedAt) {
		return errors.New("invalid retry-patch request")
	}
	result := r.db.WithContext(ctx).Model(&model.AgentCardSurface{}).
		Where(
			`id = ? AND revision = ? AND patch_status = ?
			 AND patch_worker_id = ? AND patch_attempt_count = ?`,
			request.SurfaceID,
			request.ExpectedRevision,
			string(agentcard.PatchStatusRunning),
			request.WorkerID,
			request.AttemptCount,
		).
		Updates(map[string]any{
			"patch_status":    string(agentcard.PatchStatusPending),
			"next_patch_at":   request.RetryAt.UTC(),
			"patch_worker_id": "", "patch_lease_expires_at": nil,
			"last_error": request.ErrorCode,
			"updated_at": request.FailedAt.UTC(),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return agentcard.ErrCardConflict
	}
	return nil
}

func validSurfaceTransition(from, to agentcard.SurfaceStatus) bool {
	if from == to {
		return false
	}
	switch from {
	case agentcard.SurfaceStatusSent:
		return to == agentcard.SurfaceStatusSubmitted ||
			to == agentcard.SurfaceStatusCancelled ||
			to == agentcard.SurfaceStatusExpired ||
			to == agentcard.SurfaceStatusFailed
	case agentcard.SurfaceStatusSubmitted:
		return to == agentcard.SurfaceStatusProcessing ||
			to == agentcard.SurfaceStatusResolved ||
			to == agentcard.SurfaceStatusCancelled ||
			to == agentcard.SurfaceStatusExpired ||
			to == agentcard.SurfaceStatusFailed
	case agentcard.SurfaceStatusProcessing:
		return to == agentcard.SurfaceStatusResolved ||
			to == agentcard.SurfaceStatusCancelled ||
			to == agentcard.SurfaceStatusExpired ||
			to == agentcard.SurfaceStatusFailed
	default:
		return false
	}
}

func jsonContainsToken(document []byte) bool {
	var value any
	if json.Unmarshal(document, &value) != nil {
		return true
	}
	var visit func(any) bool
	visit = func(current any) bool {
		switch typed := current.(type) {
		case map[string]any:
			for key, child := range typed {
				if strings.EqualFold(key, "token") {
					return true
				}
				if visit(child) {
					return true
				}
			}
		case []any:
			for _, child := range typed {
				if visit(child) {
					return true
				}
			}
		}
		return false
	}
	return visit(value)
}
