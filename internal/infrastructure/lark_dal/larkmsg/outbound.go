package larkmsg

import (
	"context"
	"errors"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"go.uber.org/zap"
)

func sendCreateMessage(ctx context.Context, req *larkim.CreateMessageReq, recordContents ...string) (resp *larkim.CreateMessageResp, err error) {
	resp, err = lark_dal.Client().Im.V1.Message.Create(ctx, req)
	if err != nil {
		logs.L().Ctx(ctx).Error("CreateMessage", zap.Error(err))
		return nil, err
	}
	if !resp.Success() {
		logs.L().Ctx(ctx).Error("CreateMessage", zap.String("respError", resp.Error()))
		return nil, errors.New(resp.Error())
	}
	go RecordMessage2Opensearch(ctx, resp, recordContents...)
	return resp, nil
}

func sendReplyMessage(ctx context.Context, req *larkim.ReplyMessageReq, recordContents ...string) (resp *larkim.ReplyMessageResp, err error) {
	resp, err = lark_dal.Client().Im.V1.Message.Reply(ctx, req)
	if err != nil {
		logs.L().Ctx(ctx).Error("ReplyMessage", zap.Error(err))
		return nil, err
	}
	if !resp.Success() {
		logs.L().Ctx(ctx).Error("ReplyMessage", zap.String("Error", larkcore.Prettify(resp.CodeError.Err)))
		return nil, errors.New(resp.Error())
	}
	go RecordReplyMessage2Opensearch(ctx, resp, recordContents...)
	return resp, nil
}
