package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/modelapi"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/memereference"
	"github.com/cloudwego/eino/schema"
)

func TestMemeCorrectionDoesNotDependOnKnowledgeAvailability(t *testing.T) {
	// The native instruction exists even when the corpus or reference planner is unavailable.
	if !strings.Contains(instruction, memereference.CorrectionInstruction) {
		t.Fatal("native instruction lost the explicit-feedback correction path")
	}
}

func TestMemeCorrectionRefreshesLegacyCheckpointAndContinuation(t *testing.T) {
	old := coreInstruction + "\n" + conversationOutputInstruction
	history := []*schema.Message{schema.SystemMessage(old), schema.UserMessage("你没理解这个梗")}
	client := &correctionCapture{}
	adapter := newNativeChatAdapter(client, 131072, false)
	for range 2 {
		if _, err := adapter.Generate(context.Background(), history); err != nil {
			t.Fatal(err)
		}
	}
	for _, request := range client.requests {
		count := 0
		for _, message := range request.Messages {
			if message.Role == "system" {
				text, _ := message.Content.(string)
				count += strings.Count(text, memereference.CorrectionInstruction)
			}
		}
		if count != 1 {
			t.Fatal("legacy native request must receive current correction rule exactly once")
		}
	}
	if history[0].Content != old {
		t.Fatal("checkpoint history was mutated")
	}
}

type correctionCapture struct{ requests []modelapi.ChatRequest }

func (c *correctionCapture) Generate(_ context.Context, r modelapi.ChatRequest) (modelapi.ChatResponse, error) {
	c.requests = append(c.requests, r)
	return modelapi.ChatResponse{Message: modelapi.ChatMessage{Role: "assistant", Content: "收到"}}, nil
}
