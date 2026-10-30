// Package modelapi provides independent bounded model protocols. It does not own
// application identities, persistence, or agent orchestration.
package modelapi

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/telemetry"
)

type Endpoint struct {
	BaseURL, APIKey, Model, Protocol, ServiceTier string
	Timeout                                       time.Duration
	MaxRequestBytes, MaxResponseBytes             int64
	HTTPClient                                    *http.Client
}
type EmbeddingConfig struct {
	Endpoint
	Dimensions, Concurrency, MaxInputs int
}
type EmbedPort interface {
	Embed(context.Context, []string) ([][]float32, error)
}
type RerankPort interface {
	Rerank(context.Context, string, []domain.Evidence) ([]domain.Evidence, error)
}

func normalize(e Endpoint) (Endpoint, error) {
	u, err := url.Parse(e.BaseURL)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.TrimSpace(e.Model) == "" || strings.ContainsAny(e.APIKey, "\r\n") {
		return e, fmt.Errorf("%w: model endpoint", domain.ErrInvalid)
	}
	e.BaseURL = strings.TrimRight(e.BaseURL, "/")
	if e.Timeout == 0 {
		e.Timeout = 90 * time.Second
	}
	if e.Timeout <= 0 || e.Timeout > 10*time.Minute {
		return e, fmt.Errorf("%w: model timeout", domain.ErrInvalid)
	}
	if e.MaxRequestBytes == 0 {
		e.MaxRequestBytes = 16 << 20
	}
	if e.MaxResponseBytes == 0 {
		e.MaxResponseBytes = 8 << 20
	}
	if e.MaxRequestBytes < 1 || e.MaxRequestBytes > 64<<20 || e.MaxResponseBytes < 1 || e.MaxResponseBytes > 64<<20 {
		return e, fmt.Errorf("%w: model budgets", domain.ErrInvalid)
	}
	hc := http.Client{}
	if e.HTTPClient != nil {
		hc = *e.HTTPClient
	}
	if hc.Timeout == 0 {
		hc.Timeout = e.Timeout
	}
	if hc.Transport == nil {
		hc.Transport = newModelTransport()
	}
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	hc.Transport = telemetry.Transport("model", hc.Transport)
	e.HTTPClient = &hc
	return e, nil
}
