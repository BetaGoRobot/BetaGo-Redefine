package larkmsg

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg/larkcard"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	larkcardkit "github.com/larksuite/oapi-sdk-go/v3/service/cardkit/v1"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

const (
	cardKitTypeCardJSON = "card_json"
)

func ReplyCardJSON(ctx context.Context, msgID string, cardData any, suffix string, replyInThread bool) error {
	content, err := buildCardEntityContent(ctx, cardKitTypeCardJSON, cardData)
	if err != nil {
		return err
	}
	_, err = ReplyMsgRawContentType(ctx, msgID, larkim.MsgTypeInteractive, content, suffix, replyInThread)
	return err
}

func CreateCardJSON(ctx context.Context, chatID string, cardData any, msgID, suffix string) error {
	return CreateCardJSONByReceiveID(ctx, larkim.ReceiveIdTypeChatId, chatID, cardData, msgID, suffix)
}

func CreateCardJSONByReceiveID(ctx context.Context, receiveIDType, receiveID string, cardData any, msgID, suffix string) error {
	content, err := buildCardEntityContent(ctx, cardKitTypeCardJSON, cardData)
	if err != nil {
		return err
	}
	_, err = CreateMsgRawContentTypeByReceiveID(ctx, receiveIDType, receiveID, larkim.MsgTypeInteractive, content, msgID, suffix)
	return err
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
		return errors.New(resp.Error())
	}
	return nil
}

func BuildCardEntityContent(ctx context.Context, cardData any) (string, error) {
	return buildCardEntityContent(ctx, cardKitTypeCardJSON, cardData)
}

func buildCardEntityContent(ctx context.Context, cardType string, cardData any) (string, error) {
	raw, err := json.Marshal(cardData)
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

// IdConvert converts a message ID to a card ID for use with cardKit APIs.
func IdConvert(ctx context.Context, messageID string) (string, error) {
	req := larkcardkit.NewIdConvertCardReqBuilder().
		Body(
			larkcardkit.NewIdConvertCardReqBodyBuilder().
				MessageId(messageID).
				Build(),
		).
		Build()
	resp, err := lark_dal.Client().Cardkit.V1.Card.IdConvert(ctx, req)
	if err != nil {
		return "", err
	}
	if !resp.Success() {
		return "", errors.New(resp.CodeError.Error())
	}
	if resp.Data == nil || resp.Data.CardId == nil || *resp.Data.CardId == "" {
		return "", errors.New("empty card_id from id_convert")
	}
	return *resp.Data.CardId, nil
}

// BatchUpdateCard performs a batch update on a card entity with streaming sequence support.
// The actions parameter should be a JSON-encoded array of Action objects.
func BatchUpdateCard(ctx context.Context, cardID string, sequence int, actions string) error {
	req := larkcardkit.NewBatchUpdateCardReqBuilder().
		CardId(cardID).
		Body(
			larkcardkit.NewBatchUpdateCardReqBodyBuilder().
				Sequence(sequence).
				Actions(actions).
				Build(),
		).
		Build()
	resp, err := lark_dal.Client().Cardkit.V1.Card.BatchUpdate(ctx, req)
	if err != nil {
		return err
	}
	if !resp.Success() {
		return errors.New(resp.CodeError.Error())
	}
	return nil
}

// UpdateCardElement replaces an entire element in a card entity by element_id with streaming sequence support.
func UpdateCardElement(ctx context.Context, cardID, elementID string, sequence int, element string) error {
	req := larkcardkit.NewUpdateCardElementReqBuilder().
		CardId(cardID).
		ElementId(elementID).
		Body(
			larkcardkit.NewUpdateCardElementReqBodyBuilder().
				Element(element).
				Sequence(sequence).
				Build(),
		).
		Build()
	resp, err := lark_dal.Client().Cardkit.V1.CardElement.Update(ctx, req)
	if err != nil {
		return err
	}
	if !resp.Success() {
		return errors.New(resp.CodeError.Error())
	}
	return nil
}

// CreateAndSendCardJSON creates a card entity and sends it as a message, returning the message ID.
func CreateAndSendCardJSON(ctx context.Context, receiveIDType, receiveID string, cardData any, msgID, suffix string) (string, error) {
	content, err := buildCardEntityContent(ctx, cardKitTypeCardJSON, cardData)
	if err != nil {
		return "", err
	}
	resp, err := CreateMsgRawContentTypeByReceiveID(ctx, receiveIDType, receiveID, larkim.MsgTypeInteractive, content, msgID, suffix)
	if err != nil {
		return "", err
	}
	if resp != nil && resp.Data != nil && resp.Data.MessageId != nil && *resp.Data.MessageId != "" {
		return *resp.Data.MessageId, nil
	}
	return "", nil
}
