package luckinaction

import (
	"context"
	"encoding/json"
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

// errOnlyInitiator 临界操作（选店/搜索/结算等）只能由发起人触发；其他用户得到 toast。
const onlyInitiatorMsg = "只有发起人可以执行此操作"

// errCartLineForbidden 非加入者也非发起人想改某条购物车行；提示并 no-op。
const cartLineForbiddenMsg = "只有该商品的加入者或发起人可以调整"

func handleShopSelect(session luckin.SessionStore) appcardaction.SyncHandler {
	return func(ctx context.Context, actionCtx *appcardaction.Context) (*callback.CardActionTriggerResponse, error) {
		key, sess, ok := requireSession(ctx, session, actionCtx)
		if !ok {
			return appcardaction.InfoToastWithRawCardPayload("会话已失效，请重新发起点单", luckin.AppendInitiatorFooter(sessionMissingCard(ctx, session, actionCtx), sess.InitiatorOpenID)), nil
		}
		if !luckin.IsInitiator(sess, actionCtx.OpenID()) {
			return appcardaction.ErrorToast(onlyInitiatorMsg), nil
		}
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
		// 切换门店要清掉旧购物车（避免跨店混单），整体写回。
		newSess := luckin.OrderSession{
			InitiatorOpenID: sess.InitiatorOpenID,
			ChatID:          sess.ChatID,
			Shop:            shop,
			CheckoutMode:    sess.CheckoutMode,
		}
		if err := mcpstore.WithSessionLock(ctx, key.MessageID, func() error {
			session.SetSession(ctx, key, newSess)
			session.AddRecentShop(ctx, luckin.NewUserHistoryKey(credentialRequestFromAction(actionCtx)), shop)
			return nil
		}); err != nil {
			return appcardaction.ErrorToast("操作太频繁，请重试"), nil
		}
		card := luckin.AppendInitiatorFooter(luckin.BuildProductQueryCard(shop, luckin.Cart{}), sess.InitiatorOpenID)
		return appcardaction.InfoToastWithRawCardPayload("已选门店："+shop.DeptName, card), nil
	}
}

// handleRegionSelect 刷新同一张门店搜索卡：第一步选省份，第二步展示该省份下的城市/区县。
func handleRegionSelect(session luckin.SessionStore) appcardaction.SyncHandler {
	return func(ctx context.Context, actionCtx *appcardaction.Context) (*callback.CardActionTriggerResponse, error) {
		_, sess, ok := requireSession(ctx, session, actionCtx)
		if !ok {
			return appcardaction.InfoToastWithRawCardPayload("会话已失效，请重新发起点单", sessionMissingCard(ctx, session, actionCtx)), nil
		}
		if !luckin.IsInitiator(sess, actionCtx.OpenID()) {
			return appcardaction.ErrorToast(onlyInitiatorMsg), nil
		}
		province := strings.TrimSpace(formValue(actionCtx, cardactionproto.LuckinProvinceFormField))
		if province == "" {
			return appcardaction.ErrorToast("请选择省份"), nil
		}
		req := credentialRequestFromAction(actionCtx)
		card := luckin.AppendInitiatorFooter(luckin.BuildRegionSelectCard(province, recentShops(ctx, session, req)), sess.InitiatorOpenID)
		return appcardaction.InfoToastWithRawCardPayload("已选择："+province, card), nil
	}
}

func handleProductQuery(session luckin.SessionStore, draft luckin.DraftService, tokens luckin.CredentialStore, images luckin.ImageUploader) appcardaction.AsyncHandler {
	return func(ctx context.Context, actionCtx *appcardaction.Context) (appcardaction.AsyncTask, error) {
		msgID := strings.TrimSpace(actionCtx.MessageID())
		if msgID == "" {
			return nil, errors.New("message id is required")
		}
		key, sess, ok := requireSession(ctx, session, actionCtx)
		if !ok {
			return patchSessionMissing(session, actionCtx, msgID), nil
		}
		shop := sess.Shop
		if shop.DeptID == 0 {
			return patchSessionMissing(session, actionCtx, msgID), nil
		}
		query := strings.TrimSpace(formValue(actionCtx, cardactionproto.LuckinQueryFormField))
		if query == "" {
			return nil, errors.New("请输入商品关键词")
		}
		// 商品搜索用发起人的凭证：所有商品价格、可选规格都和发起人账号绑定（优惠券归属一致）。
		_ = key
		req := initiatorCredentialRequest(sess, actionCtx)
		cart := sess.Cart

		return func(runCtx context.Context) {
			_ = larkmsg.PatchCardJSON(runCtx, msgID, luckin.AppendInitiatorFooter(luckin.BuildProductSearchingCard(shop, cart, query), sess.InitiatorOpenID))
			cred, err := resolveCredential(runCtx, tokens, req)
			if err != nil {
				sendBindGuide(runCtx, req)
				_ = larkmsg.PatchCardJSON(runCtx, msgID, luckin.AppendInitiatorFooter(luckin.BuildProductQueryCard(shop, cart), sess.InitiatorOpenID))
				return
			}
			products, err := draft.SearchProducts(runCtx, cred, shop, query, 5)
			if err != nil {
				logs.L().Ctx(runCtx).Warn("luckin product search failed", zap.String("query", query), zap.Error(err))
				_ = larkmsg.PatchCardJSON(runCtx, msgID, luckin.AppendInitiatorFooter(luckin.BuildProductSearchErrorCard(shop, cart, query), sess.InitiatorOpenID))
				return
			}
			imageKeys := luckin.UploadProductImages(runCtx, images, products)
			if err := larkmsg.PatchCardJSON(runCtx, msgID, luckin.AppendInitiatorFooter(luckin.BuildProductSelectCard(shop, cart, products, imageKeys), sess.InitiatorOpenID)); err != nil {
				logs.L().Ctx(runCtx).Warn("luckin patch product card failed", zap.String("message_id", msgID), zap.Error(err))
			}
		}, nil
	}
}

// handleShopSearch 卡片内按位置文本搜索门店；只有发起人可以触发。
func handleShopSearch(session luckin.SessionStore, draft luckin.DraftService, geocoder luckin.Geocoder, tokens luckin.CredentialStore) appcardaction.AsyncHandler {
	return func(ctx context.Context, actionCtx *appcardaction.Context) (appcardaction.AsyncTask, error) {
		msgID := strings.TrimSpace(actionCtx.MessageID())
		if msgID == "" {
			return nil, errors.New("message id is required")
		}
		_, sess, ok := requireSession(ctx, session, actionCtx)
		if !ok {
			return patchSessionMissing(session, actionCtx, msgID), nil
		}
		if !luckin.IsInitiator(sess, actionCtx.OpenID()) {
			return func(context.Context) { /* no-op */ }, errInitiatorOnly()
		}
		location := strings.TrimSpace(strings.Join([]string{
			formValue(actionCtx, cardactionproto.LuckinRegionFormField),
			formValue(actionCtx, cardactionproto.LuckinLocationFormField),
		}, " "))
		location = luckin.NormalizeLocationText(location)
		if location == "" {
			return nil, errors.New("请选择城市/区县，或输入位置关键词")
		}
		req := initiatorCredentialRequest(sess, actionCtx)
		return func(runCtx context.Context) {
			_ = larkmsg.PatchCardJSON(runCtx, msgID, luckin.AppendInitiatorFooter(luckin.BuildShopSearchingCard(location), sess.InitiatorOpenID))
			cred, err := resolveCredential(runCtx, tokens, req)
			if err != nil {
				sendBindGuide(runCtx, req)
				_ = larkmsg.PatchCardJSON(runCtx, msgID, luckin.AppendInitiatorFooter(luckin.BuildSessionExpiredCardWithRecent(recentShops(runCtx, session, req)), sess.InitiatorOpenID))
				return
			}
			if geocoder == nil {
				_ = larkmsg.PatchCardJSON(runCtx, msgID, luckin.AppendInitiatorFooter(luckin.BuildShopSelectCard(location, nil), sess.InitiatorOpenID))
				return
			}
			point, err := geocoder.Geocode(runCtx, location)
			if err != nil {
				logs.L().Ctx(runCtx).Warn("luckin geocode failed", zap.String("location", location), zap.Error(err))
				_ = larkmsg.PatchCardJSON(runCtx, msgID, luckin.AppendInitiatorFooter(luckin.BuildShopSelectCard(location, nil), sess.InitiatorOpenID))
				return
			}
			shops, err := draft.SearchShops(runCtx, cred, luckin.GeoPoint{Longitude: point.Longitude, Latitude: point.Latitude}, "", 5)
			if err != nil {
				logs.L().Ctx(runCtx).Warn("luckin shop search failed", zap.String("location", location), zap.Error(err))
				_ = larkmsg.PatchCardJSON(runCtx, msgID, luckin.AppendInitiatorFooter(luckin.BuildShopSelectCard(location, nil), sess.InitiatorOpenID))
				return
			}
			_ = larkmsg.PatchCardJSON(runCtx, msgID, luckin.AppendInitiatorFooter(luckin.BuildShopSelectCard(location, shops), sess.InitiatorOpenID))
		}, nil
	}
}

type pendingOrderCreator interface {
	CreatePendingOrder(context.Context, luckin.PendingOrder) error
}

type pendingOrderStore interface {
	CreatePendingOrder(context.Context, luckin.PendingOrder) error
	FindPendingOrder(context.Context, string) (luckin.PendingOrder, error)
	UpdateDraft(context.Context, luckin.PendingOrder, time.Time) error
}

// handleProductSelect 异步处理：有规格则先弹规格卡，否则把商品加入购物车并刷新购物车卡。
// 任何群成员都可以加购，加购的商品行带 AddedByOpenID = 该用户。
func handleProductSelect(session luckin.SessionStore, draft luckin.DraftService, tokens luckin.CredentialStore, images luckin.ImageUploader) appcardaction.AsyncHandler {
	return func(ctx context.Context, actionCtx *appcardaction.Context) (appcardaction.AsyncTask, error) {
		msgID := strings.TrimSpace(actionCtx.MessageID())
		if msgID == "" {
			return nil, errors.New("message id is required")
		}
		key, sess, ok := requireSession(ctx, session, actionCtx)
		if !ok {
			return patchSessionMissing(session, actionCtx, msgID), nil
		}
		shop := sess.Shop
		if shop.DeptID == 0 {
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
		// 商品价格/规格切换走发起人凭证；加购"行归属"是当前点击者。
		credReq := initiatorCredentialRequest(sess, actionCtx)
		adderOpenID := actionCtx.OpenID()

		return func(runCtx context.Context) {
			cred, err := resolveCredential(runCtx, tokens, credReq)
			if err != nil {
				sendBindGuide(runCtx, credReq)
				_ = larkmsg.PatchCardJSON(runCtx, msgID, luckin.AppendInitiatorFooter(luckin.BuildCartCard(shop, sess.Cart, sess.CheckoutMode), sess.InitiatorOpenID))
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
					_ = larkmsg.PatchCardJSON(runCtx, msgID, luckin.AppendInitiatorFooter(luckin.BuildSpecSelectCard(shop, detail, imgKey, amount), sess.InitiatorOpenID))
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
						if k := images.UploadByURL(runCtx, detail.PictureURL); k != "" {
							imageKey = k
						}
					}
				}
			}

			// 进入临界区：读 session → 加购 → 写回 session，全程持锁。
			lockErr := mcpstore.WithSessionLock(runCtx, msgID, func() error {
				curSess, ok := session.GetSession(runCtx, key)
				if !ok {
					curSess = sess
				}
				curSess.Cart.Add(luckin.CartItem{
					ProductID:     productID,
					SkuCode:       skuCode,
					ProductName:   productName,
					Amount:        amount,
					UnitPrice:     unitPrice,
					ImageKey:      imageKey,
					AddedByOpenID: adderOpenID,
				})
				session.SetSession(runCtx, key, curSess)
				_ = larkmsg.PatchCardJSON(runCtx, msgID, luckin.AppendInitiatorFooter(luckin.BuildCartCard(curSess.Shop, curSess.Cart, curSess.CheckoutMode), curSess.InitiatorOpenID))
				return nil
			})
			if lockErr != nil {
				logs.L().Ctx(runCtx).Warn("luckin cart add failed", zap.Error(lockErr))
			}
		}, nil
	}
}

// handleCartUpdate 调整购物车某条目数量（含删除）。
// 只允许该行的加入者或发起人；其他人只 toast 并不动 session。
func handleCartUpdate(session luckin.SessionStore) appcardaction.AsyncHandler {
	return func(ctx context.Context, actionCtx *appcardaction.Context) (appcardaction.AsyncTask, error) {
		msgID := strings.TrimSpace(actionCtx.MessageID())
		if msgID == "" {
			return nil, errors.New("message id is required")
		}
		key, sess, ok := requireSession(ctx, session, actionCtx)
		if !ok {
			return patchSessionMissing(session, actionCtx, msgID), nil
		}
		lineID := actionValue(actionCtx, cardactionproto.LuckinLineIDField)
		item, found := sess.Cart.FindLine(lineID)
		if !found {
			return nil, errors.New("条目已不存在")
		}
		if !luckin.CanModifyLine(sess, item, actionCtx.OpenID()) {
			return nil, errCartLineForbidden()
		}
		qty := parseAmount(actionValue(actionCtx, cardactionproto.LuckinQtyFormField))
		return func(runCtx context.Context) {
			_ = mcpstore.WithSessionLock(runCtx, msgID, func() error {
				curSess, ok := session.GetSession(runCtx, key)
				if !ok {
					curSess = sess
				}
				curSess.Cart.SetAmountByLine(lineID, qty)
				session.SetSession(runCtx, key, curSess)
				_ = larkmsg.PatchCardJSON(runCtx, msgID, luckin.AppendInitiatorFooter(luckin.BuildCartCard(curSess.Shop, curSess.Cart, curSess.CheckoutMode), curSess.InitiatorOpenID))
				return nil
			})
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
		key, sess, ok := requireSession(ctx, session, actionCtx)
		if !ok {
			return patchSessionMissing(session, actionCtx, msgID), nil
		}
		lineID := actionValue(actionCtx, cardactionproto.LuckinLineIDField)
		item, found := sess.Cart.FindLine(lineID)
		if !found {
			return nil, errors.New("条目已不存在")
		}
		if !luckin.CanModifyLine(sess, item, actionCtx.OpenID()) {
			return nil, errCartLineForbidden()
		}
		return func(runCtx context.Context) {
			_ = mcpstore.WithSessionLock(runCtx, msgID, func() error {
				curSess, ok := session.GetSession(runCtx, key)
				if !ok {
					curSess = sess
				}
				curSess.Cart.RemoveByLine(lineID)
				session.SetSession(runCtx, key, curSess)
				_ = larkmsg.PatchCardJSON(runCtx, msgID, luckin.AppendInitiatorFooter(luckin.BuildCartCard(curSess.Shop, curSess.Cart, curSess.CheckoutMode), curSess.InitiatorOpenID))
				return nil
			})
		}, nil
	}
}

// handleCartCheckout 预览购物车并生成确认订单卡片。
func handleCartCheckout(session luckin.SessionStore, draft luckin.DraftService, pending pendingOrderStore, tokens luckin.CredentialStore) appcardaction.AsyncHandler {
	return checkoutTask(session, draft, pending, tokens, nil)
}

// handleCouponApply 用户在确认卡上选中优惠券后重新预览，刷新价格与确认卡。
// 优惠券卡片一定源自某张待确认订单卡（携带 pending_order_id + payload_hash），
// 因此这里只按 pending order 重新预览、原地刷新当前卡片；绝不回退到购物车结算流程
// （那会在回复卡的 msgID 上找不到 session，进而把卡片打回“选择门店”，正是本次要修的 bug）。
func handleCouponApply(session luckin.SessionStore, draft luckin.DraftService, pending pendingOrderStore, tokens luckin.CredentialStore) appcardaction.AsyncHandler {
	return func(ctx context.Context, actionCtx *appcardaction.Context) (appcardaction.AsyncTask, error) {
		pendingID, _ := actionCtx.Action.RequiredString(cardactionproto.PendingOrderIDField)
		payloadHash, _ := actionCtx.Action.RequiredString(cardactionproto.PayloadHashField)
		coupons := selectedCoupons(actionCtx)
		msgID := strings.TrimSpace(actionCtx.MessageID())
		pendingID = strings.TrimSpace(pendingID)
		payloadHash = strings.TrimSpace(payloadHash)
		if pendingID == "" || payloadHash == "" || pending == nil {
			// 理论上不会发生：优惠券卡一定带 pending 信息。宁可原地报错，也不要回退到选门店。
			return func(runCtx context.Context) {
				if msgID != "" {
					_ = larkmsg.PatchCardJSON(runCtx, msgID, luckin.BuildOrderFailedCard("待确认订单信息缺失，请重新结算"))
				}
			}, nil
		}
		return func(runCtx context.Context) {
			order, err := pending.FindPendingOrder(runCtx, pendingID)
			if err != nil {
				if msgID != "" {
					_ = larkmsg.PatchCardJSON(runCtx, msgID, luckin.BuildOrderFailedCard("待确认订单已失效，请重新结算"))
				}
				return
			}
			if strings.TrimSpace(order.PayloadHash) != payloadHash || order.Status != luckin.PendingStatusPending || !order.ExpiresAt.After(time.Now()) {
				if msgID != "" {
					_ = larkmsg.PatchCardJSON(runCtx, msgID, luckin.BuildOrderFailedCard("待确认订单已过期，请重新结算"))
				}
				return
			}
			cred, err := tokens.FindToken(runCtx, luckin.CredentialLookup{
				Provider:  luckin.ProviderLuckin,
				AppID:     order.AppID,
				BotOpenID: order.BotOpenID,
				Scope:     order.CredentialScope,
			})
			if err != nil {
				if msgID != "" {
					_ = larkmsg.PatchCardJSON(runCtx, msgID, luckin.BuildOrderFailedCard("瑞幸账号凭证失效，请重新绑定后结算"))
				}
				return
			}
			var payload map[string]any
			if err := json.Unmarshal(order.CreateOrderPayload, &payload); err != nil {
				if msgID != "" {
					_ = larkmsg.PatchCardJSON(runCtx, msgID, luckin.BuildOrderFailedCard("订单草稿损坏，请重新结算"))
				}
				return
			}
			shop := luckin.ShopSelection{
				DeptID:    int64(numberFloat(payload["deptId"])),
				Longitude: numberFloat(payload["longitude"]),
				Latitude:  numberFloat(payload["latitude"]),
			}
			nextOrder, _, err := draft.Draft(runCtx, luckin.DraftRequest{
				AppID:           order.AppID,
				BotOpenID:       order.BotOpenID,
				ChatID:          order.ChatID,
				InitiatorOpenID: order.InitiatorOpenID,
				RequesterOpenID: order.RequesterOpenID,
				CheckoutMode:    order.CheckoutMode,
				Credential:      cred,
				Shop:            shop,
				Items:           order.CartSnapshot,
				CouponCodeList:  coupons,
				Now:             time.Now(),
			})
			if err != nil {
				if msgID != "" {
					_ = larkmsg.PatchCardJSON(runCtx, msgID, luckin.BuildOrderFailedCard("预览优惠券失败："+err.Error()))
				}
				return
			}
			// draft.Draft 会分配新的 UUID；这里必须让 pending_order 落库 ID 与卡片按钮里的 pending_order_id
			// 保持一致，否则用户点「确认下单」时会拿着 draft 分配的新 UUID 去查 DB，永远 not found。
			nextOrder.ID = order.ID
			if err := pending.UpdateDraft(runCtx, nextOrder, time.Now()); err != nil {
				if msgID != "" {
					_ = larkmsg.PatchCardJSON(runCtx, msgID, luckin.BuildOrderFailedCard("刷新待确认订单失败："+err.Error()))
				}
				return
			}
			if msgID != "" {
				_ = larkmsg.PatchCardJSON(runCtx, msgID, luckin.AppendInitiatorFooter(luckin.BuildPendingOrderCard(nextOrder), order.InitiatorOpenID))
			}
		}, nil
	}
}

func checkoutTask(session luckin.SessionStore, draft luckin.DraftService, pending pendingOrderStore, tokens luckin.CredentialStore, coupons []string) appcardaction.AsyncHandler {
	return func(ctx context.Context, actionCtx *appcardaction.Context) (appcardaction.AsyncTask, error) {
		msgID := strings.TrimSpace(actionCtx.MessageID())
		if msgID == "" {
			return nil, errors.New("message id is required")
		}
		_, sess, ok := requireSession(ctx, session, actionCtx)
		if !ok {
			return patchSessionMissing(session, actionCtx, msgID), nil
		}
		mode := luckin.NormalizeCheckoutMode(formValue(actionCtx, cardactionproto.LuckinCheckoutModeField))
		if mode == "" {
			mode = luckin.NormalizeCheckoutMode(string(sess.CheckoutMode))
		}
		if mode == "" {
			mode = luckin.CheckoutModeInitiatorUnified
		}
		if mode == luckin.CheckoutModeInitiatorUnified && !luckin.IsInitiator(sess, actionCtx.OpenID()) {
			return func(context.Context) { /* no-op */ }, errInitiatorOnly()
		}
		shop := sess.Shop
		if shop.DeptID == 0 || sess.Cart.Empty() {
			return patchSessionMissing(session, actionCtx, msgID), nil
		}
		operatorOpenID := actionCtx.OpenID()
		requesterOpenID := sess.InitiatorOpenID
		if mode == luckin.CheckoutModeSelfService {
			requesterOpenID = operatorOpenID
		}
		items := luckin.SelectCheckoutItems(sess.Cart, mode, operatorOpenID)
		if len(items) == 0 {
			return nil, errors.New("当前结算模式下没有可下单商品")
		}
		req := initiatorCredentialRequest(sess, actionCtx)
		initiator := sess.InitiatorOpenID

		return func(runCtx context.Context) {
			_ = larkmsg.PatchCardJSON(runCtx, msgID, luckin.AppendInitiatorFooter(luckin.BuildCartCheckoutProcessingCard(shop), initiator))
			cred, err := resolveCredential(runCtx, tokens, req)
			if err != nil {
				sendBindGuide(runCtx, req)
				_ = larkmsg.PatchCardJSON(runCtx, msgID, luckin.AppendInitiatorFooter(luckin.BuildCartCard(shop, luckin.Cart{Items: items}, mode), initiator))
				return
			}
			subOrders := luckin.SplitItemsToSingleCupOrders(items)
			if len(subOrders) == 0 {
				_ = larkmsg.PatchCardJSON(runCtx, msgID, luckin.AppendInitiatorFooter(luckin.BuildOrderFailedCard("没有可拆分的下单商品"), initiator))
				return
			}
			summaryCard := luckin.BuildOrderProcessingCard("已按单杯拆成 " + strconv.Itoa(len(subOrders)) + " 个待确认订单，请分别选择优惠券并支付。")
			_ = larkmsg.PatchCardJSON(runCtx, msgID, luckin.AppendInitiatorFooter(summaryCard, initiator))
			for idx, orderItems := range subOrders {
				order, card, err := draft.Draft(runCtx, luckin.DraftRequest{
					AppID:           req.AppID,
					BotOpenID:       req.BotOpenID,
					ChatID:          req.ChatID,
					InitiatorOpenID: initiator,
					RequesterOpenID: requesterOpenID,
					CheckoutMode:    mode,
					Credential:      cred,
					Shop:            shop,
					Items:           orderItems,
					CouponCodeList:  coupons,
					Now:             time.Now(),
				})
				if err != nil {
					_ = larkmsg.ReplyCardJSON(runCtx, msgID, luckin.AppendInitiatorFooter(luckin.BuildOrderFailedCard("预览订单失败："+err.Error()), initiator), splitOrderReplySuffix("_luckinSplitDraft", "", idx), false)
					continue
				}
				if pending != nil {
					if err := pending.CreatePendingOrder(runCtx, order); err != nil {
						logs.L().Ctx(runCtx).Warn("luckin create pending order failed", zap.Error(err))
						_ = larkmsg.ReplyCardJSON(runCtx, msgID, luckin.AppendInitiatorFooter(luckin.BuildOrderFailedCard("创建待确认订单失败："+err.Error()), initiator), splitOrderReplySuffix("_luckinSplitPending", order.ID, idx), false)
						continue
					}
				}
				_ = larkmsg.ReplyCardJSON(runCtx, msgID, luckin.AppendInitiatorFooter(card, initiator), splitOrderReplySuffix("_luckinSplitOrder", order.ID, idx), false)
			}
			_ = mcpstore.WithSessionLock(runCtx, msgID, func() error {
				key, curSess, ok := requireSession(runCtx, session, actionCtx)
				if !ok {
					return nil
				}
				curSess.CheckoutMode = mode
				curSess.Cart = luckin.RemoveCheckoutItems(curSess.Cart, mode, operatorOpenID)
				session.SetSession(runCtx, key, curSess)
				return larkmsg.PatchCardJSON(runCtx, msgID, luckin.AppendInitiatorFooter(luckin.BuildCartCard(curSess.Shop, curSess.Cart, curSess.CheckoutMode), curSess.InitiatorOpenID))
			})
		}, nil
	}
}

func numberFloat(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case int64:
		return float64(x)
	case int:
		return float64(x)
	default:
		return 0
	}
}

func splitOrderReplySuffix(prefix, orderID string, index int) string {
	if orderID = strings.TrimSpace(orderID); orderID != "" {
		return prefix + "_" + orderID
	}
	return prefix + "_" + strconv.Itoa(index)
}

// handleOrderStatus 实时查询订单状态并刷新卡片。任何人都可点（公共信息）。
func handleOrderStatus(tokens luckin.CredentialStore, draft luckin.DraftService) appcardaction.AsyncHandler {
	return func(ctx context.Context, actionCtx *appcardaction.Context) (appcardaction.AsyncTask, error) {
		orderID := strings.TrimSpace(actionValue(actionCtx, cardactionproto.LuckinOrderIDField))
		if orderID == "" {
			return nil, errors.New("订单号缺失")
		}
		msgID := strings.TrimSpace(actionCtx.MessageID())
		mode := strings.TrimSpace(actionValue(actionCtx, cardactionproto.LuckinStatusModeField))
		// 用发起人凭证查；若 session 已不存在（取餐通知卡场景），退回点击者本人凭证。
		req := credentialRequestFromAction(actionCtx)
		if _, sess, ok := requireSession(ctx, tokensSessionStore(tokens), actionCtx); ok && sess.InitiatorOpenID != "" {
			req = initiatorCredentialRequest(sess, actionCtx)
		}
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

// tokensSessionStore 是个 stub：handleOrderStatus 不真正访问 SessionStore，
// 只想复用 requireSession 的解析逻辑。这里用 nil 触发其"无 session"分支即可。
func tokensSessionStore(_ luckin.CredentialStore) luckin.SessionStore { return nil }

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

// initiatorCredentialRequest 把"凭证请求"切到发起人。
// chat_id / chat_type 仍使用当前点击者的会话上下文（同群里都一致），但 OpenID 强制为发起人。
func initiatorCredentialRequest(sess luckin.OrderSession, actionCtx *appcardaction.Context) luckin.CredentialRequest {
	req := credentialRequestFromAction(actionCtx)
	if openID := strings.TrimSpace(sess.InitiatorOpenID); openID != "" {
		req.OpenID = openID
	}
	return req
}

// requireSession 解析当前 actionCtx 对应的 SessionKey 并加载 OrderSession；
// session 不存在或缺 InitiatorOpenID 时返回 ok=false（卡片可能已过期）。
func requireSession(ctx context.Context, session luckin.SessionStore, actionCtx *appcardaction.Context) (luckin.SessionKey, luckin.OrderSession, bool) {
	if session == nil {
		return luckin.SessionKey{}, luckin.OrderSession{}, false
	}
	msgID := strings.TrimSpace(actionCtx.MessageID())
	if msgID == "" {
		return luckin.SessionKey{}, luckin.OrderSession{}, false
	}
	req := credentialRequestFromAction(actionCtx)
	key := luckin.NewSessionKey(req, msgID)
	sess, ok := session.GetSession(ctx, key)
	if !ok || strings.TrimSpace(sess.InitiatorOpenID) == "" {
		return key, sess, false
	}
	return key, sess, true
}

// errInitiatorOnly 异步路径下用 error 返回，让 cardaction 框架把错误透出给前端
// （前端目前只能看到 toast）。
func errInitiatorOnly() error { return errors.New(onlyInitiatorMsg) }

func errCartLineForbidden() error { return errors.New(cartLineForbiddenMsg) }

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

func sessionMissingCard(ctx context.Context, session luckin.SessionStore, actionCtx *appcardaction.Context) map[string]any {
	req := credentialRequestFromAction(actionCtx)
	recent := recentShops(ctx, session, req)
	if session != nil && session.Seen(ctx, luckin.NewUserHistoryKey(req)) {
		return luckin.BuildSessionExpiredCardWithRecent(recent)
	}
	return luckin.BuildShopStartCard(recent)
}

// patchSessionMissing 在会话失效或从未开始点单时，把卡片替换为「位置重选」表单。
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
	return session.GetRecentShops(ctx, luckin.NewUserHistoryKey(req), 3)
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
