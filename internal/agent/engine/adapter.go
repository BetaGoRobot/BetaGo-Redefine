package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/modelapi"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/telemetry"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"go.opentelemetry.io/otel/attribute"
)

type Chat interface {
	Generate(context.Context, modelapi.ChatRequest) (modelapi.ChatResponse, error)
}

// ChatAdapter has immutable tool bindings; each native agent gets its own clone.
type ChatAdapter struct {
	client             Chat
	tools              []modelapi.ToolDefinition
	maxBytes           int
	conversationOutput bool
	replyConstraints   bool
}

func NewChatAdapter(c Chat, maxBytes int, conversationOutput ...bool) *ChatAdapter {
	return &ChatAdapter{client: c, maxBytes: maxBytes, conversationOutput: len(conversationOutput) > 0 && conversationOutput[0]}
}

func newNativeChatAdapter(c Chat, maxBytes int, conversationOutput bool) *ChatAdapter {
	adapter := NewChatAdapter(c, maxBytes, conversationOutput)
	adapter.replyConstraints = true
	return adapter
}
func definitions(in []*schema.ToolInfo) ([]modelapi.ToolDefinition, error) {
	out := make([]modelapi.ToolDefinition, 0, len(in))
	for _, t := range in {
		if t == nil {
			return nil, domain.ErrInvalid
		}
		params := json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)
		if t.ParamsOneOf != nil {
			sc, e := t.ParamsOneOf.ToJSONSchema()
			if e != nil {
				return nil, e
			}
			params, e = json.Marshal(sc)
			if e != nil {
				return nil, e
			}
		}
		out = append(out, modelapi.ToolDefinition{Type: "function", Function: modelapi.FunctionDefinition{Name: t.Name, Description: t.Desc, Parameters: params}})
	}
	return out, nil
}
func (m *ChatAdapter) WithTools(in []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	defs, e := definitions(in)
	if e != nil {
		return nil, telemetry.Wrap("model.tools.validate", "model_tools_invalid", e)
	}
	return &ChatAdapter{client: m.client, tools: defs, maxBytes: m.maxBytes, conversationOutput: m.conversationOutput, replyConstraints: m.replyConstraints}, nil
}
func (m *ChatAdapter) Generate(ctx context.Context, in []*schema.Message, opts ...model.Option) (result *schema.Message, retErr error) {
	if identified, ok := m.client.(interface{ ModelIdentity() (string, string) }); ok {
		id, protocol := identified.ModelIdentity()
		ctx = telemetry.Context(ctx, "model_name", id, "model_id", id, "model_protocol", protocol, "gen_ai.request.model", id)
	}
	ctx, span := telemetry.Start(ctx, "agent.model.generate", attribute.Int("input_message_count", len(in)), attribute.Int("limit_bytes", m.maxBytes))
	defer func() { telemetry.End(ctx, span, retErr) }()
	if m.client == nil {
		return nil, telemetry.Wrap("model.request.validate", "model_client_missing", domain.ErrInvalid)
	}
	o := model.GetCommonOptions(nil, opts...)
	r := modelapi.ChatRequest{Tools: m.tools, MaxTokens: 4096}
	if m.conversationOutput {
		r.ResponseFormat = conversationResponseFormat
	}
	if o.MaxTokens != nil {
		r.MaxTokens = *o.MaxTokens
	}
	if o.Tools != nil {
		var e error
		r.Tools, e = definitions(o.Tools)
		if e != nil {
			return nil, telemetry.Wrap("model.tools.validate", "model_tools_invalid", e)
		}
	}
	toolMessageBytes, estimatedSceneBytes := 0, 0
	for _, msg := range in {
		if msg == nil {
			return nil, telemetry.Wrap("model.request.validate", "model_message_invalid", domain.ErrInvalid)
		}
		v := modelapi.ChatMessage{Role: string(msg.Role), Content: msg.Content, ToolCallID: msg.ToolCallID}
		for _, call := range msg.ToolCalls {
			v.ToolCalls = append(v.ToolCalls, modelapi.ToolCall{ID: call.ID, Type: call.Type, Function: modelapi.ToolFunction{Name: call.Function.Name, Arguments: call.Function.Arguments}})
		}
		r.Messages = append(r.Messages, v)
		if msg.Role == schema.Tool {
			body, _ := json.Marshal(v)
			toolMessageBytes += len(body)
		} else if msg.Role == schema.User {
			estimatedSceneBytes += len(msg.Content)
		}
	}
	if m.replyConstraints {
		// Native resume restores the checkpoint's old system message instead of
		// rebuilding it from the current agent Instruction. Preserve that history
		// and append only the missing rules in this model-bound request copy.
		firstSystem, hasConstraints := -1, false
		for i, message := range r.Messages {
			if message.Role != "system" {
				continue
			}
			if firstSystem < 0 {
				firstSystem = i
			}
			content, _ := message.Content.(string)
			hasConstraints = hasConstraints || strings.Contains(content, replyConstraintsInstruction)
		}
		if !hasConstraints {
			if firstSystem >= 0 {
				content, _ := r.Messages[firstSystem].Content.(string)
				r.Messages[firstSystem].Content = content + "\n" + replyConstraintsInstruction
			} else {
				r.Messages = append([]modelapi.ChatMessage{{Role: "system", Content: replyConstraintsInstruction}}, r.Messages...)
			}
		}
	}
	// Always refresh the clock, including restored native checkpoints. Do not
	// mutate the incoming history or confuse message timestamps with now.
	clock := time.Now().In(beijing)
	clockMessage := fmt.Sprintf("[当前时间] 北京时间 %s；时区 Asia/Shanghai（UTC+08:00）。这是本次模型调用的当前时间，以此确定现在的日期与时刻，历史消息的时间仅代表各自发生时刻。", clock.Format(time.RFC3339))
	clockAt := 0
	for clockAt < len(r.Messages) && r.Messages[clockAt].Role == "system" {
		clockAt++
	}
	messages := make([]modelapi.ChatMessage, 0, len(r.Messages)+1)
	messages = append(messages, r.Messages[:clockAt]...)
	messages = append(messages, modelapi.ChatMessage{Role: "system", Content: clockMessage})
	messages = append(messages, r.Messages[clockAt:]...)
	r.Messages = messages
	telemetry.Info(ctx, "agent.clock", "current_time", clock.Format(time.RFC3339), "timezone", "Asia/Shanghai", "utc_offset", "+08:00")
	b, e := json.Marshal(r)
	if e != nil {
		return nil, telemetry.Wrap("model.request.encode", "model_request_encoding", e)
	}
	toolDefinitions, _ := json.Marshal(r.Tools)
	span.SetAttributes(attribute.Int("message_count", len(r.Messages)), attribute.Int("json_request_bytes", len(b)), attribute.Int("tool_message_bytes", toolMessageBytes), attribute.Int("tool_definitions_bytes", len(toolDefinitions)), attribute.Int("response_schema_bytes", len(r.ResponseFormat)), attribute.Int("estimated_scene_bytes", estimatedSceneBytes), attribute.Int("tool_count", len(r.Tools)))
	telemetry.Info(ctx, "agent.model.request", "json_request_bytes", len(b), "limit_bytes", m.maxBytes, "message_count", len(r.Messages), "tool_message_bytes", toolMessageBytes, "tool_definitions_bytes", len(toolDefinitions), "response_schema_bytes", len(r.ResponseFormat), "estimated_scene_bytes", estimatedSceneBytes, "tool_count", len(r.Tools))
	telemetry.Payload(ctx, "agent.model.request.content", "model_request", b)
	if m.maxBytes > 0 && len(b) > m.maxBytes {
		return nil, telemetry.Wrap("model.request.validate", "model_context_budget", fmt.Errorf("%w: complete model context budget", domain.ErrInvalid))
	}
	callCtx, callSpan := telemetry.Start(ctx, "agent.model.call")
	out, e := m.client.Generate(callCtx, r)
	telemetry.End(callCtx, callSpan, telemetry.Wrap("model.provider", "model_provider_failed", e))
	if e != nil {
		return nil, telemetry.Wrap("model.provider", "model_provider_failed", e)
	}
	span.SetAttributes(attribute.String("gen_ai.response.model", out.Model), attribute.String("gen_ai.response.id", out.ID), attribute.Int("gen_ai.usage.input_tokens", out.PromptTokens), attribute.Int("gen_ai.usage.output_tokens", out.CompletionTokens), attribute.Int("gen_ai.usage.cache_read.input_tokens", out.CachedTokens))
	telemetry.Payload(ctx, "agent.model.response.content", "model_response", out)
	if out.Message.Role != "assistant" {
		return nil, telemetry.Wrap("model.response.validate", "model_response_role", modelapi.ErrProtocol)
	}
	content := ""
	if out.Message.Content != nil {
		var ok bool
		content, ok = out.Message.Content.(string)
		if !ok {
			return nil, telemetry.Wrap("model.response.validate", "model_response_content", modelapi.ErrProtocol)
		}
	}
	span.SetAttributes(attribute.Int("prompt_tokens", out.PromptTokens), attribute.Int("completion_tokens", out.CompletionTokens), attribute.Int("tool_call_count", len(out.Message.ToolCalls)), attribute.Int("content_bytes", len(content)), attribute.String("finish_reason", safeFinishReason(out.FinishReason)))
	telemetry.Info(ctx, "agent.model.response", "prompt_tokens", out.PromptTokens, "completion_tokens", out.CompletionTokens, "tool_call_count", len(out.Message.ToolCalls), "content_bytes", len(content), "finish_reason", safeFinishReason(out.FinishReason))
	result = &schema.Message{Role: schema.Assistant, Content: content, ResponseMeta: &schema.ResponseMeta{FinishReason: out.FinishReason, Usage: &schema.TokenUsage{PromptTokens: out.PromptTokens, CompletionTokens: out.CompletionTokens}}}
	for _, call := range out.Message.ToolCalls {
		result.ToolCalls = append(result.ToolCalls, schema.ToolCall{ID: call.ID, Type: call.Type, Function: schema.FunctionCall{Name: call.Function.Name, Arguments: call.Function.Arguments}})
	}
	if content == "" && len(result.ToolCalls) == 0 {
		return nil, telemetry.Wrap("model.response.validate", "model_response_empty", modelapi.ErrProtocol)
	}
	return result, nil
}

func safeFinishReason(reason string) string {
	switch reason {
	case "stop", "length", "tool_calls", "content_filter", "function_call":
		return reason
	default:
		return "other"
	}
}
func (m *ChatAdapter) Stream(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	out, e := m.Generate(ctx, in, opts...)
	if e != nil {
		return nil, e
	}
	return schema.StreamReaderFromArray([]*schema.Message{out}), nil
}

var _ model.ToolCallingChatModel = (*ChatAdapter)(nil)
