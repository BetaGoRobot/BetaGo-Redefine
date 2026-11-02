// verify-observability emits a synthetic trace/log pair without model calls or
// messages to users. Set OTEL_EXPORTER_OTLP_ENDPOINT to the target collector.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/telemetry"
	"go.opentelemetry.io/otel/attribute"
)

func main() {
	ctx := context.Background()
	stop, err := telemetry.Initialize(ctx, "agent")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	ctx = telemetry.Context(ctx, "synthetic", true, "message_id", "observability-probe", "model_name", "synthetic-model")
	ctx, span := telemetry.Start(ctx, "agent.synthetic_verification", attribute.Bool("synthetic", true))
	id := span.SpanContext().TraceID().String()
	telemetry.Payload(ctx, "agent.model.request.content", "model_request", map[string]any{"synthetic": true, "input": "北京时间与明文业务日志关联验证", "api_key": "probe-secret-canary"})
	telemetry.Warn(ctx, "agent.synthetic_warning", "synthetic", true)
	telemetry.Info(ctx, "agent.turn.decision", "choice", "observe", "synthetic", true)
	telemetry.End(ctx, span, nil)
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := stop(shutdown); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("verified_trace_id=" + id)
}
