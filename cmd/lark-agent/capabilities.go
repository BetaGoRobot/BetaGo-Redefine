package main

import (
	"context"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/atoms"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/business"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

func capabilityDefinitions(c config.Config, p *components) ([]atoms.Definition, error) {
	if p == nil {
		return nil, domain.ErrInvalid
	}
	x := c.Capabilities
	timeout := time.Duration(x.TimeoutSeconds) * time.Second
	defs, err := atoms.PublicDataDefinitions(atoms.PublicDataOptions{AKShareURL: x.AKShareURL, HitokotoURL: x.HitokotoURL, Timeout: timeout})
	if err != nil {
		return nil, err
	}
	defs = append(defs, atoms.RuntimeLimitsDefinition(atoms.RuntimeLimits{MaxConcurrentTasks: c.Runtime.MaxConcurrentTasks, MaxTurns: c.Runtime.MaxTurns, MaxContextBytes: c.Runtime.MaxContextBytes, TaskTimeoutSeconds: c.Runtime.TaskTimeoutSeconds, TurnLeaseSeconds: c.Runtime.TurnLeaseSeconds, WorkerConcurrency: c.Worker.Concurrency, WorkerPollMilliseconds: c.Worker.PollMilliseconds}))
	b := atoms.BusinessOptions{}
	b.MusicCards = p.musicCards
	if p.lark != nil {
		b.Messenger = p.lark
	}
	if x.MusicURL != "" {
		b.Music, err = business.NewMusicClient(business.MusicConfig{BaseURL: x.MusicURL, Cookie: x.MusicCookie, Timeout: timeout})
		if err != nil {
			return nil, err
		}
	}
	if x.AudioEnabled {
		if p.lark == nil {
			return nil, domain.ErrInvalid
		}
		if p.musicAudio != nil {
			b.Audio = p.musicAudio
		} else {
			b.Audio, err = business.NewLarkAudioUploader(business.LarkAudioConfig{AppID: c.Lark.AppID, AppSecret: c.Lark.AppSecret, TenantID: c.App.TenantID, FFmpegPath: x.FFmpegPath, AllowedMediaHosts: x.AudioMediaHosts, Timeout: timeout})
			if err != nil {
				return nil, err
			}
		}
	}
	if x.LuckinURL != "" {
		if p.accounts == nil || p.lark == nil || x.LuckinCredentialsKey == "" {
			return nil, domain.ErrInvalid
		}
		managed := &business.ManagedLuckinAccounts{Store: p.accounts, Sender: p.lark}
		b.LuckinBinding = managed
		b.Luckin, err = business.NewLuckinClient(business.LuckinConfig{ServerURL: x.LuckinURL, BindingURL: x.LuckinBindingURL, SigningKey: []byte(x.LuckinSigningKey), Credentials: managed, Timeout: timeout})
		if err != nil {
			return nil, err
		}
	}
	businessDefs, err := atoms.BusinessDefinitions(b)
	if err != nil {
		return nil, err
	}
	defs = append(defs, businessDefs...)
	if p.store != nil {
		admins := append([]config.CapabilityPrincipal(nil), x.Admins...)
		admin := func(_ context.Context, scope domain.Scope, actor string) (bool, error) {
			if scope.AppID != c.Lark.AppID || scope.Visibility != c.App.Visibility {
				return false, nil
			}
			for _, a := range admins {
				if a.TenantID == scope.TenantID && a.ChatID == scope.ChatID && a.ActorID == actor {
					return true, nil
				}
			}
			return false, nil
		}
		defs = append(defs, atoms.CatalogDefinitions(p.store, atoms.CatalogOptions{Admin: admin, Effective: true})...)
	}
	return defs, nil
}
