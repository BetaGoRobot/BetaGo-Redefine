package config

import (
	"context"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	infraConfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
)

func TestBuildConfigKeyUsesBotNamespace(t *testing.T) {
	oldIdentity := currentBotIdentity
	currentBotIdentity = func() botidentity.Identity {
		return botidentity.Identity{
			AppID:     "cli_test_app",
			BotOpenID: "ou_test_bot",
		}
	}
	defer func() { currentBotIdentity = oldIdentity }()

	got := buildConfigKey(ScopeUser, "oc_test_chat", "ou_test_user", KeyRepeatDefaultRate)
	want := "bot:cli_test_app:ou_test_bot:user:oc_test_chat:ou_test_user:repeat_default_rate"
	if got != want {
		t.Fatalf("buildConfigKey() = %q, want %q", got, want)
	}
}

func TestBuildConfigKeyKeepsLegacyFormatWithoutBotIdentity(t *testing.T) {
	oldIdentity := currentBotIdentity
	currentBotIdentity = func() botidentity.Identity { return botidentity.Identity{} }
	defer func() { currentBotIdentity = oldIdentity }()

	got := buildConfigKey(ScopeChat, "oc_test_chat", "", KeyIntentRecognitionEnabled)
	want := "chat:oc_test_chat:intent_recognition_enabled"
	if got != want {
		t.Fatalf("buildConfigKey() = %q, want %q", got, want)
	}
}

func TestBuildFeatureBlockKeyUsesBotNamespace(t *testing.T) {
	oldIdentity := currentBotIdentity
	currentBotIdentity = func() botidentity.Identity {
		return botidentity.Identity{
			AppID:     "cli_test_app",
			BotOpenID: "ou_test_bot",
		}
	}
	defer func() { currentBotIdentity = oldIdentity }()

	got := buildFeatureBlockKey(ScopeChat, "oc_test_chat", "", "send_message")
	want := "bot:cli_test_app:ou_test_bot:feature_block:chat:oc_test_chat:send_message"
	if got != want {
		t.Fatalf("buildFeatureBlockKey() = %q, want %q", got, want)
	}
}

func TestGetStringFallsBackToToml(t *testing.T) {
	oldIdentity := currentBotIdentity
	oldConfig := currentBaseConfig
	currentBotIdentity = func() botidentity.Identity { return botidentity.Identity{} }
	currentBaseConfig = func() *infraConfig.BaseConfig {
		return &infraConfig.BaseConfig{
			ArkConfig: &infraConfig.ArkConfig{
				ReasoningModel: "deep-reasoner",
				NormalModel:    "fast-chat",
				LiteModel:      "intent-lite",
			},
			OpensearchConfig: &infraConfig.OpensearchConfig{
				LarkMsgIndex:   "msg-index",
				LarkChunkIndex: "chunk-index",
			},
		}
	}
	defer func() {
		currentBotIdentity = oldIdentity
		currentBaseConfig = oldConfig
	}()

	manager := NewManager()

	if got := manager.GetString(context.Background(), KeyChatReasoningModel, "", ""); got != "deep-reasoner" {
		t.Fatalf("GetString(reasoning) = %q, want %q", got, "deep-reasoner")
	}
	if got := manager.GetString(context.Background(), KeyChatNormalModel, "", ""); got != "fast-chat" {
		t.Fatalf("GetString(normal) = %q, want %q", got, "fast-chat")
	}
	if got := manager.GetString(context.Background(), KeyIntentLiteModel, "", ""); got != "intent-lite" {
		t.Fatalf("GetString(intent) = %q, want %q", got, "intent-lite")
	}
	if got := manager.GetString(context.Background(), KeyLarkMsgIndex, "", ""); got != "msg-index" {
		t.Fatalf("GetString(msg index) = %q, want %q", got, "msg-index")
	}
	if got := manager.GetString(context.Background(), KeyLarkChunkIndex, "", ""); got != "chunk-index" {
		t.Fatalf("GetString(chunk index) = %q, want %q", got, "chunk-index")
	}
}

func TestGetBoolFallsBackToTomlForBusinessFlags(t *testing.T) {
	oldIdentity := currentBotIdentity
	oldConfig := currentBaseConfig
	currentBotIdentity = func() botidentity.Identity { return botidentity.Identity{} }
	currentBaseConfig = func() *infraConfig.BaseConfig {
		return &infraConfig.BaseConfig{
			NeteaseMusicConfig: &infraConfig.NeteaseMusicConfig{
				MusicCardInThread: true,
			},
			LarkConfig: &infraConfig.LarkConfig{
				WithDrawReplace: true,
			},
		}
	}
	defer func() {
		currentBotIdentity = oldIdentity
		currentBaseConfig = oldConfig
	}()

	manager := NewManager()

	if !manager.GetBool(context.Background(), KeyMusicCardInThread, "", "") {
		t.Fatal("expected music_card_in_thread TOML fallback to be true")
	}
	if !manager.GetBool(context.Background(), KeyWithDrawReplace, "", "") {
		t.Fatal("expected with_draw_replace TOML fallback to be true")
	}
}

func TestParseConfigKeySupportsBotNamespace(t *testing.T) {
	entry, ok := parseConfigKey(
		"bot:cli_test_app:ou_test_bot:user:oc_test_chat:ou_test_user:repeat_default_rate",
		"42",
	)
	if !ok {
		t.Fatal("expected parseConfigKey to succeed")
	}
	if entry.Scope != ScopeUser || entry.ChatID != "oc_test_chat" || entry.OpenID != "ou_test_user" {
		t.Fatalf("unexpected entry scope/chat/user: %+v", entry)
	}
	if entry.Key != KeyRepeatDefaultRate || entry.Value != "42" {
		t.Fatalf("unexpected entry key/value: %+v", entry)
	}
}
