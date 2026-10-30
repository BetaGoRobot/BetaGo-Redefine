// Package memesource imports public Wiki facts and encodes immutable, source-backed
// retrieval cards offline. Online conversation retrieval never calls the Wiki.
package memesource

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/memes"
	"golang.org/x/net/html"
)

const DefaultWikiBase = "https://cnmeme.wiki"
const maxWikiResponse = 2 << 20

var ErrWikiResponse = errors.New("invalid or unsupported Wiki response")
var ErrWikiTransport = errors.New("Wiki transport failed")

type WikiHTTPError struct{ Status int }

func (e *WikiHTTPError) Error() string { return fmt.Sprintf("Wiki HTTP status %d", e.Status) }

type SourceVideo struct {
	BVID        string  `json:"bvid"`
	Title       *string `json:"title"`
	CoverURL    *string `json:"cover_url"`
	CreatorName *string `json:"creator_name"`
	PublishedAt *string `json:"published_at"`
}
type WikiItem struct {
	Slug        string       `json:"slug"`
	Title       string       `json:"title"`
	Summary     string       `json:"summary"`
	Tags        []string     `json:"tags"`
	CoverURL    *string      `json:"cover_url"`
	UpdatedAt   *string      `json:"updated_at"`
	SourceVideo *SourceVideo `json:"source_video"`
	ContentHTML string       `json:"content_html"`
}

// Source preserves both the raw API detail and deterministic plain-text description.
// Revision identifies this complete snapshot; it contains no fetch timestamp or credentials.
type Source struct {
	ID             string          `json:"id"`
	Revision       string          `json:"revision"`
	URL            string          `json:"url"`
	SchemaVersion  int             `json:"schema_version"`
	Item           WikiItem        `json:"item"`
	Description    string          `json:"description"`
	RawCatalogItem json.RawMessage `json:"raw_catalog_item"`
	RawDetail      json.RawMessage `json:"raw_detail"`
}

func (s Source) WikiSource() memes.WikiSource {
	return memes.WikiSource{ID: s.ID, Title: s.Item.Title, URL: s.URL, Description: s.Description}
}
func (s Source) StorageID() string { return s.ID + ":" + s.Revision }

type SourceError struct {
	SourceID string `json:"source_id"`
	Stage    string `json:"stage"`
	Error    string `json:"error"`
}
type CatalogOptions struct {
	PageSize, Offset, Limit int
	FetchWorkers            int
	sourceSink              func(Source) error
}
type Catalog struct {
	Sources    []Source      `json:"sources"`
	Errors     []SourceError `json:"errors"`
	Pages      int           `json:"pages"`
	NextOffset int           `json:"next_offset"`
	HasMore    bool          `json:"has_more"`
	Complete   bool          `json:"complete"`
}
type WikiClient struct {
	base     *url.URL
	http     *http.Client
	options  WikiClientOptions
	mu       sync.Mutex
	next     time.Time
	cooldown time.Time
}

func NewWikiClient(base string, client *http.Client) (*WikiClient, error) {
	return NewWikiClientWithOptions(base, client, WikiClientOptions{})
}

func NewWikiClientWithOptions(base string, client *http.Client, options WikiClientOptions) (*WikiClient, error) {
	if err := options.validate(); err != nil {
		return nil, err
	}
	if base == "" {
		base = DefaultWikiBase
	}
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("invalid Wiki base URL")
	}
	var safe http.Client
	if client != nil {
		safe = *client
	}
	if safe.Timeout <= 0 {
		safe.Timeout = 20 * time.Second
	}
	safe.Jar = nil
	safe.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &WikiClient{base: u, http: &safe, options: options}, nil
}
func (c *WikiClient) endpoint(slug string, query url.Values) string {
	u := *c.base
	u.Path = strings.TrimRight(u.Path, "/") + "/api/v1/public/memes"
	u.RawPath = ""
	if slug != "" {
		u.Path += "/" + slug
		u.RawPath = strings.TrimRight(c.base.EscapedPath(), "/") + "/api/v1/public/memes/" + url.PathEscape(slug)
	}
	u.RawQuery = query.Encode()
	return u.String()
}
func (c *WikiClient) read(ctx context.Context, endpoint string) ([]byte, error) {
	for attempt := 0; ; attempt++ {
		if err := c.waitRequest(ctx); err != nil {
			return nil, err
		}
		raw, retryAfter, err := c.readAttempt(ctx, endpoint)
		if err == nil || attempt >= c.options.MaxRetries || !retryableWikiError(err) {
			return raw, err
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		delay := c.options.RetryDelay
		if delay == 0 {
			delay = 500 * time.Millisecond
		}
		delay *= time.Duration(1 << attempt)
		maxDelay := c.options.MaxRetryAfter
		if maxDelay == 0 {
			maxDelay = 30 * time.Second
		}
		if retryAfter > maxDelay {
			return nil, err // Do not retry earlier than a longer server instruction.
		}
		if retryAfter > delay {
			delay = retryAfter
		}
		if delay > maxDelay {
			delay = maxDelay
		}
		c.deferRequests(delay)
		if waitErr := waitWiki(ctx, delay); waitErr != nil {
			return nil, waitErr
		}
	}
}

func (c *WikiClient) readAttempt(ctx context.Context, endpoint string) ([]byte, time.Duration, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, ErrWikiTransport
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "BetaGo-meme-sync/1")
	response, err := c.http.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, 0, ctx.Err()
		}
		return nil, 0, ErrWikiTransport
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return nil, wikiRetryAfter(response.Header.Get("Retry-After")), &WikiHTTPError{Status: response.StatusCode}
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxWikiResponse+1))
	if err != nil {
		return nil, 0, ErrWikiTransport
	}
	if len(raw) > maxWikiResponse || !utf8.Valid(raw) || !json.Valid(raw) {
		return nil, 0, ErrWikiResponse
	}
	return raw, 0, nil
}

// Catalog browses only the documented public list and detail endpoints. It never
// supplies a user query or tag. Limit counts unique source attempts, including errors.
// Offset pagination is best effort because the remote catalog can change during a run.
func (c *WikiClient) Catalog(ctx context.Context, options CatalogOptions) (Catalog, error) {
	result := Catalog{Sources: []Source{}, Errors: []SourceError{}, NextOffset: options.Offset}
	if options.PageSize == 0 {
		options.PageSize = 20
	}
	if options.FetchWorkers == 0 {
		options.FetchWorkers = 1
	}
	if options.PageSize < 1 || options.PageSize > 30 || options.Offset < 0 || options.Limit < 0 || options.FetchWorkers < 1 || options.FetchWorkers > 16 {
		return result, errors.New("invalid Wiki pagination options")
	}
	seen := map[string]bool{}
	for pageNumber := 0; pageNumber < 10000; pageNumber++ {
		pageSize := options.PageSize
		if options.Limit > 0 && options.Limit-len(seen) < pageSize {
			pageSize = options.Limit - len(seen)
		}
		if pageSize == 0 {
			return result, nil
		}
		query := url.Values{"limit": {strconv.Itoa(pageSize)}, "offset": {strconv.Itoa(result.NextOffset)}}
		raw, err := c.read(ctx, c.endpoint("", query))
		if err != nil {
			return result, err
		}
		var page struct {
			SchemaVersion int               `json:"schema_version"`
			Items         []json.RawMessage `json:"items"`
			Limit         int               `json:"limit"`
			Offset        int               `json:"offset"`
			HasMore       bool              `json:"has_more"`
		}
		if json.Unmarshal(raw, &page) != nil || page.SchemaVersion != 1 || page.Items == nil || page.Limit != pageSize || page.Offset != result.NextOffset || len(page.Items) > pageSize || (len(page.Items) == 0 && page.HasMore) {
			return result, ErrWikiResponse
		}
		result.Pages++
		result.NextOffset += pageSize
		result.HasMore = page.HasMore
		c.progress("page", result, "")
		details := make([]catalogDetailJob, 0, len(page.Items))
		for _, itemRaw := range page.Items {
			var item WikiItem
			if json.Unmarshal(itemRaw, &item) != nil || strings.TrimSpace(item.Slug) == "" || len(item.Slug) > 512 {
				return result, ErrWikiResponse
			}
			if seen[item.Slug] {
				continue
			}
			seen[item.Slug] = true
			details = append(details, catalogDetailJob{slug: item.Slug, raw: itemRaw})
		}
		if err := c.fetchPage(ctx, details, options.FetchWorkers, &result, options.sourceSink); err != nil {
			return result, err
		}
		if !page.HasMore {
			result.Complete = true
			return result, nil
		}
		if options.Limit > 0 && len(seen) >= options.Limit {
			return result, nil
		}
	}
	return result, errors.New("Wiki pagination exceeds page limit")
}
func (c *WikiClient) detail(ctx context.Context, slug string, catalogItem json.RawMessage) (Source, error) {
	if source, ok := c.cachedSource(slug, catalogItem); ok {
		return source, nil
	}
	endpoint := c.endpoint(slug, nil)
	raw, err := c.read(ctx, endpoint)
	if err != nil {
		return Source{}, err
	}
	source, err := c.sourceFromDetail(slug, catalogItem, raw)
	if err != nil {
		return Source{}, err
	}
	if err := c.cacheSource(source); err != nil {
		return Source{}, err
	}
	return source, nil
}

func (c *WikiClient) sourceFromDetail(slug string, catalogItem, raw json.RawMessage) (Source, error) {
	if len(raw) > maxWikiResponse || !utf8.Valid(raw) || !json.Valid(raw) {
		return Source{}, ErrWikiResponse
	}
	var detail struct {
		SchemaVersion int      `json:"schema_version"`
		Item          WikiItem `json:"item"`
	}
	if json.Unmarshal(raw, &detail) != nil || detail.SchemaVersion != 1 || detail.Item.Slug != slug || strings.TrimSpace(detail.Item.Title) == "" {
		return Source{}, ErrWikiResponse
	}
	description, err := plainText(detail.Item.ContentHTML)
	if err != nil {
		return Source{}, err
	}
	if description == "" {
		description = strings.TrimSpace(detail.Item.Summary)
	}
	if description == "" {
		return Source{}, ErrWikiResponse
	}
	source := Source{ID: "wiki:" + slug, URL: c.endpoint(slug, nil), SchemaVersion: 1, Item: detail.Item, Description: description, RawCatalogItem: catalogItem, RawDetail: json.RawMessage(raw)}
	source.Revision, err = memes.Fingerprint(source)
	return source, err
}
func plainText(input string) (string, error) {
	document, err := html.Parse(strings.NewReader(input))
	if err != nil {
		return "", ErrWikiResponse
	}
	var output strings.Builder
	var visit func(*html.Node)
	visit = func(node *html.Node) {
		if node.Type == html.ElementNode && (node.Data == "script" || node.Data == "style" || node.Data == "template") {
			return
		}
		block := node.Type == html.ElementNode && strings.Contains(" p div h1 h2 h3 h4 h5 h6 li blockquote pre br hr tr ", " "+node.Data+" ")
		if block {
			output.WriteByte('\n')
		}
		if node.Type == html.TextNode {
			output.WriteString(node.Data)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
		if block {
			output.WriteByte('\n')
		}
	}
	visit(document)
	var lines []string
	for _, line := range strings.Split(output.String(), "\n") {
		if text := strings.Join(strings.Fields(line), " "); text != "" {
			lines = append(lines, text)
		}
	}
	return strings.Join(lines, "\n"), nil
}
