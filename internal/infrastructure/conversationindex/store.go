package conversationindex

import (
	"context"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"gorm.io/gorm"
)

var _ agentruntime.ProjectionOutboxStore = (*Store)(nil)

type Store struct {
	db *gorm.DB
}

func NewStore(db *gorm.DB) *Store {
	return &Store{db: db}
}

func (s *Store) ClaimProjection(
	ctx context.Context,
	claim agentruntime.ProjectionClaim,
) (*agentruntime.ProjectionOutbox, error) {
	if err := claim.Validate(); err != nil {
		return nil, err
	}
	var claimed model.AgentProjectionOutbox
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Raw(`
			SELECT *
			FROM agent_projection_outbox
			WHERE (
				status = ? AND next_attempt_at <= ?
			) OR (
				status = ? AND lease_expires_at <= ?
			)
			ORDER BY next_attempt_at, id
			FOR UPDATE SKIP LOCKED
			LIMIT 1`,
			string(agentruntime.ProjectionStatusPending), claim.Now,
			string(agentruntime.ProjectionStatusRunning), claim.Now,
		).Scan(&claimed)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return agentruntime.ErrNotFound
		}
		leaseUntil := claim.Now.Add(claim.LeaseTTL)
		result = tx.Model(&model.AgentProjectionOutbox{}).
			Where("id = ? AND status = ?", claimed.ID, claimed.Status).
			Updates(map[string]any{
				"status":           string(agentruntime.ProjectionStatusRunning),
				"attempt_count":    gorm.Expr("attempt_count + 1"),
				"worker_id":        claim.WorkerID,
				"lease_expires_at": leaseUntil,
				"updated_at":       claim.Now,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return agentruntime.ErrProjectionLeaseLost
		}
		claimed.Status = string(agentruntime.ProjectionStatusRunning)
		claimed.AttemptCount++
		claimed.WorkerID = claim.WorkerID
		claimed.LeaseExpiresAt = leaseUntil
		claimed.UpdatedAt = claim.Now
		return nil
	})
	if err != nil {
		return nil, err
	}
	return toProjectionOutbox(&claimed), nil
}

func (s *Store) CompleteProjection(
	ctx context.Context,
	req agentruntime.CompleteProjectionRequest,
) error {
	if err := req.Validate(); err != nil {
		return err
	}
	result := s.db.WithContext(ctx).Model(&model.AgentProjectionOutbox{}).
		Where(
			"id = ? AND status = ? AND worker_id = ? AND attempt_count = ? AND lease_expires_at > ?",
			req.OutboxID, string(agentruntime.ProjectionStatusRunning),
			req.WorkerID, req.AttemptCount, req.FinishedAt,
		).
		Updates(map[string]any{
			"status":           string(agentruntime.ProjectionStatusCompleted),
			"worker_id":        "",
			"lease_expires_at": nil,
			"last_error":       "",
			"updated_at":       req.FinishedAt,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return agentruntime.ErrProjectionLeaseLost
	}
	return nil
}

func (s *Store) RetryProjection(
	ctx context.Context,
	req agentruntime.RetryProjectionRequest,
) error {
	if err := req.Validate(); err != nil {
		return err
	}
	result := s.db.WithContext(ctx).Model(&model.AgentProjectionOutbox{}).
		Where(
			"id = ? AND status = ? AND worker_id = ? AND attempt_count = ? AND lease_expires_at > ?",
			req.OutboxID, string(agentruntime.ProjectionStatusRunning),
			req.WorkerID, req.AttemptCount, req.FailedAt,
		).
		Updates(map[string]any{
			"status":           string(agentruntime.ProjectionStatusPending),
			"worker_id":        "",
			"lease_expires_at": nil,
			"last_error":       req.ErrorText,
			"next_attempt_at":  req.RetryAt,
			"updated_at":       req.FailedAt,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return agentruntime.ErrProjectionLeaseLost
	}
	return nil
}

func toProjectionOutbox(outbox *model.AgentProjectionOutbox) *agentruntime.ProjectionOutbox {
	if outbox == nil {
		return nil
	}
	return &agentruntime.ProjectionOutbox{
		ID: outbox.ID, StepID: outbox.StepID,
		IndexAlias: outbox.IndexAlias, DocumentID: outbox.DocumentID,
		Payload: []byte(outbox.PayloadJSON), Status: agentruntime.ProjectionStatus(outbox.Status),
		AttemptCount: outbox.AttemptCount, NextAttemptAt: outbox.NextAttemptAt,
		WorkerID: outbox.WorkerID, LeaseExpiresAt: outbox.LeaseExpiresAt,
		LastError: outbox.LastError,
	}
}
