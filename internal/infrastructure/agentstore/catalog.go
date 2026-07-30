package agentstore

import (
	"context"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
)

var _ agentruntime.ContinuationCatalog = (*Repository)(nil)

func (r *Repository) FindRunChatID(ctx context.Context, runID string) (string, error) {
	if strings.TrimSpace(runID) != runID || runID == "" {
		return "", agentruntime.ErrInvalidRuntimeContract
	}
	var row struct {
		ChatID string
	}
	result := r.db.WithContext(ctx).Raw(`
		SELECT sessions.chat_id
		FROM agent_runs AS runs
		JOIN agent_sessions AS sessions ON sessions.id = runs.session_id
		WHERE runs.id = ?
		  AND runs.tenant_id = ?
		LIMIT 1`,
		runID, r.tenant.ID,
	).Scan(&row)
	if result.Error != nil {
		return "", result.Error
	}
	if result.RowsAffected == 0 {
		return "", agentruntime.ErrNotFound
	}
	return row.ChatID, nil
}

func (r *Repository) ListDueContinuationRunIDs(ctx context.Context, limit int) ([]string, error) {
	if limit <= 0 || limit > 1024 {
		return nil, agentruntime.ErrInvalidRuntimeContract
	}
	type candidate struct {
		RunID string
	}
	var candidates []candidate
	now := time.Now().UTC()
	result := r.db.WithContext(ctx).Raw(`
		SELECT runs.id AS run_id
		FROM agent_runs AS runs
		JOIN agent_sessions AS sessions
		  ON sessions.id = runs.session_id
		 AND sessions.active_run_id = runs.id
		JOIN agent_steps AS steps ON steps.run_id = runs.id
		WHERE runs.status IN (?, ?)
		  AND runs.tenant_id = ?
		  AND (
		    (
		      steps.kind = ?
		      AND steps.input_json->>'version' = '1'
		      AND COALESCE(steps.input_json->>'source_step_id', '') <> ''
		      AND steps.dedupe_key LIKE '%:continuation'
		      AND EXISTS (
		        SELECT 1 FROM agent_steps AS source
		        WHERE source.id = steps.input_json->>'source_step_id'
		          AND source.run_id = steps.run_id
		          AND source.index < steps.index
		          AND source.kind IN (?, ?)
		          AND source.status = ?
		          AND source.dedupe_key <> ''
		          AND steps.dedupe_key = source.dedupe_key || ':continuation'
		      )
		    )
		    OR (
		      steps.kind = ?
		      AND steps.input_json->>'version' = '1'
		      AND steps.input_json->>'step_id' = steps.id
		      AND steps.input_json->>'run_id' = steps.run_id
		      AND steps.input_json->>'idempotency_key' = steps.id
		      AND steps.dedupe_key LIKE '%:continuation:reply'
		    )
		    OR (
		      steps.kind = ?
		      AND steps.input_json->>'version' = '1'
		      AND COALESCE(steps.input_json->>'source_step_id', '') <> ''
		      AND COALESCE(steps.input_json->>'interaction_id', '') <> ''
		      AND COALESCE(steps.input_json->>'action_id', '') <> ''
		      AND steps.input_json->'descriptor'->>'capability_name' = steps.capability_name
		      AND steps.dedupe_key LIKE '%:capability'
		      AND EXISTS (
		        SELECT 1 FROM agent_steps AS source
		        WHERE source.id = steps.input_json->>'source_step_id'
		          AND source.run_id = steps.run_id
		          AND source.index < steps.index
		          AND source.kind = ?
		          AND source.status = ?
		          AND source.external_ref = steps.external_ref
		      )
		    )
		  )
		  AND (
		    (steps.status = ? AND (steps.lease_expires_at IS NULL OR steps.lease_expires_at <= ?))
		    OR
		    (steps.status = ? AND steps.lease_expires_at <= ?)
		  )
		GROUP BY runs.id
		ORDER BY MIN(COALESCE(steps.lease_expires_at, steps.created_at)), runs.id
		LIMIT ?`,
		string(agentruntime.RunStatusQueued), string(agentruntime.RunStatusRunning),
		r.tenant.ID,
		string(agentruntime.StepKindDecide),
		string(agentruntime.StepKindCapabilityResult), string(agentruntime.StepKindResume),
		string(agentruntime.StepStatusCompleted),
		string(agentruntime.StepKindReply),
		string(agentruntime.StepKindCapabilityCall),
		string(agentruntime.StepKindCardAction),
		string(agentruntime.StepStatusCompleted),
		string(agentruntime.StepStatusQueued), now,
		string(agentruntime.StepStatusRunning), now,
		limit,
	).Scan(&candidates)
	if result.Error != nil {
		return nil, result.Error
	}
	runIDs := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		runIDs = append(runIDs, candidate.RunID)
	}
	return runIDs, nil
}
