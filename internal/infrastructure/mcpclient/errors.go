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
