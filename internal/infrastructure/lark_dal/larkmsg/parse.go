package larkmsg

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/go_utils/reflecting"
	"github.com/bytedance/sonic"
	"github.com/kevinmatthe/zaplog"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"go.uber.org/zap"
)

func PreGetTextMsg(ctx context.Context, event *larkim.P2MessageReceiveV1) string {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	defer span.End()
	rawContent := GetContentFromTextMsg(*event.Event.Message.Content)
	if len(event.Event.Message.Mentions) > 0 {
		for _, mention := range event.Event.Message.Mentions {
			rawContent = strings.ReplaceAll(rawContent, *mention.Key, fmt.Sprintf("@%s", *mention.Name))
		}
	}
	return rawContent
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
