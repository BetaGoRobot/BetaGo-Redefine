package larkmsg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	"go.uber.org/zap"
)

const ephemeralSendPath = "/open-apis/ephemeral/v1/send"

type sendEphemeralMessageReq struct {
	ChatID  string `json:"chat_id"`
	OpenID  string `json:"open_id"`
	MsgType string `json:"msg_type"`
	Card    any    `json:"card"`
}

type sendEphemeralMessageResp struct {
	larkcore.CodeError
	Data *struct {
		MessageID string `json:"message_id,omitempty"`
	} `json:"data,omitempty"`
}

func (r *sendEphemeralMessageResp) Success() bool {
	return r != nil && r.Code == 0
}

func SendEphemeralCard(ctx context.Context, chatID, openID string, cardData any) (messageID string, err error) {
	chatID = strings.TrimSpace(chatID)
	openID = strings.TrimSpace(openID)
	if chatID == "" {
		return "", fmt.Errorf("ephemeral chat_id is required")
	}
	if openID == "" {
		return "", fmt.Errorf("ephemeral open_id is required")
	}

	apiResp, err := lark_dal.Client().Post(ctx, ephemeralSendPath, sendEphemeralMessageReq{
		ChatID:  chatID,
		OpenID:  openID,
		MsgType: "interactive",
		Card:    cardData,
	}, larkcore.AccessTokenTypeTenant)
	if err != nil {
		logs.L().Ctx(ctx).Error("SendEphemeralCard failed", zap.String("chat_id", chatID), zap.String("open_id", openID), zap.Error(err))
		return "", err
	}
	if apiResp == nil {
		return "", errors.New("empty ephemeral api response")
	}

	resp := &sendEphemeralMessageResp{}
	if err := json.Unmarshal(apiResp.RawBody, resp); err != nil {
		logs.L().Ctx(ctx).Error("SendEphemeralCard decode failed", zap.String("chat_id", chatID), zap.String("open_id", openID), zap.Error(err))
		return "", err
	}
	if !resp.Success() {
		err := errors.New(resp.Error())
		logs.L().Ctx(ctx).Error("SendEphemeralCard failed", zap.String("chat_id", chatID), zap.String("open_id", openID), zap.Error(err))
		return "", err
	}
	if resp.Data == nil {
		return "", nil
	}
	return strings.TrimSpace(resp.Data.MessageID), nil
}
