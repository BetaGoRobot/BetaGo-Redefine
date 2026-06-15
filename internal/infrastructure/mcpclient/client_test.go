package mcpclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClientListToolsAndCallTool(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer token-1" {
			t.Fatalf("Authorization = %q", got)
		}
		var req jsonRPCRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "tools/list":
			_ = json.NewEncoder(w).Encode(jsonRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result:  json.RawMessage(`{"tools":[{"name":"queryShopList","description":"shops","inputSchema":{"type":"object"}}]}`),
			})
		case "tools/call":
			_ = json.NewEncoder(w).Encode(jsonRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result:  json.RawMessage(`{"content":[{"type":"text","text":"{\"ok\":true}"}]}`),
			})
		default:
			t.Fatalf("unexpected method %s", req.Method)
		}
	}))
	defer server.Close()

	client := New(ClientOptions{HTTPClient: server.Client()})
	cfg := ServerConfig{
		Name:    "my-coffee",
		URL:     server.URL,
		Headers: map[string]string{"Authorization": "Bearer token-1"},
		Timeout: time.Second,
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
		Arguments: json.RawMessage(`{"longitude":118.1,"latitude":24.1}`),
	})
	if err != nil {
		t.Fatalf("CallTool error = %v", err)
	}
	if string(res.Content) == "" || !json.Valid(res.Raw) {
		t.Fatalf("invalid result: %+v", res)
	}
}

func TestClientNormalizesUnauthorizedAndRemoteErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"bad token"}`))
	}))
	defer server.Close()

	client := New(ClientOptions{HTTPClient: server.Client()})
	_, err := client.ListTools(context.Background(), ServerConfig{Name: "my-coffee", URL: server.URL})
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
}

func TestClientNormalizesJSONRPCErrors(t *testing.T) {
	tests := []struct {
		name    string
		message string
		want    error
	}{
		{name: "tool not found", message: "tool not found: missingTool", want: ErrToolNotFound},
		{name: "invalid arguments", message: "invalid arguments: missing shopId", want: ErrInvalidArguments},
		{name: "remote", message: "upstream failed", want: ErrRemote},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(jsonRPCResponse{
					JSONRPC: "2.0",
					ID:      1,
					Error:   &jsonRPCError{Code: -32000, Message: tt.message},
				})
			}))
			defer server.Close()

			client := New(ClientOptions{HTTPClient: server.Client()})
			_, err := client.CallTool(context.Background(), CallRequest{
				Server:   ServerConfig{Name: "my-coffee", URL: server.URL},
				ToolName: "queryShopList",
			})
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestClientNormalizesTimeoutAndProtocolErrors(t *testing.T) {
	t.Run("timeout", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(50 * time.Millisecond)
		}))
		defer server.Close()

		client := New(ClientOptions{HTTPClient: server.Client()})
		_, err := client.ListTools(context.Background(), ServerConfig{
			Name:    "my-coffee",
			URL:     server.URL,
			Timeout: time.Millisecond,
		})
		if !errors.Is(err, ErrTimeout) {
			t.Fatalf("err = %v, want ErrTimeout", err)
		}
	})

	t.Run("protocol", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`not-json`))
		}))
		defer server.Close()

		client := New(ClientOptions{HTTPClient: server.Client()})
		_, err := client.ListTools(context.Background(), ServerConfig{Name: "my-coffee", URL: server.URL})
		if !errors.Is(err, ErrProtocol) {
			t.Fatalf("err = %v, want ErrProtocol", err)
		}
	})
}
