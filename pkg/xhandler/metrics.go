package xhandler

import (
	"context"
	"fmt"
	stdlog "log"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/VictoriaMetrics/metrics"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

var (
	metricsEnabled bool

	otelStageCounter   metric.Int64Counter
	otelStageHistogram metric.Float64Histogram
)

func init() {
	metrics.ExposeMetadata(true)
	initOtelInstruments()
}

func initOtelInstruments() {
	meter := otel.MeterProvider().Meter("betago/xhandler")
	var err error
	otelStageCounter, err = meter.Int64Counter(
		"betago_stage_execution_total",
		metric.WithDescription("Total number of stage executions"),
	)
	if err != nil {
		stdlog.Printf("[WARN] failed to create otel stage counter: %v", err)
	}
	otelStageHistogram, err = meter.Float64Histogram(
		"betago_stage_duration_seconds",
		metric.WithDescription("Duration of stage execution in seconds"),
	)
	if err != nil {
		stdlog.Printf("[WARN] failed to create otel stage histogram: %v", err)
	}
}

// InitMetrics enables VictoriaMetrics push mode. If pushURL is empty, metrics
// recording becomes a no-op (noop), so callers incur negligible overhead.
func InitMetrics(pushURL string, pushInterval time.Duration, instance string) {
	if pushURL == "" {
		stdlog.Printf("[WARN] vm metrics disabled: push_url is empty, falling back to noop")
		return
	}
	if pushInterval <= 0 {
		pushInterval = 10 * time.Second
	}
	extraLabels := ""
	if instance != "" {
		extraLabels = fmt.Sprintf(`instance=%q`, instance)
	}
	metrics.InitPush(pushURL, pushInterval, extraLabels, true)
	metricsEnabled = true
}

// RecordStageExecution records metrics for a single stage execution via both
// VictoriaMetrics push and OTel metrics SDK.
func RecordStageExecution(stageName, chatName string, skipped bool, profile botidentity.Profile, startTime time.Time) {
	skippedStr := "false"
	if skipped {
		skippedStr = "true"
	}

	// VictoriaMetrics push
	if metricsEnabled {
		counterName := fmt.Sprintf(`betago_stage_execution_total{stage=%q,chat_name=%q,skipped=%q,bot_name=%q}`, stageName, chatName, skippedStr, profile.BotName)
		metrics.GetOrCreateCounter(counterName).Inc()

		histogramName := fmt.Sprintf(`betago_stage_duration_seconds{stage=%q,chat_name=%q,skipped=%q,bot_name=%q}`, stageName, chatName, skippedStr, profile.BotName)
		metrics.GetOrCreatePrometheusHistogram(histogramName).UpdateDuration(startTime)
	}

	// OTel metrics
	duration := time.Since(startTime).Seconds()
	recordOtelMetrics(context.Background(), stageName, chatName, skippedStr, profile.BotName, duration)
}

func recordOtelMetrics(ctx context.Context, stageName, chatName, skippedStr, botName string, duration float64) {
	if otelStageCounter == nil || otelStageHistogram == nil {
		return
	}
	attrs := []attribute.KeyValue{
		attribute.String("stage", stageName),
		attribute.String("chat_name", chatName),
		attribute.String("skipped", skippedStr),
		attribute.String("bot_name", botName),
	}
	otelStageCounter.Add(ctx, 1, metric.WithAttributes(attrs...))
	otelStageHistogram.Record(ctx, duration, metric.WithAttributes(attrs...))
}
