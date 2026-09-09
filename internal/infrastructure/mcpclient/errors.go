package mcpclient

import "errors"

var (
	ErrUnauthorized     = errors.New("mcp unauthorized")
	ErrToolNotFound     = errors.New("mcp tool not found")
	ErrInvalidArguments = errors.New("mcp invalid arguments")
	ErrRemote           = errors.New("mcp remote error")
	ErrTimeout          = errors.New("mcp timeout")
	ErrProtocol         = errors.New("mcp protocol error")
)

// ToolError reports an explicit MCP tool result with isError set. Transport and
// JSON-RPC failures remain separate so callers can identify tool rejections.
type ToolError struct {
	Message string
}

func (e *ToolError) Error() string {
	return ErrRemote.Error() + ": " + e.Message
}

func (e *ToolError) Unwrap() error {
	return ErrRemote
}
