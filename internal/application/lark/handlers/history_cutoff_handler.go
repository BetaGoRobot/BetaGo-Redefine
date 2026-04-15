package handlers

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	arktools "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xcommand"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

// ==========================================
// set_history_cutoff Tool & Command
// ==========================================

type SetHistoryCutoffArgs struct {
	Timestamp string `json:"timestamp"` // RFC3339 format
}

type historyCutoffHandler struct{}

var SetHistoryCutoff historyCutoffHandler

const historyCutoffResultKey = "history_cutoff_result"

func (historyCutoffHandler) ParseTool(raw string) (SetHistoryCutoffArgs, error) {
	parsed := SetHistoryCutoffArgs{}
	if err := utils.UnmarshalStringPre(raw, &parsed); err != nil {
		return SetHistoryCutoffArgs{}, err
	}
	// Validate timestamp format
	if _, err := time.Parse(time.RFC3339, parsed.Timestamp); err != nil {
		return SetHistoryCutoffArgs{}, fmt.Errorf("invalid timestamp format, expected RFC3339 (e.g., 2024-01-01T00:00:00Z)")
	}
	return parsed, nil
}

func (historyCutoffHandler) ParseCLI(args []string) (SetHistoryCutoffArgs, error) {
	argMap, input := parseArgs(args...)
	ts := strings.TrimSpace(argMap["timestamp"])
	if ts == "" {
		ts = strings.TrimSpace(input) // fallback to positional arg
	}
	if ts == "" {
		return SetHistoryCutoffArgs{}, fmt.Errorf("usage: /bb forget <YYYY-MM-DD>")
	}
	// Parse various date formats
	for _, format := range []string{time.RFC3339, "2006-01-02T15:04:05Z07:00", "2006-01-02"} {
		if t, err := time.Parse(format, ts); err == nil {
			return SetHistoryCutoffArgs{Timestamp: t.Format(time.RFC3339)}, nil
		}
	}
	return SetHistoryCutoffArgs{}, fmt.Errorf("invalid date format: %s, use YYYY-MM-DD", ts)
}

func (historyCutoffHandler) ToolSpec() xcommand.ToolSpec {
	return xcommand.ToolSpec{
		Name: "set_history_cutoff",
		Desc: "设置该群聊的历史记录挡板时间。设置后，AI将不会获取到该时间之前的任何历史消息。用于让AI'遗忘'旧记忆。",
		Params: arktools.NewParams("object").
			AddProp("timestamp", &arktools.Prop{
				Type: "string",
				Desc: "截止时间戳，RFC3339格式，例如 2024-01-01T00:00:00Z",
			}).
			AddRequired("timestamp"),
		Result: func(metaData *xhandler.BaseMetaData) string {
			result, _ := metaData.GetExtra(historyCutoffResultKey)
			return result
		},
	}
}

func (historyCutoffHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg SetHistoryCutoffArgs) error {
	chatID := currentChatID(data, metaData)
	if chatID == "" {
		return fmt.Errorf("chat_id is required")
	}

	cfgManager := config.GetManager()
	if err := cfgManager.SetString(ctx, config.KeyHistoryCutoffTime, config.ScopeChat, chatID, "", arg.Timestamp); err != nil {
		return fmt.Errorf("failed to set history cutoff: %w", err)
	}

	t, _ := time.Parse(time.RFC3339, arg.Timestamp)
	msg := fmt.Sprintf("✅ 历史挡板已设置\n\n截止时间: %s\n\nAI将不会获取该时间之前的消息", t.Format("2006-01-02 15:04:05"))
	metaData.SetExtra(historyCutoffResultKey, msg)
	return sendCompatibleText(ctx, data, metaData, msg, "_historyCutoff", false)
}

func (historyCutoffHandler) CommandDescription() string {
	return "设置历史记录挡板，让AI遗忘指定时间之前的消息"
}

func (historyCutoffHandler) CommandExamples() []string {
	return []string{
		"/bb forget 2024-01-01",
		"/bb forget 2024-06-01T00:00:00Z",
	}
}
