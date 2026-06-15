package mcpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
)

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type listToolsResult struct {
	Tools []Tool `json:"tools"`
}

type callToolParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

type callToolResult struct {
	Content json.RawMessage `json:"content"`
}

var nextID atomic.Int64

func (c *Client) ListTools(ctx context.Context, server ServerConfig) ([]Tool, error) {
	raw, err := c.do(ctx, server, "tools/list", nil)
	if err != nil {
		return nil, err
	}
	var out listToolsResult
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("%w: decode tools/list: %v", ErrProtocol, err)
	}
	return out.Tools, nil
}

func (c *Client) CallTool(ctx context.Context, req CallRequest) (CallResult, error) {
	params, err := json.Marshal(callToolParams{Name: req.ToolName, Arguments: req.Arguments})
	if err != nil {
		return CallResult{}, fmt.Errorf("%w: encode call params: %v", ErrInvalidArguments, err)
	}
	raw, err := c.do(ctx, req.Server, "tools/call", params)
	if err != nil {
		return CallResult{}, err
	}
	var out callToolResult
	_ = json.Unmarshal(raw, &out)
	if len(out.Content) == 0 {
		out.Content = raw
	}
	return CallResult{Content: out.Content, Raw: raw}, nil
}

func (c *Client) do(ctx context.Context, server ServerConfig, method string, params json.RawMessage) (json.RawMessage, error) {
	if strings.TrimSpace(server.URL) == "" {
		return nil, fmt.Errorf("%w: empty server url", ErrProtocol)
	}
	if server.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, server.Timeout)
		defer cancel()
	}
	body, err := json.Marshal(jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      nextID.Add(1),
		Method:  method,
		Params:  params,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: encode request: %v", ErrProtocol, err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%w: build request: %v", ErrProtocol, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range server.Headers {
		httpReq.Header.Set(k, v)
	}
	resp, err := c.http.Do(httpReq)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("%w: %v", ErrTimeout, ctx.Err())
		}
		return nil, fmt.Errorf("%w: %v", ErrRemote, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, ErrUnauthorized
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%w: read response: %v", ErrProtocol, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%w: status %d: %s", ErrRemote, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var rpc jsonRPCResponse
	if err := json.Unmarshal(raw, &rpc); err != nil {
		return nil, fmt.Errorf("%w: decode response: %v", ErrProtocol, err)
	}
	if rpc.JSONRPC != "2.0" {
		return nil, fmt.Errorf("%w: unexpected jsonrpc version %q", ErrProtocol, rpc.JSONRPC)
	}
	if rpc.Error != nil {
		return nil, classifyRPCError(rpc.Error)
	}
	if len(rpc.Result) == 0 {
		return nil, fmt.Errorf("%w: empty result", ErrProtocol)
	}
	return rpc.Result, nil
}

func classifyRPCError(e *jsonRPCError) error {
	msg := strings.ToLower(e.Message)
	switch {
	case strings.Contains(msg, "unauthorized") || strings.Contains(msg, "token"):
		return fmt.Errorf("%w: %s", ErrUnauthorized, e.Message)
	case strings.Contains(msg, "not found"):
		return fmt.Errorf("%w: %s", ErrToolNotFound, e.Message)
	case strings.Contains(msg, "invalid"):
		return fmt.Errorf("%w: %s", ErrInvalidArguments, e.Message)
	default:
		return fmt.Errorf("%w: %s", ErrRemote, e.Message)
	}
}
