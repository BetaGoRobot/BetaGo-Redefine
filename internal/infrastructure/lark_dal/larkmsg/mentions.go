package larkmsg

import (
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func IsMentioned(mentions []*larkim.MentionEvent) bool {
	cfg := config.Get()
	if cfg == nil || cfg.LarkConfig == nil {
		return false
	}
	botOpenID := cfg.LarkConfig.BotOpenID
	if botOpenID == "" {
		return false
	}
	for _, mention := range mentions {
		if mention == nil || mention.Id == nil || mention.Id.OpenId == nil {
			continue
		}
		if *mention.Id.OpenId == botOpenID {
			return true
		}
	}
	return false
}
