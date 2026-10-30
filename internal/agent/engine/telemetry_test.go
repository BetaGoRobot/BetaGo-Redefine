package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/modelapi"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/telemetry"
	"github.com/cloudwego/eino/schema"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func observeEngine(t *testing.T) (*tracetest.SpanRecorder, *bytes.Buffer) {
	t.Helper()
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previousProvider, previousLogger := otel.GetTracerProvider(), slog.Default()
	var logs bytes.Buffer
	otel.SetTracerProvider(provider)
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() {
		otel.SetTracerProvider(previousProvider)
		slog.SetDefault(previousLogger)
		_ = provider.Shutdown(context.Background())
	})
	return recorder, &logs
}

func recordedSpan(t *testing.T, recorder *tracetest.SpanRecorder, name string) sdktrace.ReadOnlySpan {
	t.Helper()
	for _, span := range recorder.Ended() {
		if span.Name() == name {
			return span
		}
	}
	t.Fatalf("business span %q was not recorded", name)
	return nil
}

func spanValue(span sdktrace.ReadOnlySpan, key string) attribute.Value {
	for _, attr := range span.Attributes() {
		if string(attr.Key) == key {
			return attr.Value
		}
	}
	return attribute.Value{}
}

type observedModel struct {
	calls int
	err   error
}

func (m *observedModel) Generate(context.Context, modelapi.ChatRequest) (modelapi.ChatResponse, error) {
	m.calls++
	return modelapi.ChatResponse{Message: modelapi.ChatMessage{Role: "assistant", Content: "private-provider-prose"}, FinishReason: "stop", PromptTokens: 123, CompletionTokens: 7}, m.err
}

func TestAdapterBudgetFailureReportsActualEscapedRequestWithoutCallingProvider(t *testing.T) {
	recorder, logs := observeEngine(t)
	client := &observedModel{}
	content := strings.Repeat(`{"private-tool-body":"<secret>"}`, 100)
	in := []*schema.Message{schema.UserMessage("private-user-prose"), schema.ToolMessage(content, "tool-call")}
	request := modelapi.ChatRequest{Messages: []modelapi.ChatMessage{{Role: "user", Content: "private-user-prose"}, {Role: "tool", Content: content, ToolCallID: "tool-call"}}, MaxTokens: 4096, ResponseFormat: conversationResponseFormat}
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewChatAdapter(client, 1024, true).Generate(context.Background(), in)
	if !errors.Is(err, domain.ErrInvalid) || client.calls != 0 {
		t.Fatalf("budget guard changed: err=%v calls=%d", err, client.calls)
	}
	span := recordedSpan(t, recorder, "agent.model.generate")
	if span.Status().Code != codes.Error || spanValue(span, "json_request_bytes").AsInt64() <= int64(len(encoded)) || spanValue(span, "limit_bytes").AsInt64() != 1024 || spanValue(span, "message_count").AsInt64() != 3 {
		t.Fatalf("missing aggregate budget evidence: status=%v attrs=%v", span.Status(), span.Attributes())
	}
	if !strings.Contains(logs.String(), `"error_stage":"model.request.validate"`) || !strings.Contains(logs.String(), `"error_code":"model_context_budget"`) || !strings.Contains(logs.String(), `"json_request_bytes":`) {
		t.Fatalf("failure stage and request budget are unavailable: %s", logs.String())
	}
	for _, business := range []string{"private-user-prose", "private-tool-body"} {
		if !strings.Contains(logs.String(), business) {
			t.Fatalf("model request business content missing: %s", business)
		}
	}
}

func TestAdapterReportsProviderUsageAndKeepsProviderFailurePrivate(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "provider_failure"}[fail], func(t *testing.T) {
			recorder, logs := observeEngine(t)
			client := &observedModel{}
			if fail {
				client.err = errors.New("private-vendor-url?api_key=secret")
			}
			_, err := NewChatAdapter(client, 65536).Generate(context.Background(), []*schema.Message{schema.UserMessage("private-user-prose")})
			if fail && !errors.Is(err, client.err) || !fail && err != nil {
				t.Fatalf("model result changed: %v", err)
			}
			span := recordedSpan(t, recorder, "agent.model.generate")
			if fail {
				if span.Status().Code != codes.Error {
					t.Fatal("provider failure recorded as success")
				}
			} else if spanValue(span, "prompt_tokens").AsInt64() != 123 || spanValue(span, "completion_tokens").AsInt64() != 7 || spanValue(span, "content_bytes").AsInt64() != int64(len("private-provider-prose")) {
				t.Fatalf("model usage missing: %v", span.Attributes())
			}
			if strings.Contains(logs.String(), "private-vendor-url") || strings.Contains(logs.String(), "api_key=secret") {
				t.Fatal("private provider/request details leaked")
			}
		})
	}
}

func TestToolValidationFailureNamesBoundaryBeforeAnyLedgerOrRPC(t *testing.T) {
	f := newAutoEffectFixture(t, true, false)
	recorder, logs := observeEngine(t)
	tool := boundTool{definition: f.definition, store: f.store, job: f.job, task: f.task}
	_, err := tool.InvokableRun(context.Background(), `{"amount":"private-invalid-argument"}`)
	if !errors.Is(err, domain.ErrInvalid) || f.calls.Load() != 0 {
		t.Fatalf("invalid tool call reached provider: %v", err)
	}
	span := recordedSpan(t, recorder, "agent.tool.invoke")
	if span.Status().Code != codes.Error || spanValue(span, "tool").AsString() != "ordinary_action" {
		t.Fatalf("tool identity/status missing: %v", span.Attributes())
	}
	if !strings.Contains(logs.String(), `"error_stage":"tool.validate"`) || !strings.Contains(logs.String(), `"error_code":"tool_arguments_invalid"`) || !strings.Contains(logs.String(), "private-invalid-argument") {
		t.Fatalf("tool validation boundary missing or unsafe: %s", logs.String())
	}
}

func TestPromptSceneBudgetFailureKeepsMeasuredStageAndContentPrivate(t *testing.T) {
	f := newAutoEffectFixture(t, true, false)
	_, logs := observeEngine(t)
	f.engine.c.MaxContextBytes = 8192
	f.task.Request = strings.Repeat("private-scene", 1000)
	_, _, _, err := f.engine.prompt(context.Background(), f.task)
	stage, code := telemetry.Classify(err)
	if !errors.Is(err, domain.ErrInvalid) || stage != "turn.prompt.validate" || code != "scene_context_budget" {
		t.Fatalf("scene budget boundary lost: stage=%s code=%s err=%v", stage, code, err)
	}
	if !strings.Contains(logs.String(), `"estimated_scene_bytes":`) || !strings.Contains(logs.String(), `"limit_bytes":4096`) || strings.Contains(logs.String(), "private-scene") {
		t.Fatalf("scene budget evidence missing or unsafe: %s", logs.String())
	}
}
