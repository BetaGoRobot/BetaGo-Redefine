package main

import (
	"context"
	"errors"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/business"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/media"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/musiccards"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/musicmedia"
)

func initializeMusic(ctx context.Context, c config.Config, p *components) error {
	x := c.Capabilities
	if x.MusicURL == "" || x.MusicPlayerURL == "" {
		return nil
	}
	if p == nil || p.db == nil || p.store == nil || p.lark == nil {
		return domain.ErrInvalid
	}
	if p.musicService != nil {
		return nil
	}
	provider, err := business.NewMusicClient(business.MusicConfig{BaseURL: x.MusicURL, Cookie: x.MusicCookie, Timeout: time.Duration(x.TimeoutSeconds) * time.Second})
	if err != nil {
		return err
	}
	if x.AudioEnabled && p.musicAudio == nil {
		p.musicAudio, err = business.NewLarkAudioUploader(business.LarkAudioConfig{AppID: c.Lark.AppID, AppSecret: c.Lark.AppSecret, TenantID: c.App.TenantID, FFmpegPath: x.FFmpegPath, AllowedMediaHosts: x.AudioMediaHosts, Timeout: time.Duration(x.TimeoutSeconds) * time.Second})
		if err != nil {
			return err
		}
	}
	var audio musicmedia.AudioUploader
	if p.musicAudio != nil {
		audio = p.musicAudio
	}
	assets, err := musicmedia.New(musicmedia.Config{DB: p.db, Schema: c.Database.Schema, Provider: provider, ObjectStore: media.MinIOConfig{Endpoint: c.Objects.Endpoint, AccessKey: c.Objects.AccessKey, SecretKey: c.Objects.SecretKey, Bucket: c.Objects.Bucket, Secure: c.Objects.Secure}, PublicURL: c.App.PublicURL, PlayerURL: x.MusicPlayerURL, SigningKey: []byte(domain.StableID("music-assets", c.Lark.AppID, c.Lark.AppSecret)), ImageUploader: p.lark, AudioUploader: audio, AllowedMediaHosts: x.AudioMediaHosts, AllowedImageHosts: x.MusicImageHosts})
	if err != nil {
		return err
	}
	if err = assets.Initialize(ctx); err != nil {
		return err
	}
	admin := func(s domain.Scope, actor string) bool {
		if s.AppID != c.Lark.AppID || s.Visibility != c.App.Visibility {
			return false
		}
		for _, a := range x.Admins {
			if s.TenantID == a.TenantID && s.ChatID == a.ChatID && actor == a.ActorID {
				return true
			}
		}
		return false
	}
	presenter, err := musiccards.New(musiccards.Options{DB: p.db, Schema: c.Database.Schema, Store: p.store, Provider: provider, Media: assets, Messenger: p.lark, SigningKey: []byte(domain.StableID("music-actions", c.Lark.AppID, c.Lark.AppSecret)), Admin: admin, Member: p.lark.MusicMember})
	if err != nil {
		return err
	}
	if err = presenter.Initialize(ctx); err != nil {
		return err
	}
	p.musicMedia = assets
	p.musicService = presenter
	p.musicCards = presenter
	return nil
}

func musicTargetValidator(p *components) func(context.Context, domain.Scope, string) error {
	return func(ctx context.Context, s domain.Scope, id string) error {
		if p == nil || p.store == nil {
			return domain.ErrUnauthorized
		}
		err := localTargetValidator(p.store)(ctx, s, id)
		if err == nil {
			return nil
		}
		if p.musicService != nil && (errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrUnauthorized)) {
			return p.musicService.CheckMessage(ctx, s, id)
		}
		return err
	}
}
