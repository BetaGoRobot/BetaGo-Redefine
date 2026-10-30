// Package participation checks whether fresh ambient input can enter the
// conversation engine. The engine alone decides whether to speak or observe.
package participation

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

const freshnessWindow = 3 * time.Minute

// EligibleForConversation is a deterministic ingress check, not a second social
// decision. A pause or absence of a recent bot reply cannot discard a fresh
// question before the full scene is assembled. It grants no action authority.
func EligibleForConversation(ctx context.Context, m domain.Message) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	age := time.Since(m.Source.OccurredAt)
	if m.Deleted || m.Source.Validate() != nil || age < 0 || age > freshnessWindow || strings.TrimSpace(m.Text) == "" || len(m.Text) > 4096 {
		return false, nil
	}
	var meta domain.IngressMetadata
	if json.Unmarshal(m.Raw, &meta) != nil || meta.Kind != "message" || meta.SenderType != "user" {
		return false, nil
	}
	if len(meta.Mentions) > 0 && !meta.Addressed {
		return false, nil
	}
	return true, nil
}
