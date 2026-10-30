package atoms

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/business"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

const luckinProductsSchema = `"productList":{"type":"array","minItems":1,"maxItems":20,"items":{"type":"object","properties":{"amount":{"type":"integer","minimum":1,"maximum":50},"productId":{"type":"integer","minimum":1},"skuCode":{"type":"string","minLength":1,"maxLength":256}},"required":["amount","productId","skuCode"],"additionalProperties":false}}`
const luckinOrderSchema = `{"type":"object","properties":{"deptId":{"type":"integer","minimum":1},` + luckinProductsSchema + `,"couponCodeList":{"type":"array","maxItems":20,"items":{"type":"string","minLength":1,"maxLength":256}},"longitude":{"type":"number","minimum":-180,"maximum":180},"latitude":{"type":"number","minimum":-90,"maximum":90}},"required":["deptId","productList"],"additionalProperties":false}`

func luckinBusinessDefinitions(o BusinessOptions) []Definition {
	if o.Luckin == nil {
		return nil
	}
	fp := o.Luckin.Fingerprint()
	defs := []Definition{businessDefinition("luckin_bind_token_guide", "Return personal Luckin account binding instructions and the configured binding URL. Does not send a card or accept a token. Credentials belong only to the current server-verified actor.", `{"type":"object","properties":{},"additionalProperties":false}`, fp, func(context.Context, Invocation, string) (string, error) {
		return businessJSON(o.Luckin.BindingGuide())
	}),
		businessDefinition("luckin_shop_search", "Find Luckin shops using a location description or explicit GCJ-02 coordinates, with optional shop-name filtering. Returns structured data. Uses only the current actor's own account; never sends selection cards.", `{"type":"object","properties":{"locationText":{"type":"string","minLength":1,"maxLength":1024},"deptName":{"type":"string","maxLength":256},"longitude":{"type":"number","minimum":-180,"maximum":180},"latitude":{"type":"number","minimum":-90,"maximum":90}},"additionalProperties":false}`, fp, func(ctx context.Context, in Invocation, raw string) (string, error) {
			var a struct {
				LocationText, DeptName string
				Longitude, Latitude    *float64
			}
			if json.Unmarshal([]byte(raw), &a) != nil || (a.Longitude == nil) != (a.Latitude == nil) {
				return "", domain.ErrInvalid
			}
			var p *business.GeoPoint
			if a.Longitude != nil {
				p = &business.GeoPoint{Longitude: *a.Longitude, Latitude: *a.Latitude}
			}
			out, err := o.Luckin.SearchShops(ctx, businessIdentity(in), a.LocationText, a.DeptName, p)
			if err != nil {
				return "", err
			}
			return businessJSON(map[string]any{"data": out})
		}),
	}
	if o.LuckinBinding != nil {
		bind := businessDefinition("luckin_account_bind", "Ensure the current verified actor has a personal Luckin account for ordering. By default, already-bound accounts return already_bound without sending; otherwise reuse a pending form or send an ephemeral form CARD inside the CURRENT CHAT, visible only to the verified actor. This is not a direct-message conversation and not a binding URL. Set manage=true only for an explicit request to rebind or revoke, which opens that current-chat management card even when already bound. Credentials are entered only in the card and never accepted as tool arguments or group messages. Returns outcome already_bound, sent or reused; actual form receipts include delivery_location=current_chat. Does not order or pay.", `{"type":"object","properties":{"manage":{"type":"boolean"}},"additionalProperties":false}`, fp, func(ctx context.Context, in Invocation, raw string) (string, error) {
			var a struct {
				Manage bool `json:"manage"`
			}
			if json.Unmarshal([]byte(raw), &a) != nil {
				return "", domain.ErrInvalid
			}
			receipt, err := o.LuckinBinding.RequestBinding(ctx, businessIdentity(in), a.Manage)
			if err != nil {
				return "", err
			}
			return businessJSON(receipt)
		})
		bind.SideEffect = true
		bind.AutoExecute = true
		bind.PerActorTurn = true
		defs = append(defs, bind)
	}
	queries := []struct{ name, tool, description, schema string }{
		{"luckin_product_search", "searchProductForMcp", "Search Luckin products at a selected shop, using the current actor's own account. Returns structured products and IDs.", `{"type":"object","properties":{"deptId":{"type":"integer","minimum":1},"query":{"type":"string","minLength":1,"maxLength":1024}},"required":["deptId","query"],"additionalProperties":false}`},
		{"luckin_product_detail", "queryProductDetailInfo", "Read a selected Luckin product's available specifications and SKU at a shop. Returns structured data; does not add to a cart or send messages.", `{"type":"object","properties":{"deptId":{"type":"integer","minimum":1},"productId":{"type":"integer","minimum":1}},"required":["deptId","productId"],"additionalProperties":false}`},
		{"luckin_product_switch", "switchProduct", "Calculate a selected Luckin product specification change and return its SKU and quote. Does not place an order.", `{"type":"object","properties":{"deptId":{"type":"integer","minimum":1},"productId":{"type":"integer","minimum":1},"skuCode":{"type":"string","minLength":1,"maxLength":256},"amount":{"type":"integer","minimum":1,"maximum":50},"attrOperationParam":{"type":"object","properties":{"attributeId":{"type":"integer","minimum":1},"subAttr":{"type":"object","properties":{"attributeId":{"type":"integer","minimum":1},"operation":{"type":"integer","enum":[3]}},"required":["attributeId","operation"],"additionalProperties":false}},"required":["attributeId","subAttr"],"additionalProperties":false}},"required":["deptId","productId","skuCode","amount","attrOperationParam"],"additionalProperties":false}`},
		{"luckin_order_preview", "previewOrder", "Read Luckin's estimated price, selected/available coupons and pickup information for exact products. No order is created, paid or sent. Use prepare then approved create to submit.", luckinOrderSchema},
		{"luckin_order_detail", "queryOrderDetailInfo", "Query a Luckin order by its actual provider order ID using the current actor's own credential. Use this to reconcile a known order; never retry create blindly after an uncertain outcome.", `{"type":"object","properties":{"orderId":{"type":"string","minLength":1,"maxLength":256}},"required":["orderId"],"additionalProperties":false}`},
	}
	for _, q := range queries {
		q := q
		defs = append(defs, businessDefinition(q.name, q.description, q.schema, fp, func(ctx context.Context, in Invocation, raw string) (string, error) {
			out, err := o.Luckin.Query(ctx, businessIdentity(in), q.tool, json.RawMessage(raw))
			if err != nil {
				return "", err
			}
			return businessJSON(map[string]any{"data": out})
		}))
	}
	defs = append(defs, businessDefinition("luckin_order_prepare_create", "Freeze an exact Luckin order and current price into a signed five-minute preparation bound to this task, group and current actor's personal account. Returns payload, quote and opaque preparation. Does not create an order or send a card. Show the quote before requesting approval to create.", luckinOrderSchema, fp, func(ctx context.Context, in Invocation, raw string) (string, error) {
		out, err := o.Luckin.Prepare(ctx, businessIdentity(in), json.RawMessage(raw))
		if err != nil {
			return "", err
		}
		return businessJSON(out)
	}))
	commit := businessDefinition("luckin_order_create", "Create exactly one Luckin order from an unchanged signed preparation after explicit approval. High-impact effect: no automatic execution. Rechecks the same actor's credentials, expiry and current price; rejects changes. Creates an unpaid order only. Unknown outcomes must be reconciled in the Luckin app or order-detail query; never prepare/replay another create to recover.", `{"type":"object","properties":{"preparation":{"type":"string","minLength":1,"maxLength":32768}},"required":["preparation"],"additionalProperties":false}`, fp, func(ctx context.Context, in Invocation, raw string) (string, error) {
		var a struct {
			Preparation string `json:"preparation"`
		}
		if json.Unmarshal([]byte(raw), &a) != nil {
			return "", domain.ErrInvalid
		}
		out, err := o.Luckin.Commit(ctx, businessIdentity(in), a.Preparation)
		if err != nil {
			if errors.Is(err, domain.ErrEffectUnknown) {
				return "", err
			}
			// Commit returns ErrEffectUnknown for every ambiguous error after
			// createOrder has been attempted. All other errors are definite
			// pre-submission rejections, so completing this no-create receipt
			// permits a newly quoted, newly approved attempt in the same task.
			reason := "preflight_unavailable"
			switch {
			case errors.Is(err, business.ErrQuoteChanged):
				reason = "quote_changed"
			case errors.Is(err, business.ErrPreparationExpired):
				reason = "preparation_expired"
			case errors.Is(err, domain.ErrUnauthorized):
				reason = "account_or_preparation_mismatch"
			case errors.Is(err, domain.ErrInvalid):
				reason = "invalid_preparation"
			}
			return businessJSON(map[string]any{"created": false, "status": "rejected", "reason": reason, "requires_new_preparation": true})
		}
		return businessJSON(map[string]any{"data": out})
	})
	commit.SideEffect = true
	defs = append(defs, commit)
	return defs
}
