package engine_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/engine"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/modelapi"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type captureModel struct{ requests []modelapi.ChatRequest }

func (c *captureModel) Generate(_ context.Context, r modelapi.ChatRequest) (modelapi.ChatResponse, error) {
	c.requests = append(c.requests, r)
	return modelapi.ChatResponse{Message: modelapi.ChatMessage{Role: "assistant", Content: "ok"}}, nil
}
func TestAdapterWithToolsDoesNotMutateSharedBase(t *testing.T) {
	ctx := context.Background()
	c := &captureModel{}
	base := engine.NewChatAdapter(c, 8192)
	bound, e := base.WithTools([]*schema.ToolInfo{{Name: "read"}})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = bound.Generate(ctx, []*schema.Message{schema.UserMessage("a")}, model.WithMaxTokens(12)); e != nil {
		t.Fatal(e)
	}
	if _, e = base.Generate(ctx, []*schema.Message{schema.UserMessage("b")}); e != nil {
		t.Fatal(e)
	}
	if len(c.requests) != 2 || len(c.requests[0].Tools) != 1 || len(c.requests[1].Tools) != 0 || c.requests[0].MaxTokens != 12 {
		t.Fatalf("tool binding mutated base: %+v", c.requests)
	}
}

func TestApprovalCardKeepsExactArgumentsInPlainTextAndV2Callback(t *testing.T) {
	task := domain.Task{ID: "task", OwnerID: "owner", Revision: 3}
	a := domain.Approval{ID: "approval", Tool: "write", Arguments: `{"text":"\u0060\u0060\u0060 malicious"}`}
	card, e := engine.ApprovalCard(task, a)
	if e != nil {
		t.Fatal(e)
	}
	var v struct {
		Schema string `json:"schema"`
		Body   struct {
			Elements []map[string]any `json:"elements"`
		} `json:"body"`
	}
	if json.Unmarshal([]byte(card), &v) != nil || v.Schema != "2.0" {
		t.Fatal("invalid card schema")
	}
	text, ok := v.Body.Elements[0]["text"].(map[string]any)
	if !ok || text["tag"] != "plain_text" || !strings.Contains(text["content"].(string), a.Arguments) {
		t.Fatal("exact frozen args are interpreted as markdown")
	}
	if !strings.Contains(card, `"behaviors":[{"type":"callback","value":`) || !strings.Contains(card, `"revision":"3"`) {
		t.Fatal("v2 callback or string binding missing")
	}
}

func TestConversationAdapterKeepsSchemaWhenBindingTools(t *testing.T) {
	c := &captureModel{}
	base := engine.NewChatAdapter(c, 32768, true)
	bound, err := base.WithTools([]*schema.ToolInfo{{Name: "read"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = bound.Generate(context.Background(), []*schema.Message{schema.UserMessage("scene")}); err != nil {
		t.Fatal(err)
	}
	if len(c.requests) != 1 || len(c.requests[0].ResponseFormat) == 0 || len(c.requests[0].Tools) != 1 {
		t.Fatal("conversation schema or tools disappeared in native adapter clone")
	}
	var format map[string]any
	if json.Unmarshal(c.requests[0].ResponseFormat, &format) != nil || format["type"] != "json_schema" {
		t.Fatal("terminal envelope not backed by provider response schema")
	}
}
