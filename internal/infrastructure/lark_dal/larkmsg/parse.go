package larkmsg

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/BetaGoRobot/go_utils/reflecting"
	"github.com/bytedance/sonic"
	"github.com/kevinmatthe/zaplog"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"go.uber.org/zap"
)

type MsgResult struct {
	textBuilder strings.Builder
	images      []string
	rawContent  string
}

func NewMsgResult() *MsgResult {
	return &MsgResult{images: make([]string, 0)}
}

func (r *MsgResult) AddImage(imgKey string) {
	r.images = append(r.images, imgKey)
}

func (r *MsgResult) AddText(text string) {
	r.textBuilder.WriteString(text)
	r.textBuilder.WriteString("\n")
}

func (r *MsgResult) GetText() string {
	return r.textBuilder.String()
}

func (r *MsgResult) Raw() string {
	return r.rawContent
}

func PreGetTextMsg(ctx context.Context, event *larkim.P2MessageReceiveV1) (res *MsgResult) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	defer span.End()
	res = NewMsgResult()
	res.rawContent = *event.Event.Message.Content
	msgType := *event.Event.Message.MessageType
	switch msgType {
	case larkim.MsgTypePost:
		return GetContentFromPostMsg(ctx, event)
	case larkim.MsgTypeText:
		rawText := GetContentFromTextMsg(*event.Event.Message.Content)
		if len(event.Event.Message.Mentions) > 0 {
			for _, mention := range event.Event.Message.Mentions {
				rawText = strings.ReplaceAll(rawText, *mention.Key, fmt.Sprintf("@%s", *mention.Name))
			}
		}
		res.AddText(rawText)
	}

	return res
}

func GetContentFromTextMsg(s string) string {
	msgMap := make(map[string]interface{})
	err := sonic.UnmarshalString(s, &msgMap)
	if err != nil {
		logs.L().Error("repeatMessage", zaplog.Error(err))
		return ""
	}
	if text, ok := msgMap["text"]; ok {
		s = text.(string)
	}
	return s
}

func GetMsgFullByID(ctx context.Context, msgID string) *larkim.GetMessageResp {
	resp, err := lark_dal.Client().Im.V1.Message.Get(ctx, larkim.NewGetMessageReqBuilder().MessageId(msgID).Build())
	if err != nil {
		logs.L().Ctx(ctx).Error("GetMsgByID", zap.Error(err))
	}
	if !resp.Success() {
		logs.L().Ctx(ctx).Error("GetMsgByID", zap.String("error", resp.Error()))
	}
	return resp
}

func GetChatIDFromMsgID(ctx context.Context, msgID string) (chatID string, err error) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	defer span.End()
	defer func() { span.RecordError(err) }()

	resp := GetMsgFullByID(ctx, msgID)
	if !resp.Success() {
		err = errors.New(resp.Error())
		return
	}
	chatID = *resp.Data.Items[0].ChatId
	return
}

func GetContentFromPostMsg(ctx context.Context, event *larkim.P2MessageReceiveV1) (res *MsgResult) {
	res = NewMsgResult()

	postMsg := utils.MustUnmarshalString[PostMsg](*event.Event.Message.Content)
	if postMsg == nil {
		return
	}
	if postTitle := postMsg.Title; len(postTitle) > 0 {
		res.AddText(postTitle)
	}
	postContent := postMsg.Content
	for _, line := range postContent {
		tmpBuilder := strings.Builder{}
		for _, element := range line {
			item := element.Item
			switch item.GetTag() {
			case "text":
				data := item.(*PostMsgContentItemText)
				tmpBuilder.WriteString(data.Text)
			case "at":
				data := item.(*PostMsgContentItemAt)
				text := fmt.Sprintf("@%s", data.UserName)
				if data.UserID != "" &&
					getOpenIDByUserNumberInAt(data.UserID, event.Event.Message.Mentions) == config.Get().LarkConfig.BotOpenID {
					text = "你"
				}
				tmpBuilder.WriteString(text)
			case "img":
				data := item.(*PostMsgContentItemImg)
				res.AddImage(data.ImageKey)
			case "emotion":
				data := item.(*PostMsgContentItemEmotion)
				tmpBuilder.WriteString(fmt.Sprintf(":%s:", data.EmojiType))
			case "hr":
				tmpBuilder.WriteString("---")
			case "code_block":
				data := item.(*PostMsgContentItemCodeBlock)
				tmpBuilder.WriteString(fmt.Sprintf("```%s\n%s```", data.Language, data.Text))
			case "a":
				data := item.(*PostMsgContentItemA)
				tmpBuilder.WriteString(fmt.Sprintf("[%s](%s)", data.Text, data.Href))
			case "media":
				data := item.(*PostMsgContentItemMedia)
				if data.ImageKey != "" {
					res.AddImage(data.ImageKey)
				}
			}
		}
		if tmpBuilder.Len() > 0 {
			res.AddText(tmpBuilder.String())
		}
	}
	return res
}

func getOpenIDByUserNumberInAt(userKey string, mentions []*larkim.MentionEvent) string {
	for _, mention := range mentions {
		if *mention.Key == userKey {
			return *mention.Id.OpenId
		}
	}
	return ""
}
