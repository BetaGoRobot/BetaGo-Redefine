package mcpstore

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/luckin"
	redis_dal "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/redis"
	"github.com/jellydator/ttlcache/v3"
	"github.com/redis/go-redis/v9"
)

const (
	// sessionTTL 进程内缓存与 Redis 持久化共用的过期时长。一次点单流程绑定到卡片消息 ID，
	// 卡片在飞书侧约 7 天内可 patch，因此放长到 7 天。
	sessionTTL = 7 * 24 * time.Hour
	// historyTTL 用户级历史（最近门店、是否曾点过单）远长于一次点单流程。
	historyTTL = 30 * 24 * time.Hour

	// luckin:session:order:<msg_id> -> OrderSession（卡片维度）
	redisOrderSessionKeyPfx = "luckin:session:order:"
	// luckin:session:recent_shops:<chat>:<user> -> []ShopSelection（用户维度）
	redisRecentShopsKeyPfx = "luckin:session:recent_shops:"
	// luckin:session:seen:<chat>:<user> -> bool（用户维度）
	redisSeenKeyPfx = "luckin:session:seen:"
)

var (
	defaultSessionStore *SessionStore
	defaultSessionOnce  sync.Once
)

// DefaultSessionStore 返回进程级共享的会话存储，供工具层与卡片回调共用。
func DefaultSessionStore() *SessionStore {
	defaultSessionOnce.Do(func() {
		defaultSessionStore = NewSessionStore()
	})
	return defaultSessionStore
}

// SessionStore 两级存储：
//   - L1 进程内 ttlcache，加速热路径；
//   - L2 Redis，跨副本/跨进程共享，是真实状态。
//
// 写操作必须先写 L2 再写 L1，避免并发副本读到自己 L1 的旧值。
type SessionStore struct {
	sessions    *ttlcache.Cache[string, luckin.OrderSession]
	recentShops *ttlcache.Cache[string, []luckin.ShopSelection]
	seen        *ttlcache.Cache[string, bool]
}

func NewSessionStore() *SessionStore {
	sessions := ttlcache.New(
		ttlcache.WithTTL[string, luckin.OrderSession](sessionTTL),
		ttlcache.WithCapacity[string, luckin.OrderSession](2000),
	)
	recentShops := ttlcache.New(
		ttlcache.WithTTL[string, []luckin.ShopSelection](historyTTL),
		ttlcache.WithCapacity[string, []luckin.ShopSelection](5000),
	)
	seen := ttlcache.New(
		ttlcache.WithTTL[string, bool](historyTTL),
		ttlcache.WithCapacity[string, bool](5000),
	)
	go sessions.Start()
	go recentShops.Start()
	go seen.Start()
	return &SessionStore{sessions: sessions, recentShops: recentShops, seen: seen}
}

// GetSession 获取一次点单流程的状态（发起人 + 门店 + 购物车）。
func (s *SessionStore) GetSession(ctx context.Context, key luckin.SessionKey) (luckin.OrderSession, bool) {
	if !key.Valid() {
		return luckin.OrderSession{}, false
	}
	cacheKey := key.String()
	if item := s.sessions.Get(cacheKey); item != nil {
		return item.Value(), true
	}
	var sess luckin.OrderSession
	if getRedisJSON(ctx, redisOrderSessionKeyPfx+cacheKey, &sess) {
		s.sessions.Set(cacheKey, sess, ttlcache.DefaultTTL)
		return sess, true
	}
	return luckin.OrderSession{}, false
}

// SetSession 写入完整 OrderSession。调用方负责持有锁，避免 read-modify-write 撕裂。
func (s *SessionStore) SetSession(ctx context.Context, key luckin.SessionKey, sess luckin.OrderSession) {
	if !key.Valid() {
		return
	}
	cacheKey := key.String()
	setRedisJSON(ctx, redisOrderSessionKeyPfx+cacheKey, sess, sessionTTL)
	s.sessions.Set(cacheKey, sess, ttlcache.DefaultTTL)
}

// DeleteSession 清空一次点单流程（用于结算后清空购物车场景，调用方决定是否调用）。
func (s *SessionStore) DeleteSession(ctx context.Context, key luckin.SessionKey) {
	if !key.Valid() {
		return
	}
	cacheKey := key.String()
	s.sessions.Delete(cacheKey)
	delRedis(ctx, redisOrderSessionKeyPfx+cacheKey)
}

// GetRecentShops 用户级"最近选过的门店"。
func (s *SessionStore) GetRecentShops(ctx context.Context, key luckin.UserHistoryKey, limit int) []luckin.ShopSelection {
	if limit <= 0 {
		return nil
	}
	cacheKey := key.String()
	if item := s.recentShops.Get(cacheKey); item != nil {
		return limitRecentShops(item.Value(), limit)
	}
	var shops []luckin.ShopSelection
	if getRedisJSON(ctx, redisRecentShopsKeyPfx+cacheKey, &shops) {
		shops = normalizeRecentShops(shops, 10)
		s.recentShops.Set(cacheKey, shops, ttlcache.DefaultTTL)
		return limitRecentShops(shops, limit)
	}
	return nil
}

// AddRecentShop 把一次成功选店记录到用户偏好。
func (s *SessionStore) AddRecentShop(ctx context.Context, key luckin.UserHistoryKey, shop luckin.ShopSelection) {
	if shop.DeptID == 0 {
		return
	}
	shops := s.GetRecentShops(ctx, key, 10)
	shops = append([]luckin.ShopSelection{shop}, shops...)
	shops = normalizeRecentShops(shops, 10)
	cacheKey := key.String()
	s.recentShops.Set(cacheKey, shops, ttlcache.DefaultTTL)
	setRedisJSON(ctx, redisRecentShopsKeyPfx+cacheKey, shops, historyTTL)
}

// Seen 该用户此前是否在该机器人下点过单。
func (s *SessionStore) Seen(ctx context.Context, key luckin.UserHistoryKey) bool {
	cacheKey := key.String()
	if item := s.seen.Get(cacheKey); item != nil {
		return item.Value()
	}
	var flag bool
	if getRedisJSON(ctx, redisSeenKeyPfx+cacheKey, &flag) && flag {
		s.seen.Set(cacheKey, true, ttlcache.DefaultTTL)
		return true
	}
	return false
}

// MarkSeen 墓碑标记，决定后续的会话过期 vs 从未开始文案。
func (s *SessionStore) MarkSeen(ctx context.Context, key luckin.UserHistoryKey) {
	cacheKey := key.String()
	s.seen.Set(cacheKey, true, ttlcache.DefaultTTL)
	setRedisJSON(ctx, redisSeenKeyPfx+cacheKey, true, historyTTL)
}

func normalizeRecentShops(shops []luckin.ShopSelection, limit int) []luckin.ShopSelection {
	out := make([]luckin.ShopSelection, 0, len(shops))
	seen := make(map[int64]struct{}, len(shops))
	for _, shop := range shops {
		if shop.DeptID == 0 {
			continue
		}
		if _, ok := seen[shop.DeptID]; ok {
			continue
		}
		seen[shop.DeptID] = struct{}{}
		out = append(out, shop)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func limitRecentShops(shops []luckin.ShopSelection, limit int) []luckin.ShopSelection {
	if limit <= 0 || len(shops) == 0 {
		return nil
	}
	if len(shops) > limit {
		shops = shops[:limit]
	}
	out := make([]luckin.ShopSelection, len(shops))
	copy(out, shops)
	return out
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
	if err := client.Set(ctx, key, data, ttl).Err(); err != nil && !errors.Is(err, redis.Nil) {
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
