// Package meili indexes and retrieves scoped candidates. Callers must verify
// every candidate's source references against the authoritative business store.
// This package never generates embeddings or invokes a model.
package meili

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/telemetry"
)

// Options keeps credentials and index configuration explicit. EmbeddingModel is
// the adapter's complete provider/model/config fingerprint, not a display name.
// Changing that value separates vector candidates even at equal dimensions.
type Options struct {
	Endpoint, APIKey, MessagesIndex, ContextsIndex, EmbeddingModel string
	HTTPClient                                                     *http.Client
	Timeout, PollInterval                                          time.Duration
	MaxRequestBytes, MaxResponseBytes                              int64
	VectorDimensions                                               int
}

type Client struct {
	endpoint, apiKey, messages, contexts, model string
	http                                        *http.Client
	timeout, poll                               time.Duration
	maxRequest, maxResponse                     int64
	dimensions                                  int
}

var indexName = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// New validates options without making network requests. Zero time/cap values
// select bounded defaults (30s, 25ms polling, 8 MiB). Dimensions and the model
// fingerprint are required. The injected client is copied, never mutated.
func New(o Options) (*Client, error) {
	u, err := url.Parse(o.Endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.TrimSpace(o.APIKey) == "" || strings.ContainsAny(o.APIKey, "\r\n") {
		return nil, fmt.Errorf("%w: meili endpoint or credentials", domain.ErrInvalid)
	}
	if o.MessagesIndex == "" {
		o.MessagesIndex = "messages"
	}
	if o.ContextsIndex == "" {
		o.ContextsIndex = "contexts"
	}
	if !indexName.MatchString(o.MessagesIndex) || !indexName.MatchString(o.ContextsIndex) || o.MessagesIndex == o.ContextsIndex || o.VectorDimensions <= 0 || o.VectorDimensions > 65536 || strings.TrimSpace(o.EmbeddingModel) == "" || o.Timeout < 0 || o.PollInterval < 0 || o.MaxRequestBytes < 0 || o.MaxResponseBytes < 0 {
		return nil, fmt.Errorf("%w: meili index, vector, or request options", domain.ErrInvalid)
	}
	if o.Timeout == 0 {
		o.Timeout = 30 * time.Second
	}
	if o.PollInterval == 0 {
		o.PollInterval = 25 * time.Millisecond
	}
	if o.MaxResponseBytes == 0 {
		o.MaxResponseBytes = 8 << 20
	}
	if o.MaxRequestBytes == 0 {
		o.MaxRequestBytes = 8 << 20
	}
	if o.MaxRequestBytes > 1<<30 || o.MaxResponseBytes > 1<<30 {
		return nil, fmt.Errorf("%w: meili request or response cap", domain.ErrInvalid)
	}
	hc := http.Client{}
	if o.HTTPClient != nil {
		hc = *o.HTTPClient
	}
	// Never forward the bearer secret through an endpoint redirect.
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	hc.Transport = telemetry.Transport("meili", hc.Transport)
	return &Client{endpoint: strings.TrimRight(u.String(), "/"), apiKey: o.APIKey, messages: o.MessagesIndex, contexts: o.ContextsIndex, model: stableID(o.EmbeddingModel, fmt.Sprint(o.VectorDimensions)), http: &hc, timeout: o.Timeout, poll: o.PollInterval, maxRequest: o.MaxRequestBytes, maxResponse: o.MaxResponseBytes, dimensions: o.VectorDimensions}, nil
}

type responseError struct {
	status int
	code   string
}

// Keep only protocol constants needed for safe initialization recovery. Server
// errors may reflect document bodies or credentials, including in their codes.
func safeCode(code string) string {
	switch code {
	case "index_not_found", "index_already_exists":
		return code
	default:
		return ""
	}
}

func (e *responseError) Error() string {
	return fmt.Sprintf("meili request rejected (HTTP %d)", e.status)
}

func (c *Client) request(ctx context.Context, method, path string, body, out any) error {
	var data []byte
	if body != nil {
		var err error
		data, err = encodeRequest(body, c.maxRequest)
		if err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.endpoint+path, bytes.NewReader(data))
	if err != nil {
		return errors.New("meili request creation failed")
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("meili request canceled: %w", ctx.Err())
		}
		return errors.New("meili request failed")
	}
	defer resp.Body.Close()
	if resp.ContentLength > c.maxResponse {
		return errors.New("meili response exceeds byte limit")
	}
	data, err = io.ReadAll(io.LimitReader(resp.Body, c.maxResponse+1))
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("meili response canceled: %w", ctx.Err())
		}
		return errors.New("meili response read failed")
	}
	if int64(len(data)) > c.maxResponse {
		return errors.New("meili response exceeds byte limit")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var api struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(data, &api)
		return &responseError{status: resp.StatusCode, code: safeCode(api.Code)}
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return errors.New("meili response decoding failed")
		}
	}
	return nil
}

type taskFailure struct{ code string }

func (*taskFailure) Error() string { return "meili asynchronous task failed" }

// mutate waits for confirmed success, not merely an accepted enqueue response.
func (c *Client) mutate(ctx context.Context, method, path string, body any) error {
	var accepted struct {
		UID *int64 `json:"taskUid"`
	}
	if err := c.request(ctx, method, path, body, &accepted); err != nil {
		return err
	}
	if accepted.UID == nil || *accepted.UID < 0 {
		return errors.New("meili response missing task identity")
	}
	for {
		var task struct {
			Status string `json:"status"`
			Error  struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := c.request(ctx, http.MethodGet, fmt.Sprintf("/tasks/%d", *accepted.UID), nil, &task); err != nil {
			return err
		}
		switch task.Status {
		case "succeeded":
			return nil
		case "failed":
			return &taskFailure{code: safeCode(task.Error.Code)}
		case "canceled":
			return errors.New("meili asynchronous task canceled")
		case "enqueued", "processing":
		default:
			return errors.New("meili asynchronous task has unknown status")
		}
		timer := time.NewTimer(c.poll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("meili task wait canceled: %w", ctx.Err())
		case <-timer.C:
		}
	}
}

func (c *Client) operation(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, c.timeout)
}
