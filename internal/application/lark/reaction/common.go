package reaction

import "go.opentelemetry.io/otel/trace"

func recordSpanError(span trace.Span, err *error) {
	if err == nil || *err == nil {
		return
	}
	span.RecordError(*err)
}
