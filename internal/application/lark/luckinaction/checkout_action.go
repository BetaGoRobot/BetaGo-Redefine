package luckinaction

import (
	"context"
	"errors"
	appcardaction "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/cardaction"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/luckin"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/mcpstore"
	cardactionproto "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"go.uber.org/zap"
	"strconv"
	"strings"
	"time"
)

// checkoutEffects owns checkout delivery and locking so tests can execute the
// same checkout task with local fakes instead of sending cards or taking Redis locks.
type checkoutEffects struct {
	patch func(context.Context, string, any) error
	reply func(context.Context, string, any, string, bool) error
	bind  func(context.Context, luckin.CredentialRequest)
	lock  func(context.Context, string, func() error) error
}

func checkoutTask(session luckin.SessionStore, draft luckin.DraftService, pending pendingOrderStore, tokens luckin.CredentialStore, coupons []string) appcardaction.AsyncHandler {
	return checkoutTaskWithEffects(session, draft, pending, tokens, coupons, checkoutEffects{
		patch: larkmsg.PatchCardJSON, reply: larkmsg.ReplyCardJSON, bind: sendBindGuide, lock: mcpstore.WithSessionLock,
	})
}

func checkoutTaskWithEffects(session luckin.SessionStore, draft luckin.DraftService, pending pendingOrderStore, tokens luckin.CredentialStore, coupons []string, effects checkoutEffects) appcardaction.AsyncHandler {
	return func(ctx context.Context, actionCtx *appcardaction.Context) (appcardaction.AsyncTask, error) {
		msgID := strings.TrimSpace(actionCtx.MessageID())
		if msgID == "" {
			return nil, errors.New("message id is required")
		}
		_, sess, ok := requireSession(ctx, session, actionCtx)
		if !ok {
			return patchSessionMissing(session, actionCtx, msgID), nil
		}
		modeValue := strings.TrimSpace(formValue(actionCtx, cardactionproto.LuckinCheckoutModeField))
		if modeValue == "" {
			modeValue = string(sess.CheckoutMode)
		}
		mode := luckin.NormalizeCheckoutMode(modeValue)
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
		req := credentialRequestFromAction(actionCtx)
		req.OpenID = requesterOpenID
		initiator := sess.InitiatorOpenID

		return func(runCtx context.Context) {
			_ = effects.patch(runCtx, msgID, luckin.AppendInitiatorFooter(luckin.BuildCartCheckoutProcessingCard(shop), initiator))
			cred, err := resolveCredential(runCtx, tokens, req)
			if err != nil {
				effects.bind(runCtx, req)
				_ = effects.patch(runCtx, msgID, luckin.AppendInitiatorFooter(luckin.BuildCartCard(shop, sess.Cart, mode), initiator))
				return
			}
			subOrders := luckin.SplitItemsToSingleCupOrders(items)
			if len(subOrders) == 0 {
				_ = effects.patch(runCtx, msgID, luckin.AppendInitiatorFooter(luckin.BuildOrderFailedCard("没有可拆分的下单商品"), initiator))
				return
			}
			summaryCard := luckin.BuildOrderProcessingCard("已按单杯拆成 " + strconv.Itoa(len(subOrders)) + " 个待确认订单，请分别选择优惠券并支付。")
			_ = effects.patch(runCtx, msgID, luckin.AppendInitiatorFooter(summaryCard, initiator))
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
					_ = effects.reply(runCtx, msgID, luckin.AppendInitiatorFooter(luckin.BuildOrderFailedCard("预览订单失败："+err.Error()), initiator), splitOrderReplySuffix("_luckinSplitDraft", "", idx), false)
					continue
				}
				if pending != nil {
					if err := pending.CreatePendingOrder(runCtx, order); err != nil {
						logs.L().Ctx(runCtx).Warn("luckin create pending order failed", zap.Error(err))
						_ = effects.reply(runCtx, msgID, luckin.AppendInitiatorFooter(luckin.BuildOrderFailedCard("创建待确认订单失败："+err.Error()), initiator), splitOrderReplySuffix("_luckinSplitPending", order.ID, idx), false)
						continue
					}
				}
				_ = effects.reply(runCtx, msgID, luckin.AppendInitiatorFooter(card, initiator), splitOrderReplySuffix("_luckinSplitOrder", order.ID, idx), false)
			}
			_ = effects.lock(runCtx, msgID, func() error {
				key, curSess, ok := requireSession(runCtx, session, actionCtx)
				if !ok {
					return nil
				}
				curSess.CheckoutMode = mode
				curSess.Cart = luckin.RemoveCheckoutItems(curSess.Cart, mode, operatorOpenID)
				session.SetSession(runCtx, key, curSess)
				return effects.patch(runCtx, msgID, luckin.AppendInitiatorFooter(luckin.BuildCartCard(curSess.Shop, curSess.Cart, curSess.CheckoutMode), curSess.InitiatorOpenID))
			})
		}, nil
	}
}
