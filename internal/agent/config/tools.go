package config

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
)

type API struct {
	Name           string            `toml:"name"`
	Description    string            `toml:"description"`
	URL            string            `toml:"url"`
	Method         string            `toml:"method"`
	Headers        map[string]string `toml:"headers"`
	Parameters     string            `toml:"parameters"`
	ReadOnly       bool              `toml:"read_only"`
	SideEffect     bool              `toml:"side_effect"`
	AutoExecute    bool              `toml:"auto_execute"`
	TimeoutSeconds int               `toml:"timeout_seconds"`
}
type MCP struct {
	Name           string            `toml:"name"`
	Description    string            `toml:"description"`
	ServerURL      string            `toml:"server_url"`
	ToolName       string            `toml:"tool_name"`
	Headers        map[string]string `toml:"headers"`
	Parameters     string            `toml:"parameters"`
	ReadOnly       bool              `toml:"read_only"`
	SideEffect     bool              `toml:"side_effect"`
	AutoExecute    bool              `toml:"auto_execute"`
	TimeoutSeconds int               `toml:"timeout_seconds"`
}

var headerName = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]+$")
var toolName = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]{0,63}$`)

func validTool(n, description, endpoint, parameters string, headers map[string]string, timeout int, readonly, effect bool) bool {
	if !toolName.MatchString(n) || !safe(description) || len(description) > 2048 || !httpURL(endpoint) || len(headers) > 16 || len(parameters) > 32<<10 || timeout < 1 || timeout > 300 || (readonly && effect) {
		return false
	}
	var schema map[string]any
	if json.Unmarshal([]byte(parameters), &schema) != nil || schema["type"] != "object" {
		return false
	}
	for key, value := range headers {
		if !headerName.MatchString(key) || !safe(value) {
			return false
		}
		switch strings.ToLower(key) {
		case "host", "content-length", "transfer-encoding", "connection", "upgrade":
			return false
		}
	}
	return true
}
func (c Config) validateTools() error {
	if len(c.APIs)+len(c.MCPs) > 32 {
		return errors.New("too many configured atomic tools")
	}
	seen := map[string]bool{}
	for _, tool := range c.APIs {
		if seen[tool.Name] || (tool.AutoExecute && !tool.SideEffect) || !validTool(tool.Name, tool.Description, tool.URL, tool.Parameters, tool.Headers, tool.TimeoutSeconds, tool.ReadOnly, tool.SideEffect) {
			return errors.New("invalid atomic API tool configuration")
		}
		seen[tool.Name] = true
		switch tool.Method {
		case "POST", "PUT", "PATCH":
		default:
			return errors.New("invalid atomic API method")
		}
	}
	for _, tool := range c.MCPs {
		if seen[tool.Name] || (tool.AutoExecute && !tool.SideEffect) || !validTool(tool.Name, tool.Description, tool.ServerURL, tool.Parameters, tool.Headers, tool.TimeoutSeconds, tool.ReadOnly, tool.SideEffect) || !safe(tool.ToolName) || len(tool.ToolName) > 128 {
			return errors.New("invalid atomic MCP tool configuration")
		}
		seen[tool.Name] = true
	}
	return nil
}
