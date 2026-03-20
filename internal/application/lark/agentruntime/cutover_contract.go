package agentruntime

import (
	"context"
	"time"

	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

type RuntimeAgenticCutoverRequest struct {
	Event     *larkim.P2MessageReceiveV1
	Plan      ChatGenerationPlan
	StartedAt time.Time
	Ownership InitialRunOwnership
}

type RuntimeStandardCutoverRequest struct {
	Event     *larkim.P2MessageReceiveV1
	Plan      ChatGenerationPlan
	StartedAt time.Time
	Ownership InitialRunOwnership
}

type RuntimeAgenticCutoverHandler interface {
	Handle(context.Context, RuntimeAgenticCutoverRequest) error
}

type RuntimeStandardCutoverHandler interface {
	Handle(context.Context, RuntimeStandardCutoverRequest) error
}
