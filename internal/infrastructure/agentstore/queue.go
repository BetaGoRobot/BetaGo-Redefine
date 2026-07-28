package agentstore

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (r *Repository) AppendEvent(ctx context.Context, step *agentruntime.AgentStep, projection agentruntime.ProjectionDocument) (*agentruntime.AgentStep, error) {
	if err := validateAppendEvent(step, projection); err != nil {
		return nil, err
	}
	var stored *agentruntime.AgentStep
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var run model.AgentRun
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&run, "id = ?", step.RunID).Error; err != nil {
			return mapNotFound(err)
		}

		var maxIndex int32
		if err := tx.Model(&model.AgentStep{}).
			Select(`COALESCE(MAX("index"), -1)`).
			Where("run_id = ?", step.RunID).
			Scan(&maxIndex).Error; err != nil {
			return err
		}
		if run.CurrentStepIndex > maxIndex {
			maxIndex = run.CurrentStepIndex
		}
		candidate := *step
		candidate.Index = maxIndex + 1
		if candidate.CreatedAt.IsZero() {
			candidate.CreatedAt = time.Now().UTC()
		}

		dbStep := toDBStep(&candidate)
		create := tx
		if candidate.DedupeKey != "" {
			create = create.Clauses(clause.OnConflict{
				Columns: []clause.Column{{Name: "run_id"}, {Name: "dedupe_key"}},
				TargetWhere: clause.Where{Exprs: []clause.Expression{
					clause.Neq{Column: clause.Column{Name: "dedupe_key"}, Value: ""},
				}},
				DoNothing: true,
			})
		}
		result := create.Create(dbStep)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			var existing model.AgentStep
			if err := tx.Where("run_id = ? AND dedupe_key = ?", candidate.RunID, candidate.DedupeKey).
				First(&existing).Error; err != nil {
				return mapNotFound(err)
			}
			stored = toRuntimeStep(&existing)
			return nil
		}

		now := time.Now().UTC()
		if err := insertProjectionOutbox(tx, candidate.ID, projection, now); err != nil {
			return err
		}
		result = tx.Model(&model.AgentRun{}).Where("id = ?", candidate.RunID).
			Updates(map[string]any{"current_step_index": candidate.Index, "updated_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return agentruntime.ErrNotFound
		}
		stored = &candidate
		return nil
	})
	return stored, err
}

func (r *Repository) ClaimQueuedStep(ctx context.Context, claim agentruntime.StepClaim) (*agentruntime.AgentStep, error) {
	if err := claim.Validate(); err != nil {
		return nil, err
	}
	var claimed model.AgentStep
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Raw(`
			SELECT *
			FROM agent_steps
			WHERE status = ?
			  AND (lease_expires_at IS NULL OR lease_expires_at <= ?)
			ORDER BY created_at, id
			FOR UPDATE SKIP LOCKED
			LIMIT 1`,
			string(agentruntime.StepStatusQueued), claim.Now,
		).Scan(&claimed)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return agentruntime.ErrNotFound
		}
		leaseExpiresAt := claim.Now.Add(claim.LeaseTTL)
		result = tx.Model(&model.AgentStep{}).
			Where("id = ? AND status = ?", claimed.ID, string(agentruntime.StepStatusQueued)).
			Updates(map[string]any{
				"status":           string(agentruntime.StepStatusRunning),
				"attempt_count":    gorm.Expr("attempt_count + 1"),
				"worker_id":        claim.WorkerID,
				"lease_expires_at": leaseExpiresAt,
				"started_at":       claim.Now,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return agentruntime.ErrNotFound
		}
		claimed.Status = string(agentruntime.StepStatusRunning)
		claimed.AttemptCount++
		claimed.WorkerID = claim.WorkerID
		claimed.LeaseExpiresAt = leaseExpiresAt
		claimed.StartedAt = claim.Now
		return nil
	})
	if err != nil {
		return nil, err
	}
	return toRuntimeStep(&claimed), nil
}

func (r *Repository) CompleteStep(ctx context.Context, req agentruntime.CompleteStepRequest) error {
	if err := req.Validate(); err != nil {
		return err
	}
	result := r.db.WithContext(ctx).Model(&model.AgentStep{}).
		Where("id = ? AND status = ?", req.StepID, string(agentruntime.StepStatusRunning)).
		Updates(map[string]any{
			"status":           string(agentruntime.StepStatusCompleted),
			"output_json":      string(req.Output),
			"finished_at":      req.FinishedAt,
			"worker_id":        "",
			"lease_expires_at": nil,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return agentruntime.ErrNotFound
	}
	return nil
}

func (r *Repository) RetryStep(ctx context.Context, req agentruntime.RetryStepRequest) error {
	if err := req.Validate(); err != nil {
		return err
	}
	result := r.db.WithContext(ctx).Model(&model.AgentStep{}).
		Where("id = ? AND status = ?", req.StepID, string(agentruntime.StepStatusRunning)).
		Updates(map[string]any{
			"status":           string(agentruntime.StepStatusQueued),
			"error_text":       req.ErrorText,
			"worker_id":        "",
			"lease_expires_at": req.RetryAt,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return agentruntime.ErrNotFound
	}
	return nil
}

func (r *Repository) ReclaimStaleSteps(ctx context.Context, req agentruntime.ReclaimStaleStepsRequest) (int64, error) {
	if err := req.Validate(); err != nil {
		return 0, err
	}
	var count int64
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Exec(`
			WITH stale AS (
				SELECT id
				FROM agent_steps
				WHERE status = ?
				  AND lease_expires_at < ?
				ORDER BY lease_expires_at, id
				FOR UPDATE SKIP LOCKED
				LIMIT ?
			)
			UPDATE agent_steps AS steps
			SET status = ?, worker_id = '', lease_expires_at = NULL
			FROM stale
			WHERE steps.id = stale.id`,
			string(agentruntime.StepStatusRunning), req.Now, req.Limit,
			string(agentruntime.StepStatusQueued),
		)
		if result.Error != nil {
			return result.Error
		}
		count = result.RowsAffected
		return nil
	})
	return count, err
}

func validateAppendEvent(step *agentruntime.AgentStep, projection agentruntime.ProjectionDocument) error {
	if step == nil {
		return invalidContract("step is required")
	}
	if err := validateCanonicalValue("step id", step.ID, false); err != nil {
		return err
	}
	if err := validateCanonicalValue("run id", step.RunID, false); err != nil {
		return err
	}
	if err := validateCanonicalValue("dedupe key", step.DedupeKey, true); err != nil {
		return err
	}
	return projection.Validate()
}

func validateCanonicalValue(field, value string, allowEmpty bool) error {
	if value == "" {
		if allowEmpty {
			return nil
		}
		return invalidContract(field + " is required")
	}
	if strings.TrimSpace(value) != value {
		return invalidContract(field + " must not have surrounding whitespace")
	}
	return nil
}

func invalidContract(reason string) error {
	return fmt.Errorf("%w: %s", agentruntime.ErrInvalidRuntimeContract, reason)
}

func mapNotFound(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return agentruntime.ErrNotFound
	}
	return err
}
