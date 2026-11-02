package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/modelapi"
)

// This test-only executable presents schemas to the provider. It never starts
// the agent, connects to a database, executes tools, or sends Lark messages.
func TestDeploymentFullSchemaAdmission(t *testing.T) {
	path := os.Getenv("DEPLOYMENT_PROBE_CONFIG")
	if path == "" {
		t.Skip("explicit private deployment config required")
	}
	c, err := config.Load(path, "agent")
	if err != nil {
		t.Fatal("private config invalid")
	}
	registry := inventoryRegistry(t, true)
	defs := registry.Definitions()
	if len(defs) != 74 {
		t.Fatal("unexpected inventory size")
	}
	tools := make([]modelapi.ToolDefinition, 0, len(defs))
	names := make([]string, 0, len(defs))
	for _, d := range defs {
		tools = append(tools, modelapi.ToolDefinition{Type: "function", Function: modelapi.FunctionDefinition{Name: d.Name, Description: d.Description, Parameters: d.Parameters}})
		names = append(names, d.Name)
	}
	raw, _ := json.Marshal(tools)
	chat, err := modelapi.NewChat(endpoint(c.Models.Answer))
	if err != nil {
		t.Fatal("model configuration invalid")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	res, err := chat.Generate(ctx, modelapi.ChatRequest{Messages: []modelapi.ChatMessage{{Role: "system", Content: "This is a deployment schema validation request. Reply only OK. Do not call any tools."}, {Role: "user", Content: "Validate that this complete tool schema set is accepted, then reply OK."}}, Tools: tools, MaxTokens: 64})
	report := map[string]any{"passed": err == nil, "tool_count": len(tools), "schema_bytes": len(raw), "prompt_tokens": res.PromptTokens, "completion_tokens": res.CompletionTokens, "tool_calls_executed": 0, "real_lark_messages_sent": 0, "tool_names": names}
	b, _ := json.Marshal(report)
	fmt.Println("DEPLOYMENT_SCHEMA_PROOF=" + string(b))
	if err != nil {
		t.Fatal("provider rejected schema admission")
	}
}
