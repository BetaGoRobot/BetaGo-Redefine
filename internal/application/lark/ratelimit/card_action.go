package ratelimit

import (
	"fmt"
	"strings"

	cardactionproto "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
)

type StatsViewRequest struct {
	ChatID string
}

func BuildStatsViewValue(chatID string) map[string]string {
	return cardactionproto.New(cardactionproto.ActionRateLimitView).
		WithValue(cardactionproto.ChatIDField, strings.TrimSpace(chatID)).
		Payload()
}

func ParseStatsViewRequest(parsed *cardactionproto.Parsed) (*StatsViewRequest, error) {
	if parsed == nil {
		return nil, fmt.Errorf("ratelimit view action is nil")
	}
	if parsed.Name != cardactionproto.ActionRateLimitView {
		return nil, fmt.Errorf("unsupported ratelimit view action: %s", parsed.Name)
	}
	chatID, _ := parsed.String(cardactionproto.ChatIDField)
	return &StatsViewRequest{
		ChatID: strings.TrimSpace(chatID),
	}, nil
}
