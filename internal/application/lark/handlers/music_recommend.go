package handlers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/music"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type musicRecommendationLookup func(context.Context, music.Identity, int) (music.Recommendations, error)

var musicBotRecommendationLookup musicRecommendationLookup = loadMusicBotRecommendations
var musicBotRecommendationReply = larkmsg.ReplyMsgText
var musicBotDatabase struct {
	once sync.Once
	db   *gorm.DB
	err  error
}

// This handler receives the original event from the SDK-verified transport.
// Synthetic CLI events or missing identity fields only receive the web entry.
// Never substitute tenant.Tenant, bot identity, metadata or URL parameters here.
func musicRecommendationIdentity(event *larkim.P2MessageReceiveV1, appID string) (music.Identity, bool) {
	if appID == "" || event == nil || event.EventV2Base == nil || event.EventV2Base.Header == nil || event.Event == nil || event.Event.Sender == nil {
		return music.Identity{}, false
	}
	header, sender := event.EventV2Base.Header, event.Event.Sender
	if header.AppID != appID || header.EventType != "im.message.receive_v1" || sender.SenderType == nil || *sender.SenderType != "user" || sender.TenantKey == nil || sender.SenderId == nil || sender.SenderId.OpenId == nil {
		return music.Identity{}, false
	}
	tenant, subject := strings.TrimSpace(*sender.TenantKey), strings.TrimSpace(*sender.SenderId.OpenId)
	if tenant == "" || subject == "" {
		return music.Identity{}, false
	}
	return music.Identity{AppID: header.AppID, TenantKey: tenant, Subject: subject}, true
}

func musicBotPGConfig(dsn string) (*pgx.ConnConfig, error) {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, errors.New("music recommendation database configuration is invalid")
	}
	cfg.ConnectTimeout = 2 * time.Second
	cfg.RuntimeParams["default_transaction_read_only"] = "on"
	cfg.RuntimeParams["statement_timeout"] = "2500"
	cfg.RuntimeParams["lock_timeout"] = "1000"
	cfg.RuntimeParams["application_name"] = "betago_music_bot_readonly"
	return cfg, nil
}

func loadMusicBotRecommendations(ctx context.Context, identity music.Identity, limit int) (music.Recommendations, error) {
	// This DSN is independent of both the Bot global DB and web session secrets.
	dsn := strings.TrimSpace(os.Getenv("MUSIC_BOT_DATABASE_URL"))
	if dsn == "" {
		return music.Recommendations{}, errors.New("music recommendation database is not configured")
	}
	musicBotDatabase.once.Do(func() {
		cfg, err := musicBotPGConfig(dsn)
		if err != nil {
			musicBotDatabase.err = err
			return
		}
		sqlDB := stdlib.OpenDB(*cfg)
		sqlDB.SetMaxOpenConns(2)
		sqlDB.SetMaxIdleConns(1)
		sqlDB.SetConnMaxLifetime(5 * time.Minute)
		musicBotDatabase.db, musicBotDatabase.err = gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{DisableAutomaticPing: true, SkipDefaultTransaction: true, Logger: logger.Default.LogMode(logger.Silent)})
		if musicBotDatabase.err != nil {
			_ = sqlDB.Close()
		}
	})
	if musicBotDatabase.err != nil {
		return music.Recommendations{}, errors.New("music recommendation database is unavailable")
	}
	// No catalogue provider: the Bot reads canonical local candidates only.
	return music.RecommendForIdentity(ctx, musicBotDatabase.db, nil, identity, limit)
}

func musicRecommendationText(ctx context.Context, event *larkim.P2MessageReceiveV1, lookup musicRecommendationLookup) string {
	links := utils.BuildMusicWebLinks("", "")
	fallback := func() string {
		if links.BrowserURL == "" {
			return "音乐网页入口暂未配置。"
		}
		text := "打开音乐网页查看推荐；登录后可结合你的喜欢、播放和搜索兴趣。"
		if links.AppLink != "" {
			text += "\n飞书应用：" + links.AppLink
		}
		return text + "\n浏览器：" + links.BrowserURL
	}
	identity, ok := musicRecommendationIdentity(event, strings.TrimSpace(os.Getenv("MUSIC_WEB_APP_ID")))
	if !ok || links.BrowserURL == "" || lookup == nil {
		return fallback()
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	recommendations, err := lookup(ctx, identity, 5)
	if err != nil || len(recommendations.Tracks) == 0 {
		return fallback()
	}
	var body strings.Builder
	if recommendations.Personalized {
		body.WriteString("根据你的音乐兴趣，推荐这几首：")
	} else {
		body.WriteString("从公开曲库挑了几首：")
	}
	count := 0
	for _, item := range recommendations.Tracks {
		entry := utils.BuildMusicWebLinks("", item.Track.ID)
		if entry.BrowserURL == "" {
			continue
		}
		title, artist, reason := musicRecommendationLabel(item.Track.Title), musicRecommendationLabel(item.Track.Artist), musicRecommendationLabel(item.Reason)
		if title == "" {
			continue
		}
		count++
		fmt.Fprintf(&body, "\n\n%d. %s", count, title)
		if artist != "" {
			body.WriteString(" — " + artist)
		}
		if reason != "" {
			body.WriteString("\n" + reason)
		}
		if entry.AppLink != "" {
			body.WriteString("\n飞书应用：" + entry.AppLink)
		}
		body.WriteString("\n浏览器：" + entry.BrowserURL)
		if count == 5 {
			break
		}
	}
	if count == 0 {
		return fallback()
	}
	return body.String()
}

func musicRecommendationLabel(value string) string {
	value = strings.NewReplacer("<", "＜", ">", "＞").Replace(strings.Join(strings.Fields(value), " "))
	runes := []rune(value)
	if len(runes) > 160 {
		return string(runes[:160]) + "…"
	}
	return value
}

func handleMusicRecommendation(ctx context.Context, event *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData) error {
	messageID := currentMessageID(event)
	if messageID == "" {
		return errors.New("music recommendation requires a current message")
	}
	text := musicRecommendationText(ctx, event, musicBotRecommendationLookup)
	response, err := musicBotRecommendationReply(ctx, text, messageID, "_musicRecommendation", false)
	if err != nil {
		return err
	}
	if response == nil || !response.Success() {
		return errors.New("music recommendation reply was not accepted")
	}
	if meta != nil {
		meta.SetExtra(musicSearchToolResultKey, text)
	}
	return nil
}
