package llmusage

import "context"

type businessAttributionContextKey struct{}

type businessAttribution struct {
	scene     BusinessScene
	operation BusinessOperation
}

func WithBusinessAttribution(ctx context.Context, scene BusinessScene, operation BusinessOperation) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, businessAttributionContextKey{}, businessAttribution{scene: scene, operation: operation})
}

func BusinessAttributionFromContext(ctx context.Context) (BusinessScene, BusinessOperation, bool) {
	if ctx == nil {
		return "", "", false
	}
	attribution, ok := ctx.Value(businessAttributionContextKey{}).(businessAttribution)
	if !ok || !attribution.scene.Valid() || !attribution.operation.Valid() {
		return "", "", false
	}
	return attribution.scene, attribution.operation, true
}

func ApplyBusinessAttribution(
	ctx context.Context,
	scope Scope,
	fallbackScene BusinessScene,
	fallbackOperation BusinessOperation,
) Scope {
	if scene, operation, ok := BusinessAttributionFromContext(ctx); ok {
		scope.BusinessScene = scene
		scope.BusinessOperation = operation
		return scope
	}
	scope.BusinessScene = fallbackScene
	scope.BusinessOperation = fallbackOperation
	return scope
}
