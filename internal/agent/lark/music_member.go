package lark

import (
	"context"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

// MusicMember supplies fresh permission evidence for an explicitly clicked
// music card. Display-name caches are never membership authority.
func (c *Client) MusicMember(ctx context.Context, scope domain.Scope, actor string) (bool, error) {
	if c.authorize(scope) != nil || !validBindingID(actor, 1024) {
		return false, domain.ErrUnauthorized
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cursor := ""
	seen := map[string]bool{}
	for range 10 {
		page, err := c.GroupMembers(ctx, scope, 100, cursor)
		if err != nil {
			return false, err
		}
		for _, member := range page.Members {
			if member.ID == actor && (member.IDType == "" || member.IDType == "open_id") {
				return true, nil
			}
		}
		if page.SecurityLimited {
			return false, domain.ErrUnauthorized
		}
		if !page.HasMore {
			return false, nil
		}
		if page.PageToken == "" || seen[page.PageToken] {
			return false, domain.ErrUnauthorized
		}
		seen[page.PageToken] = true
		cursor = page.PageToken
	}
	return false, domain.ErrUnauthorized
}
