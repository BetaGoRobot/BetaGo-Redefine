package mcpbridge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/luckin"
	arktools "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/mcpclient"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
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
	if unit.Parameters == nil || !unit.Parameters.AdditionalProperties {
		t.Fatalf("prepare-create tool params should allow raw MCP arguments")
	}
}

func TestHandleReadToolCallsRemoteMCPWithResolvedCredential(t *testing.T) {
	useWorkspaceConfigPath(t)
	var sawAuth string
	var sawTool string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		var req struct {
			ID     int64 `json:"id"`
			Params struct {
				Name string `json:"name"`
			} `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode mcp request: %v", err)
		}
		sawTool = req.Params.Name
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"result": map[string]any{
				"content": []any{map[string]any{"type": "text", "text": "ok"}},
			},
		})
	}))
	t.Cleanup(server.Close)

	resolver := &fakeResolver{credential: luckin.Credential{
		Scope: luckin.CredentialScope{Type: luckin.ScopePersonal, ID: "ou_user"},
		Token: "token-read",
	}}
	pending := &fakePendingOrderService{}
	policy, _ := luckin.PolicyByRobotTool("luckin_shop_search")
	h := handler{
		policy:    policy,
		client:    mcpclient.New(mcpclient.ClientOptions{}),
		resolver:  resolver,
		pending:   pending,
		serverURL: server.URL,
	}
	meta := &xhandler.BaseMetaData{ChatID: "oc_chat", OpenID: "ou_user", IsP2P: true}
	args, err := h.ParseTool(`{"keyword":"人民广场"}`)
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
	if resolver.request.ChatType != luckin.ChatTypePrivate || resolver.request.ChatID != "oc_chat" || resolver.request.OpenID != "ou_user" {
		t.Fatalf("credential request mismatch: %+v", resolver.request)
	}
	if pending.called {
		t.Fatalf("read tool should not create pending order")
	}
	got, ok := meta.GetExtra("luckin_shop_search_result")
	if !ok || got == "" {
		t.Fatalf("tool result missing")
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
	policy, _ := luckin.PolicyByRobotTool("luckin_order_prepare_create")
	h := handler{
		policy:    policy,
		client:    mcpclient.New(mcpclient.ClientOptions{}),
		resolver:  resolver,
		pending:   pending,
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
}

func (r *fakeResolver) Resolve(ctx context.Context, req luckin.CredentialRequest) (luckin.Credential, error) {
	r.request = req
	return r.credential, nil
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
