package lark

import (
	"context"

	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
	larkapplication "github.com/larksuite/oapi-sdk-go/v3/service/application/v6"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"go.uber.org/zap"
)

func MessageV2Handler(ctx context.Context, event *larkim.P2MessageReceiveV1) (err error) {
	logs.L().Ctx(ctx).Debug("Received message event", zap.String("event", utils.MustMarshalString(event)))
	return
}

func MessageReactionHandler(ctx context.Context, event *larkim.P2MessageReactionCreatedV1) (err error) {
	return
}

func CardActionHandler(ctx context.Context, cardAction *callback.CardActionTriggerEvent) (resp *callback.CardActionTriggerResponse, err error) {
	return
}

func AuditV6Handler(ctx context.Context, event *larkapplication.P2ApplicationAppVersionAuditV6) (err error) {
	return
}
