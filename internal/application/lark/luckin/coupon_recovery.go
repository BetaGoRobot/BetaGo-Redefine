package luckin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/mcpclient"
	cardactionproto "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
)

// submissionUncertain distinguishes errors after createOrder was attempted from
// pre-submission validation errors. It is a UI guard, not a durable submission state.
type submissionUncertain struct{ cause error }

func (e *submissionUncertain) Error() string { return e.cause.Error() }
func (e *submissionUncertain) Unwrap() error { return e.cause }

type couponRecovery struct {
	cause error
	order PendingOrder
	next  *PendingOrder
}

func (e *couponRecovery) Error() string { return e.cause.Error() }
func (e *couponRecovery) Unwrap() error { return e.cause }

// Only a tool-returned, explicit coupon rejection permits a new quote. ErrRemote
// alone also includes transport failures and says nothing about order creation.
func isCouponRejection(err error) bool {
	var toolErr *mcpclient.ToolError
	if !errors.As(err, &toolErr) {
		return false
	}
	message := strings.ToLower(toolErr.Message)
	for _, uncertain := range []string{"timeout", "timed out", "超时", "connection", "网络", "unknown", "未知", "order created", "订单已创建", "authentication", "service", "response", "protocol", "服务", "响应", "请求", "解析", "认证", "鉴权"} {
		if strings.Contains(message, uncertain) {
			return false
		}
	}
	for _, rejected := range []string{
		"coupon already used", "coupon is already used", "coupon already redeemed",
		"invalid coupon", "coupon invalid", "coupon is invalid", "expired coupon", "coupon expired", "coupon has expired", "coupon is expired",
		"coupon unavailable", "coupon is unavailable", "coupon not available", "coupon is not available", "coupon not applicable", "coupon is not applicable",
		"优惠券已被使用", "优惠券已使用", "优惠券已经使用", "优惠券已过期", "优惠券不可用", "优惠券不适用", "优惠券已失效", "优惠券无效", "优惠券不存在", "优惠券被占用",
	} {
		if strings.Contains(message, rejected) {
			return true
		}
	}
	return false
}

// RefreshPending re-quotes the exact persisted products, retaining all submission
// fields except selected coupons. It never creates an order or extends its expiry.
func (s DraftService) RefreshPending(ctx context.Context, order PendingOrder, cred Credential, coupons []string) (PendingOrder, error) {
	if s.caller == nil {
		return PendingOrder{}, errors.New("order preview is unavailable")
	}
	var payload map[string]json.RawMessage
	if json.Unmarshal(order.CreateOrderPayload, &payload) != nil || len(payload["deptId"]) == 0 || len(payload["productList"]) == 0 {
		return PendingOrder{}, errors.New("order payload cannot be re-quoted")
	}
	if coupons == nil {
		coupons = []string{}
	}
	payload["couponCodeList"], _ = json.Marshal(coupons)
	previewPayload, _ := json.Marshal(map[string]json.RawMessage{
		"deptId": payload["deptId"], "productList": payload["productList"], "couponCodeList": payload["couponCodeList"],
	})
	result, err := s.caller.CallTool(ctx, s.callReq(cred, "previewOrder", previewPayload))
	if err != nil {
		return PendingOrder{}, err
	}
	preview := ExtractData(result.Content)
	var quote map[string]any
	if json.Unmarshal(preview, &quote) != nil {
		return PendingOrder{}, errors.New("invalid order preview")
	}
	price, err := strconv.ParseFloat(numberValue(quote["discountPrice"]), 64)
	if err != nil || price < 0 || math.IsNaN(price) || math.IsInf(price, 0) {
		return PendingOrder{}, errors.New("order preview has no valid price")
	}
	order.CreateOrderPayload, err = json.Marshal(payload)
	if err != nil {
		return PendingOrder{}, err
	}
	// The existing callback hash is also the revision token. Include the prior
	// revision and quote so even an unchanged coupon choice invalidates stale clicks.
	revision, _ := json.Marshal(struct {
		Previous string
		Payload  json.RawMessage
		Quote    json.RawMessage
	}{order.PayloadHash, order.CreateOrderPayload, preview})
	hash := sha256.Sum256(revision)
	order.PayloadHash = hex.EncodeToString(hash[:])
	order.PreviewResult = preview
	return order, nil
}

func (s confirmationService) recoverCoupon(ctx context.Context, order PendingOrder, cred Credential, cause error) error {
	recovery := &couponRecovery{cause: cause, order: order}
	next, err := NewDraftService(s.caller, s.remoteURL()).RefreshPending(ctx, order, cred, nil)
	if err == nil {
		recovery.next = &next
		err = s.store.UpdateDraft(ctx, next, order.PayloadHash, time.Now())
	}
	if err != nil {
		recovery.cause = errors.Join(cause, err)
	}
	return recovery
}

// Unlike a normal failure card, this has no checkout or shop-search actions.
func buildOrderCheckCard(message string) map[string]any {
	return map[string]any(larkmsg.NewCardV2("瑞幸订单待核对", []any{
		larkmsg.Markdown("⚠️ " + message),
	}, larkmsg.StandardPanelCardV2Options()))
}

// BuildCouponRefreshCard only retries preview; it cannot create a remote order.
func BuildCouponRefreshCard(order PendingOrder) map[string]any {
	elements := []any{
		larkmsg.Markdown("**优惠券与价格需要刷新**"),
		larkmsg.Markdown("暂未获取到最新报价。请刷新后重新选择优惠券并确认下单。"),
		larkmsg.HintMarkdown("刷新会清除之前选择的优惠券，不会提交订单。"),
		larkmsg.ButtonRow("none", larkmsg.Button("刷新优惠券与价格", larkmsg.ButtonOptions{
			Type: "primary", Payload: map[string]any{
				cardactionproto.ActionField:         cardactionproto.ActionLuckinCouponApply,
				cardactionproto.PendingOrderIDField: order.ID,
				cardactionproto.PayloadHashField:    order.PayloadHash,
			},
		})),
	}
	return map[string]any(larkmsg.NewCardV2("瑞幸点单", elements, larkmsg.StandardPanelCardV2Options()))
}
