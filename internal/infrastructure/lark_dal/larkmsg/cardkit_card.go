package larkmsg

import (
	"context"
	"errors"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg/larkcard"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/bytedance/sonic"
	larkcardkit "github.com/larksuite/oapi-sdk-go/v3/service/cardkit/v1"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"go.uber.org/zap"
)

const (
	cardKitTypeCardJSON = "card_json"
)

func ReplyCardJSON(ctx context.Context, msgID string, cardData any, suffix string, replyInThread bool) error {
	_, err := ReplyCardJSONReturning(ctx, msgID, cardData, suffix, replyInThread)
	return err
}

// ReplyCardJSONReturning 同 ReplyCardJSON，但返回新发卡片的 message_id。
// 用于发卡瞬间需要锁定一次点单流程的场景。
func ReplyCardJSONReturning(ctx context.Context, msgID string, cardData any, suffix string, replyInThread bool) (string, error) {
	content, err := buildCardEntityContent(ctx, cardKitTypeCardJSON, cardData)
	if err != nil {
		return "", err
	}
	resp, err := ReplyMsgRawContentType(ctx, msgID, larkim.MsgTypeInteractive, content, suffix, replyInThread)
	if err != nil {
		return "", err
	}
	return messageIDFromReplyResp(resp), nil
}

func CreateCardJSON(ctx context.Context, chatID string, cardData any, msgID, suffix string) error {
	_, err := CreateCardJSONReturning(ctx, chatID, cardData, msgID, suffix)
	return err
}

// CreateCardJSONReturning 同 CreateCardJSON，但返回新发卡片的 message_id。
func CreateCardJSONReturning(ctx context.Context, chatID string, cardData any, msgID, suffix string) (string, error) {
	return CreateCardJSONByReceiveIDReturning(ctx, larkim.CreateMessageV1ReceiveIDTypeChatId, chatID, cardData, msgID, suffix)
}

func CreateCardJSONByReceiveID(ctx context.Context, receiveIDType, receiveID string, cardData any, msgID, suffix string) error {
	_, err := CreateCardJSONByReceiveIDReturning(ctx, receiveIDType, receiveID, cardData, msgID, suffix)
	return err
}

// CreateCardJSONByReceiveIDReturning 同 CreateCardJSONByReceiveID，但返回新发卡片的 message_id。
func CreateCardJSONByReceiveIDReturning(ctx context.Context, receiveIDType, receiveID string, cardData any, msgID, suffix string) (string, error) {
	content, err := buildCardEntityContent(ctx, cardKitTypeCardJSON, cardData)
	if err != nil {
		return "", err
	}
	resp, err := CreateMsgRawContentTypeByReceiveID(ctx, receiveIDType, receiveID, larkim.MsgTypeInteractive, content, msgID, suffix)
	if err != nil {
		return "", err
	}
	return messageIDFromCreateResp(resp), nil
}

func messageIDFromCreateResp(resp *larkim.CreateMessageResp) string {
	if resp == nil || resp.Data == nil || resp.Data.MessageId == nil {
		return ""
	}
	return strings.TrimSpace(*resp.Data.MessageId)
}

func messageIDFromReplyResp(resp *larkim.ReplyMessageResp) string {
	if resp == nil || resp.Data == nil || resp.Data.MessageId == nil {
		return ""
	}
	return strings.TrimSpace(*resp.Data.MessageId)
}

func PatchCardJSON(ctx context.Context, msgID string, cardData any) error {
	content := utils.MustMarshalString(cardData)
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
		logs.L().Error("patch card json failed", zap.String("err", resp.Error()), zap.Stack("trace"))
		return errors.New(resp.Error())
	}
	return nil
}

func BuildCardEntityContent(ctx context.Context, cardData any) (string, error) {
	return buildCardEntityContent(ctx, cardKitTypeCardJSON, cardData)
}

func SetCardEntityStreaming(ctx context.Context, cardID string, enabled bool, sequence int) error {
	ctx, span := otel.Start(ctx)
	defer span.End()

	cardID = strings.TrimSpace(cardID)
	if cardID == "" {
		return errors.New("empty card id")
	}
	settings := larkcard.DisableCardStreaming().String()
	if enabled {
		settings = larkcard.EnableCardStreaming().String()
	}
	reqBodyBuilder := larkcardkit.NewSettingsCardReqBodyBuilder().
		Settings(settings).
		Uuid(streamingUUID("settings", cardID, sequence))
	if sequence > 0 {
		reqBodyBuilder.Sequence(sequence)
	}
	resp, err := lark_dal.Client().Cardkit.V1.Card.Settings(
		ctx,
		larkcardkit.NewSettingsCardReqBuilder().
			CardId(cardID).
			Body(reqBodyBuilder.Build()).
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

func CardIDFromMessageID(ctx context.Context, msgID string) (string, error) {
	ctx, span := otel.Start(ctx)
	defer span.End()

	msgID = strings.TrimSpace(msgID)
	if msgID == "" {
		return "", errors.New("empty message id")
	}
	resp, err := lark_dal.Client().Cardkit.V1.Card.IdConvert(
		ctx,
		larkcardkit.NewIdConvertCardReqBuilder().
			Body(larkcardkit.NewIdConvertCardReqBodyBuilder().MessageId(msgID).Build()).
			Build(),
	)
	if err != nil {
		return "", err
	}
	if !resp.Success() {
		return "", errors.New(resp.Error())
	}
	if resp.Data == nil || resp.Data.CardId == nil || strings.TrimSpace(*resp.Data.CardId) == "" {
		return "", errors.New("empty card_id from cardkit id_convert")
	}
	return strings.TrimSpace(*resp.Data.CardId), nil
}

func createCardEntityFromData(ctx context.Context, cardData any) (string, error) {
	raw, err := sonic.Marshal(cardData)
	if err != nil {
		return "", err
	}
	return createCardEntity(ctx, cardKitTypeCardJSON, string(raw))
}

func buildCardEntityContent(ctx context.Context, cardType string, cardData any) (string, error) {
	raw, err := sonic.Marshal(cardData)
	if err != nil {
		return "", err
	}

	cardID, err := createCardEntity(ctx, cardType, string(raw))
	if err != nil {
		return "", err
	}
	return larkcard.NewCardEntityContent(cardID).String(), nil
}

func createCardEntity(ctx context.Context, cardType, data string) (string, error) {
	ctx, span := otel.Start(ctx)
	defer span.End()

	req := larkcardkit.NewCreateCardReqBuilder().
		Body(
			larkcardkit.NewCreateCardReqBodyBuilder().
				Type(cardType).
				Data(data).
				Build(),
		).
		Build()
	resp, err := lark_dal.Client().Cardkit.V1.Card.Create(ctx, req)
	if err != nil {
		return "", err
	}
	if !resp.Success() {
		return "", errors.New(resp.CodeError.Error())
	}
	if resp.Data == nil || resp.Data.CardId == nil || *resp.Data.CardId == "" {
		return "", errors.New("empty card_id from cardkit create")
	}
	return *resp.Data.CardId, nil
}
