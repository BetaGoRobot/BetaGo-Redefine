package neteaseapi

import (
	"bytes"
	"context"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/miniodal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/xmodel"
	"github.com/bytedance/sonic"
	"github.com/minio/minio-go/v7"
)

const (
	cacheBucket = "cloudmusic"
	cacheExpiry = 30 * 24 * time.Hour // 30 days
)

// MinioCache provides a typed caching layer over Minio for cloudmusic data.
type MinioCache[T any] struct {
	bucket string
}

// NewMinioCache creates a MinioCache for the cloudmusic bucket.
func NewMinioCache[T any]() *MinioCache[T] {
	return &MinioCache[T]{bucket: cacheBucket}
}

// Get retrieves a JSON-encodable struct from Minio cache.
// Returns true if found and successfully unmarshaled into dest.
func (c *MinioCache[T]) Get(ctx context.Context, key string, dest *T) bool {
	client := miniodal.GetInternalClient()
	if client == nil {
		return false
	}
	obj, err := client.GetObject(ctx, c.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return false
	}
	defer obj.Close()
	data, err := io.ReadAll(obj)
	if err != nil || len(data) == 0 {
		return false
	}
	if err := sonic.Unmarshal(data, dest); err != nil {
		return false
	}
	return true
}

// Set marshals src as JSON and uploads to Minio cache.
func (c *MinioCache[T]) Set(ctx context.Context, key string, src *T) {
	data, err := sonic.Marshal(src)
	if err != nil {
		return
	}
	miniodal.New(miniodal.Internal).Upload(ctx).
		WithContentType("application/json").
		SkipDedup(false).
		WithReader(io.NopCloser(bytes.NewReader(data))).
		Do(c.bucket, key, minio.PutObjectOptions{})
}

// TextCache provides plain text caching with presigned URL support.
type TextCache struct {
	*MinioCache[string]
}

// NewTextCache creates a TextCache for plain text content.
func NewTextCache() *TextCache {
	return &TextCache{MinioCache: NewMinioCache[string]()}
}

// GetText retrieves plain text content from Minio cache.
// Returns the content and a presigned URL (valid for 5 minutes).
// Returns empty string if not found.
func (c *TextCache) GetText(ctx context.Context, key string) (string, string) {
	client := miniodal.GetInternalClient()
	if client == nil {
		return "", ""
	}
	obj, err := client.GetObject(ctx, c.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return "", ""
	}
	defer obj.Close()
	data, err := io.ReadAll(obj)
	if err != nil || len(data) == 0 {
		return "", ""
	}
	presignedURL, err := miniodal.PresignGetObject(ctx, c.bucket, key, time.Minute*5)
	if err != nil {
		return string(data), ""
	}
	return string(data), presignedURL
}

// SetText uploads string content as plain text to Minio cache.
func (c *TextCache) SetText(ctx context.Context, key string, content string) {
	miniodal.New(miniodal.Internal).Upload(ctx).
		WithContentType(xmodel.ContentTypePlainText.String()).
		SkipDedup(false).
		WithReader(io.NopCloser(strings.NewReader(content))).
		Do(c.bucket, key, minio.PutObjectOptions{})
}

var (
	musicDetailCache *MinioCache[MusicDetail]
	lyricsCache      *TextCache
	cacheOnce        sync.Once
)

// getMusicDetailCache returns the MusicDetail cache, initializing it if needed.
func getMusicDetailCache() *MinioCache[MusicDetail] {
	cacheOnce.Do(func() {
		musicDetailCache = NewMinioCache[MusicDetail]()
		lyricsCache = NewTextCache()
	})
	return musicDetailCache
}

// getLyricsCache returns the lyrics cache, initializing it if needed.
func getLyricsCache() *TextCache {
	cacheOnce.Do(func() {
		musicDetailCache = NewMinioCache[MusicDetail]()
		lyricsCache = NewTextCache()
	})
	return lyricsCache
}

// InitCaches is called explicitly from neteaseapi.Init() to eagerly initialize caches.
// It is safe to call multiple times (subsequent calls are no-ops).
func InitCaches() {
	getMusicDetailCache()
	getLyricsCache()
}
