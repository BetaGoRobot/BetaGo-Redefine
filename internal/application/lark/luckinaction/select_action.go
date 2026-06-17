package luckinaction

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	appcardaction "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/cardaction"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/luckin"
	infraDB "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkimg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/mcpstore"
	cardactionproto "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
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
			Address:   actionValue(actionCtx, cardactionproto.LuckinLocationFormField),
			Longitude: parseFloat(actionValue(actionCtx, cardactionproto.LuckinLongitudeField)),
			Latitude:  parseFloat(actionValue(actionCtx, cardactionproto.LuckinLatitudeField)),
		}
		if session != nil {
			key := sessionKey(actionCtx)
			session.SetShop(ctx, key, shop)
			// 切换门店后清空购物车，避免跨店混单。
			session.ClearCart(ctx, key)
		}
		return appcardaction.InfoToastWithRawCardPayload("已选门店："+shop.DeptName, luckin.BuildProductQueryCard(shop, luckin.Cart{})), nil
	}
}

// handleRegionSelect 刷新同一张门店搜索卡：第一步选省份，第二步展示该省份下的城市/区县。
func handleRegionSelect(session luckin.SessionStore) appcardaction.SyncHandler {
	return func(ctx context.Context, actionCtx *appcardaction.Context) (*callback.CardActionTriggerResponse, error) {
		province := strings.TrimSpace(formValue(actionCtx, cardactionproto.LuckinProvinceFormField))
		if province == "" {
			return appcardaction.ErrorToast("请选择省份"), nil
		}
		req := credentialRequestFromAction(actionCtx)
		return appcardaction.InfoToastWithRawCardPayload("已选择："+province, luckin.BuildRegionSelectCard(province, recentShops(ctx, session, req))), nil
	}
}

func handleProductQuery(session luckin.SessionStore, draft luckin.DraftService, tokens luckin.CredentialStore, images luckin.ImageUploader) appcardaction.AsyncHandler {
	return func(ctx context.Context, actionCtx *appcardaction.Context) (appcardaction.AsyncTask, error) {
		msgID := strings.TrimSpace(actionCtx.MessageID())
		if msgID == "" {
			return nil, errors.New("message id is required")
		}
		shop, ok := lookupShop(ctx, session, actionCtx)
		if !ok {
			return patchSessionMissing(session, actionCtx, msgID), nil
		}
		cart, _ := session.GetCart(ctx, sessionKey(actionCtx))
		query := strings.TrimSpace(formValue(actionCtx, cardactionproto.LuckinQueryFormField))
		if query == "" {
			return nil, errors.New("请输入商品关键词")
		}
		req := credentialRequestFromAction(actionCtx)

		return func(runCtx context.Context) {
			// 先把卡片更新为“搜索中”，避免用户以为无响应。
			_ = larkmsg.PatchCardJSON(runCtx, msgID, luckin.BuildProductSearchingCard(shop, cart, query))

			cred, err := resolveCredential(runCtx, tokens, req)
			if err != nil {
				sendBindGuide(runCtx, req)
				_ = larkmsg.PatchCardJSON(runCtx, msgID, luckin.BuildProductQueryCard(shop, cart))
				return
			}
			products, err := draft.SearchProducts(runCtx, cred, shop, query, 5)
			if err != nil {
				logs.L().Ctx(runCtx).Warn("luckin product search failed", zap.String("query", query), zap.Error(err))
				_ = larkmsg.PatchCardJSON(runCtx, msgID, luckin.BuildProductSearchErrorCard(shop, cart, query))
				return
			}
			imageKeys := luckin.UploadProductImages(runCtx, images, products)
			if err := larkmsg.PatchCardJSON(runCtx, msgID, luckin.BuildProductSelectCard(shop, cart, products, imageKeys)); err != nil {
				logs.L().Ctx(runCtx).Warn("luckin patch product card failed", zap.String("message_id", msgID), zap.Error(err))
			}
		}, nil
	}
}

// handleShopSearch 卡片内按位置文本搜索门店（用于会话过期重选 / 换位置重搜），
// 经纬度通过 geocode 解析，结果异步刷新到门店选择卡。
func handleShopSearch(session luckin.SessionStore, draft luckin.DraftService, geocoder luckin.Geocoder, tokens luckin.CredentialStore) appcardaction.AsyncHandler {
	return func(ctx context.Context, actionCtx *appcardaction.Context) (appcardaction.AsyncTask, error) {
		msgID := strings.TrimSpace(actionCtx.MessageID())
		if msgID == "" {
			return nil, errors.New("message id is required")
		}
		location := strings.TrimSpace(strings.Join([]string{
			formValue(actionCtx, cardactionproto.LuckinRegionFormField),
			formValue(actionCtx, cardactionproto.LuckinLocationFormField),
		}, " "))
		location = luckin.NormalizeLocationText(location)
		if location == "" {
			return nil, errors.New("请选择城市/区县，或输入位置关键词")
		}
		req := credentialRequestFromAction(actionCtx)
		return func(runCtx context.Context) {
			_ = larkmsg.PatchCardJSON(runCtx, msgID, luckin.BuildShopSearchingCard(location))
			cred, err := resolveCredential(runCtx, tokens, req)
			if err != nil {
				sendBindGuide(runCtx, req)
				_ = larkmsg.PatchCardJSON(runCtx, msgID, luckin.BuildSessionExpiredCardWithRecent(recentShops(runCtx, session, req)))
				return
			}
			if geocoder == nil {
				_ = larkmsg.PatchCardJSON(runCtx, msgID, luckin.BuildShopSelectCard(location, nil))
				return
			}
			point, err := geocoder.Geocode(runCtx, location)
			if err != nil {
				logs.L().Ctx(runCtx).Warn("luckin geocode failed", zap.String("location", location), zap.Error(err))
				_ = larkmsg.PatchCardJSON(runCtx, msgID, luckin.BuildShopSelectCard(location, nil))
				return
			}
			shops, err := draft.SearchShops(runCtx, cred, luckin.GeoPoint{Longitude: point.Longitude, Latitude: point.Latitude}, "", 5)
			if err != nil {
				logs.L().Ctx(runCtx).Warn("luckin shop search failed", zap.String("location", location), zap.Error(err))
				_ = larkmsg.PatchCardJSON(runCtx, msgID, luckin.BuildShopSelectCard(location, nil))
				return
			}
			_ = larkmsg.PatchCardJSON(runCtx, msgID, luckin.BuildShopSelectCard(location, shops))
		}, nil
	}
}

type pendingOrderCreator interface {
	CreatePendingOrder(context.Context, luckin.PendingOrder) error
}

// handleProductSelect 异步处理：有规格则先弹规格卡，否则把商品加入购物车并刷新购物车卡。
func handleProductSelect(session luckin.SessionStore, draft luckin.DraftService, tokens luckin.CredentialStore, images luckin.ImageUploader) appcardaction.AsyncHandler {
	return func(ctx context.Context, actionCtx *appcardaction.Context) (appcardaction.AsyncTask, error) {
		msgID := strings.TrimSpace(actionCtx.MessageID())
		if msgID == "" {
			return nil, errors.New("message id is required")
		}
		shop, ok := lookupShop(ctx, session, actionCtx)
		if !ok {
			return patchSessionMissing(session, actionCtx, msgID), nil
		}
		productID, err := strconv.ParseInt(actionValue(actionCtx, cardactionproto.LuckinProductIDField), 10, 64)
		if err != nil {
			return nil, errors.New("商品信息无效")
		}
		skuCode := actionValue(actionCtx, cardactionproto.LuckinSkuCodeField)
		productName := actionValue(actionCtx, cardactionproto.LuckinProductName)
		unitPrice := parseFloat(actionValue(actionCtx, cardactionproto.LuckinUnitPriceField))
		imageKey := actionValue(actionCtx, cardactionproto.LuckinImageKeyField)
		customize := actionValue(actionCtx, cardactionproto.LuckinCustomizeField) == "1"
		// 商品卡每行 qty 字段名带 productID 后缀以避免同卡重名；规格卡用无后缀字段。
		qtyRaw := formValue(actionCtx, luckin.QtyFormField(productID))
		if qtyRaw == "" {
			qtyRaw = formValue(actionCtx, cardactionproto.LuckinQtyFormField)
		}
		amount := luckin.ClampAmount(parseAmount(qtyRaw))
		specSelections := luckin.ParseSpecSelection(formValuesWithPrefix(actionCtx, cardactionproto.LuckinSpecFormFieldPrefix))
		fromSpecForm := len(specSelections) > 0
		key := sessionKey(actionCtx)
		req := credentialRequestFromAction(actionCtx)

		return func(runCtx context.Context) {
			cred, err := resolveCredential(runCtx, tokens, req)
			if err != nil {
				sendBindGuide(runCtx, req)
				_ = larkmsg.PatchCardJSON(runCtx, msgID, luckin.BuildCartCard(shop, mustCart(session, runCtx, key)))
				return
			}

			// 第一次点选商品且未走过规格表单时，若商品有规格，先弹规格选择卡。
			if !fromSpecForm && customize {
				detail, derr := draft.ProductDetail(runCtx, cred, shop, productID)
				if derr == nil && detail.HasSpecs() {
					imgKey := ""
					if images != nil && detail.PictureURL != "" {
						imgKey = images.UploadByURL(runCtx, detail.PictureURL)
					}
					_ = larkmsg.PatchCardJSON(runCtx, msgID, luckin.BuildSpecSelectCard(shop, detail, imgKey, amount))
					return
				}
			} else {
				// 规格表单提交：切换规格拿到最新 sku/价格。
				if detail, serr := draft.SwitchSpec(runCtx, cred, shop, productID, skuCode, specSelections); serr == nil && detail.SkuCode != "" {
					skuCode = detail.SkuCode
					if detail.Price > 0 {
						unitPrice = detail.Price
					}
					if detail.ProductName != "" {
						productName = detail.ProductName
					}
					if images != nil && detail.PictureURL != "" {
						if key := images.UploadByURL(runCtx, detail.PictureURL); key != "" {
							imageKey = key
						}
					}
				}
			}

			cart, _ := session.GetCart(runCtx, key)
			cart.Add(luckin.CartItem{
				ProductID:   productID,
				SkuCode:     skuCode,
				ProductName: productName,
				Amount:      amount,
				UnitPrice:   unitPrice,
				ImageKey:    imageKey,
			})
			session.SetCart(runCtx, key, cart)
			_ = larkmsg.PatchCardJSON(runCtx, msgID, luckin.BuildCartCard(shop, cart))
		}, nil
	}
}

// handleCartUpdate 调整购物车某条目数量（含删除）。
func handleCartUpdate(session luckin.SessionStore) appcardaction.AsyncHandler {
	return func(ctx context.Context, actionCtx *appcardaction.Context) (appcardaction.AsyncTask, error) {
		msgID := strings.TrimSpace(actionCtx.MessageID())
		if msgID == "" {
			return nil, errors.New("message id is required")
		}
		shop, ok := lookupShop(ctx, session, actionCtx)
		if !ok {
			return patchSessionMissing(session, actionCtx, msgID), nil
		}
		productID, err := strconv.ParseInt(actionValue(actionCtx, cardactionproto.LuckinProductIDField), 10, 64)
		if err != nil {
			return nil, errors.New("商品信息无效")
		}
		skuCode := actionValue(actionCtx, cardactionproto.LuckinSkuCodeField)
		qty := parseAmount(actionValue(actionCtx, cardactionproto.LuckinQtyFormField))
		key := sessionKey(actionCtx)
		return func(runCtx context.Context) {
			cart, _ := session.GetCart(runCtx, key)
			cart.SetAmount(productID, skuCode, qty)
			session.SetCart(runCtx, key, cart)
			_ = larkmsg.PatchCardJSON(runCtx, msgID, luckin.BuildCartCard(shop, cart))
		}, nil
	}
}

// handleCartRemove 删除购物车某条目。
func handleCartRemove(session luckin.SessionStore) appcardaction.AsyncHandler {
	return func(ctx context.Context, actionCtx *appcardaction.Context) (appcardaction.AsyncTask, error) {
		msgID := strings.TrimSpace(actionCtx.MessageID())
		if msgID == "" {
			return nil, errors.New("message id is required")
		}
		shop, ok := lookupShop(ctx, session, actionCtx)
		if !ok {
			return patchSessionMissing(session, actionCtx, msgID), nil
		}
		productID, err := strconv.ParseInt(actionValue(actionCtx, cardactionproto.LuckinProductIDField), 10, 64)
		if err != nil {
			return nil, errors.New("商品信息无效")
		}
		skuCode := actionValue(actionCtx, cardactionproto.LuckinSkuCodeField)
		key := sessionKey(actionCtx)
		return func(runCtx context.Context) {
			cart, _ := session.GetCart(runCtx, key)
			cart.Remove(productID, skuCode)
			session.SetCart(runCtx, key, cart)
			_ = larkmsg.PatchCardJSON(runCtx, msgID, luckin.BuildCartCard(shop, cart))
		}, nil
	}
}

// handleCartCheckout 预览购物车并生成确认订单卡片。
func handleCartCheckout(session luckin.SessionStore, draft luckin.DraftService, pending pendingOrderCreator, tokens luckin.CredentialStore) appcardaction.AsyncHandler {
	return checkoutTask(session, draft, pending, tokens, nil)
}

// handleCouponApply 用户在确认卡上选中优惠券后重新预览，刷新价格与确认卡。
func handleCouponApply(session luckin.SessionStore, draft luckin.DraftService, pending pendingOrderCreator, tokens luckin.CredentialStore) appcardaction.AsyncHandler {
	return func(ctx context.Context, actionCtx *appcardaction.Context) (appcardaction.AsyncTask, error) {
		coupons := selectedCoupons(actionCtx)
		return checkoutTask(session, draft, pending, tokens, coupons)(ctx, actionCtx)
	}
}

func checkoutTask(session luckin.SessionStore, draft luckin.DraftService, pending pendingOrderCreator, tokens luckin.CredentialStore, coupons []string) appcardaction.AsyncHandler {
	return func(ctx context.Context, actionCtx *appcardaction.Context) (appcardaction.AsyncTask, error) {
		msgID := strings.TrimSpace(actionCtx.MessageID())
		if msgID == "" {
			return nil, errors.New("message id is required")
		}
		shop, ok := lookupShop(ctx, session, actionCtx)
		if !ok {
			return patchSessionMissing(session, actionCtx, msgID), nil
		}
		cart, ok := session.GetCart(ctx, sessionKey(actionCtx))
		if !ok || cart.Empty() {
			return patchSessionMissing(session, actionCtx, msgID), nil
		}
		req := credentialRequestFromAction(actionCtx)
		return func(runCtx context.Context) {
			_ = larkmsg.PatchCardJSON(runCtx, msgID, luckin.BuildCartCheckoutProcessingCard(shop))
			cred, err := resolveCredential(runCtx, tokens, req)
			if err != nil {
				sendBindGuide(runCtx, req)
				_ = larkmsg.PatchCardJSON(runCtx, msgID, luckin.BuildCartCard(shop, cart))
				return
			}
			order, card, err := draft.Draft(runCtx, luckin.DraftRequest{
				AppID:           req.AppID,
				BotOpenID:       req.BotOpenID,
				ChatID:          req.ChatID,
				RequesterOpenID: req.OpenID,
				Credential:      cred,
				Shop:            shop,
				Items:           cart.Items,
				CouponCodeList:  coupons,
				Now:             time.Now(),
			})
			if err != nil {
				_ = larkmsg.PatchCardJSON(runCtx, msgID, luckin.BuildOrderFailedCard("预览订单失败："+err.Error()))
				return
			}
			if pending != nil {
				if err := pending.CreatePendingOrder(runCtx, order); err != nil {
					logs.L().Ctx(runCtx).Warn("luckin create pending order failed", zap.Error(err))
					_ = larkmsg.PatchCardJSON(runCtx, msgID, luckin.BuildOrderFailedCard("创建待确认订单失败："+err.Error()))
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
		mode := strings.TrimSpace(actionValue(actionCtx, cardactionproto.LuckinStatusModeField))
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
			if mode == luckin.OrderStatusModeReply && msgID != "" {
				if err := larkmsg.ReplyCardJSON(runCtx, msgID, luckin.BuildOrderStatusCard(detail), "_luckinOrderStatus", false); err != nil {
					logs.L().Ctx(runCtx).Warn("luckin reply order detail failed", zap.String("order_id", orderID), zap.Error(err))
				}
				return
			}
			if msgID != "" {
				_ = larkmsg.PatchCardJSON(runCtx, msgID, luckin.BuildOrderStatusCard(detail))
			}
		}, nil
	}
}

func handleBindToken(store luckin.CredentialWriter, dismiss ephemeralDeleter) appcardaction.SyncHandler {
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
		// 绑定卡为用户私密临时卡，提交成功后撤回，避免其他成员看到。
		if dismiss != nil {
			for _, msgID := range bindDismissMessageIDs(actionCtx) {
				if err := dismiss(ctx, msgID); err != nil {
					logs.L().Ctx(ctx).Warn("luckin delete bind ephemeral failed", zap.String("message_id", msgID), zap.Error(err))
				}
			}
		}
		logs.L().Ctx(ctx).Info("luckin token bound",
			zap.String("scope", string(scope.Type)),
			zap.String("operator", req.OpenID),
			zap.String("token_hint", luckin.MaskToken(token)),
		)
		return appcardaction.InfoToast("已绑定瑞幸账号（" + luckin.ScopeLabel(scope) + "，" + luckin.MaskToken(token) + "）"), nil
	}
}

func bindDismissMessageIDs(actionCtx *appcardaction.Context) []string {
	seen := make(map[string]struct{}, 2)
	out := make([]string, 0, 2)
	for _, id := range []string{
		actionValue(actionCtx, cardactionproto.IDField),
		actionCtx.MessageID(),
	} {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
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

// ephemeralDeleter 撤回临时卡。
type ephemeralDeleter func(ctx context.Context, messageID string) error

// sendBindGuide 以临时卡（仅发起人可见）发送绑定引导，避免群内其他成员看到。
func sendBindGuide(ctx context.Context, req luckin.CredentialRequest) {
	if strings.TrimSpace(req.ChatID) == "" || strings.TrimSpace(req.OpenID) == "" {
		return
	}
	if _, err := larkmsg.SendEphemeralCard(ctx, req.ChatID, req.OpenID, luckin.BuildBindTokenCard(req.ChatType)); err != nil {
		logs.L().Ctx(ctx).Warn("luckin send bind ephemeral failed", zap.String("open_id", req.OpenID), zap.Error(err))
	}
}

func mustCart(session luckin.SessionStore, ctx context.Context, key luckin.SessionKey) luckin.Cart {
	cart, _ := session.GetCart(ctx, key)
	return cart
}

// lookupShop 取当前会话门店；是否曾开始过会话交给 SessionStore.Seen 判断。
func lookupShop(ctx context.Context, session luckin.SessionStore, actionCtx *appcardaction.Context) (luckin.ShopSelection, bool) {
	if session == nil {
		return luckin.ShopSelection{}, false
	}
	return session.GetShop(ctx, sessionKey(actionCtx))
}

func sessionMissingCard(ctx context.Context, session luckin.SessionStore, actionCtx *appcardaction.Context) map[string]any {
	req := credentialRequestFromAction(actionCtx)
	recent := recentShops(ctx, session, req)
	if session != nil && session.Seen(ctx, sessionKey(actionCtx)) {
		return luckin.BuildSessionExpiredCardWithRecent(recent)
	}
	return luckin.BuildShopStartCard(recent)
}

// patchSessionMissing 在会话失效或从未开始点单时，把卡片替换为「位置重选」表单，
// 同时用 Seen 墓碑区分过期与未选择，避免用户从头自然语言交互。
func patchSessionMissing(session luckin.SessionStore, actionCtx *appcardaction.Context, msgID string) appcardaction.AsyncTask {
	if msgID == "" {
		return nil
	}
	return func(runCtx context.Context) {
		_ = larkmsg.PatchCardJSON(runCtx, msgID, sessionMissingCard(runCtx, session, actionCtx))
	}
}

func recentShops(ctx context.Context, session luckin.SessionStore, req luckin.CredentialRequest) []luckin.ShopSelection {
	if session == nil {
		return nil
	}
	return session.GetRecentShops(ctx, luckin.NewSessionKey(req), 3)
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

// parseAmount 解析数量输入，非法/空返回 1。
func parseAmount(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return 1
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 1
	}
	return n
}

// selectedCoupons 从确认卡的多选优惠券字段中解析选中的编码列表。
func selectedCoupons(actionCtx *appcardaction.Context) []string {
	if actionCtx == nil || actionCtx.Action == nil {
		return nil
	}
	raw, ok := actionCtx.Action.FormValue[cardactionproto.LuckinCouponFormField]
	if !ok {
		return nil
	}
	switch v := raw.(type) {
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				if s = strings.TrimSpace(s); s != "" {
					out = append(out, s)
				}
			}
		}
		return out
	case []string:
		out := make([]string, 0, len(v))
		for _, s := range v {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
		return out
	case string:
		if s := strings.TrimSpace(v); s != "" {
			return []string{s}
		}
	}
	return nil
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
