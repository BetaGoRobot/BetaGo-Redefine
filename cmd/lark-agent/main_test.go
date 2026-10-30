package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHealthModeNeedsNoConfigurationAndChecksReadiness(t *testing.T) {
	status := http.StatusOK
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health/ready" {
			t.Error("wrong probe path")
		}
		w.WriteHeader(status)
	}))
	defer s.Close()
	if err := run(context.Background(), []string{"-mode", "health", "-health-url", s.URL + "/health/ready", "-config", "/nonexistent"}); err != nil {
		t.Fatal(err)
	}
	status = http.StatusServiceUnavailable
	if err := run(context.Background(), []string{"-mode", "health", "-health-url", s.URL + "/health/ready"}); err == nil {
		t.Fatal("unready service passed health check")
	}
}

func TestHealthModeHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	if err := run(ctx, []string{"-mode", "health", "-health-url", "http://127.0.0.1:1/health/ready"}); err == nil {
		t.Fatal("canceled probe succeeded")
	}
	if time.Since(start) > time.Second {
		t.Fatal("canceled health probe blocked")
	}
}

func TestUnknownModeFailsBeforeReadingConfiguration(t *testing.T) {
	err := run(context.Background(), []string{"-mode", "old-runtime", "-config", "/sensitive-configuration"})
	if err == nil || strings.Contains(err.Error(), "sensitive-configuration") {
		t.Fatal("invalid mode or unsafe configuration error")
	}
}
