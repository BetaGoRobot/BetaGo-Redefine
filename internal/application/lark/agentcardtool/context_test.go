package agentcardtool

import (
	"context"
	"testing"
)

type contextServiceStub struct{}

func (contextServiceStub) DiscoverComponents(
	context.Context,
	DiscoverRequest,
) (DiscoverResponse, error) {
	return DiscoverResponse{}, nil
}

func (contextServiceStub) ComposeCard(
	context.Context,
	ComposeContext,
	ComposeRequest,
) (ComposeResponse, error) {
	return ComposeResponse{}, nil
}

func TestServiceContextIsScopedAndRejectsNil(t *testing.T) {
	service := contextServiceStub{}
	ctx := WithService(context.Background(), service)
	if got, ok := ServiceFromContext(ctx); !ok || got != service {
		t.Fatalf("ServiceFromContext() = (%v, %v)", got, ok)
	}
	if _, ok := ServiceFromContext(context.Background()); ok {
		t.Fatal("empty context unexpectedly contains a service")
	}
	if got := WithService(ctx, nil); got != ctx {
		t.Fatal("nil service should leave the context unchanged")
	}
}
