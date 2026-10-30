package telemetry

import (
	"errors"
	"net/http"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
)

type transport struct {
	component string
	next      http.RoundTripper
}

// Transport traces status/timing and propagates W3C context, never headers,
// payloads, full URLs or provider error bodies.
func Transport(component string, next http.RoundTripper) http.RoundTripper {
	if next == nil {
		next = http.DefaultTransport
	}
	return transport{component, next}
}
func (t transport) RoundTrip(request *http.Request) (response *http.Response, result error) {
	base := request.Context()
	if t.component == "meili" {
		base = Quiet(base)
	}
	ctx, span := Start(base, t.component+".http", attribute.String("http.method", request.Method), attribute.String("server.address", request.URL.Hostname()))
	defer func() {
		observed := result
		if response != nil {
			span.SetAttributes(attribute.Int("http.response.status_code", response.StatusCode))
			Info(ctx, t.component+".http.response", "http_status", response.StatusCode, "response_bytes", response.ContentLength)
			if response.StatusCode >= 400 {
				observed = Wrap("http.response", "upstream_rejected", errors.New("upstream rejected request"))
			}
		}
		End(ctx, span, observed)
	}()
	copy := request.Clone(ctx)
	if copy.Header == nil {
		copy.Header = make(http.Header)
	}
	propagation.TraceContext{}.Inject(ctx, propagation.HeaderCarrier(copy.Header))
	return t.next.RoundTrip(copy)
}
