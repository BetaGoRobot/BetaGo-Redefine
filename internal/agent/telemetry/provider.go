package telemetry

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	logapi "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

type bridge struct {
	stdout slog.Handler
	logger logapi.Logger
	attrs  []slog.Attr
	group  string
}

func (h *bridge) Enabled(ctx context.Context, level slog.Level) bool {
	return h.stdout.Enabled(ctx, level)
}
func (h *bridge) WithAttrs(attrs []slog.Attr) slog.Handler {
	x := *h
	x.attrs = append([]slog.Attr(nil), h.attrs...)
	for _, a := range attrs {
		a.Key = h.group + a.Key
		x.attrs = append(x.attrs, a)
	}
	return &x
}
func (h *bridge) WithGroup(name string) slog.Handler {
	x := *h
	if name != "" {
		x.group += name + "."
	}
	return &x
}
func (h *bridge) Handle(ctx context.Context, r slog.Record) error {
	clean := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	values := map[string]slog.Attr{}
	order := []string{}
	add := func(a slog.Attr) {
		a = safeAttr(a)
		if _, ok := values[a.Key]; !ok {
			order = append(order, a.Key)
		}
		values[a.Key] = a
	}
	for _, a := range h.attrs {
		add(a)
	}
	r.Attrs(func(a slog.Attr) bool { a.Key = h.group + a.Key; add(a); return true })
	add(slog.String("level", r.Level.String()))
	add(slog.String("severity", strings.ToLower(r.Level.String())))
	add(slog.String("Severity", r.Level.String()))
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		add(slog.String("trace_id", sc.TraceID().String()))
		add(slog.String("span_id", sc.SpanID().String()))
	}
	for _, key := range order {
		if key != "level" {
			clean.AddAttrs(values[key])
		}
	}
	err := h.stdout.Handle(ctx, clean)
	if h.logger != nil {
		var record logapi.Record
		record.SetTimestamp(r.Time)
		record.SetObservedTimestamp(time.Now())
		record.SetEventName(r.Message)
		body := description(r.Message) + " [" + r.Message + "]"
		for _, key := range []string{"tool", "model_id", "response_model", "response_id", "provider_request_id", "choice", "reason", "error_stage", "error_code"} {
			if a, ok := values[key]; ok {
				body += " " + key + "=" + a.Value.String()
			}
		}
		if field, ok := values["payload_field"]; ok {
			if content, ok := values[field.Value.String()]; ok {
				body += "\n" + content.Value.String()
			}
		}
		record.SetBody(attribute.StringValue(body))
		record.SetSeverityText(r.Level.String())
		switch {
		case r.Level >= slog.LevelError:
			record.SetSeverity(logapi.SeverityError)
		case r.Level >= slog.LevelWarn:
			record.SetSeverity(logapi.SeverityWarn)
		case r.Level >= slog.LevelInfo:
			record.SetSeverity(logapi.SeverityInfo)
		default:
			record.SetSeverity(logapi.SeverityDebug)
		}
		record.AddAttributes(attribute.String("level", r.Level.String()))
		clean.Attrs(func(a slog.Attr) bool {
			key := a.Key
			switch a.Value.Kind() {
			case slog.KindAny:
				if content, ok := a.Value.Any().(sanitizedContent); ok {
					record.AddAttributes(attribute.String(key, string(content)))
				}
			case slog.KindString:
				record.AddAttributes(attribute.String(key, a.Value.String()))
			case slog.KindInt64:
				record.AddAttributes(attribute.Int64(key, a.Value.Int64()))
			case slog.KindUint64:
				record.AddAttributes(attribute.String(key, strconv.FormatUint(a.Value.Uint64(), 10)))
			case slog.KindFloat64:
				record.AddAttributes(attribute.Float64(key, a.Value.Float64()))
			case slog.KindBool:
				record.AddAttributes(attribute.Bool(key, a.Value.Bool()))
			case slog.KindDuration:
				record.AddAttributes(attribute.Int64(key, a.Value.Duration().Nanoseconds()))
			case slog.KindTime:
				record.AddAttributes(attribute.String(key, a.Value.Time().Format(time.RFC3339Nano)))
			}
			return true
		})
		h.logger.Emit(ctx, record)
	}
	return err
}

// Initialize uses the same SDKs and OTLP collector as the original service,
// without importing its package-level configuration/bootstrap side effects.
func Initialize(ctx context.Context, mode string) (func(context.Context) error, error) {
	return initialize(ctx, mode, os.Stdout)
}
func initialize(ctx context.Context, mode string, writer io.Writer) (func(context.Context) error, error) {
	if mode != "agent" && mode != "worker" && mode != "models" {
		return nil, errors.New("invalid telemetry service role")
	}
	service := "betago-agent-next-" + mode
	if value := strings.TrimSpace(os.Getenv("OTEL_SERVICE_NAME")); value != "" {
		if !token.MatchString(value) {
			return nil, errors.New("invalid telemetry service name")
		}
		service = value
	}
	host, _ := os.Hostname()
	namespace := serviceNamespace()
	res := resource.NewSchemaless(attribute.String("service.name", service), attribute.String("service.namespace", namespace), attribute.String("service_name", service), attribute.String("service_namespace", namespace), attribute.String("namespace", namespace), attribute.String("service.instance.id", host), attribute.String("deployment.environment.name", "production"))
	traceOpts := []sdktrace.TracerProviderOption{sdktrace.WithResource(res)}
	logOpts := []sdklog.LoggerProviderOption{sdklog.WithResource(res)}
	metricOpts := []sdkmetric.Option{sdkmetric.WithResource(res)}
	target := strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
	collector := "disabled"
	if target != "" {
		u, err := url.Parse(target)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return nil, errors.New("invalid OTLP collector URL")
		}
		collector = u.Host
		exportCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		traces, err := otlptracegrpc.New(exportCtx, otlptracegrpc.WithEndpointURL(target))
		if err != nil {
			return nil, errors.New("OTLP trace exporter initialization failed")
		}
		logs, err := otlploggrpc.New(exportCtx, otlploggrpc.WithEndpointURL(target))
		if err != nil {
			traces.Shutdown(exportCtx)
			return nil, errors.New("OTLP log exporter initialization failed")
		}
		metrics, err := otlpmetricgrpc.New(exportCtx, otlpmetricgrpc.WithEndpointURL(target))
		if err != nil {
			traces.Shutdown(exportCtx)
			logs.Shutdown(exportCtx)
			return nil, errors.New("OTLP metric exporter initialization failed")
		}
		traceOpts = append(traceOpts, sdktrace.WithBatcher(traces, sdktrace.WithBatchTimeout(time.Second)))
		logOpts = append(logOpts, sdklog.WithProcessor(sdklog.NewBatchProcessor(logs, sdklog.WithExportInterval(time.Second))))
		metricOpts = append(metricOpts, sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metrics, sdkmetric.WithInterval(15*time.Second))))
	}
	tp := sdktrace.NewTracerProvider(traceOpts...)
	lp := sdklog.NewLoggerProvider(logOpts...)
	mp := sdkmetric.NewMeterProvider(metricOpts...)
	otel.SetTracerProvider(tp)
	otel.SetMeterProvider(mp)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	level := slog.LevelInfo
	if strings.EqualFold(os.Getenv("OTEL_LOG_LEVEL"), "debug") {
		level = slog.LevelDebug
	}
	stdout := slog.NewJSONHandler(writer, &slog.HandlerOptions{Level: level})
	slog.SetDefault(slog.New(&bridge{stdout: stdout, logger: lp.Logger("betago-agent-next"), attrs: []slog.Attr{slog.String("service.name", service), slog.String("service.namespace", namespace), slog.String("service_name", service), slog.String("service_namespace", namespace), slog.String("namespace", namespace), slog.String("service.role", mode)}}))
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(error) {
		Warn(context.Background(), "telemetry.export.failed", "error_code", "exporter_unavailable", "collector", collector)
	}))
	Info(ctx, "agent.telemetry.ready", "service_name", service, "collector", collector, "otlp_enabled", target != "")
	return func(stop context.Context) error {
		return errors.Join(tp.Shutdown(stop), lp.Shutdown(stop), mp.Shutdown(stop))
	}, nil
}

func serviceNamespace() string {
	if value := strings.TrimSpace(os.Getenv("OTEL_SERVICE_NAMESPACE")); value != "" && token.MatchString(value) {
		return value
	}
	return "betago"
}
