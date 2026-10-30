// Package business contains transport adapters and typed business operations.
// It cannot send conversation replies, grant approval, or run a task loop.
package business

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"time"
)

var ErrProvider = errors.New("business provider call failed")

func endpoint(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, ErrProvider
	}
	return u, nil
}
func httpClient(in *http.Client, timeout time.Duration) (*http.Client, error) {
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	if timeout < 0 || timeout > 5*time.Minute {
		return nil, ErrProvider
	}
	c := http.Client{}
	if in != nil {
		c = *in
	}
	c.Timeout = timeout
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &c, nil
}
func readResponse(c *http.Client, req *http.Request, limit int64) ([]byte, error) {
	r, err := c.Do(req)
	if err != nil {
		return nil, ErrProvider
	}
	defer r.Body.Close()
	if r.StatusCode < 200 || r.StatusCode >= 300 || r.ContentLength > limit {
		return nil, ErrProvider
	}
	b, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil || int64(len(b)) > limit {
		return nil, ErrProvider
	}
	return b, nil
}
func deadline(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, 2*time.Minute)
}

type limitedBody struct {
	io.ReadCloser
	remaining int64
}

func (b *limitedBody) Read(p []byte) (int, error) {
	if int64(len(p)) > b.remaining+1 {
		p = p[:b.remaining+1]
	}
	n, err := b.ReadCloser.Read(p)
	b.remaining -= int64(n)
	if b.remaining < 0 {
		b.Close()
		return 0, ErrProvider
	}
	return n, err
}
