package handlers

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	toolkit "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func useWorkspaceConfigPath(t *testing.T) {
	t.Helper()
	configPath, err := filepath.Abs("../../../../.dev/config.toml")
	if err != nil {
		t.Fatalf("resolve config path: %v", err)
	}
	t.Setenv("BETAGO_CONFIG_PATH", configPath)
}

func TestBuildSchedulableToolsContainsStandardToolset(t *testing.T) {
	useWorkspaceConfigPath(t)
	schedulable := BuildSchedulableTools()
	allTools := larktools()

	excluded := map[string]struct{}{
		"create_schedule":   {},
		"list_schedules":    {},
		"query_schedule":    {},
		"delete_schedule":   {},
		"pause_schedule":    {},
		"resume_schedule":   {},
		"revert_message":    {},
		"permission_manage": {},
	}

	for name := range allTools.FunctionCallMap {
		if _, skip := excluded[name]; skip {
			continue
		}
		if _, ok := schedulable.FunctionCallMap[name]; !ok {
			t.Fatalf("schedulable tools missing %q", name)
		}
	}

	if _, ok := schedulable.FunctionCallMap["gold_price_get"]; !ok {
		t.Fatal("schedulable tools missing gold_price_get")
	}
	if _, ok := allTools.FunctionCallMap["query_schedule"]; !ok {
		t.Fatal("lark tools missing query_schedule")
	}
}

func TestBuildSchedulableToolsRestrictsSendMessageChatOverride(t *testing.T) {
	useWorkspaceConfigPath(t)
	schedulable := BuildSchedulableTools()
	unit, ok := schedulable.Get("send_message")
	if !ok {
		t.Fatal("schedulable tools missing send_message")
	}

	result := unit.Function(context.Background(), `{"content":"hi","chat_id":"oc_other"}`, toolkit.FCMeta[larkim.P2MessageReceiveV1]{
		ChatID: "oc_self",
		OpenID: "ou_user",
	})
	if !result.IsErr() {
		t.Fatal("expected send_message to reject chat_id override in schedule context")
	}
	if !strings.Contains(result.Err().Error(), "cannot override chat_id") {
		t.Fatalf("unexpected error: %v", result.Err())
	}
}
