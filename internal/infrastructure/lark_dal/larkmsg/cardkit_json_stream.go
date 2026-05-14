package larkmsg

import (
	"context"
	"errors"
	"sync"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg/larkcard"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/bytedance/sonic"
	larkcardkit "github.com/larksuite/oapi-sdk-go/v3/service/cardkit/v1"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

type CardJSONEntityStream struct {
	mu               sync.Mutex
	cardID           string
	lastSequence     int
	streamingEnabled bool
}

func NewCardJSONEntityStream() *CardJSONEntityStream {
	return &CardJSONEntityStream{}
}

func (s *CardJSONEntityStream) Reply(ctx context.Context, cardData any, msgID, suffix string, replyInThread bool, sequence int) (string, error) {
	resp, cardID, err := ReplyStreamingCardJSONEntityWithResp(ctx, cardData, msgID, suffix, replyInThread)
	if err != nil {
		return "", err
	}
	replyMsgID, err := repliedMessageID(resp)
	if err != nil {
		return "", err
	}
	s.remember(cardID, sequence, true)
	return replyMsgID, nil
}

func (s *CardJSONEntityStream) UpdateMessage(ctx context.Context, msgID string, cardData any, sequence int) (string, error) {
	cardID, err := s.ensureCardID(ctx, msgID)
	if err != nil {
		return "", err
	}
	if err := s.ensureStreaming(ctx, cardID); err != nil {
		return "", err
	}
	if err := UpdateCardJSONEntity(ctx, cardID, cardData, sequence); err != nil {
		return "", err
	}
	s.remember(cardID, sequence, true)
	return msgID, nil
}

func (s *CardJSONEntityStream) Patch(ctx context.Context, msgID string, cardData any, sequence int) error {
	cardID, err := s.ensureCardID(ctx, msgID)
	if err != nil {
		return err
	}
	// if err := s.ensureStreaming(ctx, cardID); err != nil {
	// 	return err
	// }
	if err := UpdateCardJSONEntity(ctx, cardID, cardData, sequence); err != nil {
		return err
	}
	s.remember(cardID, sequence, true)
	return nil
}

func (s *CardJSONEntityStream) Close(ctx context.Context) error {
	s.mu.Lock()
	cardID := s.cardID
	sequence := s.lastSequence + 1
	enabled := s.streamingEnabled
	s.mu.Unlock()
	if cardID == "" || !enabled {
		return nil
	}
	if err := SetCardEntityStreaming(ctx, cardID, false, sequence); err != nil {
		return err
	}
	s.remember(cardID, sequence, false)
	return nil
}

func (s *CardJSONEntityStream) Active() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cardID != "" && s.streamingEnabled
}

func (s *CardJSONEntityStream) ensureCardID(ctx context.Context, msgID string) (string, error) {
	s.mu.Lock()
	cardID := s.cardID
	s.mu.Unlock()
	if cardID != "" {
		return cardID, nil
	}
	cardID, err := CardIDFromMessageID(ctx, msgID)
	if err != nil {
		return "", err
	}
	s.remember(cardID, 0, false)
	return cardID, nil
}

func (s *CardJSONEntityStream) ensureStreaming(ctx context.Context, cardID string) error {
	s.mu.Lock()
	enabled := s.streamingEnabled
	s.mu.Unlock()
	if enabled {
		return nil
	}
	if err := SetCardEntityStreaming(ctx, cardID, true, 1); err != nil {
		return err
	}
	s.remember(cardID, 1, true)
	return nil
}

func (s *CardJSONEntityStream) remember(cardID string, sequence int, streamingEnabled bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cardID != "" {
		s.cardID = cardID
	}
	if sequence > s.lastSequence {
		s.lastSequence = sequence
	}
	s.streamingEnabled = streamingEnabled
}

func ReplyStreamingCardJSONEntityWithResp(ctx context.Context, cardData any, msgID, suffix string, replyInThread bool) (*larkim.ReplyMessageResp, string, error) {
	content, cardID, err := BuildCardJSONEntityContent(ctx, cardData)
	if err != nil {
		return nil, "", err
	}
	// if err := SetCardEntityStreaming(ctx, cardID, true, 1); err != nil {
	// 	return nil, "", err
	// }
	resp, err := ReplyMsgRawContentType(ctx, msgID, larkim.MsgTypeInteractive, content, suffix, replyInThread)
	if err != nil {
		return nil, "", err
	}
	return resp, cardID, nil
}

func BuildCardJSONEntityContent(ctx context.Context, cardData any) (string, string, error) {
	cardID, err := CreateCardJSONEntity(ctx, cardData)
	if err != nil {
		return "", "", err
	}
	return larkcard.NewCardEntityContent(cardID).String(), cardID, nil
}

func CreateCardJSONEntity(ctx context.Context, cardData any) (string, error) {
	raw, err := sonic.Marshal(cardData)
	if err != nil {
		return "", err
	}
	return createCardEntity(ctx, cardKitTypeCardJSON, string(raw))
}

func UpdateCardJSONEntity(ctx context.Context, cardID string, cardData any, sequence int) error {
	ctx, span := otel.Start(ctx)
	defer span.End()

	if cardID == "" {
		return errors.New("empty card id")
	}
	reqBodyBuilder := larkcardkit.NewUpdateCardReqBodyBuilder().
		Card(
			larkcardkit.NewCardBuilder().
				Type(cardKitTypeCardJSON).
				Data(utils.MustMarshalString(cardData)).
				Build(),
		).
		Uuid(streamingUUID("card-json-update", cardID, sequence))
	if sequence > 0 {
		reqBodyBuilder.Sequence(sequence)
	}
	resp, err := lark_dal.Client().Cardkit.V1.Card.Update(
		ctx,
		larkcardkit.NewUpdateCardReqBuilder().
			CardId(cardID).
			Body(reqBodyBuilder.Build()).
			Build(),
	)
	if err != nil {
		return err
	}
	if !resp.Success() {
		if resp.CodeError.Code == 300317 {
			return nil
		}
		return errors.New(resp.Error())
	}
	return nil
}

func repliedMessageID(resp *larkim.ReplyMessageResp) (string, error) {
	if resp != nil && resp.Data != nil && resp.Data.MessageId != nil && *resp.Data.MessageId != "" {
		return *resp.Data.MessageId, nil
	}
	return "", errors.New("reply card succeeded but message_id is empty")
}
