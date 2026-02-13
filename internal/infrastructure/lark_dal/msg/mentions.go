package msg

import (
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func IsMentioned(mentions []*larkim.MentionEvent) bool {
	for _, mention := range mentions {
		if *mention.Id.OpenId == config.Get().LarkConfig.BotOpenID {
			return true
		}
	}
	return false
}
