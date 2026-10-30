package memesource

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/memes"
)

type WikiClientOptions struct {
	RequestInterval time.Duration
	MaxRetries      int
	RetryDelay      time.Duration
	MaxRetryAfter   time.Duration
	CacheDir        string
	Progress        func(CatalogProgress)
}

type CatalogProgress struct {
	Phase      string
	SourceID   string
	Pages      int
	Sources    int
	Errors     int
	NextOffset int
}

func (o WikiClientOptions) validate() error {
	if o.RequestInterval < 0 || o.RequestInterval > time.Minute || o.MaxRetries < 0 || o.MaxRetries > 8 || o.RetryDelay < 0 || o.RetryDelay > 30*time.Second || o.MaxRetryAfter < 0 || o.MaxRetryAfter > time.Minute {
		return errors.New("invalid Wiki request policy")
	}
	return nil
}

func (c *WikiClient) progress(phase string, result Catalog, sourceID string) {
	if c.options.Progress != nil {
		c.options.Progress(CatalogProgress{Phase: phase, SourceID: sourceID, Pages: result.Pages, Sources: len(result.Sources), Errors: len(result.Errors), NextOffset: result.NextOffset})
	}
}

func waitWiki(ctx context.Context, delay time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (c *WikiClient) deferRequests(delay time.Duration) {
	c.mu.Lock()
	until := time.Now().Add(delay)
	if until.After(c.cooldown) {
		c.cooldown = until
	}
	if c.next.Before(c.cooldown) {
		c.next = c.cooldown
	}
	c.mu.Unlock()
}

func (c *WikiClient) waitRequest(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	start := time.Now()
	if c.next.After(start) {
		start = c.next
	}
	if c.cooldown.After(start) {
		start = c.cooldown
	}
	c.next = start.Add(c.options.RequestInterval)
	c.mu.Unlock()
	for {
		if err := waitWiki(ctx, time.Until(start)); err != nil {
			return err
		}
		c.mu.Lock()
		if !c.cooldown.After(time.Now()) {
			c.mu.Unlock()
			return ctx.Err()
		}
		start = c.cooldown
		if c.next.After(start) {
			start = c.next
		}
		c.next = start.Add(c.options.RequestInterval)
		c.mu.Unlock()
	}
}

func retryableWikiError(err error) bool {
	if errors.Is(err, ErrWikiTransport) {
		return true
	}
	var status *WikiHTTPError
	if !errors.As(err, &status) {
		return false
	}
	switch status.Status {
	case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

func wikiRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if seconds, err := strconv.ParseInt(value, 10, 32); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(value); err == nil {
		if remaining := time.Until(when); remaining > 0 {
			return remaining
		}
	}
	return 0
}

type sourceCacheEntry struct {
	Revision string          `json:"revision"`
	Detail   json.RawMessage `json:"detail"`
}

func (c *WikiClient) cachePath(catalogItem json.RawMessage) string {
	key, _ := memes.Fingerprint(struct {
		Base string          `json:"base"`
		Item json.RawMessage `json:"item"`
	}{c.base.String(), catalogItem})
	return filepath.Join(c.options.CacheDir, key+".json")
}

func (c *WikiClient) cachedSource(slug string, catalogItem json.RawMessage) (Source, bool) {
	if c.options.CacheDir == "" {
		return Source{}, false
	}
	name := c.cachePath(catalogItem)
	info, err := os.Stat(name)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 4*maxWikiResponse {
		return Source{}, false
	}
	raw, err := os.ReadFile(name)
	if err != nil {
		return Source{}, false
	}
	var saved sourceCacheEntry
	if json.Unmarshal(raw, &saved) != nil || len(saved.Detail) > maxWikiResponse {
		return Source{}, false
	}
	source, err := c.sourceFromDetail(slug, catalogItem, saved.Detail)
	if err != nil || source.Revision != saved.Revision {
		return Source{}, false
	}
	return source, true
}

func (c *WikiClient) cacheSource(source Source) error {
	if c.options.CacheDir == "" {
		return nil
	}
	if err := os.MkdirAll(c.options.CacheDir, 0700); err != nil {
		return errors.New("Wiki source cache directory unavailable")
	}
	raw, err := json.Marshal(sourceCacheEntry{Revision: source.Revision, Detail: source.RawDetail})
	if err != nil {
		return ErrWikiResponse
	}
	file, err := os.CreateTemp(c.options.CacheDir, ".wiki-source-*")
	if err != nil {
		return errors.New("Wiki source cache write failed")
	}
	name := file.Name()
	defer os.Remove(name)
	if _, err = file.Write(raw); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		return errors.New("Wiki source cache write failed")
	}
	if os.Rename(name, c.cachePath(source.RawCatalogItem)) != nil {
		return errors.New("Wiki source cache publication failed")
	}
	return nil
}
