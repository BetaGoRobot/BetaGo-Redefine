package agentstore

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"gorm.io/gorm"
)

const conversationEventsSchemaVersion = "1"

func newProjectionOutbox(stepID string, projection agentruntime.ProjectionDocument, now time.Time) *model.AgentProjectionOutbox {
	sum := sha256.Sum256([]byte(stepID))
	documentID := projection.DocumentID
	if !strings.HasSuffix(documentID, ":"+stepID) {
		documentID += ":" + stepID
	}
	return &model.AgentProjectionOutbox{
		ID:            "outbox_" + hex.EncodeToString(sum[:]),
		StepID:        stepID,
		IndexAlias:    projection.IndexAlias,
		DocumentID:    documentID,
		PayloadJSON:   string(projection.Payload),
		Status:        "pending",
		NextAttemptAt: now,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
}

func insertProjectionOutbox(tx *gorm.DB, stepID string, projection agentruntime.ProjectionDocument, now time.Time) error {
	outbox := newProjectionOutbox(stepID, projection, now)
	scoped := agentruntime.ProjectionDocument{
		IndexAlias: outbox.IndexAlias,
		DocumentID: outbox.DocumentID,
		Payload:    []byte(outbox.PayloadJSON),
	}
	if err := scoped.Validate(); err != nil {
		return err
	}
	return tx.Create(outbox).Error
}

func findProjectionOutboxByStep(tx *gorm.DB, stepID string) (*model.AgentProjectionOutbox, error) {
	var outbox model.AgentProjectionOutbox
	if err := tx.Where("step_id = ?", stepID).First(&outbox).Error; err != nil {
		return nil, err
	}
	return &outbox, nil
}

func decisionProjectionTx(
	tx *gorm.DB,
	session *model.AgentSession,
	run *model.AgentRun,
	step *model.AgentStep,
	decision agentruntime.TurnDecision,
	occurredAt time.Time,
) (agentruntime.ProjectionDocument, error) {
	var input continuationStepInput
	if json.Unmarshal([]byte(step.InputJSON), &input) != nil || input.SourceStepID == "" {
		return agentruntime.ProjectionDocument{}, agentruntime.ErrInteractionConflict
	}
	source, err := findProjectionOutboxByStep(tx, input.SourceStepID)
	if err != nil {
		return agentruntime.ProjectionDocument{}, err
	}
	content := decision.Reply
	if content == "" {
		content = decision.Reason
	}
	payload, err := json.Marshal(map[string]any{
		"schema_version": conversationEventsSchemaVersion,
		"event_id":       step.ID,
		"event_type":     "model_decision",
		"run_id":         run.ID,
		"step_id":        step.ID,
		"source_step_id": input.SourceStepID,
		"session_id":     session.ID,
		"chat_id":        session.ChatID,
		"actor_open_id":  run.ActorOpenID,
		"status":         "completed",
		"step_status":    "completed",
		"outcome_status": string(decision.Decision),
		"occurred_at":    occurredAt,
		"content":        content,
		"structured_payload": map[string]any{
			"decision":           decision.Decision,
			"reason":             decision.Reason,
			"reply":              decision.Reply,
			"goal":               run.Goal,
			"trigger_message_id": run.TriggerMessageID,
		},
	})
	if err != nil {
		return agentruntime.ProjectionDocument{}, err
	}
	return agentruntime.ProjectionDocument{
		IndexAlias: source.IndexAlias,
		DocumentID: projectionBaseDocumentID(source.DocumentID, input.SourceStepID),
		Payload:    payload,
	}, nil
}

func replyProjectionTx(
	tx *gorm.DB,
	session *model.AgentSession,
	run *model.AgentRun,
	step *model.AgentStep,
	messageID string,
	occurredAt time.Time,
) (agentruntime.ProjectionDocument, error) {
	var frozen agentruntime.ReplyRequest
	if json.Unmarshal([]byte(step.InputJSON), &frozen) != nil ||
		frozen.StepID != step.ID || frozen.RunID != run.ID ||
		frozen.Text == "" || frozen.ChatID == "" || frozen.IdempotencyKey == "" {
		return agentruntime.ProjectionDocument{}, agentruntime.ErrInteractionConflict
	}
	parentDedupe := strings.TrimSuffix(step.DedupeKey, ":reply")
	var parent model.AgentStep
	if parentDedupe == step.DedupeKey ||
		tx.Where("run_id = ? AND kind = ? AND dedupe_key = ?",
			run.ID, string(agentruntime.StepKindDecide), parentDedupe).
			First(&parent).Error != nil {
		return agentruntime.ProjectionDocument{}, agentruntime.ErrInteractionConflict
	}
	source, err := findProjectionOutboxByStep(tx, parent.ID)
	if err != nil {
		return agentruntime.ProjectionDocument{}, err
	}
	route := projectionReplyRoute(frozen)
	payload, err := json.Marshal(map[string]any{
		"schema_version":    conversationEventsSchemaVersion,
		"event_id":          step.ID,
		"event_type":        "agent_reply",
		"run_id":            run.ID,
		"step_id":           step.ID,
		"source_step_id":    parent.ID,
		"parent_step_id":    parent.ID,
		"session_id":        session.ID,
		"chat_id":           session.ChatID,
		"actor_open_id":     run.ActorOpenID,
		"status":            "completed",
		"step_status":       "completed",
		"outcome_status":    "delivered",
		"occurred_at":       occurredAt,
		"content":           frozen.Text,
		"external_ref":      messageID,
		"message_id":        messageID,
		"source_message_id": frozen.TriggerMessageID,
		"structured_payload": map[string]any{
			"message_id":         messageID,
			"text":               frozen.Text,
			"chat_id":            frozen.ChatID,
			"trigger_message_id": frozen.TriggerMessageID,
			"delivery_key":       frozen.IdempotencyKey,
			"route":              route,
		},
	})
	if err != nil {
		return agentruntime.ProjectionDocument{}, err
	}
	return agentruntime.ProjectionDocument{
		IndexAlias: source.IndexAlias,
		DocumentID: projectionBaseDocumentID(source.DocumentID, parent.ID) + ":" + step.ID,
		Payload:    payload,
	}, nil
}

func suppressedReplyProjectionTx(
	tx *gorm.DB,
	session *model.AgentSession,
	run *model.AgentRun,
	step *model.AgentStep,
	reason string,
	occurredAt time.Time,
) (agentruntime.ProjectionDocument, error) {
	var frozen agentruntime.ReplyRequest
	if json.Unmarshal([]byte(step.InputJSON), &frozen) != nil ||
		frozen.StepID != step.ID || frozen.RunID != run.ID ||
		frozen.Text == "" || frozen.ChatID == "" || frozen.IdempotencyKey == "" {
		return agentruntime.ProjectionDocument{}, agentruntime.ErrInteractionConflict
	}
	parentDedupe := strings.TrimSuffix(step.DedupeKey, ":reply")
	var parent model.AgentStep
	if parentDedupe == step.DedupeKey ||
		tx.Where("run_id = ? AND kind = ? AND dedupe_key = ?",
			run.ID, string(agentruntime.StepKindDecide), parentDedupe).
			First(&parent).Error != nil {
		return agentruntime.ProjectionDocument{}, agentruntime.ErrInteractionConflict
	}
	source, err := findProjectionOutboxByStep(tx, parent.ID)
	if err != nil {
		return agentruntime.ProjectionDocument{}, err
	}
	payload, err := json.Marshal(map[string]any{
		"schema_version":    conversationEventsSchemaVersion,
		"event_id":          step.ID,
		"event_type":        "agent_reply",
		"run_id":            run.ID,
		"step_id":           step.ID,
		"source_step_id":    parent.ID,
		"parent_step_id":    parent.ID,
		"session_id":        session.ID,
		"chat_id":           session.ChatID,
		"actor_open_id":     run.ActorOpenID,
		"status":            "completed",
		"step_status":       "completed",
		"outcome_status":    "suppressed",
		"occurred_at":       occurredAt,
		"source_message_id": frozen.TriggerMessageID,
		"structured_payload": map[string]any{
			"reason":             reason,
			"chat_id":            frozen.ChatID,
			"trigger_message_id": frozen.TriggerMessageID,
			"delivery_key":       frozen.IdempotencyKey,
		},
	})
	if err != nil {
		return agentruntime.ProjectionDocument{}, err
	}
	return agentruntime.ProjectionDocument{
		IndexAlias: source.IndexAlias,
		DocumentID: projectionBaseDocumentID(source.DocumentID, parent.ID) + ":" + step.ID,
		Payload:    payload,
	}, nil
}

func projectionReplyRoute(request agentruntime.ReplyRequest) string {
	if request.TriggerMessageID != "" {
		return "reply"
	}
	return "create"
}

func projectionBaseDocumentID(documentID, stepID string) string {
	return strings.TrimSuffix(documentID, ":"+stepID)
}
