package larkmsg

import (
	"context"
	"errors"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func ReplyRawCard(ctx context.Context, msgID, content, suffix string, replyInThread bool) error {
	_, err := ReplyMsgRawContentType(ctx, msgID, larkim.MsgTypeInteractive, content, suffix, replyInThread)
	return err
}

func CreateRawCard(ctx context.Context, chatID, content, msgID, suffix string) error {
	_, err := CreateMsgRawContentType(ctx, chatID, larkim.MsgTypeInteractive, content, msgID, suffix)
	return err
}

func PatchRawCard(ctx context.Context, msgID, content string) (err error) {
	resp, err := lark_dal.Client().Im.V1.Message.Patch(
		ctx,
		larkim.NewPatchMessageReqBuilder().
			MessageId(msgID).
			Body(larkim.NewPatchMessageReqBodyBuilder().Content(content).Build()).
			Build(),
	)
	if err != nil {
		return err
	}
	if !resp.Success() {
		return errors.New(resp.Error())
	}
	return nil
}
