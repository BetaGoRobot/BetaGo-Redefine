package geocode

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/luckin"
	redis_dal "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/redis"
	"github.com/jellydator/ttlcache/v3"
	"github.com/redis/go-redis/v9"
)

const (
	cacheTTL      = 24 * time.Hour
	redisCacheTTL = 30 * 24 * time.Hour // 地点->坐标几乎不变，持久化保留 30 天
	redisKeyPfx   = "luckin:geocode:"
)

// Provider 把地点文本解析为 GCJ-02 经纬度。
type Provider interface {
	Geocode(context.Context, string) (luckin.GeoPoint, error)
	Name() string
}

// Cached 在多个 Provider 之上加两级缓存：进程内 ttlcache + Redis 持久化，
// 命中任一层即不消耗上游额度。Redis 不可用时自动降级为纯内存缓存。
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
	// L1: 进程内缓存。
	if item := c.cache.Get(address); item != nil {
		return item.Value(), nil
	}
	// L2: Redis 持久化缓存。
	if point, ok := c.getRedis(ctx, address); ok {
		c.cache.Set(address, point, ttlcache.DefaultTTL)
		return point, nil
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
		c.setRedis(ctx, address, point)
		return point, nil
	}
	if lastErr == nil {
		lastErr = ErrNoProvider
	}
	return luckin.GeoPoint{}, lastErr
}

func (c *Cached) getRedis(ctx context.Context, address string) (point luckin.GeoPoint, ok bool) {
	// 配置/Redis 未就绪时不应影响主流程。
	defer func() {
		if r := recover(); r != nil {
			point, ok = luckin.GeoPoint{}, false
		}
	}()
	client := redis_dal.GetRedisClient()
	if client == nil {
		return luckin.GeoPoint{}, false
	}
	val, err := client.Get(ctx, redisKey(address)).Result()
	if err != nil {
		return luckin.GeoPoint{}, false
	}
	parts := strings.SplitN(val, ",", 2)
	if len(parts) != 2 {
		return luckin.GeoPoint{}, false
	}
	lng, err1 := strconv.ParseFloat(parts[0], 64)
	lat, err2 := strconv.ParseFloat(parts[1], 64)
	if err1 != nil || err2 != nil {
		return luckin.GeoPoint{}, false
	}
	return luckin.GeoPoint{Longitude: lng, Latitude: lat}, true
}

func (c *Cached) setRedis(ctx context.Context, address string, point luckin.GeoPoint) {
	defer func() { _ = recover() }()
	client := redis_dal.GetRedisClient()
	if client == nil {
		return
	}
	val := strconv.FormatFloat(point.Longitude, 'f', -1, 64) + "," + strconv.FormatFloat(point.Latitude, 'f', -1, 64)
	if err := client.Set(ctx, redisKey(address), val, redisCacheTTL).Err(); err != nil && err != redis.Nil {
		// 缓存写失败不影响主流程。
		_ = err
	}
}

func redisKey(address string) string {
	return redisKeyPfx + address
}
