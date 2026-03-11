package larkmsg

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/bytedance/sonic"
	"github.com/dlclark/regexp2"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

var atMsgRepattern = regexp2.MustCompile(`@[^ ]+\s+(?P<content>.+)`, regexp2.RE2)

func AtUser(openID, userName string) string {
	return fmt.Sprintf("<at user_id=\"%s\">%s</at>", openID, userName)
}

// TrimAtMsg trim掉at的消息
//
//	@param ctx context.Context
//	@param msg string
//	@return string
//	@author heyuhengmatt
//	@update 2024-07-17 01:39:05
func TrimAtMsg(ctx context.Context, msg string) string {
	ctx, span := otel.Start(ctx)
	defer span.End()
	match, err := atMsgRepattern.FindStringMatch(msg)
	if err != nil {
		return msg
	}
	if match != nil && match.Length > 0 {
		return match.GroupByName("content").String()
	}
	return msg
}

type TextBuilder struct {
	builder strings.Builder
}

func NewTextMsgBuilder() *TextBuilder {
	return &TextBuilder{
		builder: strings.Builder{},
	}
}

func (t *TextBuilder) Text(text string) *TextBuilder {
	t.builder.WriteString(text)
	return t
}

func (t *TextBuilder) AtUser(openID, name string) *TextBuilder {
	t.builder.WriteString("<at user_id=\"")
	t.builder.WriteString(openID)
	t.builder.WriteString("\">")
	t.builder.WriteString(name)
	t.builder.WriteString("</at>")
	return t
}

func AtUserString(openID string) string {
	return fmt.Sprintf("<at id=%s>某个用户</at>", openID)
}

func (t *TextBuilder) Build() string {
	tmpStruct := struct {
		Text string `json:"text"`
	}{
		Text: t.builder.String(),
	}
	s, err := sonic.MarshalString(tmpStruct)
	if err != nil {
		panic(err)
	}
	return s
}

func GetAllParentMsg(ctx context.Context, data *larkim.P2MessageReceiveV1) (msgList []*larkim.Message, err error) {
	msgList = []*larkim.Message{}
	if data.Event.Message.ThreadId != nil { // 话题模式，找到所有的ID
		resp, err := lark_dal.Client().Im.Message.List(ctx, larkim.NewListMessageReqBuilder().ContainerIdType("thread").ContainerId(*data.Event.Message.ThreadId).Build())
		if err != nil {
			return nil, err
		}
		for _, msg := range resp.Data.Items {
			msgList = append(msgList, msg)
		}
	} else if data.Event.Message.ParentId != nil {
		respMsg := GetMsgFullByID(ctx, *data.Event.Message.ParentId)
		msg := respMsg.Data.Items[0]
		if msg == nil {
			return nil, errors.New("No parent message found")
		}
		msgList = append(msgList, msg)
	}
	return
}
