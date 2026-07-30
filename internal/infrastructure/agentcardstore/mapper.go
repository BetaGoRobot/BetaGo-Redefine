package agentcardstore

import (
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentcard"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
)

func toApplicationSurface(surface *model.AgentCardSurface) *agentcard.CardSurface {
	if surface == nil {
		return nil
	}
	return &agentcard.CardSurface{
		ID: surface.ID, RunID: surface.RunID, WaitStepID: surface.WaitStepID,
		InteractionID: surface.InteractionID, ChatID: surface.ChatID,
		ReplyToMessageID: surface.ReplyToMessageID, MessageID: surface.MessageID,
		SpecVersion: surface.SpecVersion, SpecJSON: surface.SpecJSON,
		CompiledJSONRedacted: surface.CompiledJSONRedacted,
		Status:               agentcard.SurfaceStatus(surface.Status), Revision: surface.Revision,
		ExpectedActorOpenID: surface.ExpectedActorOpenID,
		InteractionKind:     surface.InteractionKind, ExpiresAt: surface.ExpiresAt,
		SubmittedAt: surface.SubmittedAt, ProcessingAt: surface.ProcessingAt,
		ResolvedAt: surface.ResolvedAt, CancelledAt: surface.CancelledAt,
		FailedAt: surface.FailedAt, LastActionID: surface.LastActionID,
		LastSourceRef:     surface.LastSourceRef,
		PatchStatus:       agentcard.PatchStatus(surface.PatchStatus),
		PatchAttemptCount: surface.PatchAttemptCount,
		NextPatchAt:       surface.NextPatchAt, PatchWorkerID: surface.PatchWorkerID,
		PatchLeaseExpiresAt: surface.PatchLeaseExpiresAt,
		LastError:           surface.LastError, CreatedAt: surface.CreatedAt,
		UpdatedAt: surface.UpdatedAt,
	}
}
