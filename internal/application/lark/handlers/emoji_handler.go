package handlers

import (
	"context"
	"errors"

	arktools "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xcommand"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

type AddEmojiReactionArgs struct {
	ReactionType string `json:"reaction_type"`
	MessageID    string `json:"message_id"`
}

type addEmojiReactionHandler struct{}

var AddEmojiReaction = addEmojiReactionHandler{}

func (addEmojiReactionHandler) ParseTool(raw string) (AddEmojiReactionArgs, error) {
	parsed := AddEmojiReactionArgs{}
	if err := utils.UnmarshalStringPre(raw, &parsed); err != nil {
		return AddEmojiReactionArgs{}, err
	}
	if parsed.ReactionType == "" {
		return AddEmojiReactionArgs{}, errors.New("reaction_type is required")
	}
	return parsed, nil
}

func (addEmojiReactionHandler) ToolSpec() xcommand.ToolSpec {
	return xcommand.ToolSpec{
		Name: "add_emoji_reaction",
		Desc: "给指定消息添加表情反应。当你想对某条消息表达态度、认可、感谢、反对等情绪时使用此工具。例如：赞同某人的观点、庆祝好消息、表示惊讶等。",
		Params: arktools.NewParams("object").
			AddProp("reaction_type", &arktools.Prop{
				Type: "string",
				Desc: "表情类型，如: THUMBSUP, HEART, LAUGH, CLAP, WOW, ANGRY, CRY, etc.",
			}).
			AddProp("message_id", &arktools.Prop{
				Type: "string",
				Desc: "目标消息ID，不填则默认为当前正在讨论的消息",
			}).
			AddRequired("reaction_type"),
		Result: func(metaData *xhandler.BaseMetaData) string {
			result, _ := metaData.GetExtra("emoji_reaction_result")
			return result
		},
	}
}

func (addEmojiReactionHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg AddEmojiReactionArgs) error {
	msgID := arg.MessageID
	if msgID == "" {
		msgID = currentMessageID(data)
	}
	if msgID == "" {
		return errors.New("message_id is required")
	}

	_, err := larkmsg.AddReaction(ctx, arg.ReactionType, msgID)
	if err != nil {
		return err
	}
	metaData.SetExtra("emoji_reaction_result", "表情已添加")
	return nil
}

func (addEmojiReactionHandler) CommandDescription() string {
	return "添加表情反应"
}

func (addEmojiReactionHandler) CommandExamples() []string {
	return []string{
		"/react thumbsup",
		"/react --type=heart",
	}
}