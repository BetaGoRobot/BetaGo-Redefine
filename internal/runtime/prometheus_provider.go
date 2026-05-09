package runtime

import (
	"context"
	"strings"

	"github.com/VictoriaMetrics/metrics"
)

// PrometheusProvider implements MetricsProvider by writing all registered
// VictoriaMetrics metrics in Prometheus text exposition format.
type PrometheusProvider struct{}

// PrometheusMetrics writes all registered metrics in Prometheus format.
func (PrometheusProvider) PrometheusMetrics(_ context.Context) (string, error) {
	var builder strings.Builder
	metrics.WritePrometheus(&builder, false)
	return builder.String(), nil
}
