package agentruntime

import "context"

type InitialRunOwnership struct {
	TriggerType    TriggerType `json:"trigger_type,omitempty"`
	AttachToRunID  string      `json:"attach_to_run_id,omitempty"`
	SupersedeRunID string      `json:"supersede_run_id,omitempty"`
}

type initialRunOwnershipContextKey struct{}

func WithInitialRunOwnership(ctx context.Context, ownership InitialRunOwnership) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if ownership.TriggerType == "" && ownership.AttachToRunID == "" && ownership.SupersedeRunID == "" {
		return ctx
	}
	return context.WithValue(ctx, initialRunOwnershipContextKey{}, ownership)
}

func InitialRunOwnershipFromContext(ctx context.Context) (InitialRunOwnership, bool) {
	if ctx == nil {
		return InitialRunOwnership{}, false
	}
	ownership, ok := ctx.Value(initialRunOwnershipContextKey{}).(InitialRunOwnership)
	if !ok {
		return InitialRunOwnership{}, false
	}
	if ownership.TriggerType == "" && ownership.AttachToRunID == "" && ownership.SupersedeRunID == "" {
		return InitialRunOwnership{}, false
	}
	return ownership, true
}
