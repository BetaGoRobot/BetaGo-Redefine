package handlers

import (
	"context"
	"fmt"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/mention"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/runtimecontext"
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

type sendMessageHandler struct {
	allowTargetChatOverride bool
}

var (
	SendMessage          = sendMessageHandler{allowTargetChatOverride: true}
	ScheduledSendMessage = sendMessageHandler{allowTargetChatOverride: false}
)

func (sendMessageHandler) ParseTool(raw string) (SendMessageArgs, error) {
	parsed := SendMessageArgs{}
	if err := utils.UnmarshalStringPre(raw, &parsed); err != nil {
		return SendMessageArgs{}, err
	}
	return parsed, nil
}

func (h sendMessageHandler) ToolSpec() xcommand.ToolSpec {
	params := arktools.NewParams("object").
		AddProp("content", &arktools.Prop{
			Type: "string",
			Desc: "要发送的消息内容",
		}).
		AddRequired("content")
	desc := "发送一条消息到当前对话或指定群组。当你需要主动通知用户、发送提醒确认、或者发送额外信息时使用此工具"
	if h.allowTargetChatOverride {
		params.AddProp("chat_id", &arktools.Prop{
			Type: "string",
			Desc: "目标群组ID，不填则发送到当前对话",
		})
	} else {
		desc = "发送一条消息到当前任务所属的对话。当你需要主动通知用户、发送提醒确认、或者发送额外信息时使用此工具"
	}
	desc += "。如果需要@成员，优先直接输出飞书格式 `<at user_id=\"open_id\">姓名</at>`；如果只知道名字，也可以输出 `@姓名`，系统会尝试按当前群成员匹配。"
	desc += " 只有在需要某个具体成员响应、确认、补充或接手时再 @；普通群通知不要一上来就 @。如果只是延续当前对话，不必为了点名而强行 @。"
	return xcommand.ToolSpec{
		Name:   "send_message",
		Desc:   desc,
		Params: params,
		Result: func(metaData *xhandler.BaseMetaData) string {
			result, _ := metaData.GetExtra("send_message_result")
			return result
		},
	}
}

func (h sendMessageHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg SendMessageArgs) error {
	targetChatID := metaData.ChatID
	if arg.ChatID != "" && !h.allowTargetChatOverride {
		return fmt.Errorf("scheduled send_message cannot override chat_id")
	}

	if arg.ChatID != "" {
		targetChatID = arg.ChatID
	}
	content := arg.Content
	if normalized, err := mention.NormalizeOutgoingText(ctx, targetChatID, content); err == nil {
		content = normalized
	}

	if !h.allowTargetChatOverride {
		if msgID := currentMessageID(data); msgID != "" {
			if resp, err := larkmsg.ReplyMsgText(ctx, content, msgID, "_sendMessage", false); err == nil && resp.Success() {
				if metaData != nil {
					metaData.SetLastReplyRef(msgID, "text")
				}
				runtimecontext.RecordCompatibleReplyRef(ctx, *resp.Data.MessageId, "text")
				metaData.SetExtra("send_message_result", "消息发送成功")
				return nil
			}
		}
	}

	if err := larkmsg.CreateMsgTextRaw(ctx, larkmsg.NewTextMsgBuilder().Text(content).Build(), "", targetChatID); err != nil {
		return err
	}
	metaData.SetExtra("send_message_result", "消息发送成功")
	return nil
}

func resolveSendMessageApprovalSummary(arg SendMessageArgs) string {
	if arg.ChatID != "" {
		return "将向指定群发送一条消息"
	}
	return "将向当前对话发送一条消息"
}
