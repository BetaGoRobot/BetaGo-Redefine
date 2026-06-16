package geocode

import (
	"context"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/luckin"
	"github.com/jellydator/ttlcache/v3"
)

const cacheTTL = 24 * time.Hour

// Provider 把地点文本解析为 GCJ-02 经纬度。
type Provider interface {
	Geocode(context.Context, string) (luckin.GeoPoint, error)
	Name() string
}

// Cached 在多个 Provider 之上加一层共享缓存，命中缓存即不消耗任何上游额度。
type Cached struct {
	providers []Provider
	cache     *ttlcache.Cache[string, luckin.GeoPoint]
}

func NewCached(providers ...Provider) *Cached {
	c := ttlcache.New(
		ttlcache.WithTTL[string, luckin.GeoPoint](cacheTTL),
		ttlcache.WithCapacity[string, luckin.GeoPoint](5000),
	)
	go c.Start()
	return &Cached{providers: providers, cache: c}
}

func (c *Cached) Geocode(ctx context.Context, address string) (luckin.GeoPoint, error) {
	address = strings.TrimSpace(address)
	if address == "" {
		return luckin.GeoPoint{}, errEmptyAddress
	}
	if item := c.cache.Get(address); item != nil {
		return item.Value(), nil
	}

	var lastErr error
	for _, p := range c.providers {
		if p == nil {
			continue
		}
		point, err := p.Geocode(ctx, address)
		if err != nil {
			lastErr = err
			continue
		}
		c.cache.Set(address, point, ttlcache.DefaultTTL)
		return point, nil
	}
	if lastErr == nil {
		lastErr = ErrNoProvider
	}
	return luckin.GeoPoint{}, lastErr
}
