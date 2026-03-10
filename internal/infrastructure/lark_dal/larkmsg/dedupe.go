package larkmsg

import (
	"context"
	"strings"
	"time"

	redis_dal "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/redis"
)

const messageRecordDedupTTL = 7 * 24 * time.Hour

func messageRecordDedupKey(msgID string) string {
	return strings.Join([]string{"betago", "message_record_dedup", msgID}, ":")
}

func ClaimMessageRecord(ctx context.Context, msgID string) (bool, error) {
	msgID = strings.TrimSpace(msgID)
	if msgID == "" {
		return true, nil
	}
	return redis_dal.GetRedisClient().SetNX(ctx, messageRecordDedupKey(msgID), "1", messageRecordDedupTTL).Result()
}
