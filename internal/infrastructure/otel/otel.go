package otel

import (
	"context"
	"errors"
	stdlog "log"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xerror"
	"github.com/BetaGoRobot/go_utils/reflecting"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	log2 "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

func init() {
	tracerProvider = noop.NewTracerProvider()
	loggerProvider = log2.NewLoggerProvider()
	OtelTracer = tracerProvider.Tracer("betago")
	otel.SetTracerProvider(tracerProvider)
}

const (
	environment = "production"
	id          = 1
)

// tracerProvider jaeger provider
var (
	tracerProvider trace.TracerProvider
	loggerProvider *log2.LoggerProvider
)

func OtelProvider() trace.TracerProvider {
	return tracerProvider
}

func LoggerProvider() *log2.LoggerProvider {
	return loggerProvider
}

func T() trace.Tracer {
	return OtelTracer
}

func Start(ctx context.Context, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	return T().Start(ctx, reflecting.GetCurrentFuncDepth(2), opts...)
}

func StartNamed(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	if strings.TrimSpace(name) == "" {
		return Start(ctx, opts...)
	}
	return T().Start(ctx, name, opts...)
}

func RecordError(span trace.Span, err error) {
	if span == nil || err == nil {
		return
	}
	span.RecordError(err)
	if !errors.Is(err, xerror.ErrStageSkip) {
		span.SetStatus(codes.Error, err.Error())
	}
}

func RecordErrorPtr(span trace.Span, err *error) {
	if err == nil {
		return
	}
	RecordError(span, *err)
}

func PreviewString(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	if limit <= 3 {
		return value[:limit]
	}
	return value[:limit-3] + "..."
}

func PreviewAttrs(key, value string, limit int) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.Int(key+".len", len([]rune(strings.TrimSpace(value)))),
		attribute.String(key+".preview", PreviewString(value, limit)),
	}
}

func Init(config *config.OtelConfig) {
	if config == nil || config.CollectorEndpoint == "" || config.TracerName == "" || config.ServiceName == "" {
		setNoop("otel config missing or incomplete")
		return
	}

	tp, err := newTracerProvider(config)
	if err != nil {
		setNoop("trace exporter init failed: " + err.Error())
		return
	}
	lp, err := newLoggerProvider(config)
	if err != nil {
		setNoop("log exporter init failed: " + err.Error())
		return
	}

	tracerProvider = tp
	loggerProvider = lp
	OtelTracer = tracerProvider.Tracer(config.TracerName)
	otel.SetTracerProvider(tracerProvider)
}

// BetaGoOtelTracer a
var (
	OtelTracer trace.Tracer
)

func setNoop(reason string) {
	tracerProvider = noop.NewTracerProvider()
	loggerProvider = log2.NewLoggerProvider()
	OtelTracer = tracerProvider.Tracer("betago")
	otel.SetTracerProvider(tracerProvider)
	stdlog.Printf("[WARN] otel disabled, falling back to noop: %s", reason)
}

func newTracerProvider(config *config.OtelConfig) (*tracesdk.TracerProvider, error) {
	// Create the Jaeger exporter
	ctx := context.Background()
	exp, err := otlptracegrpc.New(ctx, otlptracegrpc.WithEndpoint(config.CollectorEndpoint), otlptracegrpc.WithInsecure())
	if err != nil {
		return nil, err
	}

	tp := tracesdk.NewTracerProvider(
		tracesdk.WithBatcher(exp),
		tracesdk.WithResource(newResource(config)),
	)
	return tp, nil
}

func newResource(config *config.OtelConfig) *resource.Resource {
	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName(config.ServiceName),
			attribute.String("environment", environment),
			attribute.Int64("ID", id),
		),
	)
	if err != nil {
		panic(err)
	}
	return res
}

func newLoggerProvider(config *config.OtelConfig) (*log2.LoggerProvider, error) {
	ctx := context.Background()
	exporter, err := otlploggrpc.New(
		ctx, otlploggrpc.WithEndpoint(config.CollectorEndpoint), otlploggrpc.WithInsecure(),
	)
	if err != nil {
		return nil, err
	}
	processor := log2.NewBatchProcessor(exporter)
	return log2.NewLoggerProvider(
		log2.WithResource(newResource(config)),
		log2.WithProcessor(processor),
	), nil
}
