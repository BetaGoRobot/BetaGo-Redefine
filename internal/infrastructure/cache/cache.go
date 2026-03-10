package cache

import (
	"context"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/go_utils/reflecting"
	"github.com/patrickmn/go-cache"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

var wrapper = NewCacheWrapper(30*time.Minute, 30*time.Minute)

// CacheWrapper 结构体和 New/GetOrExecute 方法保持不变
type CacheWrapper struct {
	c *cache.Cache
}

func NewCacheWrapper(defaultExpiration, cleanupInterval time.Duration) *CacheWrapper {
	return &CacheWrapper{
		c: cache.New(defaultExpiration, cleanupInterval),
	}
}

func GetOrExecute[T any](ctx context.Context, key string, fn func() (T, error)) (value T, err error) {
	fName := reflecting.GetFunctionName(fn)
	cacheKey := fName + ":" + key
	ctx, span := otel.Start(ctx,
		trace.WithAttributes(
			attribute.String("cache.function", fName),
			attribute.String("cache.key.preview", otel.PreviewString(key, 128)),
			attribute.Int("cache.key.len", len(key)),
		),
	)
	defer span.End()
	defer otel.RecordErrorPtr(span, &err)

	if value, found := wrapper.c.Get(cacheKey); found {
		span.SetAttributes(attribute.Bool("cache.hit", true))
		span.AddEvent("cache_hit")
		logs.L().Ctx(ctx).Info("[✅ Cache HIT] Executing function to get cache value,", zap.String("key", key), zap.String("function", fName))
		return value.(T), nil
	}

	span.SetAttributes(attribute.Bool("cache.hit", false))
	span.AddEvent("cache_miss")
	logs.L().Ctx(ctx).Debug("[❌ Cache MISS] Executing function to get cache value,", zap.String("key", key), zap.String("function", fName))
	value, err = fn()
	if err != nil {
		return
	}

	wrapper.c.Set(cacheKey, value, cache.DefaultExpiration)
	span.AddEvent("cache_store")
	logs.L().Ctx(ctx).Debug("📦 Cache SET", zap.String("key", key))

	return
}
