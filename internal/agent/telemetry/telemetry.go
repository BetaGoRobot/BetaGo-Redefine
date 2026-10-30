// Package telemetry connects the independent Agent to the existing OTLP
// collector. Business events remain visible on stdout without a collector.
package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

type contextKey struct{}
type spanKey struct{}
type quietKey struct{}

// Quiet keeps noisy polling spans and successful HTTP requests at DEBUG while
// preserving errors and exported traces.
func Quiet(ctx context.Context) context.Context { return context.WithValue(ctx, quietKey{}, true) }

type spanState struct {
	name string
	at   time.Time
	id   trace.SpanID
}
type failure struct {
	stage, code string
	cause       error
}

func (f *failure) Error() string { return f.stage + ": " + f.code }
func (f *failure) Unwrap() error { return f.cause }

var token = regexp.MustCompile(`^[a-zA-Z0-9_.-]{1,120}$`)

// Wrap preserves the first precise diagnostic through outer lifecycle wrappers.
func Wrap(stage, code string, err error) error {
	if err == nil {
		return nil
	}
	var previous *failure
	if errors.As(err, &previous) {
		return err
	}
	if !token.MatchString(stage) {
		stage = "operation"
	}
	if !token.MatchString(code) {
		code = "operation_failed"
	}
	return &failure{stage, code, err}
}
func Classify(err error) (string, string) {
	if err == nil {
		return "", "ok"
	}
	var f *failure
	if errors.As(err, &f) {
		return f.stage, f.code
	}
	for _, c := range []struct {
		err  error
		code string
	}{
		{context.Canceled, "canceled"}, {context.DeadlineExceeded, "deadline_exceeded"},
		{domain.ErrStaleClaim, "stale_claim"}, {domain.ErrStaleSource, "stale_source"},
		{domain.ErrUnauthorized, "unauthorized"}, {domain.ErrConflict, "conflict"},
		{domain.ErrInvalid, "invalid_input"}, {domain.ErrNotFound, "not_found"},
		{domain.ErrTaskBusy, "task_busy"}, {domain.ErrDeliveryPending, "delivery_pending"},
		{domain.ErrEffectUnknown, "effect_unknown"},
	} {
		if errors.Is(err, c.err) {
			return "operation", c.code
		}
	}
	return "operation", "operation_failed"
}

func safeAttr(a slog.Attr) slog.Attr { return businessAttr(a) }
func argsAttrs(args ...any) []slog.Attr {
	r := slog.NewRecord(time.Now(), slog.LevelInfo, "", 0)
	r.Add(args...)
	var out []slog.Attr
	r.Attrs(func(a slog.Attr) bool { out = append(out, safeAttr(a)); return true })
	return out
}
func Context(ctx context.Context, args ...any) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	previous, _ := ctx.Value(contextKey{}).([]slog.Attr)
	attrs := mergeAttrs(previous, argsAttrs(args...))
	if len(attrs) > 48 {
		attrs = attrs[len(attrs)-48:]
	}
	return context.WithValue(ctx, contextKey{}, attrs)
}
func mergeAttrs(groups ...[]slog.Attr) []slog.Attr {
	positions := map[string]int{}
	var out []slog.Attr
	for _, group := range groups {
		for _, a := range group {
			if index, ok := positions[a.Key]; ok {
				out[index] = a
			} else {
				positions[a.Key] = len(out)
				out = append(out, a)
			}
		}
	}
	return out
}
func logEvent(ctx context.Context, level slog.Level, event string, args ...any) {
	if ctx == nil {
		ctx = context.Background()
	}
	if quiet, _ := ctx.Value(quietKey{}).(bool); quiet && level == slog.LevelInfo {
		level = slog.LevelDebug
	}
	attrs, _ := ctx.Value(contextKey{}).([]slog.Attr)
	attrs = mergeAttrs(attrs, argsAttrs(args...))
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		attrs = mergeAttrs(attrs, []slog.Attr{slog.String("trace_id", sc.TraceID().String()), slog.String("span_id", sc.SpanID().String())})
	}
	attrs = mergeAttrs(attrs, []slog.Attr{slog.String("summary", description(event)), slog.String("event.name", event)})
	span := trace.SpanFromContext(ctx)
	if span.IsRecording() && !strings.HasSuffix(event, ".started") && !strings.HasSuffix(event, ".completed") {
		spanAttrs := []attribute.KeyValue{attribute.String("event.name", event)}
		for _, a := range attrs {
			if a.Key == "trace_id" || a.Key == "span_id" {
				continue
			}
			switch a.Value.Kind() {
			case slog.KindAny:
				if content, ok := a.Value.Any().(sanitizedContent); ok {
					spanAttrs = append(spanAttrs, attribute.String(a.Key, contentPreview(string(content), 2048)))
				}
			case slog.KindString:
				spanAttrs = append(spanAttrs, attribute.String(a.Key, contentPreview(a.Value.String(), 2048)))
			case slog.KindInt64:
				spanAttrs = append(spanAttrs, attribute.Int64(a.Key, a.Value.Int64()))
			case slog.KindBool:
				spanAttrs = append(spanAttrs, attribute.Bool(a.Key, a.Value.Bool()))
			case slog.KindFloat64:
				spanAttrs = append(spanAttrs, attribute.Float64(a.Key, a.Value.Float64()))
			}
		}
		span.AddEvent(description(event), trace.WithAttributes(spanAttrs...))
		// Payload bodies live in events/logs; concise result fields are directly
		// visible in the span's metadata panel.
		for _, a := range spanAttrs {
			key := string(a.Key)
			if key == "choice" || key == "tool" || key == "model_name" || key == "response_model" || key == "response_id" || key == "provider_request_id" || key == "finish_reason" || key == "http_status" || key == "provider_message_id" || key == "bubble_count" || key == "prompt_tokens" || key == "completion_tokens" || key == "cached_tokens" || strings.HasPrefix(key, "gen_ai.") {
				span.SetAttributes(a)
			}
		}
	}
	slog.Default().LogAttrs(ctx, level, event, attrs...)
}
func Info(ctx context.Context, event string, args ...any) {
	logEvent(ctx, slog.LevelInfo, event, args...)
}
func Warn(ctx context.Context, event string, args ...any) {
	logEvent(ctx, slog.LevelWarn, event, args...)
}
func Error(ctx context.Context, event string, err error, args ...any) {
	stage, code := Classify(err)
	args = append(args, "error_stage", stage, "error_code", code)
	var state interface{ SQLState() string }
	if errors.As(err, &state) {
		if value := state.SQLState(); len(value) == 5 && token.MatchString(value) {
			args = append(args, "db_sqlstate", value)
		}
	}
	logEvent(ctx, slog.LevelError, event, args...)
}
func Start(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	var business []attribute.KeyValue
	if fields, ok := ctx.Value(contextKey{}).([]slog.Attr); ok {
		for _, field := range fields {
			switch field.Key {
			case "scope_hash", "chat_id", "author_id", "message_id", "event_id", "job_id", "kind", "attempt", "task_id", "revision", "context_id", "tool_name", "model_name", "model_id", "model_protocol", "gen_ai.request.model", "route":
				switch field.Value.Kind() {
				case slog.KindString:
					business = append(business, attribute.String(field.Key, field.Value.String()))
				case slog.KindInt64:
					business = append(business, attribute.Int64(field.Key, field.Value.Int64()))
				}
			}
		}
	}
	attrs = append(business, attrs...)
	attrs = append(attrs, attribute.String("operation.description", description(name)), attribute.String("service.namespace", serviceNamespace()), attribute.String("namespace", serviceNamespace()))
	ctx, span := otel.Tracer("betago-agent-next").Start(ctx, name, trace.WithAttributes(attrs...))
	ctx = context.WithValue(ctx, spanKey{}, spanState{name, time.Now(), span.SpanContext().SpanID()})
	if sc := span.SpanContext(); sc.IsValid() {
		span.SetAttributes(attribute.String("logs.url", LogsURL(sc.TraceID().String(), "")), attribute.String("logs.span_url", LogsURL(sc.TraceID().String(), sc.SpanID().String())))
	}
	fields := make([]any, 0, len(attrs)*2)
	for _, a := range attrs {
		fields = append(fields, string(a.Key), a.Value.AsInterface())
	}
	Info(ctx, name+".started", fields...)
	return ctx, span
}
func End(ctx context.Context, span trace.Span, err error) {
	if span == nil {
		return
	}
	state, _ := ctx.Value(spanKey{}).(spanState)
	name := state.name
	if name == "" {
		name = "agent.operation"
	}
	duration := time.Since(state.at)
	if state.at.IsZero() {
		duration = 0
	}
	outcome := "ok"
	if fields, ok := ctx.Value(contextKey{}).([]slog.Attr); ok {
		for _, field := range fields {
			if field.Key == "outcome" {
				switch field.Value.String() {
				case "deferred", "stale", "rejected", "retry":
					outcome = field.Value.String()
				}
			}
		}
	}
	_, pendingCode := Classify(err)
	expectedPending := err != nil && (errors.Is(err, domain.ErrTaskBusy) || errors.Is(err, domain.ErrDeliveryPending) || pendingCode == "media_not_ready")
	if expectedPending || (err == nil && outcome == "deferred") {
		outcome = "deferred"
		span.SetAttributes(attribute.String("operation.outcome", "deferred"), attribute.String("defer.reason", pendingCode))
		logEvent(ctx, slog.LevelDebug, name+".deferred", "duration_ms", duration.Milliseconds(), "reason", pendingCode)
	} else if err != nil {
		stage, code := Classify(err)
		span.RecordError(errors.New(code))
		span.SetStatus(codes.Error, code)
		span.SetAttributes(attribute.String("error.stage", stage), attribute.String("error.code", code))
		outcome = "error"
		Error(ctx, name+".failed", err, "duration_ms", duration.Milliseconds())
	} else {
		Info(ctx, name+".completed", "duration_ms", duration.Milliseconds())
	}
	meter := otel.Meter("betago-agent-next")
	if counter, e := meter.Int64Counter("betago_agent_operations", metric.WithDescription("Completed Agent business operations")); e == nil {
		counter.Add(ctx, 1, metric.WithAttributes(attribute.String("operation", name), attribute.String("outcome", outcome)))
	}
	if histogram, e := meter.Float64Histogram("betago_agent_operation_duration", metric.WithUnit("s")); e == nil {
		histogram.Record(ctx, duration.Seconds(), metric.WithAttributes(attribute.String("operation", name), attribute.String("outcome", outcome)))
	}
	span.SetAttributes(attribute.String("operation.outcome", outcome), attribute.Int64("duration_ms", duration.Milliseconds()))
	span.End()
}

// InjectJobPayload adds transport metadata without touching source-event facts.
// RawMessage values preserve integer precision and existing business fields.
func InjectJobPayload(ctx context.Context, payload json.RawMessage) json.RawMessage {
	if !trace.SpanContextFromContext(ctx).IsValid() {
		return payload
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(payload, &object) != nil || object == nil {
		return payload
	}
	carrier := propagation.MapCarrier{}
	propagation.TraceContext{}.Inject(ctx, carrier)
	b, err := json.Marshal(carrier)
	if err != nil {
		return payload
	}
	object["_otel"] = b
	out, err := json.Marshal(object)
	if err != nil {
		return payload
	}
	return out
}
func ExtractJobPayload(ctx context.Context, payload json.RawMessage) context.Context {
	var object map[string]json.RawMessage
	var carrier map[string]string
	if json.Unmarshal(payload, &object) != nil || json.Unmarshal(object["_otel"], &carrier) != nil {
		return ctx
	}
	filtered := propagation.MapCarrier{}
	for _, key := range []string{"traceparent", "tracestate"} {
		if value := carrier[key]; len(value) <= 512 {
			filtered[key] = value
		}
	}
	return propagation.TraceContext{}.Extract(ctx, filtered)
}
func JobContext(ctx context.Context, j domain.Job) context.Context {
	ctx = ExtractJobPayload(ctx, j.Payload)
	var payload struct {
		TaskID   string `json:"task_id"`
		Revision int64  `json:"revision"`
	}
	json.Unmarshal(j.Payload, &payload)
	return Context(ctx, "scope_hash", j.Scope.Key(), "message_id", j.Source.MessageID, "job_id", j.ID, "kind", string(j.Kind), "attempt", j.Attempt, "task_id", payload.TaskID, "revision", payload.Revision)
}
