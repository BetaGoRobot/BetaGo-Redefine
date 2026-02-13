package user

import (
	"context"
	"errors"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/cache"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo/utility/otel"
	"github.com/BetaGoRobot/go_utils/reflecting"
	larkcontact "github.com/larksuite/oapi-sdk-go/v3/service/contact/v3"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"go.uber.org/zap"
)

func GetUserInfo(ctx context.Context, userID string) (user *larkcontact.User, err error) {
	ctx, span := otel.LarkRobotOtelTracer.Start(ctx, reflecting.GetCurrentFunc())
	defer span.End()
	defer func() { span.RecordError(err) }()
	resp, err := lark_dal.Client().Contact.V3.User.Get(ctx, larkcontact.NewGetUserReqBuilder().UserId(userID).Build())
	if err != nil {
		return
	}
	if !resp.Success() {
		err = errors.New(resp.Error())
		return
	}
	return resp.Data.User, nil
}

func GetUserInfoCache(ctx context.Context, chatID, userID string) (user *larkcontact.User, err error) {
	ctx, span := otel.LarkRobotOtelTracer.Start(ctx, reflecting.GetCurrentFunc())
	defer span.End()
	defer func() { span.RecordError(err) }()
	res, err := cache.GetOrExecute(ctx, userID, func() (*larkcontact.User, error) {
		return GetUserInfo(ctx, userID)
	})
	logs.L().Ctx(ctx).Info("GetUserInfoCache", zap.Any("user", res))
	// userInfo失败了，走群聊试试
	groupMember, err := GetUserMemberFromChat(ctx, chatID, userID)
	if err != nil {
		logs.L().Ctx(ctx).Error("GetUserMemberFromChat", zap.Any("user", groupMember))
		return
	}
	if groupMember == nil {
		err = errors.New("user not found in chat")
		return
	}
	res = &larkcontact.User{
		UserId: groupMember.MemberId,
		OpenId: &userID,
		Name:   groupMember.Name,
	}
	return res, err
}

func GetUserMemberFromChat(ctx context.Context, chatID, openID string) (member *larkim.ListMember, err error) {
	ctx, span := otel.LarkRobotOtelTracer.Start(ctx, reflecting.GetCurrentFunc())
	defer span.End()
	defer func() { span.RecordError(err) }()

	memberMap, err := GetUserMapFromChatIDCache(ctx, chatID)
	if err != nil {
		logs.L().Ctx(ctx).Error("GetUserMapFromChatIDCache error", zap.Error(err))
		return
	}
	return memberMap[openID], err
}

func GetUserMapFromChatIDCache(ctx context.Context, chatID string) (memberMap map[string]*larkim.ListMember, err error) {
	return cache.GetOrExecute(ctx, chatID, func() (map[string]*larkim.ListMember, error) {
		return GetUserMapFromChatID(ctx, chatID)
	})
}

func GetUserMapFromChatID(ctx context.Context, chatID string) (memberMap map[string]*larkim.ListMember, err error) {
	ctx, span := otel.LarkRobotOtelTracer.Start(ctx, reflecting.GetCurrentFunc())
	defer span.End()
	defer func() { span.RecordError(err) }()

	memberMap = make(map[string]*larkim.ListMember)
	hasMore := true
	pageToken := ""
	for hasMore {
		builder := larkim.
			NewGetChatMembersReqBuilder().
			MemberIdType(`open_id`).
			ChatId(chatID).
			PageSize(100)
		if pageToken != "" {
			builder.PageToken(pageToken)
		}
		resp, err := lark_dal.Client().Im.ChatMembers.Get(ctx, builder.Build())
		if err != nil {
			return memberMap, err
		}
		if !resp.Success() {
			err = errors.New(resp.Error())
			return memberMap, err
		}
		for _, item := range resp.Data.Items {
			memberMap[*item.MemberId] = item
		}
		hasMore = *resp.Data.HasMore
		pageToken = *resp.Data.PageToken
	}
	return
}
