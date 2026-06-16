package luckin

import (
	"context"
	"strings"
	"sync"
)

// ImageUploader 把图片 URL 上传到飞书并返回 img_key，用于卡片展示。
type ImageUploader interface {
	UploadByURL(ctx context.Context, url string) string
}

// CachedImageUploader 按 URL 缓存 img_key，避免重复上传消耗飞书额度。
type CachedImageUploader struct {
	upload func(ctx context.Context, url string) string
	mu     sync.RWMutex
	cache  map[string]string
}

func NewCachedImageUploader(upload func(ctx context.Context, url string) string) *CachedImageUploader {
	return &CachedImageUploader{
		upload: upload,
		cache:  make(map[string]string),
	}
}

func (u *CachedImageUploader) UploadByURL(ctx context.Context, url string) string {
	url = strings.TrimSpace(url)
	if url == "" || u == nil || u.upload == nil {
		return ""
	}
	u.mu.RLock()
	key, ok := u.cache[url]
	u.mu.RUnlock()
	if ok {
		return key
	}
	key = strings.TrimSpace(u.upload(ctx, url))
	if key == "" {
		return ""
	}
	u.mu.Lock()
	u.cache[url] = key
	u.mu.Unlock()
	return key
}
