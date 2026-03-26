package botidentity

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	infraConfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
)

func useWorkspaceConfigPath(t *testing.T) {
	t.Helper()
	configPath, err := filepath.Abs("../../../../.dev/config.toml")
	if err != nil {
		t.Fatalf("resolve config path: %v", err)
	}
	t.Setenv("BETAGO_CONFIG_PATH", configPath)
}

func TestGetProfileCacheCachesByIdentity(t *testing.T) {
	useWorkspaceConfigPath(t)
	originalLoader := profileLoader
	defer func() { profileLoader = originalLoader }()

	calls := 0
	profileLoader = func(ctx context.Context, identity Identity) (Profile, error) {
		calls++
		return Profile{
			AppID:     identity.AppID,
			BotOpenID: identity.BotOpenID,
			BotName:   "缓存命中机器人",
		}, nil
	}

	identity := Identity{AppID: "cli_test_profile_cache", BotOpenID: "ou_test_profile_cache"}
	first, err := getProfileCache(context.Background(), identity)
	if err != nil {
		t.Fatalf("getProfileCache() first error = %v", err)
	}
	second, err := getProfileCache(context.Background(), identity)
	if err != nil {
		t.Fatalf("getProfileCache() second error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("profile loader calls = %d, want 1", calls)
	}
	if first.BotName != "缓存命中机器人" || second.BotName != "缓存命中机器人" {
		t.Fatalf("cached profile = %+v / %+v, want cached bot name", first, second)
	}
}

func TestResolveProfileFallsBackToConfiguredNameOnLoaderError(t *testing.T) {
	useWorkspaceConfigPath(t)
	originalLoader := profileLoader
	defer func() { profileLoader = originalLoader }()

	profileLoader = func(ctx context.Context, identity Identity) (Profile, error) {
		return Profile{}, errors.New("boom")
	}

	identity := Identity{AppID: "cli_test_profile_fallback", BotOpenID: "ou_test_profile_fallback"}
	got := resolveProfile(context.Background(), identity)
	if got.AppID != identity.AppID || got.BotOpenID != identity.BotOpenID {
		t.Fatalf("resolved profile = %+v, want identity %q/%q", got, identity.AppID, identity.BotOpenID)
	}
	wantName := strings.TrimSpace(infraConfig.Get().BaseInfo.RobotName)
	if got.BotName != wantName {
		t.Fatalf("resolved bot name = %q, want %q", got.BotName, wantName)
	}
}
