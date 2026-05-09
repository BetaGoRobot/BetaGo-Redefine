package otel

import (
	"context"
	stdlog "log"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
)

const (
	// labelMaxLen 标签值最大长度，超长截断防高基数标签爆炸
	labelMaxLen = 64

	metricSpanDurationName = "span_duration_seconds"
	metricSpanCountName    = "span_total"
)

var (
	spanDuration metric.Float64Histogram
	spanCount    metric.Int64Counter
)

func init() {
	initSpanMetrics()
}

func initSpanMetrics() {
	meter := meterProvider.Meter("betago/otel")
	var err error
	spanCount, err = meter.Int64Counter(
		metricSpanCountName,
		metric.WithDescription("Total number of span executions"),
	)
	if err != nil {
		stdlog.Printf("[WARN] failed to create otel span counter: %v", err)
	}
	spanDuration, err = meter.Float64Histogram(
		metricSpanDurationName,
		metric.WithDescription("Duration of span execution in seconds"),
	)
	if err != nil {
		stdlog.Printf("[WARN] failed to create otel span histogram: %v", err)
	}
}

// truncateLabel 截断标签值，防止高基数标签爆炸
func truncateLabel(v string) string {
	if len(v) <= labelMaxLen {
		return v
	}
	return v[:labelMaxLen] + "..."
}

// spanMetricsProcessor 是一个 SpanProcessor，在 span 结束时
// 自动将 span 的 name 和 attributes 转为 OTel metrics。
type spanMetricsProcessor struct{}

var _ tracesdk.SpanProcessor = (*spanMetricsProcessor)(nil)

func (p *spanMetricsProcessor) OnStart(ctx context.Context, s tracesdk.ReadWriteSpan) {}

func (p *spanMetricsProcessor) OnEnd(s tracesdk.ReadOnlySpan) {
	if spanCount == nil || spanDuration == nil {
		return
	}

	name := s.Name()
	duration := s.EndTime().Sub(s.StartTime()).Seconds()

	// 从 span attributes 提取标签，截断防爆炸
	attrs := []attribute.KeyValue{
		attribute.String("span_name", truncateLabel(name)),
	}
	for _, a := range s.Attributes() {
		attrs = append(attrs, attribute.String(string(a.Key), truncateLabel(a.Value.Emit())))
	}

	opt := metric.WithAttributes(attrs...)
	ctx := context.Background()
	spanCount.Add(ctx, 1, opt)
	spanDuration.Record(ctx, duration, opt)
}

func (p *spanMetricsProcessor) ForceFlush(ctx context.Context) error { return nil }

func (p *spanMetricsProcessor) Shutdown(ctx context.Context) error { return nil }
