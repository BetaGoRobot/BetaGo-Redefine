package mcpclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	clientName    = "betago-mcp-client"
	clientVersion = "v1.0.0"
)

func (c *Client) ListTools(ctx context.Context, server ServerConfig) ([]Tool, error) {
	ctx, session, cancel, err := c.connect(ctx, server)
	if err != nil {
		return nil, err
	}
	defer session.Close()
	defer cancel()

	res, err := session.ListTools(ctx, nil)
	if err != nil {
		return nil, classifyError(err)
	}
	tools := make([]Tool, 0, len(res.Tools))
	for _, t := range res.Tools {
		if t == nil {
			continue
		}
		schema, _ := json.Marshal(t.InputSchema)
		tools = append(tools, Tool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: schema,
		})
	}
	return tools, nil
}

func (c *Client) CallTool(ctx context.Context, req CallRequest) (CallResult, error) {
	ctx, session, cancel, err := c.connect(ctx, req.Server)
	if err != nil {
		return CallResult{}, err
	}
	defer session.Close()
	defer cancel()

	var arguments any
	if len(req.Arguments) > 0 {
		if err := json.Unmarshal(req.Arguments, &arguments); err != nil {
			return CallResult{}, fmt.Errorf("%w: decode arguments: %v", ErrInvalidArguments, err)
		}
	}

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      req.ToolName,
		Arguments: arguments,
	})
	if err != nil {
		return CallResult{}, classifyError(err)
	}

	content, err := json.Marshal(res.Content)
	if err != nil {
		return CallResult{}, fmt.Errorf("%w: encode content: %v", ErrProtocol, err)
	}
	raw, err := json.Marshal(res)
	if err != nil {
		return CallResult{}, fmt.Errorf("%w: encode result: %v", ErrProtocol, err)
	}
	if res.IsError {
		return CallResult{}, fmt.Errorf("%w: %s", ErrRemote, toolErrorText(res))
	}
	return CallResult{Content: content, Raw: raw}, nil
}

func (c *Client) connect(ctx context.Context, server ServerConfig) (context.Context, *mcp.ClientSession, context.CancelFunc, error) {
	if strings.TrimSpace(server.URL) == "" {
		return ctx, nil, nil, fmt.Errorf("%w: empty server url", ErrProtocol)
	}
	cancel := context.CancelFunc(func() {})
	if server.Timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, server.Timeout)
	}
	transport := &mcp.StreamableClientTransport{
		Endpoint:   server.URL,
		HTTPClient: c.httpClientWithHeaders(server.Headers),
		MaxRetries: -1,
	}
	client := mcp.NewClient(&mcp.Implementation{Name: clientName, Version: clientVersion}, nil)
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		cancel()
		return ctx, nil, nil, classifyError(err)
	}
	return ctx, session, cancel, nil
}

func (c *Client) httpClientWithHeaders(headers map[string]string) *http.Client {
	base := c.http
	if base == nil {
		base = http.DefaultClient
	}
	if len(headers) == 0 {
		return base
	}
	clone := *base
	clone.Transport = &headerRoundTripper{base: base.Transport, headers: headers}
	return &clone
}

type headerRoundTripper struct {
	base    http.RoundTripper
	headers map[string]string
}

func (h *headerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	base := h.base
	if base == nil {
		base = http.DefaultTransport
	}
	clone := req.Clone(req.Context())
	for k, v := range h.headers {
		clone.Header.Set(k, v)
	}
	return base.RoundTrip(clone)
}

func toolErrorText(res *mcp.CallToolResult) string {
	parts := make([]string, 0, len(res.Content))
	for _, content := range res.Content {
		if text, ok := content.(*mcp.TextContent); ok {
			parts = append(parts, text.Text)
		}
	}
	if len(parts) == 0 {
		return "tool reported an error"
	}
	return strings.Join(parts, "\n")
}

func classifyError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%w: %v", ErrTimeout, err)
	}
	if errors.Is(err, context.Canceled) {
		return err
	}

	var rpcErr *jsonrpc.Error
	if errors.As(err, &rpcErr) {
		return classifyRPCError(rpcErr)
	}

	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "unauthorized") || strings.Contains(msg, "forbidden") || strings.Contains(msg, "token"):
		return fmt.Errorf("%w: %v", ErrUnauthorized, err)
	case strings.Contains(msg, "deadline") || strings.Contains(msg, "timeout"):
		return fmt.Errorf("%w: %v", ErrTimeout, err)
	case strings.Contains(msg, "not found"):
		return fmt.Errorf("%w: %v", ErrToolNotFound, err)
	default:
		return fmt.Errorf("%w: %v", ErrRemote, err)
	}
}

func classifyRPCError(e *jsonrpc.Error) error {
	msg := strings.ToLower(e.Message)
	switch {
	case strings.Contains(msg, "unauthorized") || strings.Contains(msg, "token"):
		return fmt.Errorf("%w: %s", ErrUnauthorized, e.Message)
	case e.Code == jsonrpc.CodeMethodNotFound || strings.Contains(msg, "unknown tool") || strings.Contains(msg, "not found"):
		return fmt.Errorf("%w: %s", ErrToolNotFound, e.Message)
	case e.Code == jsonrpc.CodeInvalidParams || strings.Contains(msg, "invalid"):
		return fmt.Errorf("%w: %s", ErrInvalidArguments, e.Message)
	default:
		return fmt.Errorf("%w: %s", ErrRemote, e.Message)
	}
}
