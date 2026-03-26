package larkmsg

import (
	"path/filepath"
	"testing"

	infraConfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
)

func useLarkMsgConfigPath(t *testing.T) {
	t.Helper()
	configPath, err := filepath.Abs("../../../../.dev/config.toml")
	if err != nil {
		t.Fatalf("resolve config path: %v", err)
	}
	t.Setenv("BETAGO_CONFIG_PATH", configPath)
}

func TestResolveRecordedBotIdentityPreservesSenderOpenID(t *testing.T) {
	useLarkMsgConfigPath(t)

	openID, userName := resolveRecordedBotIdentity("ou_custom_bot")
	if openID != "ou_custom_bot" {
		t.Fatalf("openID = %q, want %q", openID, "ou_custom_bot")
	}
	if userName != "你" {
		t.Fatalf("userName = %q, want %q", userName, "你")
	}
}

func TestResolveRecordedBotIdentityFallsBackToConfiguredBotOpenID(t *testing.T) {
	useLarkMsgConfigPath(t)

	openID, userName := resolveRecordedBotIdentity("")
	want := infraConfig.Get().LarkConfig.BotOpenID
	if openID != want {
		t.Fatalf("openID = %q, want %q", openID, want)
	}
	if userName != "你" {
		t.Fatalf("userName = %q, want %q", userName, "你")
	}
}
