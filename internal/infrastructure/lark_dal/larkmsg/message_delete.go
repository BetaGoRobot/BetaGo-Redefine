package larkmsg

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"go.uber.org/zap"
)

func DeleteMessage(ctx context.Context, messageID string) error {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return fmt.Errorf("message_id is required")
	}

	resp, err := lark_dal.Client().Im.Message.Delete(ctx, larkim.NewDeleteMessageReqBuilder().MessageId(messageID).Build())
	if err != nil {
		logs.L().Ctx(ctx).Error("DeleteMessage failed", zap.String("message_id", messageID), zap.Error(err))
		return err
	}
	if !resp.Success() {
		err := errors.New(resp.Error())
		logs.L().Ctx(ctx).Error("DeleteMessage failed", zap.String("message_id", messageID), zap.Error(err))
		return err
	}
	return nil
}
