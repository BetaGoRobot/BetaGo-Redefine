package config

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	infraConfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/pelletier/go-toml/v2"
)

func TestModelOptionsScopePrecedence(t *testing.T) {
	var cfg infraConfig.BaseConfig
	if err := toml.Unmarshal([]byte("[ark_config.model_options.target]\nservice_tier = 'flex'\n"), &cfg); err != nil {
		t.Fatal(err)
	}
	oldConfig, oldIdentity := currentBaseConfig, currentBotIdentity
	currentBaseConfig = func() *infraConfig.BaseConfig { return &cfg }
	currentBotIdentity = func() botidentity.Identity { return botidentity.Identity{AppID: "app", BotOpenID: "bot"} }
	t.Cleanup(func() { currentBaseConfig, currentBotIdentity = oldConfig, oldIdentity })
	m := newManagerWithMutationStore(&mutationStoreFake{})
	key := ConfigKey("ark_model_options")
	if got := m.GetString(context.Background(), key, "chat", "user"); !json.Valid([]byte(got)) || got == "{}" {
		t.Fatalf("TOML options missing: %q", got)
	}
	for _, tc := range []struct {
		scope             ConfigScope
		chat, user, value string
	}{
		{ScopeGlobal, "", "", `{"target":{"service_tier":"default"}}`},
		{ScopeChat, "chat", "", `{"target":{"service_tier":"auto"}}`},
		{ScopeUser, "", "user", `{"target":{"service_tier":"fast"}}`},
		{ScopeUser, "chat", "user", `{}`},
	} {
		if err := m.SetString(context.Background(), key, tc.scope, tc.chat, tc.user, tc.value); err != nil {
			t.Fatal(err)
		}
		if got := m.GetString(context.Background(), key, "chat", "user"); got != tc.value {
			t.Fatalf("got %s, want %s", got, tc.value)
		}
	}
	currentBotIdentity = func() botidentity.Identity { return botidentity.Identity{AppID: "other", BotOpenID: "other"} }
	if got := m.GetString(context.Background(), key, "chat", "user"); got != `{"target":{"service_tier":"flex"}}` {
		t.Fatalf("bot isolation / TOML fallback: %q", got)
	}
}

func TestModelOptionsManagerRejectsInvalidWrite(t *testing.T) {
	store := &mutationStoreFake{}
	m := newManagerWithMutationStore(store)
	if err := m.SetString(context.Background(), ConfigKey("ark_model_options"), ScopeGlobal, "", "", `{"target":{"service_tier":"typo"}}`); err == nil {
		t.Fatal("invalid options accepted")
	}
	if len(store.applied) != 0 {
		t.Fatal("invalid options were persisted")
	}
}
