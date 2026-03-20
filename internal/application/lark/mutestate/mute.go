package mutestate

import "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"

const RedisKeyPrefix = "betago:mute"

func RedisKey(chatID string) string {
	return botidentity.Current().NamespaceKey(RedisKeyPrefix, chatID)
}
