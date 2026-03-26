package larkuser

import (
	"context"
	"errors"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/cache"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/bytedance/sonic"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkcontact "github.com/larksuite/oapi-sdk-go/v3/service/contact/v3"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

const basicUserPath = "/open-apis/contact/v3/users/basic_batch"

type basicUserInfoReq struct {
	UserIDs []string `json:"user_ids"`
}

type basicUserInfoResp struct {
	larkcore.CodeError
	Data struct {
		Users []*basicUserInfo `json:"users,omitempty"`
	} `json:"data,omitempty"`
}

type basicUserInfo struct {
	I18nName *larkcontact.ProductI18nName `json:"i18n_name,omitempty"`
	Name     string                       `json:"name,omitempty"`
	UserID   string                       `json:"user_id,omitempty"`
}

func userInfoCacheKey(openID string) string {
	return botidentity.Current().NamespaceKey("lark_user_info", openID)
}

func chatMemberCacheKey(chatID string) string {
	return botidentity.Current().NamespaceKey("lark_chat_members", chatID)
}

func GetUserInfo(ctx context.Context, openID string) (user *larkcontact.User, err error) {
	ctx, span := otel.Start(ctx, trace.WithAttributes(attribute.String("user.open_id", otel.PreviewString(openID, 128))))
	defer span.End()
	defer func() { otel.RecordError(span, err) }()
	resp, err := lark_dal.Client().Contact.V3.User.Get(ctx, larkcontact.NewGetUserReqBuilder().
		UserId(openID).
		UserIdType("open_id").
		Build(),
	)
	if err != nil {
		return
	}
	if !resp.Success() {
		err = errors.New(resp.Error())
		return
	}
	return resp.Data.User, nil
}

func GetUserInfoCache(ctx context.Context, chatID, openID string) (user *larkcontact.User, err error) {
	ctx, span := otel.Start(ctx, trace.WithAttributes(
		attribute.String("chat.id", chatID),
		attribute.String("user.open_id", otel.PreviewString(openID, 128)),
	))
	defer span.End()
	defer func() { otel.RecordError(span, err) }()
	res, err := cache.GetOrExecute(ctx, userInfoCacheKey(openID), func() (*larkcontact.User, error) {
		return GetUserInfo(ctx, openID)
	})
	logs.L().Ctx(ctx).Debug("GetUserInfoCache", zap.Any("user", res))
	if err == nil && res != nil {
		return res, nil
	}
	// userInfo失败了，走群聊试试
	groupMember, err := GetUserMemberFromChat(ctx, chatID, openID)
	if err != nil {
		logs.L().Ctx(ctx).Error("GetUserMemberFromChat", zap.Any("user", groupMember), zap.Error(err))
		return
	}
	if groupMember == nil {
		err = errors.New("user not found in chat")
		return
	}
	res = &larkcontact.User{
		UserId: groupMember.MemberId,
		OpenId: &openID,
		Name:   groupMember.Name,
	}
	return res, err
}

func GetUserNameCache(ctx context.Context, chatID, openID string) (userName string, err error) {
	ctx, span := otel.Start(ctx, trace.WithAttributes(
		attribute.String("chat.id", chatID),
		attribute.String("user.open_id", otel.PreviewString(openID, 128)),
	))
	defer span.End()
	defer func() { otel.RecordError(span, err) }()
	res, err := cache.GetOrExecute(ctx, userInfoCacheKey(openID), func() (string, error) {
		m, err := GetUserName(ctx, chatID, openID)
		if err != nil {
			return "", err
		}
		return m[openID], nil
	})
	if err == nil {
		return res, nil
	}
	// 否则走群聊试试
	groupMember, err := GetUserMemberFromChat(ctx, chatID, openID)
	if err != nil {
		logs.L().Ctx(ctx).Error("GetUserMemberFromChat", zap.Any("user", groupMember), zap.Error(err))
		return
	}
	if groupMember == nil {
		err = errors.New("user not found in chat")
		return
	}
	res = *groupMember.Name
	return
}

func GetUserName(ctx context.Context, chatID string, openID ...string) (userNames map[string]string, err error) {
	userNames = make(map[string]string)
	req := &basicUserInfoReq{UserIDs: openID}
	apiResp, err := lark_dal.Client().Post(ctx, basicUserPath, req, larkcore.AccessTokenTypeTenant)
	if err != nil {
		logs.L().Ctx(ctx).Error("GetUserNameCahce", zap.Any("user", apiResp), zap.Error(err))
		return
	}
	if apiResp == nil {
		logs.L().Ctx(ctx).Error("GetUserNameCahce", zap.Any("user", apiResp), zap.Error(err))
		return
	}
	resp := &basicUserInfoResp{}
	if err = sonic.Unmarshal(apiResp.RawBody, resp); err != nil {
		logs.L().Ctx(ctx).Error("GetUserNameCahce", zap.Any("user", apiResp), zap.Error(err))
		return nil, err
	}
	for _, item := range resp.Data.Users {
		userNames[item.UserID] = item.Name
	}
	return
}

func GetUserMemberFromChat(ctx context.Context, chatID, openID string) (member *larkim.ListMember, err error) {
	ctx, span := otel.Start(ctx, trace.WithAttributes(
		attribute.String("chat.id", chatID),
		attribute.String("user.open_id", otel.PreviewString(openID, 128)),
	))
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	memberMap, err := GetUserMapFromChatIDCache(ctx, chatID)
	if err != nil {
		logs.L().Ctx(ctx).Error("GetUserMapFromChatIDCache error", zap.String("chatID", chatID), zap.Error(err))
		return
	}
	return memberMap[openID], err
}

func GetUserMapFromChatIDCache(ctx context.Context, chatID string) (memberMap map[string]*larkim.ListMember, err error) {
	return cache.GetOrExecute(ctx, chatMemberCacheKey(chatID), func() (map[string]*larkim.ListMember, error) {
		return GetUserMapFromChatID(ctx, chatID)
	})
}

func GetUserMapFromChatID(ctx context.Context, chatID string) (memberMap map[string]*larkim.ListMember, err error) {
	ctx, span := otel.Start(ctx, trace.WithAttributes(attribute.String("chat.id", chatID)))
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

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
	span.SetAttributes(attribute.Int("member.count", len(memberMap)))
	return
}
