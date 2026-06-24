package mcpstore

import (
	"context"
	"errors"
	"strings"
	"time"

	redis_dal "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/redis"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// ErrSessionLocked 别人正在持有同一会话的锁，调用方应提示用户重试。
var ErrSessionLocked = errors.New("luckin session is busy, please retry")

// ErrRedisUnavailable Redis 未就绪。点单流程是多副本共享的，没有 Redis 就不允许进入临界区。
var ErrRedisUnavailable = errors.New("redis is not available for luckin session lock")

const (
	// luckin:session:lock:<message_id>
	redisSessionLockKeyPfx = "luckin:session:lock:"
	// 锁租约：覆盖一次 patch + 远程调用最坏耗时；瑞幸接口 10s 超时，留些余量。
	sessionLockTTL = 12 * time.Second
	// 抢锁的总等待时间上限。
	sessionLockMaxWait = 1500 * time.Millisecond
	// 第一次重试的退避起点。
	sessionLockBackoffStart = 30 * time.Millisecond
	// 退避步进。
	sessionLockBackoffMax = 200 * time.Millisecond
)

// 释放锁的 Lua 脚本：仅当 token 匹配时才删除，避免误删别人的锁。
var sessionLockReleaseScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
    return redis.call('DEL', KEYS[1])
end
return 0
`)

// WithSessionLock 在 Redis 上抢一把按 messageID 隔离的分布式锁，独占一次"读-改-写"。
// fn 在持锁期间执行；fn 返回的 error 透传给调用方。
//
// 使用示例：
//
//	err := mcpstore.WithSessionLock(ctx, msgID, func() error {
//	    sess, _ := store.GetSession(ctx, key)
//	    sess.Cart.Add(...)
//	    store.SetSession(ctx, key, sess)
//	    return nil
//	})
func WithSessionLock(ctx context.Context, messageID string, fn func() error) error {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return errors.New("luckin session lock requires non-empty message id")
	}
	client := redis_dal.GetRedisClient()
	if client == nil {
		return ErrRedisUnavailable
	}
	key := redisSessionLockKeyPfx + messageID
	token := uuid.NewString()

	if err := acquireSessionLock(ctx, client, key, token); err != nil {
		return err
	}
	defer releaseSessionLock(ctx, client, key, token)
	return fn()
}

func acquireSessionLock(ctx context.Context, client *redis.Client, key, token string) error {
	deadline := time.Now().Add(sessionLockMaxWait)
	backoff := sessionLockBackoffStart
	for {
		ok, err := client.SetNX(ctx, key, token, sessionLockTTL).Result()
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		if time.Now().After(deadline) {
			return ErrSessionLocked
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > sessionLockBackoffMax {
			backoff = sessionLockBackoffMax
		}
	}
}

func releaseSessionLock(ctx context.Context, client *redis.Client, key, token string) {
	defer func() { _ = recover() }()
	// 用 background 兜底：fn 把 ctx 取消后，仍然要尽量释放锁，避免后续请求多等一个 TTL。
	if ctx == nil || ctx.Err() != nil {
		ctx = context.Background()
	}
	_, _ = sessionLockReleaseScript.Run(ctx, client, []string{key}, token).Result()
}
