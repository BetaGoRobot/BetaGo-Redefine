// Package memeindex owns only explicitly published, immutable meme generations.
package memeindex

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/memes"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/memestore"
)

const schemaVersion = "meme_projection_v2"
const maxResponseBytes = 16 << 20

var safeName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,79}$`)
var fields = []memes.Field{memes.FieldRelation, memes.FieldEmotion, memes.FieldKeys, memes.FieldDescription}
var (
	ErrInvalid   = errors.New("invalid meme index configuration or record")
	ErrConflict  = errors.New("immutable meme generation conflict")
	ErrResponse  = errors.New("invalid meme index response")
	ErrTransport = errors.New("meme index transport failed")
	ErrEmbedding = errors.New("meme embedding unavailable or invalid")
)

// HTTPError intentionally carries no URL, response body, dialogue or credentials.
type HTTPError struct{ Status int }

func (e *HTTPError) Error() string { return fmt.Sprintf("meme index HTTP status %d", e.Status) }

type Embedder interface {
	Embed(context.Context, []string) ([][]float32, error)
}
type PublicationStore interface {
	Put(context.Context, memestore.Kind, string, any) (memestore.Record, error)
}
type Options struct {
	Endpoint       string
	Username       string
	Password       string
	IndexPrefix    string
	EmbeddingModel string
	HTTPClient     *http.Client
	Dimensions     int
	Embedder       Embedder
	Ledger         PublicationStore
}
type Client struct {
	options  Options
	endpoint *url.URL
	http     *http.Client
}
type retriever struct {
	client   *Client
	manifest memestore.GenerationManifest
}

func New(o Options) (*Client, error) {
	u, err := url.Parse(o.Endpoint)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !safeName.MatchString(o.IndexPrefix) || o.Dimensions < 1 || o.Dimensions > 16000 || strings.TrimSpace(o.EmbeddingModel) == "" {
		return nil, ErrInvalid
	}
	var client http.Client
	if o.HTTPClient != nil {
		client = *o.HTTPClient
	}
	if client.Timeout <= 0 {
		client.Timeout = 15 * time.Second
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{options: o, endpoint: u, http: &client}, nil
}
func (c *Client) indexes(generation string) []string {
	names := make([]string, len(fields))
	for i, f := range fields {
		names[i] = c.options.IndexPrefix + "_" + generation + "_" + string(f)
	}
	return names
}
func (c *Client) ForGeneration(m memestore.GenerationManifest) (memes.Retriever, error) {
	if c == nil || !safeName.MatchString(m.Generation) || !m.Verified || m.CorpusHash == "" || m.ProjectionHash == "" || m.Dimension != c.options.Dimensions || m.EmbeddingModel != c.options.EmbeddingModel || m.RecordCount < 0 || !slices.Equal(m.Indexes, c.indexes(m.Generation)) {
		return nil, ErrInvalid
	}
	m.Indexes = slices.Clone(m.Indexes)
	return &retriever{client: c, manifest: m}, nil
}
func documentID(id string) string { return base64.RawURLEncoding.EncodeToString([]byte(id)) }
func sanitized(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	return ErrTransport
}
func (c *Client) request(ctx context.Context, method, path string, body []byte, contentType string) (int, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	u := *c.endpoint
	u.Path = strings.TrimRight(u.Path, "/") + pathPart(path)
	u.RawQuery = queryPart(path)
	req, err := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(body))
	if err != nil {
		return 0, nil, ErrInvalid
	}
	if c.options.Username != "" || c.options.Password != "" {
		req.SetBasicAuth(c.options.Username, c.options.Password)
	}
	if contentType == "" {
		contentType = "application/json"
	}
	req.Header.Set("Content-Type", contentType)
	response, err := c.http.Do(req)
	if err != nil {
		return 0, nil, sanitized(err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return response.StatusCode, nil, sanitized(err)
	}
	if len(raw) > maxResponseBytes {
		return response.StatusCode, nil, ErrResponse
	}
	return response.StatusCode, raw, nil
}
func pathPart(path string) string  { p, _, _ := strings.Cut(path, "?"); return p }
func queryPart(path string) string { _, q, _ := strings.Cut(path, "?"); return q }
func successful(status int) bool   { return status >= 200 && status < 300 }
func validVector(v []float32, dimensions int) bool {
	if len(v) != dimensions {
		return false
	}
	for _, n := range v {
		if math.IsNaN(float64(n)) || math.IsInf(float64(n), 0) {
			return false
		}
	}
	return true
}
func (c *Client) embed(ctx context.Context, texts []string) ([][]float32, error) {
	if c.options.Embedder == nil {
		return nil, ErrEmbedding
	}
	vectors, err := c.options.Embedder.Embed(ctx, slices.Clone(texts))
	if err != nil || len(vectors) != len(texts) {
		return nil, ErrEmbedding
	}
	for _, v := range vectors {
		if !validVector(v, c.options.Dimensions) {
			return nil, ErrEmbedding
		}
	}
	return vectors, nil
}
