package history

import (
	"fmt"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
)

func TagText(text string, color string) string {
	return fmt.Sprintf("<text_tag color='%s'>%s</text_tag>", color, text)
}

type Mention struct {
	Key string `json:"key"`
	ID  struct {
		LegacyUserID string `json:"user_id"`
		OpenID       string `json:"open_id"`
		UnionID      string `json:"union_id"`
	} `json:"id"`
	Name      string `json:"name"`
	TenantKey string `json:"tenant_key"`
}

// ReplaceMentionToName 将@user_1 替换成 name
func ReplaceMentionToName(input string, mentions []*Mention) string {
	if mentions != nil {
		currentBot := botidentity.Current()
		for _, mention := range mentions {
			if mention == nil || mention.Key == "" {
				continue
			}
			displayName := strings.TrimSpace(mention.Name)
			if currentBot.BotOpenID != "" && strings.TrimSpace(mention.ID.OpenID) == currentBot.BotOpenID {
				displayName = "你"
			}
			if displayName == "" {
				displayName = strings.TrimSpace(mention.ID.OpenID)
			}
			input = strings.ReplaceAll(input, mention.Key, fmt.Sprintf("@%s", displayName))
			if len(input) > 0 && string(input[0]) == "/" {
				if inputs := strings.Split(input, " "); len(inputs) > 0 {
					input = strings.Join(inputs[1:], " ")
				}
			}

		}
	}
	return input
}
