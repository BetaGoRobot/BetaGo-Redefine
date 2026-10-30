package lark

import (
	"context"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	core "github.com/larksuite/oapi-sdk-go/v3/core"
	im "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

// Retract checks the provider's current group and exact application/bot sender
// before deleting. It never inherits moderator permission to delete user text.
func (c *Client) Retract(ctx context.Context, scope domain.Scope, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	message, err := c.get(ctx, scope, id)
	if err != nil {
		return err
	}
	sender := message.Sender
	if sender == nil {
		return domain.ErrUnauthorized
	}
	own := false
	switch value(sender.IdType) {
	case "app_id":
		own = value(sender.Id) == c.cfg.AppID && (value(sender.OpenBotId) == "" || value(sender.OpenBotId) == c.cfg.BotOpenID)
	case "open_id":
		own = value(sender.Id) == c.cfg.BotOpenID && (value(sender.OpenBotId) == "" || value(sender.OpenBotId) == c.cfg.BotOpenID)
	}
	if !own || (value(sender.SenderType) != "app" && value(sender.SenderType) != "bot") {
		return domain.ErrUnauthorized
	}
	if message.Deleted != nil && *message.Deleted {
		return nil
	}
	response, err := c.sdk.Im.Message.Delete(ctx, im.NewDeleteMessageReqBuilder().MessageId(id).Build(), core.WithTenantKey(scope.TenantID))
	if err != nil || response == nil {
		return communityAPIError(ctx, err, 0, 0)
	}
	if !response.Success() {
		return communityAPIError(ctx, nil, response.StatusCode, response.Code, response.CodeError)
	}
	return nil
}
