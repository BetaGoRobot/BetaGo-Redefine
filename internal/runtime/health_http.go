package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// HealthHTTPModule 承载轻量级管理面服务，用于暴露 liveness、readiness
// 和更细粒度的组件状态。
type HealthHTTPModule struct {
	addr            string
	shutdownTimeout time.Duration
	registry        *Registry
	metrics         []MetricsProvider
	server          *http.Server
	listener        net.Listener
}

// NewHealthHTTPModule 构造一个可选的管理面 HTTP 模块，用来输出
// liveness、readiness 和完整组件快照。
func NewHealthHTTPModule(addr string, shutdownTimeout time.Duration, registry *Registry, metricsProviders ...MetricsProvider) *HealthHTTPModule {
	return &HealthHTTPModule{
		addr:            addr,
		shutdownTimeout: shutdownTimeout,
		registry:        registry,
		metrics:         append([]MetricsProvider(nil), metricsProviders...),
	}
}

// Name 返回管理面模块在注册表里的稳定名称。
func (m *HealthHTTPModule) Name() string {
	return "management_http"
}

// Critical 返回 false，因为管理面虽然重要，但不应该阻断核心机器人服务启动。
func (m *HealthHTTPModule) Critical() bool {
	return false
}

// Init 校验管理面是否启用。空地址不是错误，而是运维显式关闭了这块能力。
func (m *HealthHTTPModule) Init(context.Context) error {
	if m == nil {
		return errors.New("health http module is nil")
	}
	if m.addr == "" {
		return ErrDisabled
	}
	return nil
}

// Start 监听 TCP 地址并启动一个极小的管理面 mux。这里刻意不依赖业务路由，
// 以确保业务处理异常时健康面仍然可用。
func (m *HealthHTTPModule) Start(context.Context) error {
	listener, err := net.Listen("tcp", m.addr)
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/livez", m.handleLive)
	mux.HandleFunc("/readyz", m.handleReady)
	mux.HandleFunc("/healthz", m.handleHealth)
	mux.HandleFunc("/statusz", m.handleHealth)
	mux.HandleFunc("/metrics", m.handleMetrics)
	m.server = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       30 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      5 * time.Second,
	}
	m.listener = listener
	go func() {
		_ = m.server.Serve(listener)
	}()
	return nil
}

// Ready 判断 HTTP 服务是否已经成功绑定监听端口。
func (m *HealthHTTPModule) Ready(context.Context) error {
	if m == nil || m.server == nil || m.listener == nil {
		return errors.New("health http server not started")
	}
	return nil
}

// Stop 尝试优雅关闭 HTTP 服务，避免探针或运维只看到一次硬断连。
func (m *HealthHTTPModule) Stop(ctx context.Context) error {
	if m == nil || m.server == nil {
		return nil
	}
	timeout := m.shutdownTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	shutdownCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return m.server.Shutdown(shutdownCtx)
}

// Stats 返回监听地址，方便运维排查和状态面输出。
func (m *HealthHTTPModule) Stats() map[string]any {
	if m == nil {
		return nil
	}
	return map[string]any{
		"addr": m.addr,
	}
}

// handleLive 返回进程级 liveness，它不要求所有 critical 模块都 ready。
func (m *HealthHTTPModule) handleLive(w http.ResponseWriter, _ *http.Request) {
	snapshot := m.snapshot()
	if snapshot.Live {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
		return
	}
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte("not live"))
}

// handleReady 返回注册表聚合后的 readiness 状态。
func (m *HealthHTTPModule) handleReady(w http.ResponseWriter, _ *http.Request) {
	snapshot := m.snapshot()
	if snapshot.Ready {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready"))
		return
	}
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte("not ready"))
}

// handleHealth 返回完整组件快照，供运维和自动化系统读取。
func (m *HealthHTTPModule) handleHealth(w http.ResponseWriter, _ *http.Request) {
	snapshot := m.snapshot()
	statusCode := http.StatusOK
	if !snapshot.Ready {
		statusCode = http.StatusServiceUnavailable
	}
	writeJSON(w, statusCode, snapshot)
}

func (m *HealthHTTPModule) handleMetrics(w http.ResponseWriter, r *http.Request) {
	payload, err := m.metricsPayload(r.Context())
	if err != nil {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(err.Error()))
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(payload))
}

// snapshot 对 nil registry 做保护，避免测试接线错误时管理面直接 panic。
func (m *HealthHTTPModule) snapshot() Snapshot {
	if m == nil || m.registry == nil {
		return Snapshot{}
	}
	return m.registry.Snapshot()
}

// writeJSON 统一管理面响应的 JSON 输出格式。
func writeJSON(w http.ResponseWriter, statusCode int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(payload)
}

func (m *HealthHTTPModule) metricsPayload(ctx context.Context) (string, error) {
	var builder strings.Builder
	snapshot := m.snapshot()

	writePrometheusGauge(&builder, "betago_runtime_live", boolToFloat(snapshot.Live))
	writePrometheusGauge(&builder, "betago_runtime_ready", boolToFloat(snapshot.Ready))
	writePrometheusGauge(&builder, "betago_runtime_degraded", boolToFloat(snapshot.Degraded))
	for _, component := range snapshot.Components {
		fmt.Fprintf(
			&builder,
			"betago_runtime_component_state{name=%q,state=%q,critical=%q} 1\n",
			prometheusLabelValue(component.Name),
			prometheusLabelValue(string(component.State)),
			prometheusLabelValue(strconv.FormatBool(component.Critical)),
		)
	}

	for _, provider := range m.metrics {
		if provider == nil {
			continue
		}
		payload, err := provider.PrometheusMetrics(ctx)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(payload) == "" {
			continue
		}
		builder.WriteString(payload)
		if !strings.HasSuffix(payload, "\n") {
			builder.WriteString("\n")
		}
	}

	return builder.String(), nil
}

func writePrometheusGauge(builder *strings.Builder, name string, value float64) {
	builder.WriteString(name)
	builder.WriteByte(' ')
	builder.WriteString(strconv.FormatFloat(value, 'f', -1, 64))
	builder.WriteByte('\n')
}

func boolToFloat(value bool) float64 {
	if value {
		return 1
	}
	return 0
}

func prometheusLabelValue(value string) string {
	return strings.NewReplacer(`\`, `\\`, "\n", `\n`, `"`, `\"`).Replace(value)
}
