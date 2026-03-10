package redis_dal

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/redis/go-redis/v9"
)

// RedisClient  12
var RedisClient *redis.Client
var redisDisableReason string

func HasConfig() bool {
	cfg := config.Get().RedisConfig
	return cfg != nil && cfg.Addr != ""
}

func Init(ctx context.Context) error {
	if !HasConfig() {
		redisDisableReason = "redis config missing or incomplete"
		return errors.New(redisDisableReason)
	}
	redisDisableReason = ""
	return Ping(ctx)
}

// GetRedisClient 1
func GetRedisClient() *redis.Client {
	if RedisClient == nil {
		cfg := config.Get().RedisConfig
		if cfg == nil {
			return nil
		}
		clientName := ""
		if baseInfo := config.Get().BaseInfo; baseInfo != nil {
			clientName = baseInfo.RobotName
		}
		RedisClient = redis.NewClient(&redis.Options{
			Addr:       cfg.Addr,
			Password:   cfg.Password,
			DB:         cfg.DB,
			ClientName: clientName,
		})
	}
	return RedisClient
}

func Ping(ctx context.Context) error {
	client := GetRedisClient()
	if client == nil {
		if redisDisableReason == "" {
			redisDisableReason = "redis client unavailable"
		}
		return errors.New(redisDisableReason)
	}
	if err := client.Ping(ctx).Err(); err != nil {
		redisDisableReason = err.Error()
		return err
	}
	redisDisableReason = ""
	return nil
}

func Close() error {
	if RedisClient == nil {
		return nil
	}
	err := RedisClient.Close()
	RedisClient = nil
	return err
}

func Status() (bool, string) {
	if redisDisableReason != "" {
		return false, redisDisableReason
	}
	if RedisClient == nil {
		return false, "redis not initialized"
	}
	return true, ""
}

// (使用我们上面修改的版本)
var setOrGetExpireAtScriptV2 = redis.NewScript(`
    local result = redis.call('SET', KEYS[1], ARGV[1], 'EXAT', ARGV[2], 'NX')
    if result then
        -- 返回 {1, new_timestamp}
        return {1, tonumber(ARGV[2])}
    else
        -- 返回 {0, existing_timestamp}
        return {0, redis.call('EXPIRETIME', KEYS[1])}
    end
`)

// SetOrGetExpireAtV2 原子地执行操作，并明确返回操作类型。
//
// 返回值:
// - wasSet (bool): true 表示 key 是新创建的; false 表示 key 已存在。
// - timestamp (int64): 相关的时间戳 (新设置的或已存在的)。
// - error: 如果执行出错。
func SetOrGetExpireAt(ctx context.Context, rdb *redis.Client, key string, value interface{}, expireAt time.Time) (set bool, t time.Time, err error) {
	timestamp := expireAt.Unix()

	result, err := setOrGetExpireAtScriptV2.Run(ctx, rdb, []string{key}, value, timestamp).Result()
	if err != nil {
		if err == redis.Nil {
			return set, t, fmt.Errorf("script execution returned nil: %w", err)
		}
		return set, t, fmt.Errorf("failed to run SetOrGetExpireAt script: %w", err)
	}

	// Lua 脚本返回一个 table, go-redis 将其映射为 []interface{}
	resultSlice, ok := result.([]interface{})
	if !ok {
		return set, t, fmt.Errorf("unexpected script result type: %T (value: %v)", result, result)
	}

	if len(resultSlice) != 2 {
		return set, t, fmt.Errorf("unexpected script result slice length: %d (expected 2)", len(resultSlice))
	}

	// 解析 元素 1 (标志位)
	actionFlag, ok := resultSlice[0].(int64)
	if !ok {
		return set, t, fmt.Errorf("unexpected action flag type: %T", resultSlice[0])
	}

	// 解析 元素 2 (时间戳)
	ts, ok := resultSlice[1].(int64)
	if !ok {
		return set, t, fmt.Errorf("unexpected timestamp type: %T", resultSlice[1])
	}

	// actionFlag == 1 意味着 "新 Set 了"
	wasSet := (actionFlag == 1)

	return wasSet, time.Unix(ts, 0), nil
}
