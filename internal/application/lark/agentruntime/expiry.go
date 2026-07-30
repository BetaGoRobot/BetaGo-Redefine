package agentruntime

import (
	"context"
	"time"
)

type InteractionExpirer interface {
	ExpireScheduleEditInteractions(context.Context, time.Time, int) (int, error)
}
