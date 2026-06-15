package mcpclient

import (
	"encoding/json"
	"net/http"
	"time"
)

type ServerConfig struct {
	Name    string
	URL     string
	Headers map[string]string
	Timeout time.Duration
}

type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

type CallRequest struct {
	Server    ServerConfig
	ToolName  string
	Arguments json.RawMessage
}

type CallResult struct {
	Content json.RawMessage
	Raw     json.RawMessage
}

type ClientOptions struct {
	HTTPClient *http.Client
}

type Client struct {
	http *http.Client
}

func New(opts ClientOptions) *Client {
	hc := opts.HTTPClient
	if hc == nil {
		hc = http.DefaultClient
	}
	return &Client{http: hc}
}
