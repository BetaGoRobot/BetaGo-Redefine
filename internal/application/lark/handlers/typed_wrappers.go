package handlers

import (
	"context"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/history"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	arktools "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xcommand"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

type ChatArgs struct {
	Reason    bool   `cli:"r,flag" help:"启用推理模型"`
	NoContext bool   `cli:"c,flag" help:"不携带上下文消息"`
	Input     string `cli:"input,input" help:"聊天输入内容"`
}

type chatHandler struct {
	defaultType string
}

var Chat = chatHandler{defaultType: "chat"}

func (chatHandler) CommandDescription() string {
	return "与机器人对话"
}

func (h chatHandler) ParseCLI(args []string) (ChatArgs, error) {
	argMap, input := parseArgs(args...)
	_, reason := argMap["r"]
	_, noContext := argMap["c"]
	return ChatArgs{
		Reason:    reason,
		NoContext: noContext,
		Input:     input,
	}, nil
}

func (h chatHandler) Handle(ctx context.Context, event *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg ChatArgs) error {
	defer func() { metaData.SkipDone = true }()

	chatType := h.defaultType
	size := 20
	if arg.Reason {
		chatType = MODEL_TYPE_REASON
	}
	if arg.NoContext {
		size = 0
	}

	return ChatHandlerInner(ctx, event, chatType, &size, arg.Input)
}

type debugGetIDArgs struct{}
type debugGetGroupIDArgs struct{}
type debugTryPanicArgs struct{}
type debugTraceArgs struct{}
type debugRevertArgs struct{}
type debugRepeatArgs struct{}
type debugImageArgs struct {
	Prompt string `cli:"prompt,input" help:"图片分析提示词"`
}
type debugConversationArgs struct{}

type debugGetIDHandler struct{}
type debugGetGroupIDHandler struct{}
type debugTryPanicHandler struct{}
type debugTraceHandler struct{}
type debugRevertHandler struct{}
type debugRepeatHandler struct{}
type debugImageHandler struct{}
type debugConversationHandler struct{}

var DebugGetID debugGetIDHandler
var DebugGetGroupID debugGetGroupIDHandler
var DebugTryPanic debugTryPanicHandler
var DebugTrace debugTraceHandler
var DebugRevert debugRevertHandler
var DebugRepeat debugRepeatHandler
var DebugImage debugImageHandler
var DebugConversation debugConversationHandler

func (debugGetIDHandler) ParseCLI(args []string) (debugGetIDArgs, error) {
	return debugGetIDArgs{}, nil
}

func (debugGetIDHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg debugGetIDArgs) error {
	return handleDebugGetID(ctx, data, metaData)
}

func (debugGetGroupIDHandler) ParseCLI(args []string) (debugGetGroupIDArgs, error) {
	return debugGetGroupIDArgs{}, nil
}

func (debugGetGroupIDHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg debugGetGroupIDArgs) error {
	return handleDebugGetGroupID(ctx, data, metaData)
}

func (debugTryPanicHandler) ParseCLI(args []string) (debugTryPanicArgs, error) {
	return debugTryPanicArgs{}, nil
}

func (debugTryPanicHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg debugTryPanicArgs) error {
	return handleDebugTryPanic(ctx, data, metaData)
}

func (debugTraceHandler) ParseCLI(args []string) (debugTraceArgs, error) {
	return debugTraceArgs{}, nil
}

func (debugTraceHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg debugTraceArgs) error {
	return handleDebugTrace(ctx, data, metaData)
}

func (debugRevertHandler) ParseCLI(args []string) (debugRevertArgs, error) {
	return debugRevertArgs{}, nil
}

func (debugRevertHandler) ParseTool(raw string) (debugRevertArgs, error) {
	if err := parseEmptyToolArgs(raw); err != nil {
		return debugRevertArgs{}, err
	}
	return debugRevertArgs{}, nil
}

func (debugRevertHandler) ToolSpec() xcommand.ToolSpec {
	return xcommand.ToolSpec{
		Name:   "revert_message",
		Desc:   "可以撤回指定消息,调用时不需要任何参数，工具会判断要撤回的消息是什么，并且返回撤回的结果。如果不是机器人发出的消息,是不能撤回的",
		Params: arktools.NewParams("object"),
		Result: func(metaData *xhandler.BaseMetaData) string {
			result, _ := metaData.GetExtra("revert_result")
			return result
		},
	}
}

func (debugRevertHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg debugRevertArgs) error {
	return handleDebugRevert(ctx, data, metaData)
}

func (debugRepeatHandler) ParseCLI(args []string) (debugRepeatArgs, error) {
	return debugRepeatArgs{}, nil
}

func (debugRepeatHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg debugRepeatArgs) error {
	return handleDebugRepeat(ctx, data, metaData)
}

func (debugImageHandler) ParseCLI(args []string) (debugImageArgs, error) {
	_, input := parseArgs(args...)
	return debugImageArgs{Prompt: input}, nil
}

func (debugImageHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg debugImageArgs) error {
	if arg.Prompt == "" {
		return handleDebugImage(ctx, data, metaData)
	}
	return handleDebugImage(ctx, data, metaData, arg.Prompt)
}

func (debugConversationHandler) ParseCLI(args []string) (debugConversationArgs, error) {
	return debugConversationArgs{}, nil
}

func (debugConversationHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg debugConversationArgs) error {
	return handleDebugConversation(ctx, data, metaData)
}

type HistorySearchArgs struct {
	Keywords  string `json:"keywords"`
	TopK      int    `json:"top_k"`
	StartTime string `json:"start_time"`
	EndTime   string `json:"end_time"`
	UserID    string `json:"user_id"`
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
			UserID:    arg.UserID,
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

type SendMessageArgs struct {
	Content string `json:"content"`
	ChatID  string `json:"chat_id"`
}

type sendMessageHandler struct{}

var SendMessage sendMessageHandler

func (sendMessageHandler) ParseTool(raw string) (SendMessageArgs, error) {
	parsed := SendMessageArgs{}
	if err := utils.UnmarshalStringPre(raw, &parsed); err != nil {
		return SendMessageArgs{}, err
	}
	return parsed, nil
}

func (sendMessageHandler) ToolSpec() xcommand.ToolSpec {
	return xcommand.ToolSpec{
		Name: "send_message",
		Desc: "发送一条消息到当前对话或指定群组。当你需要主动通知用户、发送提醒确认、或者发送额外信息时使用此工具",
		Params: arktools.NewParams("object").
			AddProp("content", &arktools.Prop{
				Type: "string",
				Desc: "要发送的消息内容",
			}).
			AddProp("chat_id", &arktools.Prop{
				Type: "string",
				Desc: "目标群组ID，不填则发送到当前对话",
			}).
			AddRequired("content"),
		Result: func(metaData *xhandler.BaseMetaData) string {
			result, _ := metaData.GetExtra("send_message_result")
			return result
		},
	}
}

func (sendMessageHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg SendMessageArgs) error {
	targetChatID := metaData.ChatID
	if arg.ChatID != "" {
		targetChatID = arg.ChatID
	}

	if err := larkmsg.CreateMsgTextRaw(ctx, larkmsg.NewTextMsgBuilder().Text(arg.Content).Build(), "", targetChatID); err != nil {
		return err
	}
	metaData.SetExtra("send_message_result", "消息发送成功")
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
