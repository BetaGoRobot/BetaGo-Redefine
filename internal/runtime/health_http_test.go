package runtime

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeMetricsProvider struct {
	payload string
	err     error
}

func (p fakeMetricsProvider) PrometheusMetrics(context.Context) (string, error) {
	return p.payload, p.err
}

func TestHealthHTTPModuleHandleMetricsIncludesRuntimeAndProviderMetrics(t *testing.T) {
	registry := NewRegistry()
	registry.Register("critical", true)
	registry.Update("critical", StateReady, "", nil)
	registry.SetLive(true)

	module := NewHealthHTTPModule("127.0.0.1:0", 0, registry, fakeMetricsProvider{
		payload: "betago_custom_metric 42\n",
	})

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	recorder := httptest.NewRecorder()

	module.handleMetrics(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("handleMetrics() status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if contentType := recorder.Header().Get("Content-Type"); !strings.Contains(contentType, "text/plain") {
		t.Fatalf("handleMetrics() content-type = %q, want text/plain", contentType)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, "betago_runtime_live 1") {
		t.Fatalf("handleMetrics() body = %q, want runtime live metric", body)
	}
	if !strings.Contains(body, "betago_runtime_ready 1") {
		t.Fatalf("handleMetrics() body = %q, want runtime ready metric", body)
	}
	if !strings.Contains(body, "betago_custom_metric 42") {
		t.Fatalf("handleMetrics() body = %q, want provider metric", body)
	}
}

func TestHealthHTTPModuleHandleMetricsReturnsUnavailableWhenProviderFails(t *testing.T) {
	module := NewHealthHTTPModule("127.0.0.1:0", 0, NewRegistry(), fakeMetricsProvider{
		err: errors.New("metrics unavailable"),
	})

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	recorder := httptest.NewRecorder()

	module.handleMetrics(recorder, req)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("handleMetrics() status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}
	if !strings.Contains(recorder.Body.String(), "metrics unavailable") {
		t.Fatalf("handleMetrics() body = %q, want provider error", recorder.Body.String())
	}
}
