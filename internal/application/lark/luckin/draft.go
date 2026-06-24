package luckin

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/mcpclient"
)

type DraftRequest struct {
	AppID           string
	BotOpenID       string
	ChatID          string
	InitiatorOpenID string
	Credential      Credential
	Shop            ShopSelection
	Items           []CartItem
	CouponCodeList  []string
	Now             time.Time
}

type DraftService struct {
	caller    ToolCaller
	serverURL string
}

func NewDraftService(caller ToolCaller, serverURL string) DraftService {
	return DraftService{caller: caller, serverURL: serverURL}
}

// Draft 预览购物车（含可选优惠券）并生成待确认订单与确认卡片。
func (s DraftService) Draft(ctx context.Context, req DraftRequest) (PendingOrder, map[string]any, error) {
	items := req.Items
	if len(items) == 0 {
		return PendingOrder{}, nil, errors.New("购物车为空")
	}
	payload := createOrderPayload(req.Shop, items, req.CouponCodeList)

	preview := json.RawMessage(`{}`)
	if s.caller != nil {
		data, err := s.previewCart(ctx, req.Credential, req.Shop, items, req.CouponCodeList)
		if err != nil {
			return PendingOrder{}, nil, err
		}
		if len(data) > 0 {
			preview = data
		}
	}

	order := NewPendingOrder(NewPendingOrderRequest{
		AppID:              req.AppID,
		BotOpenID:          req.BotOpenID,
		ChatID:             req.ChatID,
		InitiatorOpenID:    req.InitiatorOpenID,
		Credential:         req.Credential,
		CreateOrderPayload: payload,
		PreviewResult:      preview,
		CartSnapshot:       items,
		Now:                req.Now,
	})
	return order, BuildPendingOrderCard(order), nil
}

// previewCart 调用 previewOrder 返回业务 data（含 discountPrice / couponCodeList 等）。
// 即使显式传空 couponCodeList，瑞幸接口仍可能返回平台自动优惠后的预估实付；
// 卡片展示层会把它标注为“瑞幸预估实付”，避免误认为这是未优惠原价。
func (s DraftService) previewCart(ctx context.Context, cred Credential, shop ShopSelection, items []CartItem, coupons []string) (json.RawMessage, error) {
	if s.caller == nil {
		return nil, nil
	}
	if coupons == nil {
		coupons = []string{}
	}
	previewArgs := map[string]any{
		"deptId":         shop.DeptID,
		"productList":    productItems(items),
		"couponCodeList": coupons,
	}
	previewPayload, _ := json.Marshal(previewArgs)
	res, err := s.caller.CallTool(ctx, s.callReq(cred, "previewOrder", previewPayload))
	if err != nil {
		return nil, err
	}
	return ExtractData(res.Content), nil
}

// PreviewCart 预览购物车并返回（预览 data、可用优惠券编码列表）。
func (s DraftService) PreviewCart(ctx context.Context, cred Credential, shop ShopSelection, items []CartItem, coupons []string) (json.RawMessage, []string, error) {
	data, err := s.previewCart(ctx, cred, shop, items, coupons)
	if err != nil {
		return nil, nil, err
	}
	return data, AvailableCouponsFromPreview(data), nil
}

func (s DraftService) remoteURL() string {
	if s.serverURL != "" {
		return s.serverURL
	}
	return ServerURL
}

// SearchShops 按经纬度查询附近门店，用于卡片内的门店搜索/重选表单。
func (s DraftService) SearchShops(ctx context.Context, cred Credential, point GeoPoint, keyword string, limit int) ([]ShopOption, error) {
	if s.caller == nil {
		return nil, nil
	}
	args := map[string]any{
		"longitude": point.Longitude,
		"latitude":  point.Latitude,
	}
	if keyword != "" {
		args["deptName"] = keyword
	}
	payload, _ := json.Marshal(args)
	res, err := s.caller.CallTool(ctx, s.callReq(cred, "queryShopList", payload))
	if err != nil {
		return nil, err
	}
	return ShopOptionsFromResult(res.Content, limit), nil
}

// SearchProducts 按门店 + 关键词搜索商品，用于卡片内的商品搜索表单。
func (s DraftService) SearchProducts(ctx context.Context, cred Credential, shop ShopSelection, query string, limit int) ([]ProductOption, error) {
	if s.caller == nil {
		return nil, nil
	}
	payload, _ := json.Marshal(map[string]any{
		"deptId": shop.DeptID,
		"query":  query,
	})
	res, err := s.caller.CallTool(ctx, mcpclient.CallRequest{
		Server: mcpclient.ServerConfig{
			Name:    ServerName,
			URL:     s.remoteURL(),
			Headers: map[string]string{"Authorization": "Bearer " + cred.Token},
			Timeout: DefaultTimeout(),
		},
		ToolName:  "searchProductForMcp",
		Arguments: payload,
	})
	if err != nil {
		return nil, err
	}
	return ProductOptionsFromResult(res.Content, limit), nil
}

// ProductDetail 查询商品详情（含规格属性）。
func (s DraftService) ProductDetail(ctx context.Context, cred Credential, shop ShopSelection, productID int64) (ProductDetail, error) {
	if s.caller == nil {
		return ProductDetail{}, nil
	}
	payload, _ := json.Marshal(map[string]any{
		"deptId":    shop.DeptID,
		"productId": productID,
	})
	res, err := s.caller.CallTool(ctx, s.callReq(cred, "queryProductDetailInfo", payload))
	if err != nil {
		return ProductDetail{}, err
	}
	return ProductDetailFromResult(res.Content), nil
}

// SwitchSpec 按用户选中的规格切换商品，返回切换后的商品详情（含最新价格/sku）。
func (s DraftService) SwitchSpec(ctx context.Context, cred Credential, shop ShopSelection, productID int64, skuCode string, selections map[int64]int64) (ProductDetail, error) {
	if s.caller == nil {
		return ProductDetail{}, nil
	}
	detail := ProductDetail{ProductID: productID, SkuCode: skuCode}
	for attrID, subID := range selections {
		payload, _ := json.Marshal(map[string]any{
			"deptId":    shop.DeptID,
			"productId": productID,
			"skuCode":   detail.SkuCode,
			"attrOperationParam": map[string]any{
				"attributeId": attrID,
				"subAttr": map[string]any{
					"attributeId": subID,
					"operation":   3,
				},
			},
			"amount": 1,
		})
		res, err := s.caller.CallTool(ctx, s.callReq(cred, "switchProduct", payload))
		if err != nil {
			return ProductDetail{}, err
		}
		switched := ProductDetailFromResult(res.Content)
		if switched.ProductID != 0 {
			detail = switched
		}
	}
	return detail, nil
}

func (s DraftService) callReq(cred Credential, tool string, payload json.RawMessage) mcpclient.CallRequest {
	return mcpclient.CallRequest{
		Server: mcpclient.ServerConfig{
			Name:    ServerName,
			URL:     s.remoteURL(),
			Headers: map[string]string{"Authorization": "Bearer " + cred.Token},
			Timeout: DefaultTimeout(),
		},
		ToolName:  tool,
		Arguments: payload,
	}
}

// OrderDetail 查询订单详情（状态/取餐码/预计时间等）。
func (s DraftService) OrderDetail(ctx context.Context, cred Credential, orderID string) (OrderDetail, error) {
	if s.caller == nil {
		return OrderDetail{}, nil
	}
	payload, _ := json.Marshal(map[string]any{"orderId": orderID})
	res, err := s.caller.CallTool(ctx, s.callReq(cred, "queryOrderDetailInfo", payload))
	if err != nil {
		return OrderDetail{}, err
	}
	return OrderDetailFromResult(res.Content), nil
}

func createOrderPayload(shop ShopSelection, items []CartItem, couponCodeList []string) json.RawMessage {
	if couponCodeList == nil {
		couponCodeList = []string{}
	}
	args := map[string]any{
		"deptId":         shop.DeptID,
		"longitude":      shop.Longitude,
		"latitude":       shop.Latitude,
		"productList":    productItems(items),
		"couponCodeList": couponCodeList,
	}
	payload, _ := json.Marshal(args)
	return payload
}

func productItems(items []CartItem) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		amount := item.Amount
		if amount <= 0 {
			amount = 1
		}
		out = append(out, map[string]any{
			"amount":    amount,
			"productId": item.ProductID,
			"skuCode":   item.SkuCode,
		})
	}
	return out
}
