package utils

import "context"

// DurableContext preserves request-scoped values such as trace and tenant
// identity while preventing accepted background work from inheriting a
// caller's cancellation or deadline.
func DurableContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return context.WithoutCancel(ctx)
}
