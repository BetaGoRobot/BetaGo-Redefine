package music

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/minio/minio-go/v7/pkg/s3utils"
	"golang.org/x/sync/singleflight"
)

const ownedURLLifetime = 300 * time.Second

var ownedMiss = errors.New("owned cache object absent")

// Only operator configuration selects these hosts and the bucket. Client input
// selects a numeric recording ID, never an object name, URL or credentials.
type ownedCacheConfig struct {
	endpoint, publicOrigin, bucket, region, accessKey, secretKey string
	source                                                       string
	writers                                                      chan struct{}
	writeEnabled                                                 bool
}
type ownedCacheProvider struct {
	reader, signer *minio.Client
	bucket         string
	publicOrigin   string
	source         string
	fallback       Provider
	writeEnabled   bool
	writers        chan struct{}
	flights        singleflight.Group
}

func cacheOrigin(raw string, httpsOnly bool) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || (u.Scheme != "https" && (httpsOnly || u.Scheme != "http")) {
		return nil, errors.New("invalid cache origin")
	}
	return u, nil
}
func (p *ownedCacheProvider) sourceID() string {
	if p.source == "" {
		return "netease"
	}
	return p.source
}
func (p *ownedCacheProvider) externalID(id string) (string, bool) {
	prefix := p.sourceID() + ":"
	external := strings.TrimPrefix(id, prefix)
	return external, strings.HasPrefix(id, prefix) && numeric.MatchString(external)
}
func (p *ownedCacheProvider) objectKey(kind, external, suffix string) string {
	prefix := ""
	if p.sourceID() != "netease" {
		prefix = p.sourceID() + "/"
	}
	return kind + "/" + prefix + external + suffix
}
func (p *ownedCacheProvider) approvedMediaHost(host string) bool {
	host = strings.ToLower(host)
	if p.sourceID() == "netease" {
		return officialMediaHost(host)
	}
	return p.sourceID() == "kuwo" && (host == "kuwo.cn" || strings.HasSuffix(host, ".kuwo.cn"))
}
func newOwnedCacheProvider(c ownedCacheConfig, fallback Provider) (Provider, error) {
	badConfig := errors.New("MUSIC_MINIO cache requires valid endpoints, HTTPS public origin, bucket and server credentials")
	if c.source == "" {
		c.source = "netease"
	}
	if c.source != "netease" && c.source != "kuwo" {
		return nil, badConfig
	}
	internal, err := cacheOrigin(c.endpoint, false)
	if err != nil {
		return nil, badConfig
	}
	public, err := cacheOrigin(c.publicOrigin, true)
	if err != nil || c.accessKey == "" || c.secretKey == "" {
		return nil, badConfig
	}
	if c.bucket == "" {
		c.bucket = "cloudmusic"
	}
	if s3utils.CheckValidBucketName(c.bucket) != nil {
		return nil, badConfig
	}
	if c.region == "" {
		c.region = "us-east-1"
	}
	reader, err := minio.New(internal.Host, &minio.Options{Creds: credentials.NewStaticV4(c.accessKey, c.secretKey, ""), Region: c.region, Secure: internal.Scheme == "https", BucketLookup: minio.BucketLookupPath})
	if err != nil {
		return nil, badConfig
	}
	signer, err := minio.New(public.Host, &minio.Options{Creds: credentials.NewStaticV4(c.accessKey, c.secretKey, ""), Region: c.region, Secure: true, BucketLookup: minio.BucketLookupPath})
	if err != nil {
		return nil, badConfig
	}
	if c.writers == nil {
		c.writers = make(chan struct{}, 2)
	}
	return &ownedCacheProvider{reader: reader, signer: signer, bucket: c.bucket, publicOrigin: strings.TrimRight(c.publicOrigin, "/"), source: c.source, fallback: fallback, writeEnabled: c.writeEnabled, writers: c.writers}, nil
}

func ownedError(err error) error {
	e := minio.ToErrorResponse(err)
	if e.Code == "NoSuchKey" || e.Code == "NoSuchObject" || (e.StatusCode == http.StatusNotFound && e.Code != "NoSuchBucket") {
		return ownedMiss
	}
	if e.StatusCode == http.StatusForbidden || e.StatusCode == http.StatusUnauthorized || e.Code == "AccessDenied" {
		return &apiError{403, "owned_cache_access_denied"}
	}
	return &apiError{503, "owned_cache_unavailable"}
}
func (p *ownedCacheProvider) read(ctx context.Context, key string) ([]byte, error) {
	o, err := p.reader.GetObject(ctx, p.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, ownedError(err)
	}
	defer o.Close()
	b, err := io.ReadAll(io.LimitReader(o, (2<<20)+1))
	if err != nil {
		return nil, ownedError(err)
	}
	if len(b) > 2<<20 {
		return nil, &apiError{502, "owned_cache_invalid_metadata"}
	}
	return b, nil
}

type ownedDescriptor struct {
	TrackID, Source, ExternalID, Title, Album string
	Artist                                    json.RawMessage
	Picture, PictureURL                       string
	Duration, DurationMs                      int64
	Lyrics                                    json.RawMessage
}

func (d ownedDescriptor) track(id, externalID string) (Track, error) {
	source, _, _ := strings.Cut(id, ":")
	if (d.TrackID != "" && d.TrackID != id) || (d.Source != "" && d.Source != source) || (d.ExternalID != "" && d.ExternalID != externalID) || strings.TrimSpace(d.Title) == "" {
		return Track{}, &apiError{502, "owned_cache_invalid_metadata"}
	}
	artist := ""
	if len(d.Artist) > 0 {
		if json.Unmarshal(d.Artist, &artist) != nil {
			var artists []struct{ Name string }
			if json.Unmarshal(d.Artist, &artists) != nil {
				return Track{}, &apiError{502, "owned_cache_invalid_metadata"}
			}
			names := []string{}
			for _, a := range artists {
				names = append(names, a.Name)
			}
			artist = strings.Join(names, ", ")
		}
	}
	duration := d.DurationMs
	if duration == 0 {
		duration = d.Duration
	}
	if duration < 0 || len(d.Title) > 1000 || len(artist) > 2000 || len(d.Album) > 2000 {
		return Track{}, &apiError{502, "owned_cache_invalid_metadata"}
	}
	picture := d.Picture
	if picture == "" {
		picture = d.PictureURL
	}
	return Track{ID: id, Source: source, ExternalID: externalID, Title: d.Title, Artist: artist, Album: d.Album, DurationMs: duration, Picture: securePicture(picture)}, nil
}
func (p *ownedCacheProvider) lookup(ctx context.Context, id string) (Track, ownedDescriptor, error) {
	d := ownedDescriptor{}
	external, valid := p.externalID(id)
	if !valid {
		return Track{}, d, ownedMiss
	}
	b, err := p.read(ctx, p.objectKey("catalog", external, ".json"))
	if err == nil {
		var manifest ownedManifest
		if json.Unmarshal(b, &manifest) != nil || manifest.Format != "betago-music-cache" || manifest.Version != 1 || manifest.Status != "available" || manifest.AudioObject != p.objectKey("music", external, ".mp3") || manifest.Track.ID != id || manifest.Track.Source != p.sourceID() || manifest.Track.ExternalID != external {
			return Track{}, d, &apiError{502, "owned_cache_invalid_metadata"}
		}
		artist, _ := json.Marshal(manifest.Track.Artist)
		d = ownedDescriptor{TrackID: id, Source: p.sourceID(), ExternalID: external, Title: manifest.Track.Title, Artist: artist, Album: manifest.Track.Album, Picture: manifest.Track.Picture, DurationMs: manifest.Track.DurationMs}
	} else if err == ownedMiss && p.sourceID() == "netease" {
		b, err = p.read(ctx, "info/"+external+".json")
		if err == nil && json.Unmarshal(b, &d) != nil {
			return Track{}, d, &apiError{502, "owned_cache_invalid_metadata"}
		}
	}
	if err != nil {
		return Track{}, d, err
	}
	t, err := d.track(id, external)
	if err != nil {
		return Track{}, d, err
	}
	stat, err := p.reader.StatObject(ctx, p.bucket, p.objectKey("music", external, ".mp3"), minio.StatObjectOptions{})
	if err != nil {
		return Track{}, d, ownedError(err)
	}
	if stat.Size <= 0 {
		return Track{}, d, &apiError{502, "owned_cache_invalid_audio"}
	}
	if image, err := p.reader.StatObject(ctx, p.bucket, p.objectKey("picture", external, ".jpg"), minio.StatObjectOptions{}); err == nil && image.Size > 0 {
		t.Picture = p.publicOrigin + "/music-api/v1/tracks/" + url.PathEscape(id) + "/artwork"
	}
	return t, d, nil
}

func (p *ownedCacheProvider) Artwork(ctx context.Context, id string) (string, error) {
	external, valid := p.externalID(id)
	if !valid {
		return "", missing
	}
	stat, err := p.reader.StatObject(ctx, p.bucket, p.objectKey("picture", external, ".jpg"), minio.StatObjectOptions{})
	if err != nil {
		if e := ownedError(err); e != ownedMiss {
			return "", e
		}
		return "", missing
	}
	if stat.Size <= 0 {
		return "", missing
	}
	u, err := p.signer.PresignedGetObject(ctx, p.bucket, p.objectKey("picture", external, ".jpg"), ownedURLLifetime, url.Values{"response-content-type": {"image/jpeg"}})
	if err != nil {
		return "", &apiError{503, "owned_cache_signing_failed"}
	}
	return u.String(), nil
}
func (p *ownedCacheProvider) Search(ctx context.Context, q string) ([]Track, error) {
	return p.fallback.Search(ctx, q)
}
func (p *ownedCacheProvider) Detail(ctx context.Context, id string) (Track, error) {
	t, _, err := p.lookup(ctx, id)
	if err == ownedMiss {
		return p.fallback.Detail(ctx, id)
	}
	return t, err
}
func (p *ownedCacheProvider) Resolve(ctx context.Context, t Track) (Resolution, error) {
	if !p.writeEnabled {
		return p.resolve(ctx, t)
	}
	v, err, _ := p.flights.Do(t.ID, func() (any, error) { return p.resolve(ctx, t) })
	if err != nil {
		return Resolution{}, err
	}
	return v.(Resolution), nil
}
func (p *ownedCacheProvider) resolve(ctx context.Context, t Track) (Resolution, error) {
	canonical, _, err := p.lookup(ctx, t.ID)
	if err == ownedMiss {
		out, err := p.fallback.Resolve(ctx, t)
		if err != nil || !p.writeEnabled {
			return out, err
		}
		return p.cacheResolved(ctx, t, out), nil
	}
	out := Resolution{TrackID: t.ID, Status: "unavailable", Reason: "owned_cache_unavailable"}
	if err != nil {
		if ae, ok := err.(*apiError); ok {
			out.Reason = ae.Message
		}
		return out, nil
	}
	return p.signAudio(ctx, canonical)
}
func (p *ownedCacheProvider) signAudio(ctx context.Context, t Track) (Resolution, error) {
	out := Resolution{TrackID: t.ID, Status: "unavailable", Reason: "owned_cache_signing_failed"}
	u, err := p.signer.PresignedGetObject(ctx, p.bucket, p.objectKey("music", t.ExternalID, ".mp3"), ownedURLLifetime, url.Values{"response-content-type": {"audio/mpeg"}})
	if err != nil {
		out.Reason = "owned_cache_signing_failed"
		return out, nil
	}
	issued, err := time.Parse("20060102T150405Z", u.Query().Get("X-Amz-Date"))
	if err != nil {
		out.Reason = "owned_cache_signing_failed"
		return out, nil
	}
	expires := issued.Add(ownedURLLifetime)
	out.Status, out.Reason, out.URL, out.ExpiresAt = "available", "owned_minio_cache", u.String(), &expires
	return out, nil
}

func cachedLyrics(id string, b []byte) (Lyrics, error) {
	out := Lyrics{TrackID: id}
	if json.Unmarshal(b, &out.LRC) == nil {
		return out, nil
	}
	var v struct {
		Code   int
		LRC    struct{ Lyric string }
		Tlyric struct{ Lyric string }
	}
	if json.Unmarshal(b, &v) != nil || (v.Code != 0 && v.Code != 200) {
		return out, &apiError{502, "owned_cache_invalid_lyrics"}
	}
	out.LRC, out.TranslatedLRC = v.LRC.Lyric, v.Tlyric.Lyric
	return out, nil
}
func (p *ownedCacheProvider) Lyrics(ctx context.Context, t Track) (Lyrics, error) {
	canonical, d, err := p.lookup(ctx, t.ID)
	if err == ownedMiss {
		return p.fallback.Lyrics(ctx, t)
	}
	if err != nil {
		return Lyrics{TrackID: t.ID}, err
	}
	if len(d.Lyrics) > 0 && string(d.Lyrics) != "null" {
		return cachedLyrics(t.ID, d.Lyrics)
	}
	b, err := p.read(ctx, p.objectKey("lyrics", canonical.ExternalID, ".json"))
	if err == nil {
		return cachedLyrics(t.ID, b)
	}
	if err == ownedMiss {
		b, err = p.read(ctx, p.objectKey("lyrics", canonical.ExternalID, ".txt"))
		if err == nil {
			return Lyrics{TrackID: t.ID, LRC: string(b)}, nil
		}
	}
	if err == ownedMiss {
		return p.fallback.Lyrics(ctx, canonical)
	}
	return Lyrics{TrackID: t.ID}, err
}

type failedCacheProvider struct{}

func (failedCacheProvider) Search(context.Context, string) ([]Track, error) {
	return nil, &apiError{503, "owned_cache_not_configured"}
}
func (failedCacheProvider) Detail(context.Context, string) (Track, error) {
	return Track{}, &apiError{503, "owned_cache_not_configured"}
}
func (failedCacheProvider) Resolve(_ context.Context, t Track) (Resolution, error) {
	return Resolution{TrackID: t.ID, Status: "unavailable", Reason: "owned_cache_not_configured"}, nil
}
func (failedCacheProvider) Lyrics(_ context.Context, t Track) (Lyrics, error) {
	return Lyrics{TrackID: t.ID}, &apiError{503, "owned_cache_not_configured"}
}
