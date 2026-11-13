package music

import (
	"context"
	"sort"
	"strings"
)

type CatalogueSource struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type CatalogueOptions struct {
	DefaultSource string            `json:"defaultSource"`
	Sources       []CatalogueSource `json:"sources"`
}

// SourceRouter preserves the selected recording's source across every operation.
// Primary is the existing Netease/owned-cache chain. Alternatives are opt-in.
type SourceRouter struct {
	Primary Provider
	Sources map[string]Provider
}

type sourceSearcher interface {
	SearchSource(context.Context, string, string) ([]Track, error)
}
type catalogueOptionsProvider interface{ CatalogueOptions() CatalogueOptions }

func sourceLabel(source string) string {
	switch source {
	case "netease":
		return "网易云"
	case "kuwo":
		return "酷我"
	default:
		return source
	}
}
func (p SourceRouter) CatalogueOptions() CatalogueOptions {
	out := CatalogueOptions{DefaultSource: "netease", Sources: []CatalogueSource{}}
	if p.Primary != nil {
		out.Sources = append(out.Sources, CatalogueSource{ID: "netease", Name: sourceLabel("netease")})
	}
	keys := []string{}
	for source, provider := range p.Sources {
		if provider != nil && source != "netease" {
			keys = append(keys, source)
		}
	}
	sort.Strings(keys)
	for _, source := range keys {
		out.Sources = append(out.Sources, CatalogueSource{ID: source, Name: sourceLabel(source)})
	}
	return out
}
func (p SourceRouter) provider(source string) (Provider, error) {
	if source == "netease" && p.Primary != nil {
		return p.Primary, nil
	}
	if source != "netease" {
		if selected := p.Sources[source]; selected != nil {
			return selected, nil
		}
	}
	return nil, invalid("source_not_configured")
}
func recordingSource(id string) (string, string, error) {
	source, external, ok := strings.Cut(id, ":")
	if !ok || external == "" || validateID(id) != nil {
		return "", "", missing
	}
	return source, external, nil
}
func (p SourceRouter) Search(ctx context.Context, query string) ([]Track, error) {
	return p.SearchSource(ctx, "netease", query)
}
func (p SourceRouter) SearchSource(ctx context.Context, source, query string) ([]Track, error) {
	if source == "" {
		source = "netease"
	}
	selected, err := p.provider(source)
	if err != nil {
		return nil, err
	}
	tracks, err := selected.Search(ctx, query)
	if err != nil {
		return nil, err
	}
	for _, track := range tracks {
		if track.Source != source || track.ID != source+":"+track.ExternalID || track.ExternalID == "" || validateID(track.ID) != nil {
			return nil, unavailable
		}
	}
	return tracks, nil
}
func (p SourceRouter) Detail(ctx context.Context, id string) (Track, error) {
	source, external, err := recordingSource(id)
	if err != nil {
		return Track{}, err
	}
	selected, err := p.provider(source)
	if err != nil {
		return Track{}, missing
	}
	track, err := selected.Detail(ctx, id)
	if err != nil {
		return Track{}, err
	}
	if track.ID != id || track.Source != source || track.ExternalID != external {
		return Track{}, unavailable
	}
	return track, nil
}
func (p SourceRouter) forTrack(track Track) (Provider, error) {
	source, external, err := recordingSource(track.ID)
	if err != nil {
		return nil, err
	}
	if track.Source != source || track.ExternalID != external {
		return nil, invalid("track_source_mismatch")
	}
	return p.provider(source)
}
func (p SourceRouter) Resolve(ctx context.Context, track Track) (Resolution, error) {
	selected, err := p.forTrack(track)
	if err != nil {
		return Resolution{}, err
	}
	resolved, err := selected.Resolve(ctx, track)
	if err != nil {
		return resolved, err
	}
	if resolved.TrackID != track.ID {
		return Resolution{}, unavailable
	}
	if resolved.Status == "unavailable" {
		resolved.URL = ""
		resolved.ExpiresAt = nil
	}
	return resolved, nil
}
func (p SourceRouter) Lyrics(ctx context.Context, track Track) (Lyrics, error) {
	selected, err := p.forTrack(track)
	if err != nil {
		return Lyrics{}, err
	}
	lyrics, err := selected.Lyrics(ctx, track)
	if err != nil {
		return lyrics, err
	}
	if lyrics.TrackID != track.ID {
		return Lyrics{}, unavailable
	}
	return lyrics, nil
}
func (p SourceRouter) Artwork(ctx context.Context, id string) (string, error) {
	source, _, err := recordingSource(id)
	if err != nil {
		return "", err
	}
	selected, err := p.provider(source)
	if err != nil {
		return "", err
	}
	artwork, ok := selected.(artworkProvider)
	if !ok {
		return "", missing
	}
	return artwork.Artwork(ctx, id)
}
