package otel

import (
	"context"
	stdlog "log"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	log2 "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.39.0"
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
