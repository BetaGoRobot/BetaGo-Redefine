package service

import (
	"context"
	"encoding/json"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/telemetry"
	"go.opentelemetry.io/otel/attribute"
)

// A reminder is a frozen text notification, never a resumed model/tool plan.
// Its own durable deadline is narrower than the provider's UUID dedupe window.
func (s *Service) remind(ctx context.Context, j domain.Job) (retErr error) {
	ctx, span := telemetry.Start(ctx, "agent.reminder")
	defer func() { telemetry.End(ctx, span, retErr) }()
	state, err := s.c.Store.CatalogAccess(ctx, j.Scope)
	if err != nil {
		return err
	}
	if state.Muted(time.Now().UTC()) || !state.FeatureEnabled("reminders") {
		return domain.ErrUnauthorized
	}
	r, err := s.c.Store.PrepareReminder(ctx, j)
	if err != nil {
		return runtimeFailure("reminder.prepare", err)
	}
	if r.Status == "sent" || r.Status == "canceled" {
		telemetry.Info(ctx, "agent.reminder.skipped", "reason", r.Status)
		return nil
	}
	if err = s.c.Store.CheckClaim(ctx, j); err != nil {
		return runtimeFailure("reminder.claim.guard", err)
	}
	body, _ := json.Marshal(map[string]string{"text": r.Text})
	sendCtx, sendSpan := telemetry.Start(ctx, "agent.reminder.send", attribute.Int("content_bytes", len(body)), attribute.String("message_type", "text"))
	id, err := s.c.Messenger.Send(sendCtx, r.Scope, r.Origin.MessageID, "text", string(body), r.UUID)
	err = telemetry.Wrap("reminder.send", "reminder_transport_failed", err)
	telemetry.End(sendCtx, sendSpan, err)
	if err != nil {
		return err
	}
	receiptCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err = s.c.Store.FinishReminder(receiptCtx, j, r, id); err != nil {
		return runtimeFailure("reminder.receipt.persist", err)
	}
	telemetry.Info(ctx, "agent.reminder.persisted", "provider_message_id", id, "content_bytes", len(body))
	return nil
}
