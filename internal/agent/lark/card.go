package lark

import (
	"context"
	"encoding/json"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	core "github.com/larksuite/oapi-sdk-go/v3/core"
	cardkit "github.com/larksuite/oapi-sdk-go/v3/service/cardkit/v1"
)

// UpdateCard changes only this application's interactive message in the exact
// group. CardKit atomically enforces the frozen sequence and idempotency UUID;
// an older worker arriving late cannot overwrite a newer confirmed frame.
func (c *Client) UpdateCard(ctx context.Context, scope domain.Scope, id, content, uuid string, sequence int) error {
	var card struct {
		Config struct {
			UpdateMulti bool `json:"update_multi"`
		} `json:"config"`
	}
	if c.authorize(scope) != nil || uuid == "" || len(uuid) > 50 || sequence < 1 || sequence > 2147483647 || len(content) > domain.MaxConversationBytes || json.Unmarshal([]byte(content), &card) != nil || !card.Config.UpdateMulti {
		return errMalformed
	}
	m, err := c.get(ctx, scope, id)
	if err != nil {
		return err
	}
	if value(m.MsgType) != "interactive" || (m.Deleted != nil && *m.Deleted) || m.Sender == nil {
		return errMalformed
	}
	own := false
	switch value(m.Sender.IdType) {
	case "app_id":
		own = value(m.Sender.Id) == c.cfg.AppID && (value(m.Sender.OpenBotId) == "" || value(m.Sender.OpenBotId) == c.cfg.BotOpenID)
	case "open_id":
		own = value(m.Sender.Id) == c.cfg.BotOpenID && (value(m.Sender.OpenBotId) == "" || value(m.Sender.OpenBotId) == c.cfg.BotOpenID)
	}
	if !own || (value(m.Sender.SenderType) != "app" && value(m.Sender.SenderType) != "bot") {
		return errMalformed
	}

	converted, err := c.sdk.Cardkit.V1.Card.IdConvert(ctx, cardkit.NewIdConvertCardReqBuilder().Body(cardkit.NewIdConvertCardReqBodyBuilder().MessageId(id).Build()).Build(), core.WithTenantKey(scope.TenantID))
	if err != nil || converted == nil || !converted.Success() || converted.Data == nil || value(converted.Data.CardId) == "" {
		return errAPI
	}
	r, err := c.sdk.Cardkit.V1.Card.Update(ctx, cardkit.NewUpdateCardReqBuilder().CardId(value(converted.Data.CardId)).Body(cardkit.NewUpdateCardReqBodyBuilder().Card(cardkit.NewCardBuilder().Type("card_json").Data(content).Build()).Uuid(uuid).Sequence(sequence).Build()).Build(), core.WithTenantKey(scope.TenantID))

	// CardKit consumes a UUID only after accepting an operation. Its exact
	// 200770 replay acknowledgment confirms the backend-frozen target, content,
	// sequence and UUID after a lost receipt; other provider errors stay errors.
	if err != nil || r == nil || (!r.Success() && r.Code != 200770) {
		return errAPI
	}
	return nil
}
