package runtimecontext

import (
	"context"
	"strings"
)

type capabilityExecutionContext struct {
	CapabilityName           string
	SuppressCompatibleOutput bool
}

type capabilityExecutionContextKey struct{}

func WithCapabilityExecution(ctx context.Context, capabilityName string) context.Context {
	return WithCapabilityExecutionOptions(ctx, capabilityName, true)
}

func WithCapabilityExecutionOptions(ctx context.Context, capabilityName string, suppressCompatibleOutput bool) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, capabilityExecutionContextKey{}, capabilityExecutionContext{
		CapabilityName:           strings.TrimSpace(capabilityName),
		SuppressCompatibleOutput: suppressCompatibleOutput,
	})
}

func CapabilityExecutionName(ctx context.Context) string {
	state := capabilityExecutionState(ctx)
	if state == nil {
		return ""
	}
	return state.CapabilityName
}

func ShouldSuppressCompatibleOutput(ctx context.Context) bool {
	state := capabilityExecutionState(ctx)
	return state != nil && state.SuppressCompatibleOutput
}

func capabilityExecutionState(ctx context.Context) *capabilityExecutionContext {
	if ctx == nil {
		return nil
	}
	state, _ := ctx.Value(capabilityExecutionContextKey{}).(capabilityExecutionContext)
	if state == (capabilityExecutionContext{}) {
		return nil
	}
	return &state
}
