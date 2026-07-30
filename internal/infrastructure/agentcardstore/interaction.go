package agentcardstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentcard"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var _ agentcard.InteractionStore = (*Repository)(nil)

type runtimeWaitPayload struct {
	Version       int             `json:"version"`
	InteractionID string          `json:"interaction_id"`
	Kind          string          `json:"kind"`
	Revision      int64           `json:"revision"`
	ExpiresAt     time.Time       `json:"expires_at"`
	TokenHash     string          `json:"token_hash"`
	TrustedInput  json.RawMessage `json:"trusted_input"`
}

func (r *Repository) BeginCardInteraction(
	ctx context.Context,
	request agentcard.BeginCardInteractionRequest,
) (*agentcard.CardSurface, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}
	if r == nil || r.db == nil {
		return nil, errors.New("agent card repository database is required")
	}

	var stored *model.AgentCardSurface
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var reference struct {
			SessionID string
		}
		if err := tx.Model(&model.AgentRun{}).
			Select("session_id").
			Where("id = ?", request.RunID).
			Take(&reference).Error; err != nil {
			return mapStoreNotFound(err)
		}
		var session model.AgentSession
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&session, "id = ?", reference.SessionID).Error; err != nil {
			return mapStoreNotFound(err)
		}
		var run model.AgentRun
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND session_id = ?", request.RunID, reference.SessionID).
			First(&run).Error; err != nil {
			return mapStoreNotFound(err)
		}
		if session.ActiveRunID != "" && session.ActiveRunID != request.RunID {
			return agentcard.ErrCardConflict
		}

		var existing model.AgentCardSurface
		existingErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&existing, "id = ?", request.SurfaceID).Error
		switch {
		case existingErr == nil:
			matches, err := existingBeginMatches(tx, &run, &existing, request)
			if err != nil {
				return err
			}
			if !matches {
				return agentcard.ErrCardConflict
			}
			stored = &existing
			return nil
		case !errors.Is(existingErr, gorm.ErrRecordNotFound):
			return existingErr
		}

		if isTerminalRun(agentruntime.RunStatus(run.Status)) ||
			isWaitingRun(agentruntime.RunStatus(run.Status)) ||
			run.WaitingReason != "" || run.WaitingToken != "" ||
			run.Revision != request.ExpectedRunRevision ||
			request.Revision != run.Revision+1 {
			return agentcard.ErrCardConflict
		}
		var blockingCount int64
		if err := tx.Model(&model.AgentCardSurface{}).
			Where("run_id = ? AND status IN ?", request.RunID, []string{
				string(agentcard.SurfaceStatusDraft),
				string(agentcard.SurfaceStatusSent),
				string(agentcard.SurfaceStatusSubmitted),
				string(agentcard.SurfaceStatusProcessing),
			}).
			Count(&blockingCount).Error; err != nil {
			return err
		}
		if blockingCount != 0 {
			return agentcard.ErrCardConflict
		}

		index, err := nextCardStepIndex(tx, &run)
		if err != nil {
			return err
		}
		waitInput, err := json.Marshal(runtimeWaitPayload{
			Version: 1, InteractionID: request.InteractionID,
			Kind: request.InteractionKind, Revision: request.Revision,
			ExpiresAt: request.ExpiresAt, TokenHash: request.TokenHash,
			TrustedInput: append(json.RawMessage(nil), request.TrustedInput...),
		})
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		wait := &model.AgentStep{
			ID: request.StepID, TenantID: r.tenant.ID,
			RunID: request.RunID, Index: index,
			Kind:      string(agentruntime.StepKindWait),
			Status:    string(agentruntime.StepStatusCompleted),
			InputJSON: string(waitInput), OutputJSON: "{}",
			ExternalRef: request.InteractionID, StartedAt: now,
			FinishedAt: now, CreatedAt: now,
			DedupeKey: cardInteractionDedupeKey(
				request.InteractionID,
				request.Revision,
			),
		}
		if err := tx.Create(wait).Error; err != nil {
			return err
		}
		if err := insertCardProjectionOutbox(
			tx,
			request.StepID,
			request.Projection,
			now,
		); err != nil {
			return err
		}
		surface := &model.AgentCardSurface{
			ID: request.SurfaceID, TenantID: r.tenant.ID,
			RunID:      request.RunID,
			WaitStepID: request.StepID, InteractionID: request.InteractionID,
			ChatID: request.ChatID, ReplyToMessageID: request.ReplyToMessageID,
			SpecVersion: request.SpecVersion, SpecJSON: request.SpecJSON,
			CompiledJSONRedacted: request.CompiledJSONRedacted,
			Status:               string(agentcard.SurfaceStatusDraft),
			Revision:             request.Revision,
			ExpectedActorOpenID:  request.ExpectedActorOpenID,
			InteractionKind:      request.InteractionKind, ExpiresAt: request.ExpiresAt,
			PatchStatus: string(agentcard.PatchStatusIdle),
			NextPatchAt: now, CreatedAt: now, UpdatedAt: now,
		}
		if err := tx.Create(surface).Error; err != nil {
			return err
		}

		status, reason := cardWaitingState(request.InteractionKind)
		result := tx.Model(&model.AgentRun{}).
			Where("id = ? AND revision = ?", request.RunID, request.ExpectedRunRevision).
			Updates(map[string]any{
				"status": string(status), "waiting_reason": string(reason),
				"waiting_token": request.TokenHash, "revision": request.Revision,
				"current_step_index": index, "updated_at": now,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return agentcard.ErrCardConflict
		}
		result = tx.Model(&model.AgentSession{}).
			Where("id = ?", session.ID).
			Updates(map[string]any{
				"active_run_id": request.RunID,
				"updated_at":    now,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return agentcard.ErrCardNotFound
		}
		stored = surface
		return nil
	})
	if err != nil {
		return nil, err
	}
	return toApplicationSurface(stored), nil
}

func (r *Repository) GetByInteraction(
	ctx context.Context,
	request agentcard.GetSurfaceRequest,
) (*agentcard.CardSurface, error) {
	if strings.TrimSpace(request.RunID) == "" ||
		strings.TrimSpace(request.InteractionID) == "" {
		return nil, errors.New("run id and interaction id are required")
	}
	var surface model.AgentCardSurface
	if err := r.db.WithContext(ctx).
		Where(
			"run_id = ? AND interaction_id = ?",
			request.RunID,
			request.InteractionID,
		).
		First(&surface).Error; err != nil {
		return nil, mapStoreNotFound(err)
	}
	return toApplicationSurface(&surface), nil
}

func existingBeginMatches(
	tx *gorm.DB,
	run *model.AgentRun,
	surface *model.AgentCardSurface,
	request agentcard.BeginCardInteractionRequest,
) (bool, error) {
	if surface.RunID != request.RunID ||
		surface.WaitStepID != request.StepID ||
		surface.InteractionID != request.InteractionID ||
		surface.ChatID != request.ChatID ||
		surface.ReplyToMessageID != request.ReplyToMessageID ||
		surface.SpecVersion != request.SpecVersion ||
		surface.Status != string(agentcard.SurfaceStatusDraft) ||
		surface.Revision != request.Revision ||
		surface.ExpectedActorOpenID != request.ExpectedActorOpenID ||
		surface.InteractionKind != request.InteractionKind ||
		!equalDatabaseTime(surface.ExpiresAt, request.ExpiresAt) ||
		!equalJSON([]byte(surface.SpecJSON), []byte(request.SpecJSON)) ||
		!equalJSON(
			[]byte(surface.CompiledJSONRedacted),
			[]byte(request.CompiledJSONRedacted),
		) {
		return false, nil
	}
	status, reason := cardWaitingState(request.InteractionKind)
	if run.Status != string(status) || run.WaitingReason != string(reason) ||
		run.Revision != request.Revision ||
		!strings.EqualFold(run.WaitingToken, request.TokenHash) {
		return false, nil
	}
	var wait model.AgentStep
	if err := tx.First(&wait, "id = ?", request.StepID).Error; err != nil {
		return false, mapStoreNotFound(err)
	}
	if wait.RunID != request.RunID ||
		wait.Kind != string(agentruntime.StepKindWait) ||
		wait.Status != string(agentruntime.StepStatusCompleted) ||
		wait.ExternalRef != request.InteractionID {
		return false, nil
	}
	var payload runtimeWaitPayload
	if json.Unmarshal([]byte(wait.InputJSON), &payload) != nil {
		return false, nil
	}
	if payload.Version != 1 ||
		payload.InteractionID != request.InteractionID ||
		payload.Kind != request.InteractionKind ||
		payload.Revision != request.Revision ||
		!payload.ExpiresAt.Equal(request.ExpiresAt) ||
		!strings.EqualFold(payload.TokenHash, request.TokenHash) ||
		!equalJSON(payload.TrustedInput, request.TrustedInput) {
		return false, nil
	}
	var trusted agentcard.TrustedWaitInput
	if json.Unmarshal(payload.TrustedInput, &trusted) != nil ||
		trusted.ComposeKey != request.IdempotencyKey {
		return false, nil
	}
	return true, nil
}

func nextCardStepIndex(tx *gorm.DB, run *model.AgentRun) (int32, error) {
	var maxIndex int32
	if err := tx.Model(&model.AgentStep{}).
		Select(`COALESCE(MAX("index"), -1)`).
		Where("run_id = ?", run.ID).
		Scan(&maxIndex).Error; err != nil {
		return 0, err
	}
	if run.CurrentStepIndex > maxIndex {
		maxIndex = run.CurrentStepIndex
	}
	return maxIndex + 1, nil
}

func insertCardProjectionOutbox(
	tx *gorm.DB,
	stepID string,
	projection agentruntime.ProjectionDocument,
	now time.Time,
) error {
	sum := sha256.Sum256([]byte(stepID))
	documentID := projection.DocumentID
	if !strings.HasSuffix(documentID, ":"+stepID) {
		documentID += ":" + stepID
	}
	scoped := agentruntime.ProjectionDocument{
		IndexAlias: projection.IndexAlias,
		DocumentID: documentID,
		Payload:    append(json.RawMessage(nil), projection.Payload...),
	}
	if err := scoped.Validate(); err != nil {
		return err
	}
	var step model.AgentStep
	if err := tx.Select("tenant_id").First(&step, "id = ?", stepID).Error; err != nil {
		return err
	}
	return tx.Create(&model.AgentProjectionOutbox{
		ID:       "outbox_" + hex.EncodeToString(sum[:]),
		TenantID: step.TenantID, StepID: stepID,
		IndexAlias: scoped.IndexAlias, DocumentID: scoped.DocumentID,
		PayloadJSON: string(scoped.Payload), Status: "pending",
		NextAttemptAt: now, CreatedAt: now, UpdatedAt: now,
	}).Error
}

func equalJSON(left, right []byte) bool {
	var leftValue any
	var rightValue any
	if json.Unmarshal(left, &leftValue) != nil ||
		json.Unmarshal(right, &rightValue) != nil {
		return false
	}
	return reflect.DeepEqual(leftValue, rightValue)
}

func equalDatabaseTime(left, right time.Time) bool {
	return left.Equal(right.UTC().Truncate(time.Microsecond))
}

func cardWaitingState(
	kind string,
) (agentruntime.RunStatus, agentruntime.WaitingReason) {
	switch kind {
	case "approval", "schedule_edit":
		return agentruntime.RunStatusWaitingApproval, agentruntime.WaitingReasonApproval
	case "schedule":
		return agentruntime.RunStatusWaitingSchedule, agentruntime.WaitingReasonSchedule
	default:
		return agentruntime.RunStatusWaitingCallback, agentruntime.WaitingReasonCallback
	}
}

func cardInteractionDedupeKey(interactionID string, revision int64) string {
	return "interaction:" + interactionID + ":" + strconv.FormatInt(revision, 10)
}

func isTerminalRun(status agentruntime.RunStatus) bool {
	return status == agentruntime.RunStatusCompleted ||
		status == agentruntime.RunStatusFailed ||
		status == agentruntime.RunStatusCancelled
}

func isWaitingRun(status agentruntime.RunStatus) bool {
	return status == agentruntime.RunStatusWaitingApproval ||
		status == agentruntime.RunStatusWaitingSchedule ||
		status == agentruntime.RunStatusWaitingCallback
}

func mapStoreNotFound(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return agentcard.ErrCardNotFound
	}
	return err
}
