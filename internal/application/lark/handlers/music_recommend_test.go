package handlers

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/music"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkevent "github.com/larksuite/oapi-sdk-go/v3/event"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestMusicRecommendParsersDoNotRequireSearchKeywords(t *testing.T) {
	cli, err := MusicSearch.ParseCLI([]string{"--type=recommend"})
	if err != nil {
		t.Fatalf("recommend CLI rejected: %v", err)
	}
	if string(cli.Type) != "recommend" || cli.Keywords != "" {
		t.Fatalf("unexpected CLI args: %#v", cli)
	}
	tool, err := MusicSearch.ParseTool(`{"type":"recommend"}`)
	if err != nil {
		t.Fatalf("recommend tool rejected: %v", err)
	}
	if string(tool.Type) != "recommend" {
		t.Fatal("recommend type lost")
	}
	if _, err := MusicSearch.ParseTool(`{"type":"song"}`); err == nil {
		t.Fatal("song search accepted empty keywords")
	}
}

func musicInterestEvent() *larkim.P2MessageReceiveV1 {
	app, tenant, subject, senderType := "cli_music_test", "tenant_private", "ou_private", "user"
	return &larkim.P2MessageReceiveV1{
		EventV2Base: &larkevent.EventV2Base{Header: &larkevent.EventHeader{AppID: app, TenantKey: "header-is-not-sender", EventType: "im.message.receive_v1"}},
		Event:       &larkim.P2MessageReceiveV1Data{Sender: &larkim.EventSender{TenantKey: &tenant, SenderType: &senderType, SenderId: &larkim.UserId{OpenId: &subject}}},
	}
}

func TestMusicRecommendationsUseOnlyCompleteTrustedEventIdentity(t *testing.T) {
	t.Setenv("MUSIC_WEB_ORIGIN", "https://music.example.test")
	t.Setenv("MUSIC_WEB_APP_ID", "cli_music_test")
	calls := 0
	lookup := func(_ context.Context, id music.Identity, limit int) (music.Recommendations, error) {
		calls++
		if id.AppID != "cli_music_test" || id.TenantKey != "tenant_private" || id.Subject != "ou_private" || limit != 5 {
			t.Fatal("event identity was replaced or unbounded")
		}
		return music.Recommendations{Personalized: true, Tracks: []music.RecommendedTrack{{Track: music.Track{ID: "kuwo:42", Title: "public song", Artist: "artist"}, Reason: "来自你的喜欢"}}}, nil
	}
	text := musicRecommendationText(context.Background(), musicInterestEvent(), lookup)
	if calls != 1 || !strings.Contains(text, "public song") || !strings.Contains(text, "来自你的喜欢") || !strings.Contains(text, "trackID=kuwo%3A42") {
		t.Fatalf("recommendation missing: %s", text)
	}
	for _, private := range []string{"tenant_private", "ou_private", "open_id=", "session=", "code="} {
		if strings.Contains(text, private) {
			t.Fatalf("identity or credential leaked: %s", private)
		}
	}
	for _, mode := range []string{"missing-header", "different-app", "missing-sender-tenant", "missing-openid", "bot"} {
		event := musicInterestEvent()
		switch mode {
		case "missing-header":
			event.EventV2Base = nil
		case "different-app":
			event.EventV2Base.Header.AppID = "cli_other"
		case "missing-sender-tenant":
			event.Event.Sender.TenantKey = nil
		case "missing-openid":
			event.Event.Sender.SenderId.OpenId = nil
		case "bot":
			v := "bot"
			event.Event.Sender.SenderType = &v
		}
		before := calls
		text = musicRecommendationText(context.Background(), event, lookup)
		if calls != before || strings.Contains(text, "public song") || !strings.Contains(text, "音乐网页") {
			t.Fatalf("untrusted %s read personal music", mode)
		}
	}
}

func TestMusicRecommendationsMissingAccountUsesPlainWebEntry(t *testing.T) {
	t.Setenv("MUSIC_WEB_ORIGIN", "https://music.example.test")
	t.Setenv("MUSIC_WEB_APP_ID", "cli_music_test")
	text := musicRecommendationText(context.Background(), musicInterestEvent(), func(context.Context, music.Identity, int) (music.Recommendations, error) {
		return music.Recommendations{}, errors.New("private DSN or missing account")
	})
	if !strings.Contains(text, "音乐网页") || strings.Contains(text, "private DSN") || strings.Contains(text, "根据你的音乐兴趣") {
		t.Fatalf("missing account masqueraded as personalization: %s", text)
	}
}

func TestMusicBotDatabaseConfigurationEnforcesReadOnlyAndBoundedQueries(t *testing.T) {
	cfg, err := musicBotPGConfig("postgres://readonly:test@127.0.0.1:1/music_preview?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RuntimeParams["default_transaction_read_only"] != "on" || cfg.RuntimeParams["statement_timeout"] != "2500" || cfg.ConnectTimeout.Seconds() != 2 {
		t.Fatal("music DB connection is not bounded and read only")
	}
}

func TestMusicRecommendHandlerRepliesOnlyToRequestedMessageWithoutCard(t *testing.T) {
	t.Setenv("MUSIC_WEB_ORIGIN", "https://music.example.test")
	t.Setenv("MUSIC_WEB_APP_ID", "cli_music_test")
	previousLookup, previousReply := musicBotRecommendationLookup, musicBotRecommendationReply
	t.Cleanup(func() { musicBotRecommendationLookup = previousLookup; musicBotRecommendationReply = previousReply })
	musicBotRecommendationLookup = func(context.Context, music.Identity, int) (music.Recommendations, error) {
		return music.Recommendations{}, errors.New("missing account")
	}
	calls := 0
	musicBotRecommendationReply = func(_ context.Context, text, id, suffix string, thread bool) (*larkim.ReplyMessageResp, error) {
		calls++
		if id != "current-request" || thread || suffix != "_musicRecommendation" || !strings.Contains(text, "音乐网页") {
			t.Fatal("recommendation reply escaped the current request")
		}
		return &larkim.ReplyMessageResp{}, nil
	}
	event := musicInterestEvent()
	id := "current-request"
	event.Event.Message = &larkim.EventMessage{MessageId: &id}
	meta := &xhandler.BaseMetaData{}
	if err := MusicSearch.Handle(context.Background(), event, meta, MusicSearchArgs{Type: MusicSearchTypeRecommend}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("expected one text reply, got %d", calls)
	}
	if result, ok := meta.GetExtra(musicSearchToolResultKey); !ok || !strings.Contains(result, "音乐网页") {
		t.Fatal("tool result lost the actual fallback outcome")
	}
}
