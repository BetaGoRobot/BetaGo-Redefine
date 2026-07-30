package agentstore

import (
	"context"
	"encoding/json"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"gorm.io/gorm"
)

var _ agentruntime.InteractionExpirer = (*Repository)(nil)

func (r *Repository) ExpireScheduleEditInteractions(
	ctx context.Context,
	now time.Time,
	limit int,
) (int, error) {
	if now.IsZero() || limit <= 0 || limit > 1024 {
		return 0, agentruntime.ErrInvalidRuntimeContract
	}
	type candidate struct {
		StepID    string
		RunID     string
		SessionID string
	}
	expired := 0
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var candidates []candidate
		result := tx.Raw(`
			SELECT steps.id AS step_id, runs.id AS run_id, sessions.id AS session_id
			FROM agent_steps AS steps
			JOIN agent_runs AS runs ON runs.id = steps.run_id
			JOIN agent_sessions AS sessions
			  ON sessions.id = runs.session_id
			 AND sessions.active_run_id = runs.id
			WHERE steps.kind = ?
			  AND steps.status = ?
			  AND steps.input_json->>'kind' = 'schedule_edit'
			  AND steps.input_json->>'version' = '1'
			  AND (steps.input_json->>'expires_at')::timestamptz <= ?
			  AND runs.status = ?
			  AND runs.waiting_reason = ?
			ORDER BY (steps.input_json->>'expires_at')::timestamptz, steps.id
			FOR UPDATE OF sessions, runs, steps SKIP LOCKED
			LIMIT ?`,
			string(agentruntime.StepKindWait),
			string(agentruntime.StepStatusCompleted),
			now,
			string(agentruntime.RunStatusWaitingApproval),
			string(agentruntime.WaitingReasonApproval),
			limit,
		).Scan(&candidates)
		if result.Error != nil {
			return result.Error
		}
		for _, candidate := range candidates {
			var session model.AgentSession
			var run model.AgentRun
			var wait model.AgentStep
			if err := tx.First(&session, "id = ?", candidate.SessionID).Error; err != nil {
				return mapNotFound(err)
			}
			if err := tx.First(&run, "id = ?", candidate.RunID).Error; err != nil {
				return mapNotFound(err)
			}
			if err := tx.First(&wait, "id = ?", candidate.StepID).Error; err != nil {
				return mapNotFound(err)
			}
			if session.ActiveRunID != run.ID ||
				run.Status != string(agentruntime.RunStatusWaitingApproval) ||
				run.WaitingReason != string(agentruntime.WaitingReasonApproval) {
				continue
			}
			var waitPayload interactionWaitPayload
			if json.Unmarshal([]byte(wait.InputJSON), &waitPayload) != nil ||
				waitPayload.Kind != "schedule_edit" ||
				waitPayload.InteractionID == "" ||
				waitPayload.ExpiresAt.After(now) {
				continue
			}
			timeoutID := stableScheduleStepID(run.ID, waitPayload.InteractionID, "expired")
			index, err := nextStepIndex(tx, &run)
			if err != nil {
				return err
			}
			event := agentruntime.ConversationEvent{
				ID: timeoutID, Type: agentruntime.EventTypeTimeout,
				ChatID: session.ChatID, ActorOpenID: run.ActorOpenID, RunID: run.ID,
				InteractionID: waitPayload.InteractionID, Revision: waitPayload.Revision,
				Action: "schedule.edit_expired", SourceRef: "expiry:" + waitPayload.InteractionID,
				OccurredAt: now, Payload: json.RawMessage(`{"status":"expired"}`),
			}
			input, err := json.Marshal(event)
			if err != nil {
				return err
			}
			timeout := &model.AgentStep{
				ID: timeoutID, RunID: run.ID, Index: index,
				Kind: string(agentruntime.StepKindResume), Status: string(agentruntime.StepStatusCompleted),
				CapabilityName: "interaction_expiry", InputJSON: string(input),
				OutputJSON: `{"status":"expired"}`, ExternalRef: waitPayload.InteractionID,
				StartedAt: now, FinishedAt: now, CreatedAt: now,
				DedupeKey: interactionDedupeKey(waitPayload.InteractionID, waitPayload.Revision) + ":expired",
			}
			if err := tx.Create(timeout).Error; err != nil {
				return err
			}
			source, err := findProjectionOutboxByStep(tx, wait.ID)
			if err != nil {
				return err
			}
			projectionPayload, err := json.Marshal(map[string]any{
				"schema_version": "1",
				"event_id":       timeout.ID,
				"event_type":     "interaction_expired",
				"run_id":         run.ID,
				"step_id":        timeout.ID,
				"source_step_id": wait.ID,
				"session_id":     session.ID,
				"chat_id":        session.ChatID,
				"actor_open_id":  run.ActorOpenID,
				"interaction_id": waitPayload.InteractionID,
				"status":         "cancelled",
				"step_status":    "completed",
				"occurred_at":    now,
			})
			if err != nil {
				return err
			}
			if err := insertProjectionOutbox(tx, timeout.ID, agentruntime.ProjectionDocument{
				IndexAlias: source.IndexAlias,
				DocumentID: projectionBaseDocumentID(source.DocumentID, wait.ID) + ":" + timeout.ID,
				Payload:    projectionPayload,
			}, now); err != nil {
				return err
			}
			runResult := tx.Model(&model.AgentRun{}).
				Where("id = ? AND status = ? AND revision = ?",
					run.ID, string(agentruntime.RunStatusWaitingApproval), waitPayload.Revision).
				Updates(map[string]any{
					"status":             string(agentruntime.RunStatusCancelled),
					"waiting_reason":     "",
					"waiting_token":      "",
					"result_summary":     "schedule edit interaction expired",
					"current_step_index": index,
					"finished_at":        now,
					"updated_at":         now,
				})
			if runResult.Error != nil {
				return runResult.Error
			}
			if runResult.RowsAffected != 1 {
				return agentruntime.ErrInteractionConflict
			}
			sessionResult := tx.Model(&model.AgentSession{}).
				Where("id = ? AND active_run_id = ?", session.ID, run.ID).
				Updates(map[string]any{"active_run_id": "", "updated_at": now})
			if sessionResult.Error != nil {
				return sessionResult.Error
			}
			if sessionResult.RowsAffected != 1 {
				return agentruntime.ErrActiveRunConflict
			}
			expired++
		}
		return nil
	})
	return expired, err
}
