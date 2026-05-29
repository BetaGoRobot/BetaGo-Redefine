package chatmetrics

import (
	"context"
	"errors"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

func ListBotChats(ctx context.Context) (chats []Chat, err error) {
	ctx, span := otel.Start(ctx)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	client := lark_dal.Client()
	if client == nil {
		return nil, errors.New("lark client unavailable")
	}
	iterator, err := client.Im.V1.Chat.ListByIterator(
		ctx,
		larkim.NewListChatReqBuilder().
			PageSize(100).
			UserIdType(larkim.ListChatUserIDTypeOpenId).
			Build(),
	)
	if err != nil {
		return nil, err
	}

	for {
		hasNext, item, nextErr := iterator.Next()
		if nextErr != nil {
			return chats, nextErr
		}
		if !hasNext {
			break
		}
		chatID := strings.TrimSpace(ptrString(item.ChatId))
		if chatID == "" {
			continue
		}
		chats = append(chats, Chat{
			ID:     chatID,
			Name:   strings.TrimSpace(ptrString(item.Name)),
			Status: strings.TrimSpace(ptrString(item.ChatStatus)),
		})
	}
	span.SetAttributes(attribute.Int("chat.count", len(chats)))
	return chats, nil
}

func CountChatMembers(ctx context.Context, chatID string) (int, error) {
	ctx, span := otel.Start(ctx, trace.WithAttributes(attribute.String("chat.id", chatID)))
	defer span.End()

	client := lark_dal.Client()
	if client == nil {
		err := errors.New("lark client unavailable")
		otel.RecordError(span, err)
		return 0, err
	}

	req := larkim.NewGetChatMembersReqBuilder().
		ChatId(chatID).
		MemberIdType(larkim.ListMemberMemberIDTypeOpenId).
		PageSize(1).
		Build()
	resp, err := client.Im.ChatMembers.Get(ctx, req)
	if err != nil {
		otel.RecordError(span, err)
		return 0, err
	}
	if !resp.Success() {
		err = errors.New(resp.Error())
		otel.RecordError(span, err)
		return 0, err
	}
	if resp.Data == nil {
		return 0, nil
	}
	if resp.Data.MemberTotal == nil {
		return len(resp.Data.Items), nil
	}
	span.SetAttributes(attribute.Int("member.count", *resp.Data.MemberTotal))
	return *resp.Data.MemberTotal, nil
}

func ptrString(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
