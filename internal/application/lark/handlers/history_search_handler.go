package handlers

import (
	"context"
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
	Keywords  string `json:"keywords"`
	TopK      int    `json:"top_k"`
	StartTime string `json:"start_time"`
	EndTime   string `json:"end_time"`
	OpenID    string `json:"user_id"`
}

type historySearchHandler struct{}

var SearchHistory historySearchHandler

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
		Desc: "根据输入的关键词搜索相关的历史对话记录",
		Params: arktools.NewParams("object").
			AddProp("keywords", &arktools.Prop{
				Type: "string",
				Desc: "需要检索的关键词列表,逗号隔开",
			}).
			AddProp("user_id", &arktools.Prop{
				Type: "string",
				Desc: "用户ID",
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
			}).
			AddRequired("keywords"),
		Result: func(metaData *xhandler.BaseMetaData) string {
			result, _ := metaData.GetExtra("search_result")
			return result
		},
	}
}

func (historySearchHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg HistorySearchArgs) error {
	res, err := history.HybridSearch(ctx,
		history.HybridSearchRequest{
			QueryText: splitByComma(arg.Keywords),
			TopK:      arg.TopK,
			OpenID:    arg.OpenID,
			ChatID:    metaData.ChatID,
			StartTime: arg.StartTime,
			EndTime:   arg.EndTime,
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
