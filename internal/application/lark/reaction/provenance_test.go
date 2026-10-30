package reaction

import (
	"context"
	"errors"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/larkeventstore"
	otelinfra "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func TestPrepareReactionResolvesOnceUnderOriginalTrace(t *testing.T) {
	p := sdktrace.NewTracerProvider()
	defer p.Shutdown(context.Background())
	ctx, span := p.Tracer("test").Start(context.Background(), "ingress")
	defer span.End()
	mid := "om_missing"
	event := &larkim.P2MessageReactionCreatedV1{Event: &larkim.P2MessageReactionCreatedV1Data{MessageId: &mid}}
	meta := &xhandler.BaseMetaData{}
	calls := 0
	prepareReaction(ctx, event, meta, larkeventstore.NewRecorder(nil), larkeventstore.Scope{}, func(got context.Context, _ string) (string, error) {
		calls++
		if trace.SpanContextFromContext(got).TraceID() != trace.SpanContextFromContext(ctx).TraceID() {
			t.Fatal("trace changed")
		}
		return "", errors.New("230050")
	}, func(context.Context, string) string { return "" })
	if calls != 1 || meta.ChatID != "" {
		t.Fatalf("unexpected resolution calls=%d meta=%#v", calls, meta)
	}
	if err := (&RecordReactionOperator{}).Run(ctx, event, meta); err != nil {
		t.Fatalf("unresolved event should skip statistics: %v", err)
	}
	if err := (&FollowReactionOperator{}).Run(ctx, event, meta); err != nil {
		t.Fatalf("unresolved event should skip follow: %v", err)
	}
}

func TestFailedStatisticsSpanKeepsActorAndActionTime(t *testing.T) {
	spans := tracetest.NewSpanRecorder()
	p := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans))
	defer p.Shutdown(context.Background())
	prev := otelinfra.OtelTracer
	otelinfra.OtelTracer = p.Tracer("reaction-test")
	defer func() { otelinfra.OtelTracer = prev }()
	ctx, parent := otelinfra.StartNamed(context.Background(), "ingress")
	mid, actor, action, op := "om_missing", "ou_operator", "1793346959000", "user"
	event := &larkim.P2MessageReactionCreatedV1{Event: &larkim.P2MessageReactionCreatedV1Data{MessageId: &mid, ActionTime: &action, OperatorType: &op, UserId: &larkim.UserId{OpenId: &actor}}}
	meta := &xhandler.BaseMetaData{}
	prepareReaction(ctx, event, meta, larkeventstore.NewRecorder(nil), larkeventstore.Scope{}, func(context.Context, string) (string, error) { return "", errors.New("code:230050") }, nil)
	if err := (&RecordReactionOperator{}).Run(ctx, event, meta); err != nil {
		t.Fatal(err)
	}
	parent.End()
	found := false
	for _, s := range spans.Ended() {
		if s.Name() != "reaction.RecordReactionOperator.Run" {
			continue
		}
		found = true
		attrs := map[string]string{}
		for _, a := range s.Attributes() {
			attrs[string(a.Key)] = a.Value.AsString()
		}
		if attrs["actor.open_id"] != actor || attrs["event.action_time"] != action || attrs["message.id"] != mid {
			t.Fatalf("statistics span lost provenance: %#v", attrs)
		}
		if s.SpanContext().TraceID() != parent.SpanContext().TraceID() {
			t.Fatal("statistics span detached from ingress")
		}
	}
	if !found {
		t.Fatal("statistics stage span missing")
	}
}
