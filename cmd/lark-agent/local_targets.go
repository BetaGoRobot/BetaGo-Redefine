package main

import (
	"context"
	"errors"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/pgstore"
)

// Reply targets are proven by locally recorded originals or confirmed own
// deliveries. Display names and a guessed external message ID grant nothing.
func localTargetValidator(store *pgstore.Store) func(context.Context, domain.Scope, string) error {
	return func(ctx context.Context, scope domain.Scope, id string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if scope.Validate() != nil || id == "" {
			return domain.ErrUnauthorized
		}
		m, err := store.Message(ctx, scope, id)
		if err == nil {
			if m.Source.Scope != scope || m.Source.MessageID != id {
				return domain.ErrUnauthorized
			}
			if m.Deleted {
				return domain.ErrStaleSource
			}
			return nil
		}
		if !errors.Is(err, domain.ErrNotFound) {
			return err
		}
		_, receipt, err := store.TaskForMessage(ctx, scope, id)
		if err != nil {
			return err
		}
		if receipt == nil || receipt.Scope != scope || receipt.MessageID != id || receipt.Status != "delivered" {
			return domain.ErrUnauthorized
		}
		if _, ordinary := domain.ConversationDeliveryText(*receipt); !ordinary {
			return domain.ErrUnauthorized
		}
		return nil
	}
}
