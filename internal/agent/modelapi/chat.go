package modelapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}
type ToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}
type ChatMessage struct {
	Role       string     `json:"role"`
	Content    any        `json:"content,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
}
type ToolDefinition struct {
	Type     string             `json:"type"`
	Function FunctionDefinition `json:"function"`
}
type FunctionDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}
type ChatRequest struct {
	Messages       []ChatMessage
	Tools          []ToolDefinition
	ResponseFormat json.RawMessage
	MaxTokens      int
}
type ChatResponse struct {
	ID, Model                      string
	CachedTokens                   int
	Message                        ChatMessage
	FinishReason                   string
	PromptTokens, CompletionTokens int
}
type Chat struct{ endpoint Endpoint }

func NewChat(e Endpoint) (*Chat, error) {
	e, err := normalize(e)
	if err != nil {
		return nil, err
	}
	if e.Protocol != "" && e.Protocol != "openai" && e.Protocol != "ark" && e.Protocol != "json_chat" {
		return nil, fmt.Errorf("%w: chat protocol", domain.ErrInvalid)
	}
	return &Chat{e}, nil
}
func (c *Chat) Generate(ctx context.Context, input ChatRequest) (ChatResponse, error) {
	var out ChatResponse
	if len(input.Messages) == 0 || len(input.Messages) > 256 || len(input.Tools) > 128 {
		return out, fmt.Errorf("%w: chat collections", domain.ErrInvalid)
	}
	budget := int64(0)
	for _, m := range input.Messages {
		switch v := m.Content.(type) {
		case string:
			budget += int64(len(v))
		case []byte:
			budget += int64(len(v))
		}
		if budget > c.endpoint.MaxRequestBytes/6 {
			return out, fmt.Errorf("%w: chat content budget", domain.ErrInvalid)
		}
	}
	body := map[string]any{"model": c.endpoint.Model, "messages": input.Messages, "thinking": map[string]string{"type": "disabled"}}
	if c.endpoint.ServiceTier != "" {
		body["service_tier"] = c.endpoint.ServiceTier
	}
	if len(input.Tools) > 0 {
		body["tools"] = input.Tools
	}
	if len(input.ResponseFormat) > 0 {
		if !json.Valid(input.ResponseFormat) {
			return out, fmt.Errorf("%w: JSON response format", domain.ErrInvalid)
		}
		body["response_format"] = input.ResponseFormat
	}
	if input.MaxTokens > 0 {
		body["max_tokens"] = input.MaxTokens
	}
	var response struct {
		ID, Model string
		Choices   []struct {
			Message      ChatMessage `json:"message"`
			FinishReason string      `json:"finish_reason"`
		}
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			PromptDetails    struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
		}
	}
	if err := request(ctx, c.endpoint, "/chat/completions", body, &response); err != nil {
		return out, err
	}
	if len(response.Choices) != 1 {
		return out, fmt.Errorf("%w: chat choice count", ErrProtocol)
	}
	out.ID = response.ID
	out.Model = response.Model
	out.CachedTokens = response.Usage.PromptDetails.CachedTokens
	out.Message = response.Choices[0].Message
	out.FinishReason = response.Choices[0].FinishReason
	out.PromptTokens = response.Usage.PromptTokens
	out.CompletionTokens = response.Usage.CompletionTokens
	return out, nil
}
func (c *Chat) Complete(ctx context.Context, system, prompt string) (string, error) {
	out, err := c.Generate(ctx, ChatRequest{Messages: []ChatMessage{{Role: "system", Content: system}, {Role: "user", Content: prompt}}})
	if err != nil {
		return "", err
	}
	s, ok := out.Message.Content.(string)
	if !ok || s == "" {
		return "", fmt.Errorf("%w: empty chat text", ErrProtocol)
	}
	return s, nil
}
func (c *Chat) Describe(ctx context.Context, mime string, data []byte) (string, error) {
	if len(data) < 1 || int64(len(data)) > c.endpoint.MaxRequestBytes/2 {
		return "", fmt.Errorf("%w: image budget", domain.ErrInvalid)
	}
	if mime != "image/png" && mime != "image/jpeg" && mime != "image/webp" {
		return "", fmt.Errorf("%w: image MIME", domain.ErrInvalid)
	}
	return c.multimodal(ctx, map[string]any{"type": "image_url", "image_url": map[string]string{"url": "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)}}, len(data))
}
func (c *Chat) Transcribe(ctx context.Context, wav []byte) (string, error) {
	if len(wav) < 1 || int64(len(wav)) > c.endpoint.MaxRequestBytes/2 {
		return "", fmt.Errorf("%w: audio budget", domain.ErrInvalid)
	}
	return c.multimodal(ctx, map[string]any{"type": "input_audio", "input_audio": map[string]string{"data": base64.StdEncoding.EncodeToString(wav), "format": "wav"}}, len(wav))
}
func (c *Chat) multimodal(ctx context.Context, part any, size int) (string, error) {
	if size < 1 || int64(size) > c.endpoint.MaxRequestBytes/2 {
		return "", fmt.Errorf("%w: modality input budget", domain.ErrInvalid)
	}
	out, err := c.Generate(ctx, ChatRequest{Messages: []ChatMessage{{Role: "user", Content: []any{map[string]string{"type": "text", "text": "Extract the content faithfully. Preserve language, numbers and visible structure. Do not invent identities."}, part}}}})
	if err != nil {
		return "", err
	}
	s, ok := out.Message.Content.(string)
	if !ok || s == "" {
		return "", fmt.Errorf("%w: empty modality text", ErrProtocol)
	}
	return s, nil
}

// ModelIdentity exposes configuration identifiers only, never credentials.
func (c *Chat) ModelIdentity() (string, string) { return c.endpoint.Model, c.endpoint.Protocol }
