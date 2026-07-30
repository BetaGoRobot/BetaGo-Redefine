package agentstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type continuationStepInput struct {
	Version      int    `json:"version"`
	SourceStepID string `json:"source_step_id"`
}

func enqueueContinuationStepTx(
	tx *gorm.DB,
	run *model.AgentRun,
	sourceStepID string,
	dedupeBase string,
	now time.Time,
) (*model.AgentStep, error) {
	dedupeKey := dedupeBase + ":continuation"
	var existing model.AgentStep
	err := tx.Where("run_id = ? AND dedupe_key = ?", run.ID, dedupeKey).First(&existing).Error
	if err == nil {
		return &existing, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	index, err := nextStepIndex(tx, run)
	if err != nil {
		return nil, err
	}
	input, err := json.Marshal(continuationStepInput{Version: 1, SourceStepID: sourceStepID})
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(run.ID + "\x00" + dedupeKey))
	step := &model.AgentStep{
		ID: "step_continuation_" + hex.EncodeToString(sum[:]), RunID: run.ID, Index: index,
		Kind: string(agentruntime.StepKindDecide), Status: string(agentruntime.StepStatusQueued),
		InputJSON: string(input), OutputJSON: "{}", CreatedAt: now, DedupeKey: dedupeKey,
	}
	if err := tx.Create(step).Error; err != nil {
		return nil, err
	}
	run.CurrentStepIndex = index
	return step, nil
}

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
		if step.DedupeKey != "" {
			var existing model.AgentStep
			err := tx.Where("run_id = ? AND dedupe_key = ?", step.RunID, step.DedupeKey).
				First(&existing).Error
			if err == nil {
				stored = toRuntimeStep(&existing)
				return nil
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}
		if step.Status == agentruntime.StepStatusQueued &&
			terminalRunStatus(agentruntime.RunStatus(run.Status)) {
			return ErrTerminalRun
		}
		if step.Status == agentruntime.StepStatusQueued &&
			waitingRunStatus(agentruntime.RunStatus(run.Status)) {
			return ErrInteractionConflict
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
		runUpdates := map[string]any{"current_step_index": candidate.Index, "updated_at": now}
		if candidate.Status == agentruntime.StepStatusQueued {
			runUpdates["status"] = string(agentruntime.RunStatusQueued)
			runUpdates["last_relevant_at"] = candidate.CreatedAt
		}
		result = tx.Model(&model.AgentRun{}).Where("id = ?", candidate.RunID).Updates(runUpdates)
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
			SELECT steps.*
			FROM agent_steps AS steps
			JOIN agent_runs AS runs ON runs.id = steps.run_id
			WHERE steps.status = ?
			  AND (steps.lease_expires_at IS NULL OR steps.lease_expires_at <= ?)
			  AND runs.status IN (?, ?)
			  AND NOT EXISTS (
				  SELECT 1
				  FROM agent_steps AS active
				  WHERE active.run_id = steps.run_id
				    AND active.status = ?
			  )
			ORDER BY steps.created_at, steps.id
			FOR UPDATE OF runs, steps SKIP LOCKED
			LIMIT 1`,
			string(agentruntime.StepStatusQueued), claim.Now,
			string(agentruntime.RunStatusQueued), string(agentruntime.RunStatusRunning),
			string(agentruntime.StepStatusRunning),
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
		Where("id = ? AND status = ? AND worker_id = ? AND attempt_count = ?",
			req.StepID, string(agentruntime.StepStatusRunning), req.WorkerID, req.AttemptCount).
		Updates(map[string]any{
			"status":           string(agentruntime.StepStatusCompleted),
			"output_json":      string(req.Output),
			"error_text":       "",
			"finished_at":      req.FinishedAt,
			"worker_id":        "",
			"lease_expires_at": nil,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return agentruntime.ErrLeaseLost
	}
	return nil
}

func (r *Repository) RetryStep(ctx context.Context, req agentruntime.RetryStepRequest) error {
	if err := req.Validate(); err != nil {
		return err
	}
	result := r.db.WithContext(ctx).Model(&model.AgentStep{}).
		Where("id = ? AND status = ? AND worker_id = ? AND attempt_count = ?",
			req.StepID, string(agentruntime.StepStatusRunning), req.WorkerID, req.AttemptCount).
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
		return agentruntime.ErrLeaseLost
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
	if step.Kind != agentruntime.StepKindObserve {
		return invalidContract("step kind must be observe")
	}
	if step.Status != agentruntime.StepStatusQueued &&
		step.Status != agentruntime.StepStatusCompleted {
		return invalidContract("step status must be queued or completed")
	}
	if step.InputJSON == "" || !json.Valid([]byte(step.InputJSON)) {
		return invalidContract("step input_json must be valid JSON")
	}
	if step.OutputJSON == "" || !json.Valid([]byte(step.OutputJSON)) {
		return invalidContract("step output_json must be valid JSON")
	}
	if step.Status == agentruntime.StepStatusQueued &&
		(step.AttemptCount != 0 || step.WorkerID != "" || !step.LeaseExpiresAt.IsZero() ||
			step.RetryOfStepID != "" || !step.StartedAt.IsZero() || !step.FinishedAt.IsZero()) {
		return invalidContract("queued observation must not contain execution state")
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
