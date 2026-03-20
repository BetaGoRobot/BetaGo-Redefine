package agentruntime

import "context"

type InitialReplyTarget struct {
	MessageID string `json:"message_id,omitempty"`
	CardID    string `json:"card_id,omitempty"`
}

type initialReplyTargetContextKey struct{}

func WithInitialReplyTarget(ctx context.Context, target InitialReplyTarget) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if target.MessageID == "" && target.CardID == "" {
		return ctx
	}
	return context.WithValue(ctx, initialReplyTargetContextKey{}, target)
}

func InitialReplyTargetFromContext(ctx context.Context) (InitialReplyTarget, bool) {
	if ctx == nil {
		return InitialReplyTarget{}, false
	}
	target, ok := ctx.Value(initialReplyTargetContextKey{}).(InitialReplyTarget)
	if !ok {
		return InitialReplyTarget{}, false
	}
	if target.MessageID == "" && target.CardID == "" {
		return InitialReplyTarget{}, false
	}
	return target, true
}
