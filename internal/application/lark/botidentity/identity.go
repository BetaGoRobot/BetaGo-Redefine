package botidentity

import (
	"fmt"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
)

// Identity represents the currently running Lark bot instance.
type Identity struct {
	AppID     string
	BotOpenID string
}

func Current() Identity {
	cfg := config.Get()
	if cfg == nil || cfg.LarkConfig == nil {
		return Identity{}
	}
	return Identity{
		AppID:     strings.TrimSpace(cfg.LarkConfig.AppID),
		BotOpenID: strings.TrimSpace(cfg.LarkConfig.BotOpenID),
	}
}

func (i Identity) Valid() bool {
	return i.AppID != "" || i.BotOpenID != ""
}

func (i Identity) Validate() error {
	if i.Valid() {
		return nil
	}
	return fmt.Errorf("lark bot identity is missing: app_id and bot_open_id are both empty")
}

func (i Identity) Matches(appID, botOpenID string) bool {
	if !i.Valid() {
		return false
	}
	if i.AppID != "" && strings.TrimSpace(appID) != i.AppID {
		return false
	}
	if i.BotOpenID != "" && strings.TrimSpace(botOpenID) != i.BotOpenID {
		return false
	}
	return true
}

func (i Identity) EnsureMatch(appID, botOpenID string) error {
	if i.Matches(appID, botOpenID) {
		return nil
	}
	return fmt.Errorf("bot identity mismatch: task belongs to another bot")
}
