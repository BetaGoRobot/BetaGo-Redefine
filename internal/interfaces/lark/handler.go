package lark

import (
	"context"
	"strconv"
	"time"

	cardhandlers "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/card_handlers"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/messages"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/reaction"
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
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

func isOutDated(createTime string) bool {
	stamp, err := strconv.ParseInt(createTime, 10, 64)
	if err != nil {
		panic(err)
	}
	return time.Now().Sub(time.UnixMilli(stamp)) > time.Second*10
}

func recordSpanError(span trace.Span, err *error) {
	if err == nil || *err == nil {
		return
	}
	span.RecordError(*err)
}

func runProcessorAsync[T any](ctx context.Context, processor *xhandler.Processor[T, xhandler.BaseMetaData], event *T) {
	go processor.NewExecution().WithCtx(ctx).WithData(event).Run()
}

func runMessageProcessorAsync(spanName, msgID string, event *larkim.P2MessageReceiveV1) {
	go func() {
		subCtx, span := otel.T().Start(context.Background(), spanName+"_RealRun")
		defer span.End()
		span.SetAttributes(attribute.String("msgID", msgID))
		messages.Handler.NewExecution().WithCtx(subCtx).WithData(event).Run()
	}()
}

func hasCardFormValue(cardAction *callback.CardActionTriggerEvent) bool {
	return len(cardAction.Event.Action.FormValue) > 0
}

func cardActionType(cardAction *callback.CardActionTriggerEvent) (string, bool) {
	return actionStringValue(cardAction, "type")
}

func cardActionID(cardAction *callback.CardActionTriggerEvent) (string, bool) {
	return actionStringValue(cardAction, "id")
}

func actionStringValue(cardAction *callback.CardActionTriggerEvent, key string) (string, bool) {
	value, ok := cardAction.Event.Action.Value[key]
	if !ok {
		return "", false
	}
	str, ok := value.(string)
	return str, ok
}

func dispatchCardAction(ctx context.Context, metaData *xhandler.BaseMetaData, cardAction *callback.CardActionTriggerEvent) {
	buttonType, ok := cardActionType(cardAction)
	if !ok {
		return
	}

	switch buttonType {
	case "song":
		dispatchCardActionWithID(cardAction, func(id string) {
			go cardhandlers.SendMusicCard(ctx, metaData, id, cardAction.Event.Context.OpenMessageID, 1)
		})
	case "album":
		dispatchCardActionWithID(cardAction, func(id string) {
			go cardhandlers.SendAlbumCard(ctx, metaData, id, cardAction.Event.Context.OpenMessageID)
		})
	case "lyrics":
		dispatchCardActionWithID(cardAction, func(id string) {
			go cardhandlers.HandleFullLyrics(ctx, metaData, id, cardAction.Event.Context.OpenMessageID)
		})
	case "withdraw":
		go cardhandlers.HandleWithDraw(ctx, cardAction)
	case "refresh":
		dispatchCardActionWithID(cardAction, func(id string) {
			go cardhandlers.HandleRefreshMusic(ctx, id, cardAction.Event.Context.OpenMessageID)
		})
	case "refresh_obj":
		go cardhandlers.HandleRefreshObj(ctx, cardAction)
	}
}

func dispatchCardActionWithID(cardAction *callback.CardActionTriggerEvent, fn func(string)) {
	id, ok := cardActionID(cardAction)
	if !ok {
		return
	}
	fn(id)
}

func MessageV2Handler(ctx context.Context, event *larkim.P2MessageReceiveV1) (err error) {
	fn := reflecting.GetCurrentFunc()
	ctx, span := otel.T().Start(ctx, fn)
	defer larkmsg.RecoverMsg(ctx, *event.Event.Message.MessageId)
	span.SetAttributes(attribute.Key("event").String(larkcore.Prettify(event)))
	defer recordSpanError(span, &err)

	if isOutDated(*event.Event.Message.CreateTime) {
		return nil
	}
	if *event.Event.Sender.SenderId.OpenId == config.Get().LarkConfig.BotOpenID {
		return nil
	}
	logs.L().Ctx(ctx).Info("Inside the child span for complex handler", zap.String("event", larkcore.Prettify(event)))
	runMessageProcessorAsync(fn, utils.AddrOrNil(event.Event.Message.MessageId), event)

	logs.L().Ctx(ctx).Info("Message event received", zap.String("event", larkcore.Prettify(event)))
	return nil
}

// MessageReactionHandler Repeat
//
//	@param ctx
//	@param event
//	@return error
func MessageReactionHandler(ctx context.Context, event *larkim.P2MessageReactionCreatedV1) (err error) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	defer larkmsg.RecoverMsg(ctx, *event.Event.MessageId)
	defer span.End()
	defer recordSpanError(span, &err)

	runProcessorAsync(ctx, reaction.Handler, event)
	return nil
}

func CardActionHandler(ctx context.Context, cardAction *callback.CardActionTriggerEvent) (resp *callback.CardActionTriggerResponse, err error) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	defer larkmsg.RecoverMsg(ctx, cardAction.Event.Context.OpenMessageID)
	span.SetAttributes(attribute.Key("event").String(larkcore.Prettify(cardAction)))
	defer span.End()
	defer recordSpanError(span, &err)
	metaData := xhandler.NewBaseMetaDataWithChatIDUID(ctx, cardAction.Event.Context.OpenChatID, cardAction.Event.Operator.OpenID)
	// 记录一下操作记录
	defer func() { go larkmsg.RecordCardAction2Opensearch(ctx, cardAction) }()
	if hasCardFormValue(cardAction) {
		go cardhandlers.HandleSubmit(ctx, cardAction)
		return
	}
	dispatchCardAction(ctx, metaData, cardAction)
	return
}

func AuditV6Handler(ctx context.Context, event *larkapplication.P2ApplicationAppVersionAuditV6) (err error) {
	return
}
