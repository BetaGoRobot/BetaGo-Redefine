package otel

import (
	"context"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.39.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

func init() {
	otel.SetTracerProvider(tracerProvider)
}

const (
	environment = "production"
	id          = 1
)

// tracerProvider jaeger provider
var (
	tracerProvider trace.TracerProvider
	loggerProvider *log.LoggerProvider
)

func OtelProvider() trace.TracerProvider {
	return tracerProvider
}

func LoggerProvider() *log.LoggerProvider {
	return loggerProvider
}

func T() trace.Tracer {
	return OtelTracer
}

func Init(config *config.OtelConfig) {
	if config != nil {
		tracerProvider, _ = newTracerProvider(config)
		loggerProvider, _ = newLoggerProvider(config)
		tracerName := config.TracerName
		OtelTracer = tracerProvider.Tracer(tracerName)
	} else {
		tracerProvider = noop.NewTracerProvider()
		loggerProvider = log.NewLoggerProvider()
	}
}

// BetaGoOtelTracer a
var (
	OtelTracer trace.Tracer
)

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

func newLoggerProvider(config *config.OtelConfig) (*log.LoggerProvider, error) {
	ctx := context.Background()
	exporter, err := otlploggrpc.New(
		ctx, otlploggrpc.WithEndpoint(config.CollectorEndpoint), otlploggrpc.WithInsecure(),
	)
	if err != nil {
		return nil, err
	}
	processor := log.NewBatchProcessor(exporter)
	return log.NewLoggerProvider(
		log.WithResource(newResource(config)),
		log.WithProcessor(processor),
	), nil
}
