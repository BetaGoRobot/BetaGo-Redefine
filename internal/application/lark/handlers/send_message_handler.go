package handlers

import (
	"context"

	arktools "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xcommand"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

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
