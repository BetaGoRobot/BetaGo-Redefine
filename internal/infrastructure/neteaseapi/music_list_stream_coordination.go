package neteaseapi

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	redis_dal "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/redis"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/redis/go-redis/v9"
	uuid "github.com/satori/go.uuid"
	"go.uber.org/zap"
)

const musicListStreamRedisTTL = 30 * time.Minute

var deleteMusicListStreamLeaseScript = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
	return redis.call("DEL", KEYS[1])
end
return 0
`)

var currentMusicListStreamIdentity = botidentity.Current

type musicListStreamGuard struct {
	messageID string
	token     string
}

func musicListStreamRedisKey(messageID string) string {
	return currentMusicListStreamIdentity().NamespaceKey("music", "list", "stream", strings.TrimSpace(messageID))
}

func (g *musicListStreamGuard) Register(ctx context.Context, messageID string, cancel context.CancelFunc) {
	if g == nil {
		return
	}
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return
	}

	g.messageID = messageID
	g.token = uuid.NewV4().String()
	if cancel != nil {
		activeMusicListStreams.Store(messageID, cancel)
	}

	client := redis_dal.GetRedisClient()
	if client == nil || g.token == "" {
		return
	}
	if err := client.Set(ctx, musicListStreamRedisKey(messageID), g.token, musicListStreamRedisTTL).Err(); err != nil {
		logs.L().Ctx(ctx).Warn("register music list stream redis lease failed", zap.String("message_id", messageID), zap.Error(err))
	}
}

func (g *musicListStreamGuard) EnsureActive(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if g == nil || g.messageID == "" || g.token == "" {
		return nil
	}

	client := redis_dal.GetRedisClient()
	if client == nil {
		return nil
	}

	token, err := client.Get(ctx, musicListStreamRedisKey(g.messageID)).Result()
	if err == nil {
		if token != g.token {
			return context.Canceled
		}
		return nil
	}
	if errors.Is(err, redis.Nil) {
		return context.Canceled
	}

	logs.L().Ctx(ctx).Warn("check music list stream redis lease failed", zap.String("message_id", g.messageID), zap.Error(err))
	return nil
}

func (g *musicListStreamGuard) Release(ctx context.Context) {
	if g == nil || g.messageID == "" {
		return
	}

	activeMusicListStreams.Delete(g.messageID)

	client := redis_dal.GetRedisClient()
	if client == nil || g.token == "" {
		return
	}
	if err := deleteMusicListStreamLeaseScript.Run(ctx, client, []string{musicListStreamRedisKey(g.messageID)}, g.token).Err(); err != nil && !errors.Is(err, redis.Nil) {
		logs.L().Ctx(ctx).Warn("release music list stream redis lease failed", zap.String("message_id", g.messageID), zap.Error(err))
	}
}

func CancelMusicListStream(ctx context.Context, messageID string) {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return
	}

	cancelAny, ok := activeMusicListStreams.LoadAndDelete(messageID)
	if ok {
		if cancel, ok := cancelAny.(context.CancelFunc); ok && cancel != nil {
			cancel()
		}
	}

	client := redis_dal.GetRedisClient()
	if client == nil {
		return
	}
	if err := client.Del(ctx, musicListStreamRedisKey(messageID)).Err(); err != nil && !errors.Is(err, redis.Nil) {
		logs.L().Ctx(ctx).Warn("cancel music list stream redis lease failed", zap.String("message_id", messageID), zap.Error(err))
	}
}
