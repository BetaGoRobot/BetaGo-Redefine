package atoms

import (
	"context"
	"encoding/json"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/business"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/musiccards"
)

// BusinessOptions enables only concrete configured adapters. No legacy handler,
// application configuration singleton, or old card callback is consulted.
type MusicCardPresenter interface {
	Send(context.Context, business.Identity, string, musiccards.Request) (musiccards.Receipt, error)
}

type BusinessOptions struct {
	Music         *business.MusicClient
	MusicCards    MusicCardPresenter
	Luckin        *business.LuckinClient
	LuckinBinding interface {
		RequestBinding(context.Context, business.Identity, bool) (business.BindingReceipt, error)
	}
	Messenger Messenger
	Audio     business.AudioUploader
}

func BusinessDefinitions(o BusinessOptions) ([]Definition, error) {
	definitions := musicBusinessDefinitions(o)
	definitions = append(definitions, luckinBusinessDefinitions(o)...)
	return definitions, nil
}
func businessIdentity(in Invocation) business.Identity {
	return business.Identity{Scope: in.Task.Scope, ActorID: in.ActorID, TaskID: in.Task.ID, ExecutionKey: in.ExecutionKey}
}
func businessJSON(v any) (string, error) {
	b, err := json.Marshal(v)
	if len(b) > 256<<10 {
		return "", domain.ErrInvalid
	}
	return string(b), err
}
func businessDefinition(name, description, schema, fingerprint string, run func(context.Context, Invocation, string) (string, error)) Definition {
	return Definition{Name: name, Description: description, Parameters: json.RawMessage(schema), Digest: domain.StableID(name, description, schema, fingerprint), Run: run}
}

const musicSearchSchema = `{"type":"object","properties":{"keywords":{"type":"string","minLength":1,"maxLength":1024},"type":{"type":"string","enum":["song","album","playlist"]},"limit":{"type":"integer","minimum":1,"maximum":50},"offset":{"type":"integer","minimum":0,"maximum":10000}},"required":["keywords"],"additionalProperties":false}`
const musicDetailSchema = `{"type":"object","properties":{"id":{"type":"integer","minimum":1},"limit":{"type":"integer","minimum":1,"maximum":50},"offset":{"type":"integer","minimum":0,"maximum":10000}},"required":["id"],"additionalProperties":false}`

func musicBusinessDefinitions(o BusinessOptions) []Definition {
	if o.Music == nil {
		return nil
	}
	fingerprint := o.Music.Fingerprint()
	defs := []Definition{businessDefinition("music_search", "Search NetEase songs, albums or playlists. Returns structured stable IDs only. Does not send cards/audio or select a result. Use music_send_search_card for requested interactive search-result cards; selected detail/send tools require a stable ID.", musicSearchSchema, fingerprint, func(ctx context.Context, _ Invocation, raw string) (string, error) {
		var a struct {
			Keywords      string `json:"keywords"`
			Type          string `json:"type"`
			Limit, Offset int
		}
		if json.Unmarshal([]byte(raw), &a) != nil {
			return "", domain.ErrInvalid
		}
		if a.Type == "" {
			a.Type = "song"
		}
		out, err := o.Music.Search(ctx, business.MusicSearch{Kind: a.Type, Keywords: a.Keywords, Limit: a.Limit, Offset: a.Offset})
		if err != nil {
			return "", err
		}
		return businessJSON(out)
	})}
	for _, kind := range []string{"song", "album", "playlist"} {
		kind := kind
		defs = append(defs, businessDefinition("music_"+kind+"_detail", "Read one selected "+kind+" by stable provider ID. Album/playlist tracks support limit and offset. Does not send messages.", musicDetailSchema, fingerprint, func(ctx context.Context, _ Invocation, raw string) (string, error) {
			var a struct {
				ID            int64
				Limit, Offset int
			}
			if json.Unmarshal([]byte(raw), &a) != nil {
				return "", domain.ErrInvalid
			}
			out, err := o.Music.Detail(ctx, kind, a.ID, a.Limit, a.Offset)
			if err != nil {
				return "", err
			}
			return businessJSON(out)
		}))
	}
	defs = append(defs, musicPresentationDefinitions(o, fingerprint)...)
	if o.Messenger == nil {
		return defs
	}
	if o.Audio != nil {
		audioFingerprint := fingerprint
		if f, ok := o.Audio.(interface{ Fingerprint() string }); ok {
			audioFingerprint = domain.StableID(fingerprint, f.Fingerprint())
		}
		d := businessDefinition("music_send_audio", "Convert and send audio for one explicitly selected stable song ID. Downloads only the configured provider's selected track URL, converts to bounded Opus, uploads to Lark and returns the actual message receipt. No implicit search.", `{"type":"object","properties":{"id":{"type":"integer","minimum":1}},"required":["id"],"additionalProperties":false}`, audioFingerprint, func(ctx context.Context, in Invocation, raw string) (string, error) {
			var a struct{ ID int64 }
			if json.Unmarshal([]byte(raw), &a) != nil || in.ExecutionKey == "" {
				return "", domain.ErrInvalid
			}
			link, err := o.Music.PlaybackURL(ctx, a.ID)
			if err != nil {
				return "", err
			}
			key, err := o.Audio.Upload(ctx, in.Task.Scope, business.AudioSource{SongID: a.ID, URL: link})
			if err != nil {
				return "", err
			}
			body, _ := json.Marshal(map[string]string{"file_key": key})
			id, err := o.Messenger.Send(ctx, in.Task.Scope, in.Task.Root.MessageID, "audio", string(body), domain.StableUUID(in.Task.Scope.Key(), in.Task.ID, in.ExecutionKey))
			if err != nil {
				return "", err
			}
			return businessJSON(map[string]any{"message_id": id, "id": a.ID})
		})
		d.SideEffect = true
		d.AutoExecute = true
		defs = append(defs, d)
	}
	return defs
}
