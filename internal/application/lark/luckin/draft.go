package luckin

import (
	"context"
	"encoding/json"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/mcpclient"
)

type DraftRequest struct {
	AppID           string
	BotOpenID       string
	ChatID          string
	RequesterOpenID string
	Credential      Credential
	Shop            ShopSelection
	Product         ProductOption
	Amount          int
	Now             time.Time
}

type DraftService struct {
	caller    ToolCaller
	serverURL string
}

func NewDraftService(caller ToolCaller, serverURL string) DraftService {
	return DraftService{caller: caller, serverURL: serverURL}
}

func (s DraftService) Draft(ctx context.Context, req DraftRequest) (PendingOrder, map[string]any, error) {
	amount := req.Amount
	if amount <= 0 {
		amount = 1
	}
	payload := createOrderPayload(req.Shop, req.Product, amount)

	preview := json.RawMessage(`{}`)
	if s.caller != nil {
		previewPayload, _ := json.Marshal(map[string]any{
			"deptId":      req.Shop.DeptID,
			"productList": []map[string]any{productItem(req.Product, amount)},
		})
		res, err := s.caller.CallTool(ctx, mcpclient.CallRequest{
			Server: mcpclient.ServerConfig{
				Name:    ServerName,
				URL:     s.remoteURL(),
				Headers: map[string]string{"Authorization": "Bearer " + req.Credential.Token},
				Timeout: DefaultTimeout(),
			},
			ToolName:  "previewOrder",
			Arguments: previewPayload,
		})
		if err != nil {
			return PendingOrder{}, nil, err
		}
		if data := ExtractData(res.Content); len(data) > 0 {
			preview = data
		}
	}

	order := NewPendingOrder(NewPendingOrderRequest{
		AppID:              req.AppID,
		BotOpenID:          req.BotOpenID,
		ChatID:             req.ChatID,
		RequesterOpenID:    req.RequesterOpenID,
		Credential:         req.Credential,
		CreateOrderPayload: payload,
		PreviewResult:      preview,
		Now:                req.Now,
	})
	return order, BuildPendingOrderCard(order), nil
}

func (s DraftService) remoteURL() string {
	if s.serverURL != "" {
		return s.serverURL
	}
	return ServerURL
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

func createOrderPayload(shop ShopSelection, product ProductOption, amount int) json.RawMessage {
	payload, _ := json.Marshal(map[string]any{
		"deptId":      shop.DeptID,
		"longitude":   shop.Longitude,
		"latitude":    shop.Latitude,
		"productList": []map[string]any{productItem(product, amount)},
	})
	return payload
}

func productItem(product ProductOption, amount int) map[string]any {
	return map[string]any{
		"amount":    amount,
		"productId": product.ProductID,
		"skuCode":   product.SkuCode,
	}
}
