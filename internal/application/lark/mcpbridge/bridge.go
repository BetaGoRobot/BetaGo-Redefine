package mcpbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/luckin"
	arktools "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/mcpclient"
	cardactionproto "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xcommand"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

type CredentialResolver interface {
	Resolve(context.Context, luckin.CredentialRequest) (luckin.Credential, error)
}

type PendingOrderService interface {
	CreatePendingOrder(context.Context, luckin.PendingOrder) error
}

type PendingOrderCardSender interface {
	SendPendingOrderCard(context.Context, *larkim.P2MessageReceiveV1, *xhandler.BaseMetaData, luckin.PendingOrder) error
}

// CardSender 发送任意瑞幸交互卡片（门店选择、商品选择、绑定引导）。
type CardSender interface {
	SendCard(context.Context, *larkim.P2MessageReceiveV1, *xhandler.BaseMetaData, map[string]any) error
}

// EphemeralCardSender 发送仅指定用户可见的临时卡（用于绑定 token 引导）。
type EphemeralCardSender func(ctx context.Context, chatID, openID string, card any) (string, error)

type RegisterOptions struct {
	Policies  []luckin.ToolPolicy
	Client    *mcpclient.Client
	Resolver  CredentialResolver
	Pending   PendingOrderService
	Sender    PendingOrderCardSender
	Cards     CardSender
	Ephemeral EphemeralCardSender
	Session   luckin.SessionStore
	Geocoder  luckin.Geocoder
	Images    luckin.ImageUploader
	SystemURL string
}

type rawArgs struct {
	JSON json.RawMessage
}

type handler struct {
	policy    luckin.ToolPolicy
	client    *mcpclient.Client
	resolver  CredentialResolver
	pending   PendingOrderService
	sender    PendingOrderCardSender
	cards     CardSender
	ephemeral EphemeralCardSender
	session   luckin.SessionStore
	geocoder  luckin.Geocoder
	images    luckin.ImageUploader
	serverURL string
}

func Register(ins *arktools.Impl[larkim.P2MessageReceiveV1], opts RegisterOptions) {
	if ins == nil {
		return
	}
	for _, policy := range opts.Policies {
		if !policy.DirectLLM {
			continue
		}
		xcommand.RegisterTool(ins, handler{
			policy:    policy,
			client:    opts.Client,
			resolver:  opts.Resolver,
			pending:   opts.Pending,
			sender:    opts.Sender,
			cards:     opts.Cards,
			ephemeral: opts.Ephemeral,
			session:   opts.Session,
			geocoder:  opts.Geocoder,
			images:    opts.Images,
			serverURL: opts.SystemURL,
		})
	}
}

func (h handler) ParseTool(raw string) (rawArgs, error) {
	if raw == "" {
		raw = "{}"
	}
	if !json.Valid([]byte(raw)) {
		return rawArgs{}, fmt.Errorf("invalid tool arguments JSON")
	}
	return rawArgs{JSON: json.RawMessage(raw)}, nil
}

func (h handler) ToolSpec() xcommand.ToolSpec {
	params := h.policy.Params
	if params == nil {
		params = arktools.NewParams("object")
		params.AdditionalProperties = true
	}
	return xcommand.ToolSpec{
		Name:   h.policy.RobotToolName,
		Desc:   h.policy.Description,
		Params: params,
		Result: func(metaData *xhandler.BaseMetaData) string {
			if metaData == nil {
				return ""
			}
			result, _ := metaData.GetExtra(h.policy.RobotToolName + "_result")
			return result
		},
	}
}

func (h handler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg rawArgs) error {
	if metaData == nil {
		return nil
	}
	if h.resolver == nil {
		return fmt.Errorf("luckin credential resolver is not configured")
	}
	req := credentialRequestFromMessage(data, metaData)
	cred, err := h.resolver.Resolve(ctx, req)
	if err != nil {
		if errors.Is(err, luckin.ErrCredentialNotFound) {
			return h.guideBindToken(ctx, data, metaData, req)
		}
		return err
	}

	switch h.policy.RobotToolName {
	case "luckin_bind_token_guide":
		return h.guideBindToken(ctx, data, metaData, req)
	case "luckin_shop_search":
		return h.handleShopSearch(ctx, data, metaData, cred, arg)
	case "luckin_product_search":
		return h.handleProductSearch(ctx, data, metaData, req, cred, arg)
	}

	if h.policy.HighRisk {
		return h.handlePrepareCreate(ctx, data, metaData, req, cred, arg)
	}
	return h.callRemoteAndStore(ctx, metaData, cred, arg.JSON)
}

func (h handler) handleShopSearch(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, cred luckin.Credential, arg rawArgs) error {
	locationText := stringArg(arg.JSON, "locationText")
	if locationText == "" {
		metaData.SetExtra(h.policy.RobotToolName+"_result", "请提供一个用于定位的地点描述，例如“上海人民广场”")
		return nil
	}
	if h.geocoder == nil {
		metaData.SetExtra(h.policy.RobotToolName+"_result", "门店定位服务未配置，无法按地点查询门店")
		return nil
	}
	point, err := h.geocoder.Geocode(ctx, locationText)
	if err != nil {
		metaData.SetExtra(h.policy.RobotToolName+"_result", "无法定位“"+locationText+"”，请换个更具体的地点描述")
		return nil
	}

	payload := injectField(arg.JSON, "longitude", point.Longitude)
	payload = injectField(payload, "latitude", point.Latitude)
	payload = removeField(payload, "locationText")

	res, err := h.callRemote(ctx, cred, payload)
	if err != nil {
		return err
	}
	shops := luckin.ShopOptionsFromResult(res.Content, 5)
	if h.cards != nil {
		if err := h.cards.SendCard(ctx, data, metaData, luckin.BuildShopSelectCard(locationText, shops)); err != nil {
			return err
		}
	}
	if len(shops) == 0 {
		metaData.SetExtra(h.policy.RobotToolName+"_result", "附近未找到门店，已提示用户更换地点")
	} else {
		metaData.SetExtra(h.policy.RobotToolName+"_result", "已发送门店选择卡片，等待用户点选门店")
	}
	return nil
}

func (h handler) handleProductSearch(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, req luckin.CredentialRequest, cred luckin.Credential, arg rawArgs) error {
	shop, ok := h.lookupShop(ctx, req)
	if !ok {
		metaData.SetExtra(h.policy.RobotToolName+"_result", "用户还没有选择门店，请先调用 luckin_shop_search 让用户选店")
		return nil
	}
	payload := injectField(arg.JSON, "deptId", shop.DeptID)
	res, err := h.callRemote(ctx, cred, payload)
	if err != nil {
		return err
	}
	products := luckin.ProductOptionsFromResult(res.Content, 5)
	imageKeys := luckin.UploadProductImages(ctx, h.images, products)
	if h.cards != nil {
		if err := h.cards.SendCard(ctx, data, metaData, luckin.BuildProductSelectCard(shop, products, imageKeys)); err != nil {
			return err
		}
	}
	if len(products) == 0 {
		metaData.SetExtra(h.policy.RobotToolName+"_result", "未找到匹配商品，已提示用户更换关键词")
	} else {
		metaData.SetExtra(h.policy.RobotToolName+"_result", "已发送商品选择卡片，等待用户点选商品")
	}
	return nil
}

func (h handler) handlePrepareCreate(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, req luckin.CredentialRequest, cred luckin.Credential, arg rawArgs) error {
	if h.pending == nil {
		return fmt.Errorf("luckin pending order service is not configured")
	}
	payload := arg.JSON
	if shop, ok := h.lookupShop(ctx, req); ok {
		payload = injectField(payload, "deptId", shop.DeptID)
		payload = injectField(payload, "longitude", shop.Longitude)
		payload = injectField(payload, "latitude", shop.Latitude)
	}
	identity := botidentity.Current()
	order := luckin.NewPendingOrder(luckin.NewPendingOrderRequest{
		AppID:              identity.AppID,
		BotOpenID:          identity.BotOpenID,
		ChatID:             metaData.ChatID,
		RequesterOpenID:    metaData.OpenID,
		Credential:         cred,
		CreateOrderPayload: payload,
		PreviewResult:      json.RawMessage(`{}`),
	})
	if err := h.pending.CreatePendingOrder(ctx, order); err != nil {
		return err
	}
	if h.sender != nil {
		if err := h.sender.SendPendingOrderCard(ctx, data, metaData, order); err != nil {
			return err
		}
	}
	metaData.SetExtra(h.policy.RobotToolName+"_result", "瑞幸订单确认卡片已发送，请由发起人确认后再创建订单")
	return nil
}

func (h handler) callRemoteAndStore(ctx context.Context, metaData *xhandler.BaseMetaData, cred luckin.Credential, payload json.RawMessage) error {
	res, err := h.callRemote(ctx, cred, payload)
	if err != nil {
		return err
	}
	metaData.SetExtra(h.policy.RobotToolName+"_result", string(res.Content))
	return nil
}

func (h handler) callRemote(ctx context.Context, cred luckin.Credential, payload json.RawMessage) (mcpclient.CallResult, error) {
	if h.client == nil {
		return mcpclient.CallResult{}, fmt.Errorf("luckin mcp client is not configured")
	}
	return h.client.CallTool(ctx, mcpclient.CallRequest{
		Server: mcpclient.ServerConfig{
			Name:    luckin.ServerName,
			URL:     h.remoteURL(),
			Headers: map[string]string{"Authorization": "Bearer " + cred.Token},
			Timeout: luckin.DefaultTimeout(),
		},
		ToolName:  h.policy.MCPToolName,
		Arguments: payload,
	})
}

func (h handler) guideBindToken(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, req luckin.CredentialRequest) error {
	// 绑定卡含个人 token 输入，使用临时卡仅发起人可见，避免群内泄露。
	bindCard := luckin.BuildBindTokenCard(req.ChatType)
	if h.ephemeral != nil {
		msgID, err := h.ephemeral(ctx, req.ChatID, req.OpenID, bindCard)
		if err == nil {
			setBindTokenCardDismissID(bindCard, msgID)
			metaData.SetExtra(h.policy.RobotToolName+"_result", "用户尚未绑定瑞幸账号，已发送绑定引导卡片，绑定后请重试")
			return nil
		}
	}
	// 临时卡不可用或失败时降级为普通卡片，保证引导可达。
	if h.cards != nil {
		if err := h.cards.SendCard(ctx, data, metaData, bindCard); err != nil {
			return err
		}
	}
	metaData.SetExtra(h.policy.RobotToolName+"_result", "用户尚未绑定瑞幸账号，已发送绑定引导卡片，绑定后请重试")
	return nil
}

func setBindTokenCardDismissID(card map[string]any, messageID string) {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return
	}
	body, _ := card["body"].(map[string]any)
	elements, _ := body["elements"].([]any)
	for _, element := range elements {
		form, ok := element.(map[string]any)
		if !ok || form["tag"] != "form" {
			continue
		}
		formElements, _ := form["elements"].([]any)
		for _, formElement := range formElements {
			row, ok := formElement.(map[string]any)
			if !ok || row["tag"] != "column_set" {
				continue
			}
			columns, _ := row["columns"].([]any)
			for _, col := range columns {
				column, _ := col.(map[string]any)
				colElements, _ := column["elements"].([]any)
				for _, colElement := range colElements {
					button, ok := colElement.(map[string]any)
					if !ok || button["tag"] != "button" {
						continue
					}
					setButtonCallbackValue(button, cardactionproto.IDField, messageID)
				}
			}
		}
	}
}

func setButtonCallbackValue(button map[string]any, key, value string) {
	behaviors, _ := button["behaviors"].([]any)
	for _, behavior := range behaviors {
		b, ok := behavior.(map[string]any)
		if !ok || b["type"] != "callback" {
			continue
		}
		callbackValue, ok := b["value"].(map[string]any)
		if !ok {
			continue
		}
		callbackValue[key] = value
	}
}

func (h handler) lookupShop(ctx context.Context, req luckin.CredentialRequest) (luckin.ShopSelection, bool) {
	if h.session == nil {
		return luckin.ShopSelection{}, false
	}
	return h.session.GetShop(ctx, luckin.NewSessionKey(req))
}

func (h handler) remoteURL() string {
	if h.serverURL != "" {
		return h.serverURL
	}
	return luckin.ServerURL
}

func credentialRequestFromMessage(data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData) luckin.CredentialRequest {
	req := luckin.CredentialRequest{}
	identity := botidentity.Current()
	req.AppID = identity.AppID
	req.BotOpenID = identity.BotOpenID
	if metaData != nil {
		req.ChatID = metaData.ChatID
		req.OpenID = metaData.OpenID
		if metaData.IsP2P {
			req.ChatType = luckin.ChatTypePrivate
		} else {
			req.ChatType = luckin.ChatTypeGroup
		}
	}
	if data != nil && data.Event != nil {
		if data.Event.Message != nil {
			if req.ChatID == "" && data.Event.Message.ChatId != nil {
				req.ChatID = *data.Event.Message.ChatId
			}
			if data.Event.Message.ChatType != nil {
				if *data.Event.Message.ChatType == "p2p" {
					req.ChatType = luckin.ChatTypePrivate
				} else {
					req.ChatType = luckin.ChatTypeGroup
				}
			}
		}
		if req.OpenID == "" {
			req.OpenID = botidentity.MessageSenderOpenID(data)
		}
	}
	if req.ChatType == "" {
		req.ChatType = luckin.ChatTypeGroup
	}
	return req
}

func stringArg(raw json.RawMessage, key string) string {
	if len(raw) == 0 {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return ""
	}
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func injectField(raw json.RawMessage, key string, value any) json.RawMessage {
	m := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &m); err != nil {
			m = map[string]any{}
		}
	}
	m[key] = value
	out, err := json.Marshal(m)
	if err != nil {
		return raw
	}
	return out
}

func removeField(raw json.RawMessage, key string) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	m := map[string]any{}
	if err := json.Unmarshal(raw, &m); err != nil {
		return raw
	}
	delete(m, key)
	out, err := json.Marshal(m)
	if err != nil {
		return raw
	}
	return out
}
