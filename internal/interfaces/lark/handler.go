package lark

import (
	"context"
	"strconv"
	"time"

	cardhandlers "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/card_handlers"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/messages"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"

	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"

	"github.com/BetaGoRobot/go_utils/reflecting"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
	larkapplication "github.com/larksuite/oapi-sdk-go/v3/service/application/v6"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
)

func isOutDated(createTime string) bool {
	stamp, err := strconv.ParseInt(createTime, 10, 64)
	if err != nil {
		panic(err)
	}
	return time.Now().Sub(time.UnixMilli(stamp)) > time.Second*10
}

func MessageV2Handler(ctx context.Context, event *larkim.P2MessageReceiveV1) (err error) {
	fn := reflecting.GetCurrentFunc()
	ctx, span := otel.T().Start(ctx, fn)
	defer larkmsg.RecoverMsg(ctx, *event.Event.Message.MessageId)
	span.SetAttributes(attribute.Key("event").String(larkcore.Prettify(event)))
	defer func() { span.RecordError(err) }()

	if isOutDated(*event.Event.Message.CreateTime) {
		return nil
	}
	if *event.Event.Sender.SenderId.OpenId == config.Get().LarkConfig.BotOpenID {
		return nil
	}
	logs.L().Ctx(ctx).Info("Inside the child span for complex handler", zap.String("event", larkcore.Prettify(event)))
	go func() {
		subCtx, span := otel.T().Start(context.Background(), fn+"_RealRun")
		defer span.End()
		span.SetAttributes(attribute.String("msgID", utils.AddrOrNil(event.Event.Message.MessageId)))
		messages.Handler.Clean().WithCtx(subCtx).WithData(event).Run()
	}()

	logs.L().Ctx(ctx).Info("Message event received", zap.String("event", larkcore.Prettify(event)))
	return nil
}

func MessageReactionHandler(ctx context.Context, event *larkim.P2MessageReactionCreatedV1) (err error) {
	return
}

func CardActionHandler(ctx context.Context, cardAction *callback.CardActionTriggerEvent) (resp *callback.CardActionTriggerResponse, err error) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	defer larkmsg.RecoverMsg(ctx, cardAction.Event.Context.OpenMessageID)
	span.SetAttributes(attribute.Key("event").String(larkcore.Prettify(cardAction)))
	defer span.End()
	defer func() { span.RecordError(err) }()
	metaData := xhandler.NewBaseMetaDataWithChatIDUID(ctx, cardAction.Event.Context.OpenChatID, cardAction.Event.Operator.OpenID)
	// 记录一下操作记录
	defer func() { go larkmsg.RecordCardAction2Opensearch(ctx, cardAction) }()
	if len(cardAction.Event.Action.FormValue) > 0 {
		go cardhandlers.HandleSubmit(ctx, cardAction)
	} else if buttonType, ok := cardAction.Event.Action.Value["type"]; ok {
		switch buttonType {
		case "song":
			if musicID, ok := cardAction.Event.Action.Value["id"]; ok {
				go cardhandlers.SendMusicCard(ctx, metaData, musicID.(string), cardAction.Event.Context.OpenMessageID, 1)
			}
		case "album":
			if albumID, ok := cardAction.Event.Action.Value["id"]; ok {
				_ = albumID
				go cardhandlers.SendAlbumCard(ctx, metaData, albumID.(string), cardAction.Event.Context.OpenMessageID)
			}
		case "lyrics":
			if musicID, ok := cardAction.Event.Action.Value["id"]; ok {
				go cardhandlers.HandleFullLyrics(ctx, metaData, musicID.(string), cardAction.Event.Context.OpenMessageID)
			}
		case "withdraw":
			// 撤回消息
			go cardhandlers.HandleWithDraw(ctx, cardAction)
		case "refresh":
			if musicID, ok := cardAction.Event.Action.Value["id"]; ok {
				go cardhandlers.HandleRefreshMusic(ctx, musicID.(string), cardAction.Event.Context.OpenMessageID)
			}
		case "refresh_obj":
			// 通用的卡片刷新结构，重点是记录触发的command重新触发？
			go cardhandlers.HandleRefreshObj(ctx, cardAction)
		}
	}
	return
}

func AuditV6Handler(ctx context.Context, event *larkapplication.P2ApplicationAppVersionAuditV6) (err error) {
	return
}
