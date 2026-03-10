package config

import (
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
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

func TestParseConfigKeySupportsBotNamespace(t *testing.T) {
	entry, ok := parseConfigKey(
		"bot:cli_test_app:ou_test_bot:user:oc_test_chat:ou_test_user:repeat_default_rate",
		"42",
	)
	if !ok {
		t.Fatal("expected parseConfigKey to succeed")
	}
	if entry.Scope != ScopeUser || entry.ChatID != "oc_test_chat" || entry.UserID != "ou_test_user" {
		t.Fatalf("unexpected entry scope/chat/user: %+v", entry)
	}
	if entry.Key != KeyRepeatDefaultRate || entry.Value != "42" {
		t.Fatalf("unexpected entry key/value: %+v", entry)
	}
}
