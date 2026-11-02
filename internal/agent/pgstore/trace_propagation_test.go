package pgstore_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"go.opentelemetry.io/otel/trace"
)

func jobTraceContext(t *testing.T, traceHex, spanHex string) context.Context {
	t.Helper()
	tid, err := trace.TraceIDFromHex(traceHex)
	if err != nil {
		t.Fatal(err)
	}
	sid, err := trace.SpanIDFromHex(spanHex)
	if err != nil {
		t.Fatal(err)
	}
	return trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{TraceID: tid, SpanID: sid, TraceFlags: trace.FlagsSampled}))
}

func TestJobTraceMetadataSurvivesQueueWithoutChangingDedupeOrBusinessIntegers(t *testing.T) {
	f := setup(t)
	first := jobTraceContext(t, "11111111111111111111111111111111", "1111111111111111")
	second := jobTraceContext(t, "22222222222222222222222222222222", "2222222222222222")
	spec := domain.JobSpec{Scope: testScope(), Kind: domain.JobAgentTurn, DedupeKey: "trace-dedupe", Payload: json.RawMessage(`{"task_id":"task","revision":9007199254740993}`)}
	id, err := f.Enqueue(first, spec)
	if err != nil {
		t.Fatal(err)
	}
	job, err := f.Job(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(job.Payload), `"_otel"`) || !strings.Contains(string(job.Payload), "11111111111111111111111111111111") || !strings.Contains(string(job.Payload), "9007199254740993") {
		t.Fatalf("trace or exact integer lost at durable boundary: %s", job.Payload)
	}
	if again, err := f.Enqueue(second, spec); err != nil || again != id {
		t.Fatalf("new trace changed immutable work identity: id=%d err=%v", again, err)
	}
	spec.Payload = json.RawMessage(`{"task_id":"task","revision":9007199254740994}`)
	if _, err = f.Enqueue(second, spec); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("business conflict hidden by trace metadata: %v", err)
	}
	stored, err := f.Job(context.Background(), id)
	if err != nil || !strings.Contains(string(stored.Payload), "11111111111111111111111111111111") {
		t.Fatal("duplicate replaced original trace parent")
	}
}

func TestTracePropagationLeavesEventMessageAuditAndDigestUnchanged(t *testing.T) {
	f := setup(t)
	e := event("traced-event", 1)
	ctx := jobTraceContext(t, "33333333333333333333333333333333", "3333333333333333")
	accepted, err := f.Accept(ctx, e)
	if err != nil {
		t.Fatal(err)
	}
	if len(accepted.JobIDs) == 0 {
		t.Fatal("expected durable work")
	}
	for _, id := range accepted.JobIDs {
		job, err := f.Job(context.Background(), id)
		if err != nil || !strings.Contains(string(job.Payload), "33333333333333333333333333333333") {
			t.Fatalf("enqueue did not inherit ingress trace: job=%d err=%v", id, err)
		}
	}
	m, err := f.Message(context.Background(), e.Message.Source.Scope, e.Message.Source.MessageID)
	if err != nil || string(m.Raw) != string(e.Message.Raw) {
		t.Fatal("trace polluted message audit")
	}
	duplicate, err := f.Accept(context.Background(), e)
	if err != nil || !duplicate.Duplicate {
		t.Fatalf("trace polluted event idempotence: %+v %v", duplicate, err)
	}
}
