package larkmsg

import (
	"context"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/query"
)

func IsPrivateModeEnabled(ctx context.Context, chatID string) (bool, error) {
	ins := query.Q.PrivateMode
	privateModeQuery := ins.WithContext(ctx).Where(ins.ChatID.Eq(chatID))

	identity := botidentity.Current()
	if identity.AppID != "" {
		privateModeQuery = privateModeQuery.Where(ins.AppID.Eq(identity.AppID))
	}
	if identity.BotOpenID != "" {
		privateModeQuery = privateModeQuery.Where(ins.BotOpenID.Eq(identity.BotOpenID))
	}

	configs, err := privateModeQuery.Find()
	if err != nil {
		return false, err
	}
	for _, cfg := range configs {
		if cfg.Enable {
			return true, nil
		}
	}
	return false, nil
}
