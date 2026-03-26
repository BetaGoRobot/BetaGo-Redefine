package history

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/xmodel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/defensestation/osquery"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
)

func TestHelperBuildRequestMatchesBuilderState(t *testing.T) {
	helper := New(context.Background()).
		Index("custom-index").
		Query(osquery.Bool().Must(osquery.Term("chat_id", "chat-1"))).
		Source("raw_message", "user_id").
		Size(5).
		Sort("create_time", "desc")

	reqJSON := utils.MustMarshalString(helper.buildRequest())

	for _, fragment := range []string{
		`"chat_id"`,
		`"chat-1"`,
		`"raw_message"`,
		`"user_id"`,
		`"size":5`,
		`"create_time"`,
		`"desc"`,
	} {
		if !strings.Contains(reqJSON, fragment) {
			t.Fatalf("request JSON %q does not contain %q", reqJSON, fragment)
		}
	}
}

func TestHelperResolvedIndexPrefersExplicitIndex(t *testing.T) {
	helper := New(context.Background()).Index("custom-index")

	if got := helper.resolvedIndex(); got != "custom-index" {
		t.Fatalf("resolvedIndex() = %q, want %q", got, "custom-index")
	}
}

func useHistoryConfigPath(t *testing.T) {
	t.Helper()
	configPath, err := filepath.Abs("../../../../.dev/config.toml")
	if err != nil {
		t.Fatalf("resolve config path: %v", err)
	}
	t.Setenv("BETAGO_CONFIG_PATH", configPath)
}

func TestFilterMessageTreatsBotSenderAsYouButKeepsOpenID(t *testing.T) {
	useHistoryConfigPath(t)
	selfOpenID := botidentity.Current().BotOpenID

	source, err := json.Marshal(&xmodel.MessageIndex{
		MessageLog: &xmodel.MessageLog{
			MessageType: "text",
			Mentions:    "[]",
		},
		CreateTime: "2026-03-26 14:10:00",
		OpenID:     selfOpenID,
		UserName:   "任意旧昵称",
		RawMessage: `{"text":"我来跟进一下"}`,
	})
	if err != nil {
		t.Fatalf("marshal source: %v", err)
	}

	lines := FilterMessage(context.Background(), []opensearchapi.SearchHit{{Source: source}})
	if len(lines) != 1 {
		t.Fatalf("line count = %d, want 1", len(lines))
	}
	if lines[0].OpenID != selfOpenID {
		t.Fatalf("openID = %q, want %q", lines[0].OpenID, selfOpenID)
	}
	if lines[0].UserName != "你" {
		t.Fatalf("userName = %q, want %q", lines[0].UserName, "你")
	}
	if got := lines[0].ToLine(); !strings.Contains(got, "("+selfOpenID+") <你>:") {
		t.Fatalf("line = %q, want contain self identity", got)
	}
}
