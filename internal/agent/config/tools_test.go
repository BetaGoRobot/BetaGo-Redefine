package config

import (
	"strings"
	"testing"
)

const apiTool = `
[[api_tools]]
name = "business_lookup"
description = "Read a configured business record"
url = "https://business.example.test/lookup"
method = "POST"
parameters = '{"type":"object","properties":{"record_id":{"type":"string"}},"required":["record_id"],"additionalProperties":false}'
read_only = true
timeout_seconds = 15
[api_tools.headers]
Authorization = "${AGENT_NEXT_TOOL_TEST_TOKEN}"
`

func allEnvironment(k string) (string, bool) {
	if strings.HasPrefix(k, "AGENT_NEXT_MEMES_") {
		return "", false
	}
	if v, ok := modelEnvironment(k); ok {
		return v, true
	}
	for _, role := range []string{"ANSWER", "MEMORY", "EMBEDDING", "RERANK", "VISION", "AUDIO", "WEB_SEARCH"} {
		if k == "AGENT_NEXT_"+role+"_ENDPOINT" || k == "AGENT_NEXT_"+role+"_API_KEY" {
			return "", false
		}
	}
	switch k {
	case "AGENT_NEXT_PUBLIC_URL":
		return "https://agent.example.test", true
	case "AGENT_NEXT_DATABASE_URL":
		return "postgres://agent:synthetic@db:5432/agent?sslmode=disable", true
	case "AGENT_NEXT_EMBEDDING_DIMENSIONS":
		return "1024", true
	case "AGENT_NEXT_TOOL_TEST_TOKEN":
		return `Bearer synthetic"quoted`, true
	default:
		return "synthetic", true
	}
}
func TestToolCredentialsExpandAsHeaderValuesAfterParsing(t *testing.T) {
	c, err := Decode(strings.NewReader(example(t)+apiTool), "agent", allEnvironment)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.APIs) != 1 || c.APIs[0].Headers["Authorization"] != `Bearer synthetic"quoted` || !c.APIs[0].ReadOnly {
		t.Fatal("header reference or explicit read policy lost")
	}
}

func TestAutomaticToolConfigRequiresExplicitSideEffectClassification(t *testing.T) {
	for _, kind := range []string{"api", "mcp"} {
		t.Run(kind, func(t *testing.T) {
			content := strings.Replace(apiTool, "read_only = true", "side_effect = true\nauto_execute = true", 1)
			if kind == "mcp" {
				content = strings.ReplaceAll(content, "api_tools", "mcp_tools")
				content = strings.Replace(content, `url = "https://business.example.test/lookup"`, `server_url = "https://business.example.test/lookup"`, 1)
				content = strings.Replace(content, `method = "POST"`, `tool_name = "action"`, 1)
			}
			c, err := Decode(strings.NewReader(example(t)+content), "agent", allEnvironment)
			if err != nil {
				t.Fatalf("explicit automatic classification rejected: %v", err)
			}
			if kind == "api" && !c.APIs[0].AutoExecute || kind == "mcp" && !c.MCPs[0].AutoExecute {
				t.Fatal("configured automatic classification lost")
			}
			for _, bad := range []string{strings.Replace(content, "side_effect = true\n", "", 1), strings.Replace(content, "side_effect = true", "read_only = true", 1)} {
				if _, err := Decode(strings.NewReader(example(t)+bad), "agent", allEnvironment); err == nil {
					t.Fatal("automatic execution accepted without explicit effect classification")
				}
			}
		})
	}
}
func TestConfiguredToolsRejectAmbiguousNamesInvalidSchemaAndHeaderInjection(t *testing.T) {
	for name, content := range map[string]string{
		"duplicate tool name":  apiTool + strings.Replace(apiTool, "business_lookup", "business_lookup", 1),
		"endpoint credentials": strings.Replace(apiTool, "https://business.example.test/lookup", "https://user:private@business.example.test/lookup", 1),
		"invalid schema":       strings.Replace(apiTool, `{"type":"object","properties":{"record_id":{"type":"string"}},"required":["record_id"],"additionalProperties":false}`, `{"type":"array"}`, 1),
		"conflicting policy":   strings.Replace(apiTool, "read_only = true", "read_only = true\nside_effect = true", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Decode(strings.NewReader(example(t)+content), "agent", allEnvironment); err == nil {
				t.Fatal("invalid tool configuration accepted")
			}
		})
	}
	lookup := func(k string) (string, bool) {
		if k == "AGENT_NEXT_TOOL_TEST_TOKEN" {
			return "private\r\nX-Injected: yes", true
		}
		return allEnvironment(k)
	}
	if _, err := Decode(strings.NewReader(example(t)+apiTool), "agent", lookup); err == nil || strings.Contains(err.Error(), "X-Injected") {
		t.Fatal("header injection accepted or reflected")
	}
}
