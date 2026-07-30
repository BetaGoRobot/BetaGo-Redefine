package conversationindex

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/tenant"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"gorm.io/gorm"
)

var _ agentruntime.ProjectionOutboxStore = (*Store)(nil)

type Store struct {
	db         *gorm.DB
	tenant     tenant.Tenant
	indexAlias string
}

func NewStore(
	db *gorm.DB,
	owner tenant.Tenant,
	indexAlias string,
) (*Store, error) {
	if err := owner.Validate(); err != nil {
		return nil, fmt.Errorf("conversation index tenant: %w", err)
	}
	indexAlias = strings.TrimSpace(indexAlias)
	if indexAlias == "" ||
		!strings.HasSuffix(indexAlias, "-"+owner.ID) {
		return nil, fmt.Errorf("conversation index alias is not tenant scoped")
	}
	return &Store{db: db, tenant: owner, indexAlias: indexAlias}, nil
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
			WHERE tenant_id = ? AND ((
				status = ? AND next_attempt_at <= ?
			) OR (
				status = ? AND lease_expires_at <= ?
			))
			ORDER BY next_attempt_at, id
			FOR UPDATE SKIP LOCKED
			LIMIT 1`,
			s.tenant.ID, string(agentruntime.ProjectionStatusPending), claim.Now,
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
			Where("id = ? AND tenant_id = ? AND status = ?",
				claimed.ID, s.tenant.ID, claimed.Status).
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
	return s.toProjectionOutbox(&claimed)
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
			"id = ? AND tenant_id = ? AND status = ? AND worker_id = ? AND attempt_count = ? AND lease_expires_at > ?",
			req.OutboxID, s.tenant.ID, string(agentruntime.ProjectionStatusRunning),
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

func (s *Store) RenewProjectionLease(
	ctx context.Context,
	req agentruntime.RenewProjectionLeaseRequest,
) error {
	if err := req.Validate(); err != nil {
		return err
	}
	result := s.db.WithContext(ctx).Model(&model.AgentProjectionOutbox{}).
		Where(
			"id = ? AND tenant_id = ? AND status = ? AND worker_id = ? AND attempt_count = ? AND lease_expires_at > ?",
			req.OutboxID, s.tenant.ID, string(agentruntime.ProjectionStatusRunning),
			req.WorkerID, req.AttemptCount, req.Now,
		).
		Updates(map[string]any{
			"lease_expires_at": req.Now.Add(req.LeaseTTL),
			"updated_at":       req.Now,
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
			"id = ? AND tenant_id = ? AND status = ? AND worker_id = ? AND attempt_count = ? AND lease_expires_at > ?",
			req.OutboxID, s.tenant.ID, string(agentruntime.ProjectionStatusRunning),
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

func (s *Store) toProjectionOutbox(
	outbox *model.AgentProjectionOutbox,
) (*agentruntime.ProjectionOutbox, error) {
	if outbox == nil {
		return nil, nil
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(outbox.PayloadJSON), &payload); err != nil ||
		payload == nil {
		return nil, fmt.Errorf("conversation projection payload must be a JSON object")
	}
	payload["tenant_id"] = s.tenant.ID
	payload["app_id"] = s.tenant.AppID
	payload["bot_open_id"] = s.tenant.BotOpenID
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode tenant conversation projection: %w", err)
	}
	documentID, err := s.tenant.DocumentID(outbox.DocumentID)
	if err != nil {
		return nil, err
	}
	return &agentruntime.ProjectionOutbox{
		ID: outbox.ID, TenantID: outbox.TenantID, StepID: outbox.StepID,
		IndexAlias: s.indexAlias, DocumentID: documentID,
		Payload: encoded, Status: agentruntime.ProjectionStatus(outbox.Status),
		AttemptCount: outbox.AttemptCount, NextAttemptAt: outbox.NextAttemptAt,
		WorkerID: outbox.WorkerID, LeaseExpiresAt: outbox.LeaseExpiresAt,
		LastError: outbox.LastError,
	}, nil
}
