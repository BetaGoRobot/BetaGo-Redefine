package luckinaction

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	appcardaction "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/cardaction"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/luckin"
	infraDB "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/mcpstore"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	cardactionproto "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
	"go.uber.org/zap"
)

func handleShopSelect(session luckin.SessionStore) appcardaction.SyncHandler {
	return func(ctx context.Context, actionCtx *appcardaction.Context) (*callback.CardActionTriggerResponse, error) {
		deptID, err := strconv.ParseInt(actionValue(actionCtx, cardactionproto.LuckinDeptIDField), 10, 64)
		if err != nil {
			return appcardaction.ErrorToast("门店信息无效"), nil
		}
		shop := luckin.ShopSelection{
			DeptID:    deptID,
			DeptName:  actionValue(actionCtx, cardactionproto.LuckinDeptNameField),
			Longitude: parseFloat(actionValue(actionCtx, cardactionproto.LuckinLongitudeField)),
			Latitude:  parseFloat(actionValue(actionCtx, cardactionproto.LuckinLatitudeField)),
		}
		if session != nil {
			session.SetShop(ctx, sessionKey(actionCtx), shop)
		}
		return appcardaction.InfoToastWithRawCardPayload("已选门店："+shop.DeptName, luckin.BuildProductQueryCard(shop)), nil
	}
}

func handleProductQuery(session luckin.SessionStore, draft luckin.DraftService, tokens luckin.CredentialStore) appcardaction.AsyncHandler {
	return func(ctx context.Context, actionCtx *appcardaction.Context) (appcardaction.AsyncTask, error) {
		if session == nil {
			return nil, errors.New("会话已过期，请重新选择门店")
		}
		shop, ok := session.GetShop(ctx, sessionKey(actionCtx))
		if !ok {
			return nil, errors.New("请先选择门店")
		}
		query := strings.TrimSpace(formValue(actionCtx, cardactionproto.LuckinQueryFormField))
		if query == "" {
			return nil, errors.New("请输入商品关键词")
		}
		msgID := strings.TrimSpace(actionCtx.MessageID())
		if msgID == "" {
			return nil, errors.New("message id is required")
		}
		req := credentialRequestFromAction(actionCtx)

		return func(runCtx context.Context) {
			// 先把卡片更新为“搜索中”，避免用户以为无响应。
			_ = larkmsg.PatchCardJSON(runCtx, msgID, luckin.BuildProductSearchingCard(shop, query))

			cred, err := resolveCredential(runCtx, tokens, req)
			if err != nil {
				_ = larkmsg.PatchCardJSON(runCtx, msgID, luckin.BuildBindTokenCard(req.ChatType))
				return
			}
			products, err := draft.SearchProducts(runCtx, cred, shop, query, 5)
			if err != nil {
				logs.L().Ctx(runCtx).Warn("luckin product search failed", zap.String("query", query), zap.Error(err))
				_ = larkmsg.PatchCardJSON(runCtx, msgID, luckin.BuildProductSearchErrorCard(shop, query))
				return
			}
			if err := larkmsg.PatchCardJSON(runCtx, msgID, luckin.BuildProductSelectCard(shop, products)); err != nil {
				logs.L().Ctx(runCtx).Warn("luckin patch product card failed", zap.String("message_id", msgID), zap.Error(err))
			}
		}, nil
	}
}

type pendingOrderCreator interface {
	CreatePendingOrder(context.Context, luckin.PendingOrder) error
}

func handleProductSelect(session luckin.SessionStore, draft luckin.DraftService, pending pendingOrderCreator, tokens luckin.CredentialStore) appcardaction.SyncHandler {
	return func(ctx context.Context, actionCtx *appcardaction.Context) (*callback.CardActionTriggerResponse, error) {
		if session == nil {
			return appcardaction.ErrorToast("会话已过期，请重新选择门店"), nil
		}
		shop, ok := session.GetShop(ctx, sessionKey(actionCtx))
		if !ok {
			return appcardaction.ErrorToast("请先选择门店"), nil
		}
		productID, err := strconv.ParseInt(actionValue(actionCtx, cardactionproto.LuckinProductIDField), 10, 64)
		if err != nil {
			return appcardaction.ErrorToast("商品信息无效"), nil
		}
		req := credentialRequestFromAction(actionCtx)
		cred, err := resolveCredential(ctx, tokens, req)
		if err != nil {
			return appcardaction.InfoToastWithRawCardPayload("请先绑定瑞幸账号", luckin.BuildBindTokenCard(req.ChatType)), nil
		}
		order, card, err := draft.Draft(ctx, luckin.DraftRequest{
			AppID:           req.AppID,
			BotOpenID:       req.BotOpenID,
			ChatID:          req.ChatID,
			RequesterOpenID: req.OpenID,
			Credential:      cred,
			Shop:            shop,
			Product: luckin.ProductOption{
				ProductID:   productID,
				SkuCode:     actionValue(actionCtx, cardactionproto.LuckinSkuCodeField),
				ProductName: actionValue(actionCtx, cardactionproto.LuckinProductName),
			},
			Amount: 1,
			Now:    time.Now(),
		})
		if err != nil {
			return appcardaction.ErrorToast(err.Error()), nil
		}
		if pending != nil {
			if err := pending.CreatePendingOrder(ctx, order); err != nil {
				return appcardaction.ErrorToast(err.Error()), nil
			}
		}
		return appcardaction.InfoToastWithRawCardPayload("已生成订单确认卡片", card), nil
	}
}

func handleBindToken(store luckin.CredentialWriter) appcardaction.SyncHandler {
	return func(ctx context.Context, actionCtx *appcardaction.Context) (*callback.CardActionTriggerResponse, error) {
		token := strings.TrimSpace(formValue(actionCtx, cardactionproto.LuckinTokenFormField))
		if token == "" {
			return appcardaction.ErrorToast("请输入瑞幸 Token"), nil
		}
		if store == nil {
			return appcardaction.ErrorToast("凭证存储未启用，无法绑定"), nil
		}
		req := credentialRequestFromAction(actionCtx)
		scope := resolveBindScope(actionCtx, req)
		lookup := luckin.CredentialLookup{
			Provider:  luckin.ProviderLuckin,
			AppID:     req.AppID,
			BotOpenID: req.BotOpenID,
			Scope:     scope,
		}
		if err := store.UpsertToken(ctx, lookup, token, req.OpenID); err != nil {
			return appcardaction.ErrorToast(err.Error()), nil
		}
		logs.L().Ctx(ctx).Info("luckin token bound",
			zap.String("scope", string(scope.Type)),
			zap.String("operator", req.OpenID),
			zap.String("token_hint", luckin.MaskToken(token)),
		)
		return appcardaction.InfoToast("已绑定瑞幸账号（" + luckin.ScopeLabel(scope) + "，" + luckin.MaskToken(token) + "）"), nil
	}
}

func handleUnbindToken(store luckin.CredentialWriter) appcardaction.SyncHandler {
	return func(ctx context.Context, actionCtx *appcardaction.Context) (*callback.CardActionTriggerResponse, error) {
		if store == nil {
			return appcardaction.ErrorToast("凭证存储未启用"), nil
		}
		req := credentialRequestFromAction(actionCtx)
		scope := resolveBindScope(actionCtx, req)
		lookup := luckin.CredentialLookup{
			Provider:  luckin.ProviderLuckin,
			AppID:     req.AppID,
			BotOpenID: req.BotOpenID,
			Scope:     scope,
		}
		ok, err := store.DeleteToken(ctx, lookup, req.OpenID)
		if err != nil {
			return appcardaction.ErrorToast(err.Error()), nil
		}
		if !ok {
			return appcardaction.InfoToast("没有可解绑的瑞幸账号"), nil
		}
		logs.L().Ctx(ctx).Info("luckin token unbound",
			zap.String("scope", string(scope.Type)),
			zap.String("operator", req.OpenID),
		)
		return appcardaction.InfoToast("已解绑瑞幸账号（" + luckin.ScopeLabel(scope) + "）"), nil
	}
}

func handleViewScope(resolver luckin.CredentialResolverFunc) appcardaction.SyncHandler {
	return func(ctx context.Context, actionCtx *appcardaction.Context) (*callback.CardActionTriggerResponse, error) {
		req := credentialRequestFromAction(actionCtx)
		cred, err := resolver(ctx, req)
		if err != nil {
			return appcardaction.InfoToastWithRawCardPayload("尚未绑定瑞幸账号", luckin.BuildBindTokenCard(req.ChatType)), nil
		}
		return appcardaction.InfoToast("当前使用：" + luckin.ScopeLabel(cred.Scope) + "（" + cred.TokenHint + "）"), nil
	}
}

func resolveBindScope(actionCtx *appcardaction.Context, req luckin.CredentialRequest) luckin.CredentialScope {
	if scope := strings.TrimSpace(formValue(actionCtx, cardactionproto.LuckinScopeFormField)); scope != "" {
		if luckin.ScopeType(scope) == luckin.ScopeChat {
			return luckin.CredentialScope{Type: luckin.ScopeChat, ID: req.ChatID}
		}
	}
	return luckin.CredentialScope{Type: luckin.ScopePersonal, ID: req.OpenID}
}

func credentialRequestFromAction(actionCtx *appcardaction.Context) luckin.CredentialRequest {
	identity := botidentity.Current()
	req := luckin.CredentialRequest{
		AppID:     identity.AppID,
		BotOpenID: identity.BotOpenID,
		ChatID:    actionCtx.ChatID(),
		OpenID:    actionCtx.OpenID(),
	}
	if isP2PChatID(req.ChatID) {
		req.ChatType = luckin.ChatTypePrivate
	} else {
		req.ChatType = luckin.ChatTypeGroup
	}
	return req
}

func isP2PChatID(chatID string) bool {
	// 飞书私聊会话 ID 通常以 oc_ 开头无法直接区分，采用群聊为默认；
	// 私聊场景下用户也可绑定个人 token（resolveBindScope 默认 personal）。
	return false
}

func sessionKey(actionCtx *appcardaction.Context) luckin.SessionKey {
	return luckin.NewSessionKey(credentialRequestFromAction(actionCtx))
}

func actionValue(actionCtx *appcardaction.Context, key string) string {
	if actionCtx == nil || actionCtx.Action == nil {
		return ""
	}
	v, _ := actionCtx.Action.String(key)
	return v
}

func formValue(actionCtx *appcardaction.Context, key string) string {
	if actionCtx == nil || actionCtx.Action == nil {
		return ""
	}
	if v, ok := actionCtx.Action.FormString(key); ok {
		return v
	}
	v, _ := actionCtx.Action.String(key)
	return v
}

func parseFloat(s string) float64 {
	f, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return f
}

func newSessionStore() luckin.SessionStore {
	return mcpstore.DefaultSessionStore()
}

func newCredentialWriter() luckin.CredentialWriter {
	db := infraDB.DB()
	if db == nil {
		return nil
	}
	key := luckinCredentialsKey()
	if key == "" {
		return nil
	}
	codec, err := mcpstore.NewTokenCodec(key)
	if err != nil {
		return nil
	}
	return mcpstore.NewCredentialRepository(db, codec)
}

func resolveCredential(ctx context.Context, tokens luckin.CredentialStore, req luckin.CredentialRequest) (luckin.Credential, error) {
	resolver := luckin.NewCredentialResolver(tokens, luckinSystemToken())
	return resolver.Resolve(ctx, req)
}
