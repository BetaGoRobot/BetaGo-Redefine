package modelapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/telemetry"
	"go.opentelemetry.io/otel/attribute"
)

var ErrProtocol = errors.New("model protocol failure")

// Log business request/response bodies through the credential-aware telemetry
// encoder; transport errors stay classified instead of echoing credential URLs.
func request(ctx context.Context, e Endpoint, path string, input, output any) (result error) {
	ctx = telemetry.Context(ctx, "model_name", e.Model, "route", path, "request_limit_bytes", e.MaxRequestBytes, "response_limit_bytes", e.MaxResponseBytes, "timeout_ms", e.Timeout.Milliseconds())
	u, _ := url.Parse(e.BaseURL)
	operation := "chat"
	if strings.Contains(path, "embedding") {
		operation = "embeddings"
	}
	if strings.Contains(path, "rerank") {
		operation = "rerank"
	}
	if path == "/responses" {
		operation = "responses"
	}
	ctx = telemetry.Context(ctx, "model_id", e.Model, "model_protocol", e.Protocol, "model_host", u.Hostname())
	ctx, span := telemetry.Start(ctx, "model.request", attribute.String("gen_ai.request.model", e.Model), attribute.String("model.id", e.Model), attribute.String("model.protocol", e.Protocol), attribute.String("server.address", u.Hostname()), attribute.String("gen_ai.operation.name", operation))
	defer func() { telemetry.End(ctx, span, result) }()
	if !withinBudget(input, e.MaxRequestBytes) {
		return telemetry.Wrap("model.request.validate", "request_budget", fmt.Errorf("%w: request budget", ErrProtocol))
	}
	b, err := json.Marshal(input)
	if err != nil {
		return telemetry.Wrap("model.request.encode", "request_encoding", fmt.Errorf("%w: request encoding", ErrProtocol))
	}
	if int64(len(b)) > e.MaxRequestBytes {
		return telemetry.Wrap("model.request.encode", "request_budget", fmt.Errorf("%w: request budget", ErrProtocol))
	}
	telemetry.Info(ctx, "model.request.encoded", "request_bytes", len(b), "gen_ai.request.model", e.Model, "model_id", e.Model, "model_protocol", e.Protocol, "model_host", u.Hostname())
	diagnostic := func(data []byte) string {
		if e.APIKey != "" {
			return strings.ReplaceAll(string(data), e.APIKey, "[redacted]")
		}
		return string(data)
	}
	telemetry.Payload(ctx, "model.request.content", "request_body", diagnostic(b))
	ctx, cancel := context.WithTimeout(ctx, e.Timeout)
	defer cancel()
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, e.BaseURL+path, bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("%w: request", ErrProtocol)
	}
	r.Header.Set("Content-Type", "application/json")
	if e.APIKey != "" {
		r.Header.Set("Authorization", "Bearer "+e.APIKey)
	}
	resp, err := e.HTTPClient.Do(r)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return context.DeadlineExceeded
		}
		if errors.Is(err, context.Canceled) {
			return context.Canceled
		}
		return telemetry.Wrap("model.http", "transport_failed", fmt.Errorf("%w: transport", ErrProtocol))
	}
	defer resp.Body.Close()
	requestID := ""
	for _, key := range []string{"X-Request-Id", "X-Tt-Logid", "Request-Id", "X-Amzn-Requestid"} {
		if value := resp.Header.Get(key); value != "" {
			requestID = value
			break
		}
	}
	span.SetAttributes(attribute.String("provider.request_id", requestID), attribute.Int("http.response.status_code", resp.StatusCode))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, readErr := io.ReadAll(io.LimitReader(resp.Body, e.MaxResponseBytes+1))
		truncated := int64(len(raw)) > e.MaxResponseBytes
		if truncated {
			raw = raw[:e.MaxResponseBytes]
		}
		telemetry.Warn(ctx, "model.provider.rejected", "http_status", resp.StatusCode, "provider_request_id", requestID, "body_truncated", truncated, "body_read_failed", readErr != nil)
		telemetry.Payload(ctx, "model.response.content", "response_body", diagnostic(raw))
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(readErr, context.DeadlineExceeded) {
			return context.DeadlineExceeded
		}
		return telemetry.Wrap("model.response", "provider_http_rejected", fmt.Errorf("%w: HTTP %d", ErrProtocol, resp.StatusCode))
	}
	if resp.ContentLength > e.MaxResponseBytes {
		return telemetry.Wrap("model.response", "response_budget", fmt.Errorf("%w: response budget", ErrProtocol))
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, e.MaxResponseBytes+1))
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if err != nil || int64(len(raw)) > e.MaxResponseBytes {
		return telemetry.Wrap("model.response.read", "response_budget_or_read", fmt.Errorf("%w: response budget or read", ErrProtocol))
	}

	telemetry.Info(ctx, "model.provider.response", "provider_request_id", requestID, "http_status", resp.StatusCode, "response_bytes", len(raw))
	telemetry.Payload(ctx, "model.response.content", "response_body", diagnostic(raw))
	var metadata struct {
		ID, Model string
		Usage     struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			InputTokens      int `json:"input_tokens"`
			OutputTokens     int `json:"output_tokens"`
			PromptDetails    struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
			InputDetails struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"input_tokens_details"`
		}
	}
	if json.Unmarshal(raw, &metadata) == nil {
		inputTokens := metadata.Usage.PromptTokens
		if inputTokens == 0 {
			inputTokens = metadata.Usage.InputTokens
		}
		outputTokens := metadata.Usage.CompletionTokens
		if outputTokens == 0 {
			outputTokens = metadata.Usage.OutputTokens
		}
		cached := max(metadata.Usage.PromptDetails.CachedTokens, metadata.Usage.InputDetails.CachedTokens)
		span.SetAttributes(attribute.String("gen_ai.response.model", metadata.Model), attribute.String("gen_ai.response.id", metadata.ID), attribute.Int("gen_ai.usage.input_tokens", inputTokens), attribute.Int("gen_ai.usage.output_tokens", outputTokens), attribute.Int("gen_ai.usage.cache_read.input_tokens", cached))
		telemetry.Info(ctx, "agent.model.response", "model_id", e.Model, "response_model", metadata.Model, "response_id", metadata.ID, "provider_request_id", requestID, "prompt_tokens", inputTokens, "completion_tokens", outputTokens, "cached_tokens", cached)
	}
	if err = json.Unmarshal(raw, output); err != nil {
		return telemetry.Wrap("model.response.decode", "invalid_provider_json", fmt.Errorf("%w: response JSON", ErrProtocol))
	}
	telemetry.Info(ctx, "model.response.decoded", "response_bytes", len(raw), "http_status", resp.StatusCode)
	return nil
}

// Check caller-controlled collections before JSON allocates its encoding buffer.
// Escaping may grow the final encoding by at most a small constant factor; its
// exact size is checked separately before any network operation.
func withinBudget(value any, budget int64) bool {
	nodes := 0
	var walk func(reflect.Value, int) bool
	walk = func(v reflect.Value, depth int) bool {
		nodes++
		if nodes > 1_000_000 || depth > 64 || budget < 0 {
			return false
		}
		if !v.IsValid() {
			return true
		}
		switch v.Kind() {
		case reflect.Interface, reflect.Pointer:
			if v.IsNil() {
				return true
			}
			return walk(v.Elem(), depth+1)
		case reflect.String:
			budget -= int64(v.Len())
		case reflect.Slice, reflect.Array:
			if v.Type().Elem().Kind() == reflect.Uint8 {
				budget -= int64(v.Len())
				break
			}
			if v.Len() > 1_000_000 {
				return false
			}
			for i := 0; i < v.Len(); i++ {
				if !walk(v.Index(i), depth+1) {
					return false
				}
			}
		case reflect.Map:
			if v.Len() > 1_000_000 {
				return false
			}
			iter := v.MapRange()
			for iter.Next() {
				if !walk(iter.Key(), depth+1) || !walk(iter.Value(), depth+1) {
					return false
				}
			}
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				if v.Type().Field(i).PkgPath == "" && !walk(v.Field(i), depth+1) {
					return false
				}
			}
		default:
			budget -= 8
		}
		return budget >= 0
	}
	return walk(reflect.ValueOf(value), 0)
}
