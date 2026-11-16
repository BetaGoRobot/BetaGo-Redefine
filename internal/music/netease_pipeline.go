package music

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// NeteasePipeline separates anonymous catalogue reads from bounded, explicitly
// configured playback authorization. It never searches for replacement titles.
type NeteasePipeline struct {
	Catalogue       AnonymousNetease
	BridgeURL       string
	account         AnonymousNetease
	flights         singleflight.Group
	accountFlights  singleflight.Group
	accountSlots    chan struct{}
	unmSlots        chan struct{}
	accountNegative map[string]negativeResolution
	unmNegative     map[string]time.Time
	mu              sync.Mutex
	negative        map[string]negativeResolution
	window          time.Time
	accountStarts   int
	now             func() time.Time
}
type negativeResolution struct {
	resolution Resolution
	until      time.Time
}

func newNeteasePipeline(catalogue AnonymousNetease, bridge, cookie string) *NeteasePipeline {
	catalogue.ServerCookie = ""
	account := catalogue
	account.ServerCookie = cookie
	return &NeteasePipeline{Catalogue: catalogue, BridgeURL: strings.TrimSpace(bridge), account: account, accountSlots: make(chan struct{}, 2), unmSlots: make(chan struct{}, 2), accountNegative: map[string]negativeResolution{}, unmNegative: map[string]time.Time{}, negative: map[string]negativeResolution{}, now: time.Now}
}
func (p *NeteasePipeline) Search(ctx context.Context, q string) ([]Track, error) {
	return p.Catalogue.Search(ctx, q)
}
func (p *NeteasePipeline) Detail(ctx context.Context, id string) (Track, error) {
	return p.Catalogue.Detail(ctx, id)
}
func (p *NeteasePipeline) Lyrics(ctx context.Context, t Track) (Lyrics, error) {
	return p.Catalogue.Lyrics(ctx, t)
}

func (p *NeteasePipeline) Resolve(ctx context.Context, t Track) (Resolution, error) {
	if t.Source != "netease" || t.ID != "netease:"+t.ExternalID || !numeric.MatchString(t.ExternalID) {
		return Resolution{TrackID: t.ID, Status: "unavailable", Reason: "track_source_mismatch"}, nil
	}
	v, err, _ := p.flights.Do(t.ID, func() (any, error) {
		p.mu.Lock()
		cached, found := p.negative[t.ID]
		if found && p.now().Before(cached.until) {
			p.mu.Unlock()
			return cached.resolution, nil
		}
		delete(p.negative, t.ID)
		p.mu.Unlock()
		bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		out := p.resolve(bounded, t)
		if out.Status == "unavailable" {
			out.URL = ""
			out.ExpiresAt = nil
			p.mu.Lock()
			// Only classified outcomes, never temporary URLs, enter this small cache.
			if len(p.negative) >= 512 {
				clear(p.negative)
			}
			p.negative[t.ID] = negativeResolution{out, p.now().Add(15 * time.Second)}
			p.mu.Unlock()
		}
		return out, nil
	})
	if err != nil {
		return Resolution{}, err
	}
	return v.(Resolution), nil
}
func (p *NeteasePipeline) resolve(ctx context.Context, t Track) Resolution {
	if p.BridgeURL != "" {
		if out := p.resolvePyncmd(ctx, t); out.Status == "available" {
			return out
		}
	}
	out, err := p.Catalogue.Resolve(ctx, t)
	if err != nil {
		return Resolution{TrackID: t.ID, Status: "unavailable", Reason: "anonymous_source_unavailable"}
	}
	if !out.accountEligible || p.account.ServerCookie == "" {
		return out
	}
	// A valid same-ID empty URL or explicit preview is eligible; transport,
	// 401/403/429, wrong IDs/hosts and other provider codes never reach this path.
	account := p.resolveAccount(ctx, t)
	if account.Status == "available" || (out.Status != "preview" && account.Status == "preview") {
		return account
	}
	if out.Status == "preview" {
		out.Reason += ";account_fallback_unavailable"
		return out
	}
	return account
}
func (p *NeteasePipeline) resolveAccount(ctx context.Context, t Track) Resolution {
	v, _, _ := p.accountFlights.Do(t.ID, func() (any, error) {
		unavailableAccount := func(reason string) Resolution {
			return Resolution{TrackID: t.ID, Status: "unavailable", Reason: reason, MediaSource: "netease:account", OriginTrackID: t.ID, MatchMethod: "provider_external_id"}
		}
		p.mu.Lock()
		cached, found := p.accountNegative[t.ID]
		if found && p.now().Before(cached.until) {
			p.mu.Unlock()
			return cached.resolution, nil
		}
		delete(p.accountNegative, t.ID)
		p.mu.Unlock()
		remember := func(out Resolution) Resolution {
			if out.Status == "unavailable" {
				out.URL = ""
				out.ExpiresAt = nil
				p.mu.Lock()
				if len(p.accountNegative) >= 512 {
					clear(p.accountNegative)
				}
				p.accountNegative[t.ID] = negativeResolution{out, p.now().Add(15 * time.Second)}
				p.mu.Unlock()
			}
			return out
		}
		select {
		case p.accountSlots <- struct{}{}:
			defer func() { <-p.accountSlots }()
		default:
			return unavailableAccount("account_concurrency_limited"), nil
		}
		p.mu.Lock()
		now := p.now()
		if p.window.IsZero() || now.Sub(p.window) >= time.Minute {
			p.window = now
			p.accountStarts = 0
		}
		if p.accountStarts >= 12 {
			p.mu.Unlock()
			return unavailableAccount("account_budget_exhausted"), nil
		}
		p.accountStarts++
		p.mu.Unlock()
		// One account endpoint per attempt: compatibility retry is not an
		// additional authorization attempt. A transport failure stops this lane.
		accountCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		out, err := p.account.resolveNetease(accountCtx, t, false)
		if err != nil {
			return remember(unavailableAccount("account_source_unavailable")), nil
		}
		return remember(out), nil
	})
	return v.(Resolution)
}

var prefixRange = regexp.MustCompile(`^bytes 0-([0-9]+)/([0-9]+)$`)

func (p *NeteasePipeline) resolvePyncmd(ctx context.Context, t Track) Resolution {
	p.mu.Lock()
	until := p.unmNegative[t.ID]
	p.mu.Unlock()
	if p.now().Before(until) {
		return Resolution{TrackID: t.ID, Status: "unavailable", Reason: "unm_negative_ttl"}
	}
	select {
	case p.unmSlots <- struct{}{}:
		defer func() { <-p.unmSlots }()
	default:
		return Resolution{TrackID: t.ID, Status: "unavailable", Reason: "unm_concurrency_limited"}
	}
	out := p.resolvePyncmdRequest(ctx, t)
	if out.Status != "available" {
		p.mu.Lock()
		if len(p.unmNegative) >= 512 {
			clear(p.unmNegative)
		}
		p.unmNegative[t.ID] = p.now().Add(15 * time.Second)
		p.mu.Unlock()
	}
	return out
}
func (p *NeteasePipeline) resolvePyncmdRequest(ctx context.Context, t Track) Resolution {
	out := Resolution{TrackID: t.ID, Status: "unavailable", Reason: "unm_bridge_unavailable", MediaSource: "unm:pyncmd", OriginTrackID: t.ID, MatchMethod: "provider_external_id"}
	base, err := url.Parse(p.BridgeURL)
	if err != nil || base.User != nil || base.RawQuery != "" || base.Fragment != "" || (base.Path != "" && base.Path != "/") || (base.Scheme != "http" && base.Scheme != "https") {
		return out
	}
	ip := net.ParseIP(base.Hostname())
	if !strings.EqualFold(base.Hostname(), "localhost") && (ip == nil || !ip.IsLoopback()) {
		return out
	}
	// Both official bridge and pyncmd caches key the entire argument for 15
	// minutes. A new non-sensitive request ID makes every actual re-resolution
	// fresh, including media errors inside the 120-second deadline. pyncmd only
	// reads id; existing singleflight and negative TTLs still bound source work.
	argument, _ := json.Marshal(map[string]string{"id": t.ExternalID, "requestID": secret()})
	base.Path = "/pyncmd/check"
	base.RawQuery = url.QueryEscape(string(argument))
	bounded, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(bounded, http.MethodGet, base.String(), nil)
	if err != nil {
		return out
	}
	client := &http.Client{Timeout: 6 * time.Second, CheckRedirect: rejectSourceRedirect}
	response, err := client.Do(request)
	if err != nil {
		return out
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return out
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 4097))
	if err != nil || len(body) > 4096 {
		return out
	}
	u, err := url.Parse(strings.TrimSpace(string(body)))
	if err != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" || (u.Port() != "" && u.Port() != "443") || !officialMediaHost(u.Hostname()) {
		out.Reason = "unm_unsafe_media_url"
		return out
	}
	request, err = http.NewRequestWithContext(bounded, http.MethodGet, u.String(), nil)
	if err != nil {
		return out
	}
	request.Header.Set("Range", "bytes=0-65535")
	request.Header.Set("Accept-Encoding", "identity")
	media, err := client.Do(request)
	if err != nil {
		return out
	}
	defer media.Body.Close()
	sample, err := io.ReadAll(io.LimitReader(media.Body, 65537))
	ranges := prefixRange.FindStringSubmatch(media.Header.Get("Content-Range"))
	if err != nil || media.StatusCode != http.StatusPartialContent || len(sample) > 65536 || len(ranges) != 3 || !cacheableMP3(sample, media.Header.Get("Content-Type")) {
		out.Reason = "unm_audio_prefix_unverified"
		return out
	}
	last, _ := strconv.ParseInt(ranges[1], 10, 64)
	total, _ := strconv.ParseInt(ranges[2], 10, 64)
	if last+1 != int64(len(sample)) || total < int64(len(sample)) || (media.ContentLength >= 0 && media.ContentLength != int64(len(sample))) {
		out.Reason = "unm_audio_range_invalid"
		return out
	}
	exp := p.now().Add(120 * time.Second)
	out.Status, out.URL, out.ExpiresAt, out.Verification = "available", u.String(), &exp, "audio_prefix"
	out.Reason = "unm_pyncmd_provider_id_audio_prefix_only"
	return out
}
