// Package hindsight implements the pinned Hindsight 0.10.2 REST protocol.
// It returns candidates only; every dependency still needs business-PG proof.
package hindsight

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/telemetry"
)

type Options struct {
	URL, APIKey                       string
	HTTPClient                        *http.Client
	Timeout                           time.Duration
	MaxRequestBytes, MaxResponseBytes int64
}
type Client struct {
	endpoint, apiKey        string
	http                    *http.Client
	timeout                 time.Duration
	maxRequest, maxResponse int64
}

func New(o Options) (*Client, error) {
	u, err := url.Parse(o.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.TrimSpace(o.APIKey) == "" || strings.ContainsAny(o.APIKey, "\r\n") || o.Timeout < 0 || o.MaxRequestBytes < 0 || o.MaxResponseBytes < 0 || o.MaxRequestBytes > 1<<26 || o.MaxResponseBytes > 1<<26 {
		return nil, fmt.Errorf("%w: hindsight endpoint or request options", domain.ErrInvalid)
	}
	if o.Timeout == 0 {
		o.Timeout = 60 * time.Second
	}
	if o.MaxRequestBytes == 0 {
		o.MaxRequestBytes = 8 << 20
	}
	if o.MaxResponseBytes == 0 {
		o.MaxResponseBytes = 8 << 20
	}
	hc := http.Client{}
	if o.HTTPClient != nil {
		hc = *o.HTTPClient
	}
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	hc.Transport = telemetry.Transport("hindsight", hc.Transport)
	return &Client{endpoint: strings.TrimRight(u.String(), "/"), apiKey: o.APIKey, http: &hc, timeout: o.Timeout, maxRequest: o.MaxRequestBytes, maxResponse: o.MaxResponseBytes}, nil
}

func BankID(s domain.Scope) string { return s.Key() }

// DocumentID intentionally excludes versions: Hindsight replaces the complete
// old document on a successful retain of the same context identity.
func DocumentID(s domain.Scope, contextID string) string {
	b, _ := json.Marshal([]string{s.Key(), contextID})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
func bankPath(s domain.Scope) string { return "/v1/default/banks/" + BankID(s) }

type statusError struct{ status int }

func (e *statusError) Error() string {
	return fmt.Sprintf("hindsight request rejected (HTTP %d)", e.status)
}

func (c *Client) encode(body any) ([]byte, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, errors.New("hindsight request encoding failed")
	}
	if int64(len(data)) > c.maxRequest {
		return nil, fmt.Errorf("%w: hindsight request exceeds byte limit", domain.ErrInvalid)
	}
	return data, nil
}

// request never retains or reflects upstream bodies, request URLs, errors from
// transport implementations, credentials, or retained text in its errors.
func (c *Client) request(ctx context.Context, method, path string, data []byte, out any) error {
	if int64(len(data)) > c.maxRequest {
		return fmt.Errorf("%w: hindsight request exceeds byte limit", domain.ErrInvalid)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.endpoint+path, bytes.NewReader(data))
	if err != nil {
		return errors.New("hindsight request creation failed")
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	response, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("hindsight request canceled: %w", ctx.Err())
		}
		return errors.New("hindsight request failed")
	}
	defer response.Body.Close()
	if response.ContentLength > c.maxResponse {
		return errors.New("hindsight response exceeds byte limit")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, c.maxResponse+1))
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("hindsight response canceled: %w", ctx.Err())
		}
		return errors.New("hindsight response read failed")
	}
	if int64(len(body)) > c.maxResponse {
		return errors.New("hindsight response exceeds byte limit")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return &statusError{status: response.StatusCode}
	}
	if out != nil && json.Unmarshal(body, out) != nil {
		return errors.New("hindsight response decoding failed")
	}
	return nil
}

func (c *Client) ensureBank(ctx context.Context, scope domain.Scope) error {
	var profile struct {
		BankID string `json:"bank_id"`
	}
	if err := c.request(ctx, http.MethodPut, bankPath(scope), []byte(`{}`), &profile); err != nil {
		return err
	}
	if profile.BankID != BankID(scope) {
		return errors.New("hindsight bank acknowledgement invalid")
	}
	return nil
}
func (c *Client) EnsureBank(ctx context.Context, scope domain.Scope) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	return c.ensureBank(ctx, scope)
}

func (c *Client) DeleteDocument(ctx context.Context, scope domain.Scope, documentID string) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	id, err := hex.DecodeString(documentID)
	if err != nil || len(id) != sha256.Size || documentID != strings.ToLower(documentID) {
		return fmt.Errorf("%w: hindsight document identity", domain.ErrInvalid)
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	var ack struct {
		Success    *bool  `json:"success"`
		DocumentID string `json:"document_id"`
	}
	err = c.request(ctx, http.MethodDelete, bankPath(scope)+"/documents/"+documentID, nil, &ack)
	var status *statusError
	if errors.As(err, &status) && status.status == http.StatusNotFound {
		return nil
	}
	if err != nil {
		return err
	}
	if ack.Success == nil || !*ack.Success || ack.DocumentID != documentID {
		return errors.New("hindsight delete acknowledgement invalid")
	}
	return nil
}
