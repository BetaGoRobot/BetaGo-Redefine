package atoms_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/atoms"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestUnknownConfiguredToolIsEffectful(t *testing.T) {
	r, e := atoms.New(atoms.Options{APIs: []atoms.APIConfig{{Name: "business_write", URL: "http://localhost", Method: "POST"}}})
	if e != nil {
		t.Fatal(e)
	}
	d := namedTool(t, r, "business_write")
	if !d.SideEffect || d.AutoExecute {
		t.Fatalf("unknown tool not gated: %+v", d)
	}
}

func TestAutomaticClassificationIsServerOwnedAndChangesExplicitDigests(t *testing.T) {
	for _, kind := range []string{"api", "mcp"} {
		t.Run(kind, func(t *testing.T) {
			makeRegistry := func(auto bool) *atoms.Registry {
				t.Helper()
				raw, _ := json.Marshal(map[string]any{"Name": "ordinary_action", "Description": "ordinary group action", "URL": "http://localhost", "ServerURL": "http://localhost", "ToolName": "action", "SideEffect": true, "AutoExecute": auto})
				var opts atoms.Options
				if kind == "api" {
					var c atoms.APIConfig
					if err := json.Unmarshal(raw, &c); err != nil {
						t.Fatal(err)
					}
					opts.APIs = []atoms.APIConfig{c}
				} else {
					var c atoms.MCPConfig
					if err := json.Unmarshal(raw, &c); err != nil {
						t.Fatal(err)
					}
					opts.MCPs = []atoms.MCPConfig{c}
				}
				r, err := atoms.New(opts)
				if err != nil {
					t.Fatal(err)
				}
				return r
			}
			manual, automatic := makeRegistry(false), makeRegistry(true)
			if namedTool(t, manual, "ordinary_action").Digest == namedTool(t, automatic, "ordinary_action").Digest {
				t.Fatal("automatic classification missing from explicit policy digest")
			}
			if _, err := namedTool(t, automatic, "ordinary_action").Validate(`{"auto_execute":true}`); err == nil {
				t.Fatal("model arguments accepted a server-owned policy override")
			}
			var policy any
			for _, p := range automatic.Policies() {
				if p.Name == "ordinary_action" {
					policy = p
				}
			}
			b, _ := json.Marshal(policy)
			var p map[string]any
			if json.Unmarshal(b, &p) != nil || p["AutoExecute"] != true || p["SideEffect"] != true {
				t.Fatal("automatic policy classification lost")
			}
		})
	}
}

func TestAutomaticClassificationRequiresExplicitSideEffect(t *testing.T) {
	for _, raw := range []string{`{"Name":"unsafe","URL":"http://localhost","ServerURL":"http://localhost","ToolName":"call","AutoExecute":true}`, `{"Name":"unsafe","URL":"http://localhost","ServerURL":"http://localhost","ToolName":"call","AutoExecute":true,"ReadOnly":true}`} {
		var api atoms.APIConfig
		var mcp atoms.MCPConfig
		if err := json.Unmarshal([]byte(raw), &api); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(raw), &mcp); err != nil {
			t.Fatal(err)
		}
		if _, err := atoms.New(atoms.Options{APIs: []atoms.APIConfig{api}}); err == nil {
			t.Fatal("unclassified API received automatic effect authority")
		}
		if _, err := atoms.New(atoms.Options{MCPs: []atoms.MCPConfig{mcp}}); err == nil {
			t.Fatal("unclassified MCP received automatic effect authority")
		}
	}
}

func TestMCPUsesDeclaredArgumentsAndServerOwnedIdentityHeaders(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "fixture", Version: "1"}, nil)
	called := false
	mcp.AddTool(server, &mcp.Tool{Name: "increment"}, func(_ context.Context, _ *mcp.CallToolRequest, args struct {
		Amount int `json:"amount"`
	}) (*mcp.CallToolResult, any, error) {
		called = true
		if args.Amount != 7 {
			t.Errorf("declared args lost: %+v", args)
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil, nil
	})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Agent-Chat-ID") != "chat" || r.Header.Get("X-Agent-Actor-ID") != "verified-owner" {
			t.Error("server caller/scope headers missing")
		}
		handler.ServeHTTP(w, r)
	}))
	defer httpServer.Close()
	reg, e := atoms.New(atoms.Options{MCPs: []atoms.MCPConfig{{Name: "write", ToolName: "increment", ServerURL: httpServer.URL, Headers: map[string]string{"x-agent-actor-id": "configured-other-actor", "X-AGENT-ACTOR-ID": "configured-uppercase-actor", "x-agent-chat-id": "configured-other-chat", "X-aGeNt-cHaT-Id": "configured-mixed-chat"}, Parameters: json.RawMessage(`{"type":"object","properties":{"amount":{"type":"integer"}},"required":["amount"],"additionalProperties":false}`)}}})
	if e != nil {
		t.Fatal(e)
	}
	d := namedTool(t, reg, "write")
	_, e = d.Run(context.Background(), atoms.Invocation{Task: domain.Task{ID: "task", Scope: domain.Scope{TenantID: "t", AppID: "a", ChatID: "chat", Visibility: "group"}, OwnerID: "verified-owner"}, ActorID: "verified-owner", ExecutionKey: "stable"}, `{"amount":7}`)
	if e != nil || !called {
		t.Fatalf("MCP declared arguments unusable: %v %v", called, e)
	}
}
