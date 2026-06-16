package handlers

import (
	"os"
	"path/filepath"
	"testing"

	infraConfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
)

func TestLuckinRuntimeConfigReadsCredentialsKeyFromConfig(t *testing.T) {
	writeLuckinRuntimeConfigForTest(t, "system-token", "0123456789abcdef0123456789abcdef", "https://luckin.example/mcp")

	if got := luckinSystemToken(); got != "system-token" {
		t.Fatalf("luckinSystemToken() = %q", got)
	}
	if got := luckinCredentialsKey(); got != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("luckinCredentialsKey() = %q", got)
	}
	if got := luckinServerURL(); got != "https://luckin.example/mcp" {
		t.Fatalf("luckinServerURL() = %q", got)
	}
}

func writeLuckinRuntimeConfigForTest(t *testing.T, systemToken, credentialsKey, serverURL string) {
	t.Helper()
	restoreWorkspaceConfigAfterTest(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := "[luckin_mcp]\n" +
		"system_token = \"" + systemToken + "\"\n" +
		"credentials_key = \"" + credentialsKey + "\"\n" +
		"server_url = \"" + serverURL + "\"\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if _, err := infraConfig.LoadFileE(path); err != nil {
		t.Fatalf("load config: %v", err)
	}
}

func restoreWorkspaceConfigAfterTest(t *testing.T) {
	t.Helper()
	workspaceConfig, err := filepath.Abs("../../../../.dev/config.toml")
	if err != nil {
		t.Fatalf("resolve workspace config: %v", err)
	}
	t.Cleanup(func() {
		if _, err := infraConfig.LoadFileE(workspaceConfig); err != nil {
			t.Errorf("restore workspace config: %v", err)
		}
	})
}
