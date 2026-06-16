package mcpclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type shopArgs struct {
	Keyword string `json:"keyword,omitempty"`
}

func newTestMCPServer(t *testing.T, register func(*mcp.Server)) (*httptest.Server, *Client) {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "my-coffee", Version: "v0.0.1"}, nil)
	register(server)
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{
		Stateless:    true,
		JSONResponse: true,
	})
	httpServer := httptest.NewServer(handler)
	t.Cleanup(httpServer.Close)
	return httpServer, New(ClientOptions{HTTPClient: httpServer.Client()})
}

func TestClientListToolsAndCallTool(t *testing.T) {
	var sawAuth string
	httpServer, _ := newTestMCPServer(t, func(s *mcp.Server) {
		mcp.AddTool(s, &mcp.Tool{Name: "queryShopList", Description: "shops"}, func(ctx context.Context, req *mcp.CallToolRequest, args shopArgs) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: `{"ok":true}`}}}, nil, nil
		})
	})

	authObserver := &authCapture{base: httpServer.Client().Transport, target: &sawAuth}
	hc := *httpServer.Client()
	hc.Transport = authObserver
	client := New(ClientOptions{HTTPClient: &hc})

	cfg := ServerConfig{
		Name:    "my-coffee",
		URL:     httpServer.URL,
		Headers: map[string]string{"Authorization": "Bearer token-1"},
		Timeout: 5 * time.Second,
	}

	tools, err := client.ListTools(context.Background(), cfg)
	if err != nil {
		t.Fatalf("ListTools error = %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "queryShopList" {
		t.Fatalf("tools = %+v", tools)
	}

	res, err := client.CallTool(context.Background(), CallRequest{
		Server:    cfg,
		ToolName:  "queryShopList",
		Arguments: json.RawMessage(`{"keyword":"人民广场"}`),
	})
	if err != nil {
		t.Fatalf("CallTool error = %v", err)
	}
	if string(res.Content) == "" || !json.Valid(res.Raw) {
		t.Fatalf("invalid result: %+v", res)
	}
	if sawAuth != "Bearer token-1" {
		t.Fatalf("Authorization header = %q", sawAuth)
	}
}

type authCapture struct {
	base   http.RoundTripper
	target *string
}

func (a *authCapture) RoundTrip(req *http.Request) (*http.Response, error) {
	if v := req.Header.Get("Authorization"); v != "" {
		*a.target = v
	}
	base := a.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(req)
}

func TestClientNormalizesUnauthorizedErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"oauth token is not found"}`))
	}))
	defer server.Close()

	client := New(ClientOptions{HTTPClient: server.Client()})
	_, err := client.ListTools(context.Background(), ServerConfig{Name: "my-coffee", URL: server.URL})
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
}

func TestClientNormalizesToolErrors(t *testing.T) {
	httpServer, client := newTestMCPServer(t, func(s *mcp.Server) {
		mcp.AddTool(s, &mcp.Tool{Name: "queryShopList"}, func(ctx context.Context, req *mcp.CallToolRequest, args shopArgs) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{&mcp.TextContent{Text: "upstream failed"}},
			}, nil, nil
		})
	})

	_, err := client.CallTool(context.Background(), CallRequest{
		Server:   ServerConfig{Name: "my-coffee", URL: httpServer.URL},
		ToolName: "queryShopList",
	})
	if !errors.Is(err, ErrRemote) {
		t.Fatalf("err = %v, want ErrRemote", err)
	}
}

func TestClientNormalizesUnknownTool(t *testing.T) {
	httpServer, client := newTestMCPServer(t, func(s *mcp.Server) {
		mcp.AddTool(s, &mcp.Tool{Name: "queryShopList"}, func(ctx context.Context, req *mcp.CallToolRequest, args shopArgs) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil, nil
		})
	})

	_, err := client.CallTool(context.Background(), CallRequest{
		Server:   ServerConfig{Name: "my-coffee", URL: httpServer.URL},
		ToolName: "missingTool",
	})
	if !errors.Is(err, ErrToolNotFound) {
		t.Fatalf("err = %v, want ErrToolNotFound", err)
	}
}

func TestClientNormalizesTimeout(t *testing.T) {
	httpServer, client := newTestMCPServer(t, func(s *mcp.Server) {
		mcp.AddTool(s, &mcp.Tool{Name: "slow"}, func(ctx context.Context, req *mcp.CallToolRequest, args shopArgs) (*mcp.CallToolResult, any, error) {
			select {
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			case <-time.After(2 * time.Second):
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil, nil
			}
		})
	})

	_, err := client.CallTool(context.Background(), CallRequest{
		Server:   ServerConfig{Name: "my-coffee", URL: httpServer.URL, Timeout: 50 * time.Millisecond},
		ToolName: "slow",
	})
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout", err)
	}
}

func TestClientRejectsEmptyURL(t *testing.T) {
	client := New(ClientOptions{})
	_, err := client.ListTools(context.Background(), ServerConfig{Name: "my-coffee"})
	if !errors.Is(err, ErrProtocol) {
		t.Fatalf("err = %v, want ErrProtocol", err)
	}
}

func TestClientPreservesContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	client := New(ClientOptions{HTTPClient: http.DefaultClient})
	_, err := client.ListTools(ctx, ServerConfig{Name: "my-coffee", URL: "http://127.0.0.1:1"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v, did not want ErrTimeout", err)
	}
}
