package mcpstore

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/luckin"
	redis_dal "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/redis"
	"github.com/jellydator/ttlcache/v3"
	"github.com/redis/go-redis/v9"
)

const (
	// sessionTTL 进程内缓存与 Redis 持久化共用的过期时长。会话态（已选门店 + 购物车）
	// 需要跨副本/重启可恢复，因此放长到 7 天，超期按“会话过期”处理。
	sessionTTL = 7 * 24 * time.Hour
	// seenTTL 墓碑标记的存活时长，远长于 sessionTTL：用于区分“从未选过门店”和“选过但已过期”。
	seenTTL = 30 * 24 * time.Hour

	redisShopKeyPfx = "luckin:session:shop:"
	redisCartKeyPfx = "luckin:session:cart:"
	redisSeenKeyPfx = "luckin:session:seen:"
)

var (
	defaultSessionStore *SessionStore
	defaultSessionOnce  sync.Once
)

// DefaultSessionStore 返回进程级共享的门店会话缓存，供工具层与卡片回调共用。
func DefaultSessionStore() *SessionStore {
	defaultSessionOnce.Do(func() {
		defaultSessionStore = NewSessionStore()
	})
	return defaultSessionStore
}

// SessionStore 两级存储：进程内 ttlcache（L1）+ Redis 持久化（L2）。
// Redis 不可用时自动降级为纯内存，保证主流程不被影响。
type SessionStore struct {
	shops *ttlcache.Cache[string, luckin.ShopSelection]
	carts *ttlcache.Cache[string, luckin.Cart]
	seen  *ttlcache.Cache[string, bool]
}

func NewSessionStore() *SessionStore {
	shops := ttlcache.New(
		ttlcache.WithTTL[string, luckin.ShopSelection](sessionTTL),
		ttlcache.WithCapacity[string, luckin.ShopSelection](2000),
	)
	carts := ttlcache.New(
		ttlcache.WithTTL[string, luckin.Cart](sessionTTL),
		ttlcache.WithCapacity[string, luckin.Cart](2000),
	)
	seen := ttlcache.New(
		ttlcache.WithTTL[string, bool](seenTTL),
		ttlcache.WithCapacity[string, bool](5000),
	)
	go shops.Start()
	go carts.Start()
	go seen.Start()
	return &SessionStore{shops: shops, carts: carts, seen: seen}
}

func (s *SessionStore) GetShop(ctx context.Context, key luckin.SessionKey) (luckin.ShopSelection, bool) {
	if item := s.shops.Get(key.String()); item != nil {
		return item.Value(), true
	}
	var shop luckin.ShopSelection
	if getRedisJSON(ctx, redisShopKeyPfx+key.String(), &shop) {
		s.shops.Set(key.String(), shop, ttlcache.DefaultTTL)
		return shop, true
	}
	return luckin.ShopSelection{}, false
}

func (s *SessionStore) SetShop(ctx context.Context, key luckin.SessionKey, shop luckin.ShopSelection) {
	s.shops.Set(key.String(), shop, ttlcache.DefaultTTL)
	setRedisJSON(ctx, redisShopKeyPfx+key.String(), shop, sessionTTL)
	s.markSeen(ctx, key)
}

func (s *SessionStore) ClearShop(ctx context.Context, key luckin.SessionKey) {
	s.shops.Delete(key.String())
	delRedis(ctx, redisShopKeyPfx+key.String())
}

func (s *SessionStore) GetCart(ctx context.Context, key luckin.SessionKey) (luckin.Cart, bool) {
	if item := s.carts.Get(key.String()); item != nil {
		return item.Value(), true
	}
	var cart luckin.Cart
	if getRedisJSON(ctx, redisCartKeyPfx+key.String(), &cart) {
		s.carts.Set(key.String(), cart, ttlcache.DefaultTTL)
		return cart, true
	}
	return luckin.Cart{}, false
}

func (s *SessionStore) SetCart(ctx context.Context, key luckin.SessionKey, cart luckin.Cart) {
	s.carts.Set(key.String(), cart, ttlcache.DefaultTTL)
	setRedisJSON(ctx, redisCartKeyPfx+key.String(), cart, sessionTTL)
	s.markSeen(ctx, key)
}

func (s *SessionStore) ClearCart(ctx context.Context, key luckin.SessionKey) {
	s.carts.Delete(key.String())
	delRedis(ctx, redisCartKeyPfx+key.String())
}

// Seen 报告该会话此前是否选过门店/加过购物车（墓碑标记）。
// 用于区分“会话已过期”（true）与“从未开始点单”（false）。
func (s *SessionStore) Seen(ctx context.Context, key luckin.SessionKey) bool {
	if item := s.seen.Get(key.String()); item != nil {
		return item.Value()
	}
	var flag bool
	if getRedisJSON(ctx, redisSeenKeyPfx+key.String(), &flag) && flag {
		s.seen.Set(key.String(), true, ttlcache.DefaultTTL)
		return true
	}
	return false
}

func (s *SessionStore) markSeen(ctx context.Context, key luckin.SessionKey) {
	s.seen.Set(key.String(), true, ttlcache.DefaultTTL)
	setRedisJSON(ctx, redisSeenKeyPfx+key.String(), true, seenTTL)
}

// getRedisJSON 读取并反序列化 Redis 值；Redis/配置未就绪时安全降级。
func getRedisJSON(ctx context.Context, key string, dst any) (ok bool) {
	defer func() {
		if r := recover(); r != nil {
			ok = false
		}
	}()
	client := redis_dal.GetRedisClient()
	if client == nil {
		return false
	}
	val, err := client.Get(ctx, key).Result()
	if err != nil {
		return false
	}
	return json.Unmarshal([]byte(val), dst) == nil
}

func setRedisJSON(ctx context.Context, key string, value any, ttl time.Duration) {
	defer func() { _ = recover() }()
	client := redis_dal.GetRedisClient()
	if client == nil {
		return
	}
	data, err := json.Marshal(value)
	if err != nil {
		return
	}
	if err := client.Set(ctx, key, data, ttl).Err(); err != nil && err != redis.Nil {
		_ = err
	}
}

func delRedis(ctx context.Context, key string) {
	defer func() { _ = recover() }()
	client := redis_dal.GetRedisClient()
	if client == nil {
		return
	}
	_ = client.Del(ctx, key).Err()
}
