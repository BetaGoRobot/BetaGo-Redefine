package agentstore

import (
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"gorm.io/gorm"
)

func newProjectionOutbox(stepID string, projection agentruntime.ProjectionDocument, now time.Time) *model.AgentProjectionOutbox {
	sum := sha256.Sum256([]byte(stepID))
	return &model.AgentProjectionOutbox{
		ID:            "outbox_" + hex.EncodeToString(sum[:]),
		StepID:        stepID,
		IndexAlias:    projection.IndexAlias,
		DocumentID:    projection.DocumentID,
		PayloadJSON:   string(projection.Payload),
		Status:        "pending",
		NextAttemptAt: now,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
}

func insertProjectionOutbox(tx *gorm.DB, stepID string, projection agentruntime.ProjectionDocument, now time.Time) error {
	return tx.Create(newProjectionOutbox(stepID, projection, now)).Error
}

func findProjectionOutboxByStep(tx *gorm.DB, stepID string) (*model.AgentProjectionOutbox, error) {
	var outbox model.AgentProjectionOutbox
	if err := tx.Where("step_id = ?", stepID).First(&outbox).Error; err != nil {
		return nil, err
	}
	return &outbox, nil
}
