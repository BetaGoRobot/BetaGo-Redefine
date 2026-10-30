package service

import (
	"context"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/telemetry"
	"go.opentelemetry.io/otel/attribute"
)

// CardUpdater is optional so text-only messengers remain usable. Expanded
// answers require provider-fenced updates; they never fall back to extra
// notifications. UUID and sequence are frozen with the content, never per try.
type CardUpdater interface {
	UpdateCard(ctx context.Context, scope domain.Scope, messageID, content, uuid string, sequence int) error
}

func (s *Service) sendFrozenDelivery(ctx context.Context, d domain.Delivery) (receipt string, retErr error) {
	name := "agent.delivery.send"
	if d.UpdateOf != "" {
		name = "agent.delivery.update"
	}
	ctx = telemetry.Context(ctx, "task_id", d.TaskID, "revision", d.Revision)
	ctx, span := telemetry.Start(ctx, name, attribute.String("message_type", d.MessageType), attribute.String("delivery_kind", d.Kind), attribute.Int("content_bytes", len(d.Content)), attribute.Int("source_count", len(d.Sources)), attribute.Int("sequence", d.Sequence))
	telemetry.Payload(ctx, "agent.delivery.content", "delivery_content", d.Content)
	defer func() {
		if retErr == nil {
			span.SetAttributes(attribute.String("provider_message_id", receipt))
			telemetry.Info(ctx, "agent.delivery.receipt", "provider_message_id", receipt, "message_type", d.MessageType, "content_bytes", len(d.Content))
		}
		telemetry.End(ctx, span, retErr)
	}()
	if d.UpdateOf == "" {
		id, err := s.c.Messenger.Send(ctx, d.Scope, d.ParentID, d.MessageType, d.Content, d.UUID)
		return id, telemetry.Wrap("delivery.send", "delivery_transport_failed", err)
	}
	updater, ok := s.c.Messenger.(CardUpdater)
	if !ok || d.Kind != "final" || d.ApprovalID != "" || d.MessageType != "interactive" || d.MessageID == "" {
		return "", telemetry.Wrap("delivery.update.validate", "delivery_update_invalid", domain.ErrConflict)
	}
	if err := updater.UpdateCard(ctx, d.Scope, d.MessageID, d.Content, d.UUID, d.Sequence); err != nil {
		return "", telemetry.Wrap("delivery.update", "delivery_transport_failed", err)
	}
	return d.MessageID, nil
}
