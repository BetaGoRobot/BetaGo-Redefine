package mcpbridge

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/luckin"
	arktools "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/mcpclient"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestRegisterAddsAllowedTools(t *testing.T) {
	ins := arktools.New[larkim.P2MessageReceiveV1]()
	Register(ins, RegisterOptions{Policies: luckin.ToolPolicies()})
	specs := ins.Tools()

	foundCreateOrder := false
	foundPrepare := false
	for _, spec := range specs {
		if spec.GetToolFunction() == nil {
			continue
		}
		name := spec.GetToolFunction().Name
		if name == "createOrder" {
			foundCreateOrder = true
		}
		if name == "luckin_order_prepare_create" {
			foundPrepare = true
		}
	}
	if foundCreateOrder {
		t.Fatalf("raw createOrder was registered")
	}
	if !foundPrepare {
		t.Fatalf("prepare-create tool missing")
	}
	unit, ok := ins.Get("luckin_order_prepare_create")
	if !ok {
		t.Fatalf("prepare-create unit missing")
	}
	if unit.Parameters == nil || len(unit.Parameters.Props) == 0 {
		t.Fatalf("prepare-create tool params should expose the createOrder schema")
	}
	if _, ok := unit.Parameters.Props["productList"]; !ok {
		t.Fatalf("prepare-create tool params missing productList")
	}
}

func TestHandleShopSearchSendsShopCard(t *testing.T) {
	useWorkspaceConfigPath(t)
	var sawAuth string
	var sawTool string

	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "my-coffee", Version: "v0.0.1"}, nil)
	mcp.AddTool(mcpServer, &mcp.Tool{Name: "queryShopList"}, func(ctx context.Context, req *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
		sawTool = req.Params.Name
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: `{"code":0,"data":[{"deptId":245062453,"deptName":"AI点单专用","address":"北京安贞","longitude":116.39,"latitude":39.98}]}`}}}, nil, nil
	})
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpServer }, &mcp.StreamableHTTPOptions{
		Stateless:    true,
		JSONResponse: true,
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if v := r.Header.Get("Authorization"); v != "" {
			sawAuth = v
		}
		mcpHandler.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)

	resolver := &fakeResolver{credential: luckin.Credential{
		Scope: luckin.CredentialScope{Type: luckin.ScopePersonal, ID: "ou_user"},
		Token: "token-read",
	}}
	cards := &fakeCardSender{}
	policy, _ := luckin.PolicyByRobotTool("luckin_shop_search")
	h := handler{
		policy:    policy,
		client:    mcpclient.New(mcpclient.ClientOptions{HTTPClient: server.Client()}),
		resolver:  resolver,
		cards:     cards,
		geocoder:  &fakeGeocoder{point: luckin.GeoPoint{Longitude: 116.39, Latitude: 39.98}},
		serverURL: server.URL,
	}
	meta := &xhandler.BaseMetaData{ChatID: "oc_chat", OpenID: "ou_user", IsP2P: true}
	args, err := h.ParseTool(`{"locationText":"北京安贞环宇荟"}`)
	if err != nil {
		t.Fatalf("ParseTool error = %v", err)
	}

	if err := h.Handle(context.Background(), nil, meta, args); err != nil {
		t.Fatalf("Handle error = %v", err)
	}

	if sawAuth != "Bearer token-read" {
		t.Fatalf("Authorization = %q", sawAuth)
	}
	if sawTool != "queryShopList" {
		t.Fatalf("remote tool = %q", sawTool)
	}
	if !cards.called {
		t.Fatalf("shop select card was not sent")
	}
	got, ok := meta.GetExtra("luckin_shop_search_result")
	if !ok || got == "" {
		t.Fatalf("tool result missing")
	}
}

func TestHandleProductSearchRequiresShopSelection(t *testing.T) {
	useWorkspaceConfigPath(t)
	resolver := &fakeResolver{credential: luckin.Credential{
		Scope: luckin.CredentialScope{Type: luckin.ScopePersonal, ID: "ou_user"},
		Token: "token-read",
	}}
	cards := &fakeCardSender{}
	policy, _ := luckin.PolicyByRobotTool("luckin_product_search")
	h := handler{
		policy:   policy,
		resolver: resolver,
		cards:    cards,
		session:  &fakeSessionStore{},
	}
	meta := &xhandler.BaseMetaData{ChatID: "oc_chat", OpenID: "ou_user", IsP2P: true}
	args, _ := h.ParseTool(`{"query":"生椰拿铁"}`)
	if err := h.Handle(context.Background(), nil, meta, args); err != nil {
		t.Fatalf("Handle error = %v", err)
	}
	got, _ := meta.GetExtra("luckin_product_search_result")
	if got == "" {
		t.Fatalf("expected guidance result")
	}
	if cards.called {
		t.Fatalf("product card should not be sent without shop")
	}
}

func TestHandleProductSearchInjectsDeptIDFromSession(t *testing.T) {
	useWorkspaceConfigPath(t)
	var sawDeptID float64
	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "my-coffee", Version: "v0.0.1"}, nil)
	mcp.AddTool(mcpServer, &mcp.Tool{Name: "searchProductForMcp"}, func(ctx context.Context, req *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
		if v, ok := args["deptId"].(float64); ok {
			sawDeptID = v
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: `{"code":0,"data":[{"productId":5293,"productName":"生椰拿铁","skuCode":"SP-1"}]}`}}}, nil, nil
	})
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpServer }, &mcp.StreamableHTTPOptions{
		Stateless:    true,
		JSONResponse: true,
	})
	server := httptest.NewServer(mcpHandler)
	t.Cleanup(server.Close)

	resolver := &fakeResolver{credential: luckin.Credential{Token: "token-read"}}
	cards := &fakeCardSender{}
	session := &fakeSessionStore{shop: luckin.ShopSelection{DeptID: 245062453, DeptName: "AI点单专用"}, ok: true}
	policy, _ := luckin.PolicyByRobotTool("luckin_product_search")
	h := handler{
		policy:    policy,
		client:    mcpclient.New(mcpclient.ClientOptions{HTTPClient: server.Client()}),
		resolver:  resolver,
		cards:     cards,
		session:   session,
		serverURL: server.URL,
	}
	meta := &xhandler.BaseMetaData{ChatID: "oc_chat", OpenID: "ou_user"}
	args, _ := h.ParseTool(`{"query":"生椰拿铁"}`)
	if err := h.Handle(context.Background(), nil, meta, args); err != nil {
		t.Fatalf("Handle error = %v", err)
	}
	if sawDeptID != 245062453 {
		t.Fatalf("deptId not injected from session: %v", sawDeptID)
	}
	if !cards.called {
		t.Fatalf("product select card was not sent")
	}
}

func TestHandleMissingCredentialSendsBindCard(t *testing.T) {
	useWorkspaceConfigPath(t)
	resolver := &fakeResolver{err: luckin.ErrCredentialNotFound}
	cards := &fakeCardSender{}
	policy, _ := luckin.PolicyByRobotTool("luckin_shop_search")
	h := handler{
		policy:   policy,
		resolver: resolver,
		cards:    cards,
	}
	meta := &xhandler.BaseMetaData{ChatID: "oc_chat", OpenID: "ou_user"}
	args, _ := h.ParseTool(`{"deptName":"人民广场"}`)
	if err := h.Handle(context.Background(), nil, meta, args); err != nil {
		t.Fatalf("Handle error = %v", err)
	}
	if !cards.called {
		t.Fatalf("bind token card was not sent")
	}
}

func TestHandlePrepareCreateStoresPendingOrderWithoutRemoteCall(t *testing.T) {
	useWorkspaceConfigPath(t)
	serverCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serverCalled = true
		t.Fatalf("prepare-create should not call remote MCP")
	}))
	t.Cleanup(server.Close)

	resolver := &fakeResolver{credential: luckin.Credential{
		Scope: luckin.CredentialScope{Type: luckin.ScopeChat, ID: "oc_group"},
		Token: "token-create",
	}}
	pending := &fakePendingOrderService{}
	sender := &fakePendingOrderCardSender{}
	policy, _ := luckin.PolicyByRobotTool("luckin_order_prepare_create")
	h := handler{
		policy:    policy,
		client:    mcpclient.New(mcpclient.ClientOptions{}),
		resolver:  resolver,
		pending:   pending,
		sender:    sender,
		serverURL: server.URL,
	}
	meta := &xhandler.BaseMetaData{ChatID: "oc_group", OpenID: "ou_user"}
	args, err := h.ParseTool(`{"deptId":1,"productList":[{"amount":1}]}`)
	if err != nil {
		t.Fatalf("ParseTool error = %v", err)
	}

	if err := h.Handle(context.Background(), nil, meta, args); err != nil {
		t.Fatalf("Handle error = %v", err)
	}

	if serverCalled {
		t.Fatalf("remote MCP was called")
	}
	if !pending.called {
		t.Fatalf("pending order was not created")
	}
	if string(pending.order.CreateOrderPayload) != string(args.JSON) {
		t.Fatalf("pending order payload mismatch")
	}
	if pending.order.CredentialScope != resolver.credential.Scope {
		t.Fatalf("pending credential scope = %+v", pending.order.CredentialScope)
	}
	if pending.order.PayloadHash == "" || pending.order.ID == "" {
		t.Fatalf("pending id/hash missing")
	}
	if !sender.called || sender.order.ID != pending.order.ID {
		t.Fatalf("pending order confirmation card was not sent")
	}
	if resolver.request.ChatType != luckin.ChatTypeGroup {
		t.Fatalf("credential request chat type = %q", resolver.request.ChatType)
	}
	got, _ := meta.GetExtra("luckin_order_prepare_create_result")
	if got != "瑞幸订单确认卡片已发送，请由发起人确认后再创建订单" {
		t.Fatalf("prepare-create result = %q", got)
	}
}

func useWorkspaceConfigPath(t *testing.T) {
	t.Helper()
	configPath, err := filepath.Abs("../../../../.dev/config.toml")
	if err != nil {
		t.Fatalf("resolve config path: %v", err)
	}
	t.Setenv("BETAGO_CONFIG_PATH", configPath)
}

type fakeResolver struct {
	credential luckin.Credential
	request    luckin.CredentialRequest
	err        error
}

func (r *fakeResolver) Resolve(ctx context.Context, req luckin.CredentialRequest) (luckin.Credential, error) {
	r.request = req
	if r.err != nil {
		return luckin.Credential{}, r.err
	}
	return r.credential, nil
}

type fakeCardSender struct {
	called bool
	card   map[string]any
}

func (s *fakeCardSender) SendCard(ctx context.Context, data *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData, card map[string]any) error {
	s.called = true
	s.card = card
	return nil
}

type fakeSessionStore struct {
	shop luckin.ShopSelection
	ok   bool
	cart luckin.Cart
	cok  bool
}

func (s *fakeSessionStore) GetShop(ctx context.Context, key luckin.SessionKey) (luckin.ShopSelection, bool) {
	return s.shop, s.ok
}

func (s *fakeSessionStore) SetShop(ctx context.Context, key luckin.SessionKey, shop luckin.ShopSelection) {
	s.shop = shop
	s.ok = true
}

func (s *fakeSessionStore) ClearShop(ctx context.Context, key luckin.SessionKey) {
	s.ok = false
}

func (s *fakeSessionStore) GetCart(ctx context.Context, key luckin.SessionKey) (luckin.Cart, bool) {
	return s.cart, s.cok
}

func (s *fakeSessionStore) SetCart(ctx context.Context, key luckin.SessionKey, cart luckin.Cart) {
	s.cart = cart
	s.cok = true
}

func (s *fakeSessionStore) ClearCart(ctx context.Context, key luckin.SessionKey) {
	s.cok = false
}

type fakeGeocoder struct {
	point luckin.GeoPoint
	err   error
}

func (g *fakeGeocoder) Geocode(ctx context.Context, address string) (luckin.GeoPoint, error) {
	if g.err != nil {
		return luckin.GeoPoint{}, g.err
	}
	return g.point, nil
}

type fakePendingOrderService struct {
	called bool
	order  luckin.PendingOrder
}

func (s *fakePendingOrderService) CreatePendingOrder(ctx context.Context, order luckin.PendingOrder) error {
	s.called = true
	s.order = order
	return nil
}

type fakePendingOrderCardSender struct {
	called bool
	order  luckin.PendingOrder
}

func (s *fakePendingOrderCardSender) SendPendingOrderCard(ctx context.Context, data *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData, order luckin.PendingOrder) error {
	s.called = true
	s.order = order
	return nil
}
