package lark

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	core "github.com/larksuite/oapi-sdk-go/v3/core"
	cardkit "github.com/larksuite/oapi-sdk-go/v3/service/cardkit/v1"
)

// UpdateMusicCard consumes only a locally pinned own-message receipt and a
// persisted immutable frame. It does not refresh a captured message from Lark.
func (c *Client) UpdateMusicCard(ctx context.Context, scope domain.Scope, id, content, uuid string, sequence int) error {
	var card struct {
		Schema string `json:"schema"`
		Config struct {
			UpdateMulti bool `json:"update_multi"`
		} `json:"config"`
	}
	if c.authorize(scope) != nil || c.cfg.ValidateMusicCard == nil || !validBindingID(id, 1024) || uuid == "" || len(uuid) > 50 || sequence < 1 || sequence > 2147483647 || len(content) > 28<<10 || json.Unmarshal([]byte(content), &card) != nil || card.Schema != "2.0" || !card.Config.UpdateMulti {
		return domain.ErrInvalid
	}
	if err := c.cfg.ValidateMusicCard(ctx, scope, id); err != nil {
		return err
	}
	key := domain.StableID(scope.Key(), id)
	c.musicMu.Lock()
	entity := c.musicCards[key]
	c.musicMu.Unlock()
	if entity == "" {
		r, err := c.sdk.Cardkit.V1.Card.IdConvert(ctx, cardkit.NewIdConvertCardReqBuilder().Body(cardkit.NewIdConvertCardReqBodyBuilder().MessageId(id).Build()).Build(), core.WithTenantKey(scope.TenantID))
		if err != nil || r == nil || !r.Success() || r.Data == nil || value(r.Data.CardId) == "" {
			return errAPI
		}
		entity = value(r.Data.CardId)
		c.musicMu.Lock()
		if c.musicCards == nil || len(c.musicCards) >= 256 {
			c.musicCards = make(map[string]string)
		}
		c.musicCards[key] = entity
		c.musicMu.Unlock()
	}
	r, err := c.sdk.Cardkit.V1.Card.Update(ctx, cardkit.NewUpdateCardReqBuilder().CardId(entity).Body(cardkit.NewUpdateCardReqBodyBuilder().Card(cardkit.NewCardBuilder().Type("card_json").Data(content).Build()).Uuid(uuid).Sequence(sequence).Build()).Build(), core.WithTenantKey(scope.TenantID))
	if err == nil && r != nil && !r.Success() && (r.Code == 300260 || r.Code == 230099 && (strings.Contains(r.Msg, "ErrCode: 300260") || strings.Contains(r.Msg, "audio elem don't support forward"))) && hasMusicAudio(content) {
		return domain.ErrMusicAudioUnsupported
	}
	if err != nil || r == nil || (!r.Success() && r.Code != 200770) {
		return errAPI
	}
	return nil
}

func hasMusicAudio(content string) bool {
	var body any
	if json.Unmarshal([]byte(content), &body) != nil {
		return false
	}
	var contains func(any) bool
	contains = func(v any) bool {
		switch value := v.(type) {
		case map[string]any:
			if value["tag"] == "audio" {
				return true
			}
			for _, child := range value {
				if contains(child) {
					return true
				}
			}
		case []any:
			for _, child := range value {
				if contains(child) {
					return true
				}
			}
		}
		return false
	}
	return contains(body)
}
