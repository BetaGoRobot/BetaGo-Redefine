package runtime

import "context"

// MetricsProvider 负责把模块级指标编码成 Prometheus 文本格式。
// 管理面只负责聚合，不感知具体业务指标细节。
type MetricsProvider interface {
	PrometheusMetrics(context.Context) (string, error)
}
