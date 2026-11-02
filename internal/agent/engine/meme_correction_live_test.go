package engine

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/atoms"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/modelapi"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/retrieval"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/web"
)

// Paid opt-in first-response probe. Uses native instructions, transport and
// atomic tool definitions; it starts no engine/store/receiver and sends no chat.
// Group search is a fixture. Only one public web search is actually executed.
func TestMemeCorrectionLiveProbe(t *testing.T) {
	if os.Getenv("MEME_CORRECTION_LIVE") != "1" {
		t.Skip("explicit paid correction probe only")
	}
	c, err := config.Load(os.Getenv("MEME_CORRECTION_CONFIG"), "worker")
	if err != nil {
		t.Fatal("readonly next configuration unavailable:", err)
	}
	ep := func(e config.Endpoint) modelapi.Endpoint {
		return modelapi.Endpoint{BaseURL: e.BaseURL, APIKey: e.APIKey, Model: e.Model, Protocol: e.Protocol, ServiceTier: e.ServiceTier, Timeout: 60 * time.Second}
	}
	chat, err := modelapi.NewChat(ep(c.Models.Answer))
	if err != nil {
		t.Fatal("native final model unavailable")
	}
	search, err := modelapi.NewWebSearch(ep(c.Models.WebSearch))
	if err != nil {
		t.Fatal("native web model unavailable")
	}
	registry, err := atoms.New(atoms.Options{WebSearch: search, WebPages: web.NewReader(), Retrieval: correctionEmptyHistory{}})
	if err != nil {
		t.Fatal("native read tools unavailable")
	}
	var tools []modelapi.ToolDefinition
	var searchTool atoms.Definition
	for _, d := range registry.Definitions() {
		tools = append(tools, modelapi.ToolDefinition{Type: "function", Function: modelapi.FunctionDefinition{Name: d.Name, Description: d.Description, Parameters: d.Parameters}})
		if d.Name == "web_search" {
			searchTool = d
		}
	}
	type sample struct {
		ID, Feedback string
		Search       bool
	}
	cases := []sample{
		{"explicit", "你没理解这个梗，查下钢铁雄心和海岛奇兵在这里的关系。", true},
		{"rejected_interpretation", "不是这个意思，你把海岛奇兵只当游戏了，这个梗召回错了。", true},
		{"stop", "回答太长了，别回了。", false},
		{"no_web", "这个梗你理解错了，但不要联网，我只是说现在只玩海岛奇兵了。", false},
		{"quoted", "帮我把这句原样加引号：你没理解这个梗，召回错了。", false},
	}
	var frozen struct {
		Rows []struct {
			ID           string                `json:"id"`
			Response     modelapi.ChatResponse `json:"response"`
			SearchResult json.RawMessage       `json:"search_result"`
		} `json:"rows"`
	}
	if path := os.Getenv("MEME_CORRECTION_FROZEN_SEARCH"); path != "" {
		raw, e := os.ReadFile(path)
		if e != nil || json.Unmarshal(raw, &frozen) != nil || len(frozen.Rows) == 0 || frozen.Rows[0].ID != "explicit" || len(frozen.Rows[0].SearchResult) == 0 {
			t.Fatal("frozen search evidence unavailable")
		}
		cases = cases[:1]
	}
	report := map[string]any{"model": c.Models.Answer.Model, "web_model": c.Models.WebSearch.Model, "lark_sends": 0, "corpus_writes": 0, "scope": "native prompt and transport; fixture group history; not full delivery engine"}
	var rows []map[string]any
	t.Cleanup(func() {
		report["rows"] = rows
		raw, e := json.MarshalIndent(report, "", "  ")
		if e != nil || os.WriteFile(os.Getenv("MEME_CORRECTION_OUTPUT"), append(raw, '\n'), 0600) != nil {
			t.Error("probe evidence write failed")
		}
	})
	for _, tc := range cases {
		t.Run(tc.ID, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 130*time.Second)
			defer cancel()
			messages := []modelapi.ChatMessage{
				{Role: "system", Content: instruction + "\n" + replyConstraintsInstruction + "\n" + chatVoiceInstruction + "\n" + conversationOutputInstruction},
				{Role: "user", Content: "三年，从钢铁雄心玩成海岛奇兵了，机器人接一句。"},
				{Role: "assistant", Content: "海岛奇兵就是个经营海岛的游戏，换个游戏继续打。"},
				{Role: "user", Content: tc.Feedback},
			}
			var response modelapi.ChatResponse
			var e error
			if len(frozen.Rows) > 0 {
				response = frozen.Rows[0].Response
			} else {
				response, e = chat.Generate(ctx, modelapi.ChatRequest{Messages: messages, Tools: tools, ResponseFormat: conversationResponseFormat, MaxTokens: 1024})
			}
			row := map[string]any{"id": tc.ID, "feedback": tc.Feedback, "expect_search": tc.Search, "response": response}
			rows = append(rows, row)
			if e != nil {
				row["error"] = "native model request failed"
				t.Fatal("native model request failed")
			}
			var selected *modelapi.ToolCall
			for _, call := range response.Message.ToolCalls {
				if call.Function.Name == "web_search" {
					copy := call
					selected = &copy
				}
			}
			if (selected != nil) != tc.Search {
				t.Error("unexpected first-response web_search choice")
			}
			if tc.ID != "explicit" || selected == nil {
				return
			}
			if os.Getenv("MEME_CORRECTION_CHOICE_ONLY") == "1" {
				return
			}
			// Record the actual search result, including unavailable reads; do not retry.
			var result string
			if len(frozen.Rows) > 0 {
				result = string(frozen.Rows[0].SearchResult)
				row["frozen_search_reused"] = true
			} else {
				result, e = searchTool.Run(ctx, atoms.Invocation{}, selected.Function.Arguments)
			}
			if e != nil {
				row["search_error"] = "native search failed"
				t.Error("native search failed")
				return
			}
			row["search_result"] = json.RawMessage(result)
			messages = append(messages, response.Message, modelapi.ChatMessage{Role: "tool", ToolCallID: selected.ID, Content: result})
			final, e := chat.Generate(ctx, modelapi.ChatRequest{Messages: messages, Tools: tools, ResponseFormat: conversationResponseFormat, MaxTokens: 1024})
			row["continuation"] = final
			if e != nil {
				row["continuation_error"] = "native continuation failed"
				t.Error("native continuation failed")
			}
		})
	}
}

type correctionEmptyHistory struct{}

func (correctionEmptyHistory) Retrieve(context.Context, domain.Query) (retrieval.Result, error) {
	return retrieval.Result{}, nil
}
