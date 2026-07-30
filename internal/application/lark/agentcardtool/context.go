package agentcardtool

import (
	"context"
	"reflect"
)

type serviceContextKey struct{}

func WithService(ctx context.Context, service Service) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if isNilService(service) {
		return ctx
	}
	return context.WithValue(ctx, serviceContextKey{}, service)
}

func ServiceFromContext(ctx context.Context) (Service, bool) {
	if ctx == nil {
		return nil, false
	}
	service, ok := ctx.Value(serviceContextKey{}).(Service)
	if !ok || isNilService(service) {
		return nil, false
	}
	return service, true
}

func isNilService(service Service) bool {
	if service == nil {
		return true
	}
	value := reflect.ValueOf(service)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map,
		reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
