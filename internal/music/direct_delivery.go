package music

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
)

type directDeliveryKey struct{}

func isDirectDelivery(ctx context.Context) bool {
	direct, _ := ctx.Value(directDeliveryKey{}).(bool)
	return direct
}

// Existing card/mini clients retain the legacy provider. Every web operation
// explicitly selects the independent provider, which has no cache dependency.
// This is a delivery contract, never a configurable cache fallback preference.
type directDeliveryProvider struct{ legacy, direct Provider }

func (p *directDeliveryProvider) selected(ctx context.Context) Provider {
	if isDirectDelivery(ctx) {
		return p.direct
	}
	return p.legacy
}
func (p *directDeliveryProvider) Search(ctx context.Context, q string) ([]Track, error) {
	return p.selected(ctx).Search(ctx, q)
}
func (p *directDeliveryProvider) Detail(ctx context.Context, id string) (Track, error) {
	return p.selected(ctx).Detail(ctx, id)
}
func (p *directDeliveryProvider) Lyrics(ctx context.Context, t Track) (Lyrics, error) {
	return p.selected(ctx).Lyrics(ctx, t)
}
func (p *directDeliveryProvider) Artwork(ctx context.Context, id string) (string, error) {
	selected := p.selected(ctx)
	if a, ok := selected.(artworkProvider); ok {
		return a.Artwork(ctx, id)
	}
	if isDirectDelivery(ctx) {
		t, err := selected.Detail(ctx, id)
		if err != nil {
			return "", err
		}
		if t.Picture != "" {
			return t.Picture, nil
		}
	}
	return "", missing
}
func directAudioURL(raw, source string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" || (u.Port() != "" && u.Port() != "443") {
		return false
	}
	host := strings.ToLower(u.Hostname())
	switch source {
	case "netease":
		return officialMediaHost(host)
	case "kuwo":
		return host == "kuwo.cn" || strings.HasSuffix(host, ".kuwo.cn")
	}
	return false
}
func (p *directDeliveryProvider) Resolve(ctx context.Context, t Track) (out Resolution, err error) {
	if isDirectDelivery(ctx) {
		defer func() {
			mediaHost := ""
			if u, parseErr := url.Parse(out.URL); parseErr == nil {
				mediaHost = u.Hostname()
			}
			// Only source identity/status/host: signed paths, cookies and user identity
			// never enter runtime logs. This endpoint returns metadata, never media.
			log.Printf("music_direct_resolution track=%q status=%q media_host=%q", t.ID, out.Status, mediaHost)
		}()
	}
	out, err = p.selected(ctx).Resolve(ctx, t)
	if !isDirectDelivery(ctx) {
		return out, err
	}
	if err != nil {
		return Resolution{TrackID: t.ID, Delivery: "direct", Status: "unavailable", Reason: "external_source_unavailable"}, nil
	}
	out.Delivery = "direct"
	if out.TrackID != t.ID || ((out.Status == "available" || out.Status == "preview") && !directAudioURL(out.URL, t.Source)) {
		return Resolution{TrackID: t.ID, Delivery: "direct", Status: "unavailable", Reason: "external_audio_url_rejected"}, nil
	}
	if out.Status != "available" && out.Status != "preview" {
		out.Status = "unavailable"
		out.URL = ""
		out.ExpiresAt = nil
	}
	return out, nil
}

func audioDeliveryContext(r *http.Request) (context.Context, error) {
	var input *struct {
		Delivery string `json:"delivery"`
	}
	if r.Body == nil {
		return r.Context(), nil
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, 2049))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		if err == io.EOF {
			return r.Context(), nil
		}
		return nil, invalid("invalid_json")
	}
	if input == nil {
		return nil, invalid("invalid_json")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, invalid("invalid_json")
	}
	switch input.Delivery {
	case "":
		return r.Context(), nil
	case "direct":
		return context.WithValue(r.Context(), directDeliveryKey{}, true), nil
	}
	return nil, invalid("invalid_audio_delivery")
}
