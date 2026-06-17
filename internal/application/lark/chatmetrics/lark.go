package chatmetrics

import (
	"context"
	"errors"
	"iter"
	"slices"
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
	for item := range ListChats(ctx) {
		chats = append(chats, Chat{
			ID:     strings.TrimSpace(ptrString(item.ChatId)),
			Name:   strings.TrimSpace(ptrString(item.Name)),
			Status: strings.TrimSpace(ptrString(item.ChatStatus)),
		})
	}
	span.SetAttributes(attribute.Int("chat.count", len(chats)))
	return chats, nil
}

// 封装一个list迭代器
func ListChats(ctx context.Context) iter.Seq[*larkim.ListChat] {
	return func(yield func(*larkim.ListChat) bool) {
		ctx, span := otel.Start(ctx)
		defer span.End()

		var (
			err  error
			resp *larkim.ListChatResp
		)
		defer func() { otel.RecordError(span, err) }()

		client := lark_dal.Client()
		if client == nil {
			return
		}

		resp, err = client.Im.V1.Chat.List(
			ctx,
			larkim.NewListChatReqBuilder().
				PageSize(20).
				UserIdType(larkim.ListChatUserIDTypeOpenId).
				Build(),
		)

		for {
			if err != nil {
				return
			}
			if !resp.Success() {
				err = errors.New(resp.Error())
				otel.RecordError(span, err)
				return
			}

			if resp.Data == nil {
				return
			}

			if slices.ContainsFunc(resp.Data.Items, func(item *larkim.ListChat) bool {
				return !yield(item)
			}) {
				return
			}

			if resp.Data.HasMore == nil || !*resp.Data.HasMore || resp.Data.PageToken == nil {
				return
			}

			resp, err = client.Im.V1.Chat.List(
				ctx,
				larkim.NewListChatReqBuilder().
					PageSize(20).
					PageToken(*resp.Data.PageToken).
					UserIdType(larkim.ListChatUserIDTypeOpenId).
					Build(),
			)
		}
	}
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
