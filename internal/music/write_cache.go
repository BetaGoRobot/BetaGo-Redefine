package music

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
)

const ownedAudioLimit = 64 << 20

type ownedManifest struct {
	Format      string    `json:"format"`
	Version     int       `json:"version"`
	Status      string    `json:"status"`
	Track       Track     `json:"track"`
	AudioObject string    `json:"audioObject"`
	CachedAt    time.Time `json:"cachedAt"`
}

func officialMediaHost(host string) bool {
	host = strings.ToLower(host)
	return host == "music.126.net" || strings.HasSuffix(host, ".music.126.net") || host == "music.163.com" || strings.HasSuffix(host, ".music.163.com")
}
func (p *ownedCacheProvider) put(ctx context.Context, key string, b []byte, contentType string) error {
	opts := minio.PutObjectOptions{ContentType: contentType, DisableMultipart: true, DisableContentSha256: true, SendContentMd5: true}
	opts.SetMatchETagExcept("*")
	_, err := p.reader.PutObject(ctx, p.bucket, key, bytes.NewReader(b), int64(len(b)), opts)
	return err
}

// A 200 error page is not an MP3. This is a small format gate, not an audio
// decoder; unsupported source formats remain playable via the source URL.
func cacheableMP3(b []byte, contentType string) bool {
	if contentType != "" {
		kind, _, err := mime.ParseMediaType(contentType)
		if err != nil {
			return false
		}
		switch strings.ToLower(kind) {
		case "audio/mpeg", "audio/mp3", "audio/x-mpeg", "application/octet-stream", "binary/octet-stream":
		default:
			return false
		}
	}
	if len(b) > 10 && string(b[:3]) == "ID3" && b[3] >= 2 && b[3] <= 4 && b[4] != 0xff {
		for _, sizeByte := range b[6:10] {
			if sizeByte >= 128 {
				return false
			}
		}
		tagSize := int(b[6])<<21 | int(b[7])<<14 | int(b[8])<<7 | int(b[9])
		return tagSize < len(b)-10
	}
	return len(b) >= 4 && b[0] == 0xff && b[1]&0xe0 == 0xe0 && b[1]&0x18 != 0x08 && b[1]&0x06 != 0 && b[2]&0xf0 != 0 && b[2]&0xf0 != 0xf0 && b[2]&0x0c != 0x0c
}
func (p *ownedCacheProvider) cacheResolved(ctx context.Context, t Track, source Resolution) Resolution {
	if source.Status == "preview" {
		source.Reason += ";cache_skipped_preview"
		return source
	}
	if source.Status != "available" {
		return source
	}
	failure := func(reason string) Resolution {
		source.Reason += ";cache_failed_" + reason
		return source
	}
	u, err := url.Parse(source.URL)
	if err != nil || u.Scheme != "https" || u.User != nil || (u.Port() != "" && u.Port() != "443") || !officialMediaHost(u.Hostname()) || source.TrackID != t.ID || t.ID != "netease:"+t.ExternalID || !numeric.MatchString(t.ExternalID) {
		return failure("source_not_approved")
	}
	select {
	case p.writers <- struct{}{}:
		defer func() { <-p.writers }()
	default:
		source.Reason += ";cache_busy"
		return source
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	key := "music/" + t.ExternalID + ".mp3"
	if _, err := p.reader.StatObject(ctx, p.bucket, key, minio.StatObjectOptions{}); err == nil {
		source.Reason += ";cache_skipped_existing_unverified_audio"
		return source
	} else if ownedError(err) != ownedMiss {
		return failure("audio_check")
	}
	canonical, err := p.fallback.Detail(ctx, t.ID)
	if err != nil || canonical.ID != t.ID || canonical.Source != "netease" || canonical.ExternalID != t.ExternalID {
		return failure("metadata")
	}
	canonical.Picture = securePicture(canonical.Picture)
	request, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return failure("download")
	}
	// Source authorization never reaches a CDN. Redirects are rejected even to
	// another official host; a new resolve is required instead of changing URLs.
	client := &http.Client{Timeout: 12 * time.Second, CheckRedirect: rejectSourceRedirect}
	response, err := client.Do(request)
	if err != nil {
		return failure("download")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Range") != "" || response.ContentLength > ownedAudioLimit {
		return failure("download_status_or_size")
	}
	audio, err := io.ReadAll(io.LimitReader(response.Body, ownedAudioLimit+1))
	if err != nil || len(audio) == 0 || len(audio) > ownedAudioLimit || (response.ContentLength >= 0 && response.ContentLength != int64(len(audio))) {
		return failure("download_size")
	}
	if !cacheableMP3(audio, response.Header.Get("Content-Type")) {
		return failure("format")
	}
	if err := p.put(ctx, key, audio, "audio/mpeg"); err != nil {
		return failure("audio_write")
	}
	lyricsCached := false
	if lyrics, err := p.fallback.Lyrics(ctx, canonical); err == nil && lyrics.TrackID == t.ID {
		b, _ := json.Marshal(map[string]any{"code": 200, "lrc": map[string]string{"lyric": lyrics.LRC}, "tlyric": map[string]string{"lyric": lyrics.TranslatedLRC}})
		lyricsCached = p.put(ctx, "lyrics/"+t.ExternalID+".json", b, "application/json") == nil
	}
	// The manifest is the commit marker and contains no source URL or credential.
	// A failed/incomplete upload has no new manifest and cannot become a cache hit.
	manifest, _ := json.Marshal(ownedManifest{Format: "betago-music-cache", Version: 1, Status: "available", Track: canonical, AudioObject: key, CachedAt: time.Now().UTC()})
	if err := p.put(ctx, "catalog/"+t.ExternalID+".json", manifest, "application/json"); err != nil {
		return failure("manifest_write")
	}
	// Read and sign the newly committed fixed object without running source lookup.
	canonical, _, err = p.lookup(ctx, t.ID)
	if err != nil {
		return failure("verification")
	}
	out, err := p.signAudio(ctx, canonical)
	if err != nil || out.Reason != "owned_minio_cache" {
		return failure("verification")
	}
	out.Reason = "owned_minio_cache_stored"
	if !lyricsCached {
		out.Reason += ";lyrics_not_cached"
	}
	return out
}
