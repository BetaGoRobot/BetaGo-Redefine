package modelapi

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/telemetry"
	"go.opentelemetry.io/otel/propagation"
)

type GatewayConfig struct {
	APIKey, EmbeddingModel, RerankModel  string
	Dimensions, MaxInputs, MaxConcurrent int
	MaxBodyBytes                         int64
	Timeout                              time.Duration
}
type gateway struct {
	c     GatewayConfig
	embed EmbedPort
	rank  RerankPort
	slots chan struct{}
}
type statusWriter struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (w *statusWriter) WriteHeader(status int) {
	if !w.wrote {
		w.status = status
		w.wrote = true
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *statusWriter) Write(body []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}

func NewGateway(c GatewayConfig, e EmbedPort, r RerankPort) (http.Handler, error) {
	if c.APIKey == "" || c.EmbeddingModel == "" || c.RerankModel == "" || c.Dimensions < 1 || e == nil {
		return nil, fmt.Errorf("%w: model gateway", domain.ErrInvalid)
	}
	if c.MaxConcurrent == 0 {
		c.MaxConcurrent = 8
	}
	if c.MaxConcurrent < 1 || c.MaxConcurrent > 128 {
		return nil, domain.ErrInvalid
	}
	if c.MaxInputs == 0 {
		c.MaxInputs = 256
	}
	if c.MaxBodyBytes == 0 {
		c.MaxBodyBytes = 4 << 20
	}
	if c.Timeout == 0 {
		c.Timeout = 2 * time.Minute
	}
	if c.MaxInputs < 1 || c.MaxInputs > 4096 || c.MaxBodyBytes < 1 || c.MaxBodyBytes > 64<<20 || c.Timeout <= 0 || c.Timeout > 10*time.Minute {
		return nil, domain.ErrInvalid
	}
	return &gateway{c: c, embed: e, rank: r, slots: make(chan struct{}, c.MaxConcurrent)}, nil
}
func (g *gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/health/ready" && r.Method == "GET" {
		w.WriteHeader(200)
		io.WriteString(w, `{"ready":true}`)
		return
	}
	route := "unknown"
	if r.URL.Path == "/v1/embeddings" || r.URL.Path == "/v1/rerank" {
		route = r.URL.Path
	}
	ctx := propagation.TraceContext{}.Extract(r.Context(), propagation.HeaderCarrier(r.Header))
	ctx = telemetry.Context(ctx, "route", route, "request_bytes", r.ContentLength, "request_limit_bytes", g.c.MaxBodyBytes)
	ctx, span := telemetry.Start(ctx, "model.gateway")
	recorded := &statusWriter{ResponseWriter: w, status: 200}
	w = recorded
	r = r.WithContext(ctx)
	defer func() {
		telemetry.Info(ctx, "model.gateway.response", "http_status", recorded.status)
		var err error
		if recorded.status >= 400 {
			err = telemetry.Wrap("model.gateway.response", "request_rejected", domain.ErrInvalid)
		}
		telemetry.End(ctx, span, err)
	}()
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+g.c.APIKey)) != 1 {
		http.Error(w, "unauthorized", 401)
		return
	}
	if r.Method != "POST" {
		http.Error(w, "method not allowed", 405)
		return
	}
	select {
	case g.slots <- struct{}{}:
		defer func() { <-g.slots }()
	default:
		http.Error(w, "model gateway busy", 429)
		return
	}
	if r.ContentLength > g.c.MaxBodyBytes {
		http.Error(w, "request too large", 413)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, g.c.MaxBodyBytes+1))
	if err != nil || int64(len(body)) > g.c.MaxBodyBytes {
		http.Error(w, "request too large", 413)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), g.c.Timeout)
	defer cancel()
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/v1/embeddings":
		g.embedding(ctx, w, body)
	case "/v1/rerank":
		g.rerank(ctx, w, body)
	default:
		http.Error(w, "not found", 404)
	}
}
func (g *gateway) embedding(ctx context.Context, w http.ResponseWriter, body []byte) {
	var b struct {
		Model      string          `json:"model"`
		Input      json.RawMessage `json:"input"`
		Dimensions int             `json:"dimensions"`
		Encoding   string          `json:"encoding_format"`
	}
	if json.Unmarshal(body, &b) != nil || b.Model != g.c.EmbeddingModel || (b.Dimensions != 0 && b.Dimensions != g.c.Dimensions) || (b.Encoding != "" && b.Encoding != "float" && b.Encoding != "base64") {
		http.Error(w, "invalid embedding request", 400)
		return
	}
	var texts []string
	var single string
	if json.Unmarshal(b.Input, &single) == nil {
		texts = []string{single}
	} else if json.Unmarshal(b.Input, &texts) != nil {
		http.Error(w, "invalid embedding input", 400)
		return
	}
	if len(texts) < 1 || len(texts) > g.c.MaxInputs {
		http.Error(w, "invalid input count", 400)
		return
	}
	vectors, err := g.embed.Embed(ctx, texts)
	if err != nil || len(vectors) != len(texts) {
		http.Error(w, "embedding upstream unavailable", 502)
		return
	}
	data := make([]any, len(vectors))
	for i, v := range vectors {
		if !validVector(v, g.c.Dimensions) {
			http.Error(w, "invalid embedding upstream", 502)
			return
		}
		var encoded any = v
		if b.Encoding == "base64" {
			raw := make([]byte, len(v)*4)
			for j, x := range v {
				binary.LittleEndian.PutUint32(raw[j*4:], math.Float32bits(x))
			}
			encoded = base64.StdEncoding.EncodeToString(raw)
		}
		data[i] = map[string]any{"object": "embedding", "index": i, "embedding": encoded}
	}
	json.NewEncoder(w).Encode(map[string]any{"object": "list", "model": b.Model, "data": data, "usage": map[string]int{"prompt_tokens": 0, "total_tokens": 0}})
}
func (g *gateway) rerank(ctx context.Context, w http.ResponseWriter, body []byte) {
	var b struct {
		Model, Query string
		Documents    []string
		TopN         int `json:"top_n"`
	}
	if json.Unmarshal(body, &b) != nil || b.Model != g.c.RerankModel || len(b.Documents) < 1 || len(b.Documents) > g.c.MaxInputs || b.TopN < 0 {
		http.Error(w, "invalid rerank request", 400)
		return
	}
	if g.rank == nil {
		http.Error(w, "rerank unavailable", 503)
		return
	}
	items := make([]domain.Evidence, len(b.Documents))
	for i, s := range b.Documents {
		items[i] = domain.Evidence{ID: strconv.Itoa(i), Text: s}
	}
	ranked, err := g.rank.Rerank(ctx, b.Query, items)
	if err != nil || len(ranked) != len(items) {
		http.Error(w, "rerank upstream unavailable", 502)
		return
	}
	count := len(ranked)
	if b.TopN > 0 {
		count = min(count, b.TopN)
	}
	out := make([]rankResult, count)
	seen := map[int]bool{}
	for i, d := range ranked {
		idx, err := strconv.Atoi(d.ID)
		if err != nil || idx < 0 || idx >= len(items) || seen[idx] || math.IsNaN(d.Score) || math.IsInf(d.Score, 0) {
			http.Error(w, "invalid rerank upstream", 502)
			return
		}
		seen[idx] = true
		if i < count {
			out[i] = rankResult{idx, d.Score}
		}
	}
	json.NewEncoder(w).Encode(rankResponse{out})
}
