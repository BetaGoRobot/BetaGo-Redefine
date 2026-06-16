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
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkimg"
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

func handleProductQuery(session luckin.SessionStore, draft luckin.DraftService, tokens luckin.CredentialStore, images luckin.ImageUploader) appcardaction.AsyncHandler {
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
			imageKeys := luckin.UploadProductImages(runCtx, images, products)
			if err := larkmsg.PatchCardJSON(runCtx, msgID, luckin.BuildProductSelectCard(shop, products, imageKeys)); err != nil {
				logs.L().Ctx(runCtx).Warn("luckin patch product card failed", zap.String("message_id", msgID), zap.Error(err))
			}
		}, nil
	}
}

type pendingOrderCreator interface {
	CreatePendingOrder(context.Context, luckin.PendingOrder) error
}

// handleProductSelect 异步处理：有规格则先弹规格卡，否则直接生成订单确认卡。
func handleProductSelect(session luckin.SessionStore, draft luckin.DraftService, pending pendingOrderCreator, tokens luckin.CredentialStore, images luckin.ImageUploader, orders luckin.OrderTracker) appcardaction.AsyncHandler {
	return func(ctx context.Context, actionCtx *appcardaction.Context) (appcardaction.AsyncTask, error) {
		if session == nil {
			return nil, errors.New("会话已过期，请重新选择门店")
		}
		shop, ok := session.GetShop(ctx, sessionKey(actionCtx))
		if !ok {
			return nil, errors.New("请先选择门店")
		}
		productID, err := strconv.ParseInt(actionValue(actionCtx, cardactionproto.LuckinProductIDField), 10, 64)
		if err != nil {
			return nil, errors.New("商品信息无效")
		}
		msgID := strings.TrimSpace(actionCtx.MessageID())
		if msgID == "" {
			return nil, errors.New("message id is required")
		}
		skuCode := actionValue(actionCtx, cardactionproto.LuckinSkuCodeField)
		productName := actionValue(actionCtx, cardactionproto.LuckinProductName)
		specSelections := luckin.ParseSpecSelection(formValuesWithPrefix(actionCtx, cardactionproto.LuckinSpecFormFieldPrefix))
		fromSpecForm := len(specSelections) > 0
		coupons := parseCoupons(formValue(actionCtx, cardactionproto.LuckinCouponFormField))
		req := credentialRequestFromAction(actionCtx)

		return func(runCtx context.Context) {
			cred, err := resolveCredential(runCtx, tokens, req)
			if err != nil {
				_ = larkmsg.PatchCardJSON(runCtx, msgID, luckin.BuildBindTokenCard(req.ChatType))
				return
			}

			// 第一次点选商品且未走过规格表单时，若商品有规格，先弹规格选择卡。
			if !fromSpecForm {
				detail, derr := draft.ProductDetail(runCtx, cred, shop, productID)
				if derr == nil && detail.HasSpecs() {
					imgKey := ""
					if images != nil && detail.PictureURL != "" {
						imgKey = images.UploadByURL(runCtx, detail.PictureURL)
					}
					_ = larkmsg.PatchCardJSON(runCtx, msgID, luckin.BuildSpecSelectCard(shop, detail, imgKey))
					return
				}
			} else {
				// 规格表单提交：切换规格拿到最新 sku/价格。
				if detail, serr := draft.SwitchSpec(runCtx, cred, shop, productID, skuCode, specSelections); serr == nil && detail.SkuCode != "" {
					skuCode = detail.SkuCode
				}
			}

			order, card, err := draft.Draft(runCtx, luckin.DraftRequest{
				AppID:           req.AppID,
				BotOpenID:       req.BotOpenID,
				ChatID:          req.ChatID,
				RequesterOpenID: req.OpenID,
				Credential:      cred,
				Shop:            shop,
				Product: luckin.ProductOption{
					ProductID:   productID,
					SkuCode:     skuCode,
					ProductName: productName,
				},
				Amount:         1,
				CouponCodeList: coupons,
				Now:            time.Now(),
			})
			if err != nil {
				_ = larkmsg.PatchCardJSON(runCtx, msgID, luckin.BuildProductSearchErrorCard(shop, productName))
				return
			}
			if pending != nil {
				if err := pending.CreatePendingOrder(runCtx, order); err != nil {
					logs.L().Ctx(runCtx).Warn("luckin create pending order failed", zap.Error(err))
					return
				}
			}
			_ = larkmsg.PatchCardJSON(runCtx, msgID, card)
		}, nil
	}
}

// handleOrderStatus 实时查询订单状态并刷新卡片。
func handleOrderStatus(tokens luckin.CredentialStore, draft luckin.DraftService) appcardaction.AsyncHandler {
	return func(ctx context.Context, actionCtx *appcardaction.Context) (appcardaction.AsyncTask, error) {
		orderID := strings.TrimSpace(actionValue(actionCtx, cardactionproto.LuckinOrderIDField))
		if orderID == "" {
			return nil, errors.New("订单号缺失")
		}
		msgID := strings.TrimSpace(actionCtx.MessageID())
		req := credentialRequestFromAction(actionCtx)
		return func(runCtx context.Context) {
			cred, err := resolveCredential(runCtx, tokens, req)
			if err != nil {
				return
			}
			detail, err := draft.OrderDetail(runCtx, cred, orderID)
			if err != nil {
				logs.L().Ctx(runCtx).Warn("luckin query order detail failed", zap.String("order_id", orderID), zap.Error(err))
				return
			}
			if msgID != "" {
				_ = larkmsg.PatchCardJSON(runCtx, msgID, luckin.BuildOrderStatusCard(detail))
			}
		}, nil
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
	// 仅支持个人作用域：优惠券归属与隐私要求，不再支持群聊默认/系统默认。
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

// formValuesWithPrefix 收集表单中以指定前缀开头的字段值（用于规格选择）。
func formValuesWithPrefix(actionCtx *appcardaction.Context, prefix string) map[string]string {
	out := make(map[string]string)
	if actionCtx == nil || actionCtx.Action == nil {
		return out
	}
	for k, v := range actionCtx.Action.FormValue {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}

func parseFloat(s string) float64 {
	f, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return f
}

// parseCoupons 把逗号分隔的优惠券输入解析为列表（支持中英文逗号）。
func parseCoupons(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	raw = strings.ReplaceAll(raw, "，", ",")
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
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
	resolver := luckin.NewCredentialResolver(tokens, "")
	return resolver.Resolve(ctx, req)
}

func newImageUploader() luckin.ImageUploader {
	return luckin.NewCachedImageUploader(larkimg.UploadPicture2Lark)
}

func newOrderTracker() luckin.OrderTracker {
	db := infraDB.DB()
	if db == nil {
		return nil
	}
	return mcpstore.NewOrderRepository(db)
}

func luckinOrderPollConfig() luckin.OrderPollConfig {
	cfg := luckinOrderConfig()
	pollCfg := luckin.DefaultOrderPollConfig()
	if cfg == nil {
		return pollCfg
	}
	if cfg.OrderPollIntervalSeconds > 0 {
		pollCfg.PollInterval = time.Duration(cfg.OrderPollIntervalSeconds) * time.Second
	}
	if cfg.OrderPollMaxSeconds > 0 {
		pollCfg.PollMax = time.Duration(cfg.OrderPollMaxSeconds) * time.Second
	}
	if cfg.UnpaidTimeoutSeconds > 0 {
		pollCfg.UnpaidTimeout = time.Duration(cfg.UnpaidTimeoutSeconds) * time.Second
	}
	if cfg.UnpaidRemindThresholdSeconds > 0 {
		pollCfg.UnpaidRemindAt = time.Duration(cfg.UnpaidRemindThresholdSeconds) * time.Second
	}
	return pollCfg
}
