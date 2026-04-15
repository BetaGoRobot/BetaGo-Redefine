package handlers

import (
	"context"
	"fmt"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/history"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	arktools "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xcommand"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

type HistorySearchArgs struct {
	Keywords    string `json:"keywords"`
	TopK        int    `json:"top_k"`
	StartTime   string `json:"start_time"`
	EndTime     string `json:"end_time"`
	OpenID      string `json:"user_id"`
	UserName    string `json:"user_name"`
	MessageType string `json:"message_type"`
}

type historySearchHandler struct{}

var SearchHistory historySearchHandler

var historyHybridSearchFn = history.HybridSearch

func (historySearchHandler) ParseTool(raw string) (HistorySearchArgs, error) {
	parsed := HistorySearchArgs{}
	if err := utils.UnmarshalStringPre(raw, &parsed); err != nil {
		return HistorySearchArgs{}, err
	}
	return parsed, nil
}

func (historySearchHandler) ToolSpec() xcommand.ToolSpec {
	return xcommand.ToolSpec{
		Name: "search_history",
		Desc: "在当前 chat_id 范围内搜索历史对话，支持关键词和用户/消息类型等元数据过滤.你可以检索近1年的消息",
		Params: arktools.NewParams("object").
			AddProp("keywords", &arktools.Prop{
				Type: "string",
				Desc: "需要检索的关键词列表, 逗号隔开；如果只按元数据过滤可以为空",
			}).
			AddProp("user_id", &arktools.Prop{
				Type: "string",
				Desc: "按发言用户 OpenID 过滤",
			}).
			AddProp("user_name", &arktools.Prop{
				Type: "string",
				Desc: "按发言用户名精确过滤",
			}).
			AddProp("message_type", &arktools.Prop{
				Type: "string",
				Desc: "按消息类型过滤，例如 text、image、file",
			}).
			AddProp("start_time", &arktools.Prop{
				Type: "string",
				Desc: "开始时间，格式为YYYY-MM-DD HH:MM:SS",
			}).
			AddProp("end_time", &arktools.Prop{
				Type: "string",
				Desc: "结束时间，格式为YYYY-MM-DD HH:MM:SS",
			}).
			AddProp("top_k", &arktools.Prop{
				Type: "number",
				Desc: "返回的结果数量",
			}),
		Result: func(metaData *xhandler.BaseMetaData) string {
			result, _ := metaData.GetExtra("search_result")
			return result
		},
	}
}

func (historySearchHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg HistorySearchArgs) error {
	chatID := strings.TrimSpace(metaData.ChatID)
	if chatID == "" {
		return fmt.Errorf("chat_id is required for search_history")
	}
	// 自动应用 history cutoff time
	cutoffTime := getHistoryCutoffTime(ctx, chatID)
	res, err := historyHybridSearchFn(ctx,
		history.HybridSearchRequest{
			QueryText:   splitByComma(arg.Keywords),
			TopK:        arg.TopK,
			OpenID:      arg.OpenID,
			UserName:    strings.TrimSpace(arg.UserName),
			MessageType: strings.TrimSpace(arg.MessageType),
			ChatID:      chatID,
			StartTime:   arg.StartTime,
			EndTime:     arg.EndTime,
			CutoffTime:  cutoffTime,
		}, ark_dal.EmbeddingText)
	if err != nil {
		return err
	}
	metaData.SetExtra("search_result", utils.MustMarshalString(res))
	return nil
}

func splitByComma(input string) []string {
	if input == "" {
		return nil
	}
	parts := make([]string, 0)
	for _, part := range strings.Split(input, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			parts = append(parts, trimmed)
		}
	}
	return parts
}
