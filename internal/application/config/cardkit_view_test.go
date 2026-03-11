package config

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
)

func TestBuildConfigCardJSONDelegatesToSchemaV2Card(t *testing.T) {
	card := map[string]any(newRawCard(context.Background(), "配置面板", []any{buildConfigScopeRow(ConfigViewState{
		Scope:  "chat",
		ChatID: "chat-1",
		OpenID: "user-1",
	})}, larkmsg.StandardCardFooterOptions{
		RefreshPayload: larkmsg.StringMapToAnyMap(BuildConfigViewValue("chat", "chat-1", "user-1")),
	}))
	raw, err := json.Marshal(card)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	jsonStr := string(raw)
	if !strings.Contains(jsonStr, `"schema":"2.0"`) {
		t.Fatalf("expected schema v2 card json: %s", jsonStr)
	}
	if strings.Contains(jsonStr, `"tag":"action"`) {
		t.Fatalf("did not expect deprecated action tag in card json: %s", jsonStr)
	}
	if strings.Contains(jsonStr, `"tag":"note"`) {
		t.Fatalf("did not expect deprecated note tag in card json: %s", jsonStr)
	}
}
