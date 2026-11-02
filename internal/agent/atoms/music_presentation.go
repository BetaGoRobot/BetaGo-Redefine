package atoms

import (
	"context"
	"encoding/json"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/musiccards"
)

func musicPresentationDefinitions(o BusinessOptions, fingerprint string) []Definition {
	if o.MusicCards == nil {
		return nil
	}
	run := func(ctx context.Context, in Invocation, request musiccards.Request) (string, error) {
		if in.ExecutionKey == "" {
			return "", domain.ErrInvalid
		}
		receipt, err := o.MusicCards.Send(ctx, businessIdentity(in), in.Task.Root.MessageID, request)
		if err != nil && receipt.State != "rejected" {
			return "", err
		}
		return businessJSON(receipt)
	}
	selected := businessDefinition("music_send_card", "Send a rich detail/player card for one explicitly selected stable song/album/playlist ID. Includes uploaded cover, lyrics/player and asynchronous native audio for songs; track lists have working page/select controls. Never substitute another ID or repeat keyword search. For a general keyword search, send the interactive result list with music_send_search_card so the user can select. Returns the actual card receipt; later media is added to the same card.", `{"type":"object","properties":{"type":{"type":"string","enum":["song","album","playlist"]},"id":{"type":"integer","minimum":1},"page":{"type":"integer","minimum":1,"maximum":2001}},"required":["type","id"],"additionalProperties":false}`, fingerprint, func(ctx context.Context, in Invocation, raw string) (string, error) {
		var a struct {
			Type string
			ID   int64
			Page int
		}
		if json.Unmarshal([]byte(raw), &a) != nil {
			return "", domain.ErrInvalid
		}
		return run(ctx, in, musiccards.Request{Kind: a.Type, ID: a.ID, Page: a.Page})
	})
	search := businessDefinition("music_send_search_card", "Search songs, albums or playlists and send the rich interactive result card requested by this person. Shows five results per page, uploaded covers, comments and working selection/page buttons. Use this for keyword discovery instead of silently selecting one result; use music_send_card only for an explicitly selected stable ID. Returns the actual card receipt. Resource enrichment updates that same card, without extra chat messages.", `{"type":"object","properties":{"type":{"type":"string","enum":["song","album","playlist"]},"keywords":{"type":"string","minLength":1,"maxLength":1024},"page":{"type":"integer","minimum":1,"maximum":2001}},"required":["type","keywords"],"additionalProperties":false}`, fingerprint, func(ctx context.Context, in Invocation, raw string) (string, error) {
		var a struct {
			Type, Keywords string
			Page           int
		}
		if json.Unmarshal([]byte(raw), &a) != nil {
			return "", domain.ErrInvalid
		}
		return run(ctx, in, musiccards.Request{Scene: a.Type, Query: a.Keywords, Page: a.Page})
	})
	for _, d := range []*Definition{&selected, &search} {
		d.SideEffect = true
		d.AutoExecute = true
		d.PerActorTurn = true
	}
	return []Definition{selected, search}
}
