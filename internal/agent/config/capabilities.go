package config

import (
	"encoding/hex"
	"errors"
	"strings"
)

// Capabilities belongs to the independent agent config; no old singleton or
// account is imported implicitly. Secret values may reference AGENT_NEXT_* envs.
type Capabilities struct {
	AKShareURL           string                `toml:"akshare_url"`
	HitokotoURL          string                `toml:"hitokoto_url"`
	MusicURL             string                `toml:"music_url"`
	MusicPlayerURL       string                `toml:"music_player_url"`
	MusicImageHosts      []string              `toml:"music_image_hosts"`
	MusicCookie          string                `toml:"music_cookie"`
	AudioEnabled         bool                  `toml:"audio_enabled"`
	FFmpegPath           string                `toml:"ffmpeg_path"`
	AudioMediaHosts      []string              `toml:"audio_media_hosts"`
	LuckinURL            string                `toml:"luckin_url"`
	LuckinBindingURL     string                `toml:"luckin_binding_url"`
	LuckinSigningKey     string                `toml:"luckin_signing_key"`
	LuckinCredentialsKey string                `toml:"luckin_credentials_key"`
	LuckinAccounts       []CapabilityAccount   `toml:"luckin_accounts"`
	Admins               []CapabilityPrincipal `toml:"admins"`
	TimeoutSeconds       int                   `toml:"timeout_seconds"`
}
type CapabilityPrincipal struct {
	TenantID string `toml:"tenant_id"`
	ChatID   string `toml:"chat_id"`
	ActorID  string `toml:"actor_id"`
}
type CapabilityAccount struct {
	CapabilityPrincipal
	Token string `toml:"token"`
}

func (c Capabilities) validate() error {
	invalid := func() error { return errors.New("invalid independent capability configuration") }
	for _, u := range []string{c.AKShareURL, c.HitokotoURL, c.MusicURL, c.MusicPlayerURL, c.LuckinURL, c.LuckinBindingURL} {
		if u != "" && !httpURL(u) {
			return invalid()
		}
	}
	if c.MusicPlayerURL != "" && c.MusicURL == "" {
		return invalid()
	}
	if c.TimeoutSeconds < 0 || c.TimeoutSeconds > 300 || len(c.Admins) > 128 || len(c.LuckinAccounts) > 128 || len(c.AudioMediaHosts) > 32 || len(c.MusicImageHosts) > 32 {
		return invalid()
	}
	if c.AudioEnabled && c.MusicURL == "" || c.MusicCookie != "" && (!safe(c.MusicCookie) || c.MusicURL == "") {
		return invalid()
	}
	managed := c.LuckinCredentialsKey != ""
	if managed {
		key, err := hex.DecodeString(c.LuckinCredentialsKey)
		if err != nil || len(key) != 32 || len(c.LuckinAccounts) > 0 {
			return invalid()
		}
	}
	if len(c.LuckinAccounts) > 0 || c.LuckinURL != "" && (len(c.LuckinSigningKey) < 32 || !managed) || c.LuckinURL == "" && (c.LuckinSigningKey != "" || c.LuckinBindingURL != "" || managed) {
		return invalid()
	}
	if c.FFmpegPath != "" && !safe(c.FFmpegPath) {
		return invalid()
	}
	for _, host := range c.AudioMediaHosts {
		if !safe(host) || strings.ContainsAny(host, "/:@\\ ") {
			return invalid()
		}
	}
	for _, host := range c.MusicImageHosts {
		if !safe(host) || strings.ContainsAny(host, "/:@\\ ") {
			return invalid()
		}
	}
	validPrincipal := func(p CapabilityPrincipal) bool { return safe(p.TenantID) && safe(p.ChatID) && safe(p.ActorID) }
	seen := map[CapabilityPrincipal]bool{}
	for _, p := range c.Admins {
		if !validPrincipal(p) || seen[p] {
			return invalid()
		}
		seen[p] = true
	}
	seen = map[CapabilityPrincipal]bool{}
	for _, a := range c.LuckinAccounts {
		if !validPrincipal(a.CapabilityPrincipal) || !safe(a.Token) || seen[a.CapabilityPrincipal] {
			return invalid()
		}
		seen[a.CapabilityPrincipal] = true
	}
	return nil
}
