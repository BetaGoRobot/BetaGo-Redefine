package telemetry

import "context"

type withoutPayloadKey struct{}

// WithoutPayload keeps normal request timing and token telemetry while omitting
// source dialogue and full card/query bodies from the meme reference pipeline.
func WithoutPayload(ctx context.Context) context.Context {
	return context.WithValue(ctx, withoutPayloadKey{}, true)
}
