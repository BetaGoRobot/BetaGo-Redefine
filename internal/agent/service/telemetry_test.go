package service_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/pgstore"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/service"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func observeRuntime(t *testing.T) (*tracetest.SpanRecorder, *bytes.Buffer) {
	t.Helper()
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previousProvider, previousLogger := otel.GetTracerProvider(), slog.Default()
	var logs bytes.Buffer
	otel.SetTracerProvider(provider)
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() {
		otel.SetTracerProvider(previousProvider)
		slog.SetDefault(previousLogger)
		_ = provider.Shutdown(context.Background())
	})
	return recorder, &logs
}

func TestPermanentRuntimeRejectionKeepsFailureWhenJobCompletionReturnsNil(t *testing.T) {
	f := setup(t)
	recorder, logs := observeRuntime(t)
	tid, _ := trace.TraceIDFromHex("12345678901234567890123456789012")
	sid, _ := trace.SpanIDFromHex("1234567890123456")
	parent := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{TraceID: tid, SpanID: sid, TraceFlags: trace.FlagsSampled}))
	id, err := f.store.Enqueue(parent, domain.JobSpec{Scope: f.scope, Kind: domain.JobAgentTurn, DedupeKey: "invalid-turn", Payload: json.RawMessage(`{"task_id":"diagnostic-task","revision":0}`)})
	if err != nil {
		t.Fatal(err)
	}
	if err = f.svc.Step(context.Background(), domain.JobAgentTurn); err != nil {
		t.Fatal(err)
	}
	job, err := f.store.Job(context.Background(), id)
	if err != nil || job.Status != domain.JobCompleted {
		t.Fatalf("permanent rejection acknowledgement changed: %+v %v", job, err)
	}
	var found sdktrace.ReadOnlySpan
	for _, span := range recorder.Ended() {
		if span.Name() == "agent.job.agent_turn" {
			found = span
		}
	}
	if found == nil || found.Status().Code != codes.Error || found.SpanContext().TraceID() != tid || found.Parent().SpanID() != sid {
		t.Fatalf("permanent rejection lost failure or original trace: %v", found)
	}
	for _, evidence := range []string{`"agent.job.rejected"`, `"error_stage":"job.payload.validate"`, `"error_code":"turn_payload_invalid"`, `"task_id":"diagnostic-task"`, `"job_id":`, `"attempt":1`, `"trace_id":"` + tid.String() + `"`} {
		if !strings.Contains(logs.String(), evidence) {
			t.Fatalf("runtime rejection missing %s: %s", evidence, logs.String())
		}
	}
}

func TestRouteObservationRecordsQuietDecisionWithoutMessageText(t *testing.T) {
	f := setup(t)
	_, logs := observeRuntime(t)
	f.event(t, "quiet-message", "member", "private-human-chat", "", domain.IngressMetadata{Kind: "message", SenderType: "user"})
	if err := f.svc.Step(context.Background(), domain.JobRoute); err != nil {
		t.Fatal(err)
	}
	for _, evidence := range []string{`"agent.route.decision"`, `"decision":"no_eligibility"`, `"addressed":false`, `"message_id":"quiet-message"`} {
		if !strings.Contains(logs.String(), evidence) {
			t.Fatalf("route outcome missing %s: %s", evidence, logs.String())
		}
	}
	if strings.Contains(logs.String(), "private-human-chat") {
		t.Fatal("route decision leaked message text")
	}
}

func TestJobAcknowledgementFailureReportsPersistenceStageAndOriginalRejection(t *testing.T) {
	f := setup(t)
	recorder, logs := observeRuntime(t)
	if _, err := f.db.Exec(`ALTER TABLE "` + f.schema + `".jobs ADD CONSTRAINT reject_completion CHECK (status <> 'completed')`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Enqueue(context.Background(), domain.JobSpec{Scope: f.scope, Kind: domain.JobAgentTurn, DedupeKey: "invalid-persistence", Payload: json.RawMessage(`{"task_id":"diagnostic-task","revision":0}`)}); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Step(context.Background(), domain.JobAgentTurn); err == nil {
		t.Fatal("expected failed queue acknowledgement")
	}
	for _, span := range recorder.Ended() {
		if span.Name() != "agent.job.agent_turn" {
			continue
		}
		for _, attr := range span.Attributes() {
			if string(attr.Key) == "error.stage" && attr.Value.AsString() == "job.complete" {
				if !strings.Contains(logs.String(), `"error_stage":"job.payload.validate"`) {
					t.Fatal("persistence failure hid original permanent rejection")
				}
				return
			}
		}
		t.Fatalf("queue failure replaced by original rejection: %v", span.Attributes())
	}
	t.Fatal("queue acknowledgement failure had no business span")
}

func TestDurableIngressRouteTurnToolAndDeliveryShareOriginalTrace(t *testing.T) {
	f := setup(t)
	recorder, logs := observeRuntime(t)
	tid, _ := trace.TraceIDFromHex("abcdef1234567890abcdef1234567890")
	sid, _ := trace.SpanIDFromHex("abcdef1234567890")
	parent := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{TraceID: tid, SpanID: sid, TraceFlags: trace.FlagsSampled}))
	now := time.Now().UTC().Add(-time.Second)
	raw, _ := json.Marshal(domain.IngressMetadata{Kind: "message", SenderType: "user", Addressed: true})
	m := domain.Message{Source: domain.SourceRef{Scope: f.scope, MessageID: "trace-original", AuthorID: "member", Version: 1, OccurredAt: now}, EventID: "trace-event", Text: "private-request", Raw: raw, UpdatedAt: now}
	if _, err := f.store.Accept(parent, domain.Event{ID: m.EventID, Type: domain.EventMessage, Message: m, OccurredAt: now}); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []domain.JobKind{domain.JobRoute, domain.JobAgentTurn, domain.JobDelivery} {
		if err := f.svc.Step(context.Background(), kind); err != nil {
			t.Fatal(err)
		}
	}
	want := map[string]bool{"agent.job.route": false, "agent.route": false, "agent.job.agent_turn": false, "agent.turn": false, "agent.turn.native": false, "agent.model.generate": false, "agent.tool.invoke": false, "agent.job.delivery": false, "agent.delivery.send": false}
	for _, span := range recorder.Ended() {
		if _, ok := want[span.Name()]; ok {
			want[span.Name()] = true
			if span.SpanContext().TraceID() != tid {
				t.Fatalf("durable boundary disconnected %s from original trace", span.Name())
			}
		}
	}
	for name, seen := range want {
		if !seen {
			t.Fatalf("business boundary %s was invisible", name)
		}
	}
	if !strings.Contains(logs.String(), `"provider_message_id":"out_`) || !strings.Contains(logs.String(), "private-request") || !strings.Contains(logs.String(), `"arguments":`) {
		t.Fatal("delivery receipt missing or request/argument content leaked")
	}
}

func TestQueueClaimFailureIsVisibleAndPreservesClosedDatabaseError(t *testing.T) {
	f := setup(t)
	closedDB, err := sql.Open("postgres", os.Getenv("AGENT_TEST_POSTGRES_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	if err = closedDB.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := pgstore.New(closedDB, f.schema)
	if err != nil {
		t.Fatal(err)
	}
	svc, err := service.New(service.Options{Store: store, Engine: f.eng, Messenger: f.messenger, Owner: "closed-db"})
	if err != nil {
		t.Fatal(err)
	}
	_, expected := store.ClaimJob(context.Background(), []domain.JobKind{domain.JobRoute}, "closed-db", time.Minute)
	if expected == nil {
		t.Fatal("closed database unexpectedly allowed a queue claim")
	}
	recorder, logs := observeRuntime(t)
	if err = svc.Step(context.Background(), domain.JobRoute); !errors.Is(err, expected) {
		t.Fatalf("queue claim lost original database error: %v", err)
	}
	for _, evidence := range []string{`"agent.queue.claim.failed"`, `"error_stage":"queue.claim"`, `"error_code":"database_claim_failed"`, `"claim_ms":`} {
		if !strings.Contains(logs.String(), evidence) {
			t.Fatalf("queue outage was invisible (%s): %s", evidence, logs.String())
		}
	}
	for _, span := range recorder.Ended() {
		if span.Name() == "agent.queue.claim" && span.Status().Code == codes.Error {
			return
		}
	}
	t.Fatal("failed queue claim had no error span")
}

func TestQueueClaimIdleAndShutdownStayQuiet(t *testing.T) {
	f := setup(t)
	recorder, logs := observeRuntime(t)
	if err := f.svc.Step(context.Background(), domain.JobRoute); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("empty queue changed: %v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := f.svc.Step(canceled, domain.JobRoute); !errors.Is(err, context.Canceled) {
		t.Fatalf("shutdown cancellation changed: %v", err)
	}
	deadline, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer stop()
	if err := f.svc.Step(deadline, domain.JobRoute); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown deadline changed: %v", err)
	}
	if logs.Len() != 0 || len(recorder.Ended()) != 0 {
		t.Fatalf("idle/shutdown polling created noise: %s", logs.String())
	}
}

func TestQueueClaimSQLStateIsVisibleWithoutRawDatabaseError(t *testing.T) {
	f := setup(t)
	if _, err := f.db.Exec(`ALTER TABLE "` + f.schema + `".jobs RENAME TO fixture_unavailable_jobs`); err != nil {
		t.Fatal(err)
	}
	_, logs := observeRuntime(t)
	if err := f.svc.Step(context.Background(), domain.JobRoute); err == nil {
		t.Fatal("missing queue table unexpectedly allowed a claim")
	}
	if !strings.Contains(logs.String(), `"db_sqlstate":"42P01"`) || !strings.Contains(logs.String(), `"error_code":"database_claim_failed"`) || strings.Contains(logs.String(), f.schema) {
		t.Fatalf("SQLSTATE missing or raw database detail leaked: %s", logs.String())
	}
}

type failingDeliveryMessenger struct{ err error }

func (m failingDeliveryMessenger) Send(context.Context, domain.Scope, string, string, string, string) (string, error) {
	return "", m.err
}

func TestDeliveryFailurePreservesCauseAndPermanentRejection(t *testing.T) {
	for _, cause := range []error{errors.New("private-provider-sentinel"), context.Canceled, domain.ErrUnauthorized} {
		t.Run(map[bool]string{true: "unauthorized", false: "retryable"}[errors.Is(cause, domain.ErrUnauthorized)]+"_"+map[bool]string{true: "canceled", false: "provider"}[errors.Is(cause, context.Canceled)], func(t *testing.T) {
			f := setup(t)
			f.event(t, "delivery-failure", "owner", "private-human-request", "", domain.IngressMetadata{Kind: "message", SenderType: "user", Addressed: true})
			step(t, f, domain.JobRoute)
			step(t, f, domain.JobAgentTurn)
			var deliveryJobID int64
			if err := f.db.QueryRow(`SELECT id FROM "` + f.schema + `".jobs WHERE kind='delivery'`).Scan(&deliveryJobID); err != nil {
				t.Fatal(err)
			}
			svc, err := service.New(service.Options{Store: f.store, Engine: f.eng, Messenger: failingDeliveryMessenger{cause}, Owner: "failed-send"})
			if err != nil {
				t.Fatal(err)
			}
			recorder, logs := observeRuntime(t)
			err = svc.Step(context.Background(), domain.JobDelivery)
			permanent := errors.Is(cause, domain.ErrUnauthorized)
			if permanent && err != nil || !permanent && !errors.Is(err, cause) {
				t.Fatalf("delivery error lost original cause or rejection semantics: %v", err)
			}
			job, readErr := f.store.Job(context.Background(), deliveryJobID)
			if readErr != nil || permanent && job.Status != domain.JobCompleted || !permanent && job.Status != domain.JobPending {
				t.Fatalf("delivery failure was misclassified: status=%s err=%v", job.Status, readErr)
			}
			if permanent && (!strings.Contains(logs.String(), `"agent.job.rejected"`) || !strings.Contains(logs.String(), `"reason":"unauthorized"`)) {
				t.Fatal("permanent transport rejection was hidden")
			}
			for _, span := range recorder.Ended() {
				if span.Name() == "agent.delivery" && span.Status().Code == codes.Error {
					if strings.Contains(logs.String(), "private-provider-sentinel") {
						t.Fatal("raw provider error leaked")
					}
					return
				}
			}
			t.Fatal("delivery failure was invisible")
		})
	}
}

func TestMediaDeferralPersistenceFailureRemainsVisible(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	now := time.Now().UTC().Add(-time.Second)
	raw, _ := json.Marshal(domain.IngressMetadata{Kind: "message", SenderType: "user", Addressed: true})
	m := domain.Message{Source: domain.SourceRef{Scope: f.scope, MessageID: "defer-failure", AuthorID: "owner", Version: 1, OccurredAt: now}, EventID: "defer-failure", Raw: raw, UpdatedAt: now, Media: []domain.Media{{Type: domain.MediaImage, Source: domain.MediaSourceLark, ResourceID: "fixture-image", Sequence: 0}}}
	if _, err := f.store.Accept(ctx, domain.Event{ID: m.EventID, Type: domain.EventMessage, Message: m, OccurredAt: now}); err != nil {
		t.Fatal(err)
	}
	step(t, f, domain.JobRoute)
	if _, err := f.db.Exec(`ALTER TABLE "` + f.schema + `".jobs ADD CONSTRAINT reject_deferral CHECK (kind <> 'agent_turn' OR status <> 'pending') NOT VALID`); err != nil {
		t.Fatal(err)
	}
	recorder, logs := observeRuntime(t)
	if err := f.svc.Step(ctx, domain.JobAgentTurn); err == nil {
		t.Fatal("failed durable deferral was swallowed")
	}
	if !strings.Contains(logs.String(), `"error_stage":"job.defer"`) || !strings.Contains(logs.String(), `"db_sqlstate":"23514"`) || !strings.Contains(logs.String(), `"level":"ERROR"`) {
		t.Fatalf("quiet polling hid failed durable deferral: %s", logs.String())
	}
	for _, span := range recorder.Ended() {
		if span.Name() == "agent.job.agent_turn" && span.Status().Code == codes.Error {
			return
		}
	}
	t.Fatal("failed durable deferral had no error span")
}

func TestPendingDeliveryPollsRemainQuietAndKeepDeferredTrace(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	m := f.event(t, "pending-delivery", "owner", "request", "", domain.IngressMetadata{Kind: "message", SenderType: "user", Addressed: true})
	step(t, f, domain.JobRoute)
	task, _, err := f.store.TaskForMessage(ctx, f.scope, m.Source.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	j, err := f.store.ClaimJob(ctx, []domain.JobKind{domain.JobAgentTurn}, "prepare-batch", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	plan := domain.ConversationPlan{Choice: "expand", Summary: "summary", Messages: []domain.ConversationMessage{{Text: "section one", ReplyToMessageID: m.Source.MessageID}, {Text: "section two", ReplyToMessageID: m.Source.MessageID}}}
	if err = f.store.QueueConversationPlan(ctx, j, task, plan); err != nil {
		t.Fatal(err)
	}
	var first, next int64
	if err = f.db.QueryRow(`SELECT min(id),max(id) FROM "`+f.schema+`".jobs WHERE kind='delivery'`).Scan(&first, &next); err != nil || first == next {
		t.Fatalf("expected ordered delivery batch: %v", err)
	}
	if _, err = f.db.Exec(`UPDATE "`+f.schema+`".jobs SET available_at=clock_timestamp()+interval '1 hour' WHERE id=$1`, first); err != nil {
		t.Fatal(err)
	}
	recorder, logs := observeRuntime(t)
	for i := 0; i < 3; i++ {
		if _, err = f.db.Exec(`UPDATE "`+f.schema+`".jobs SET available_at=clock_timestamp() WHERE id=$1`, next); err != nil {
			t.Fatal(err)
		}
		if err = f.svc.Step(ctx, domain.JobDelivery); err != nil {
			t.Fatal(err)
		}
	}
	if logs.Len() != 0 {
		t.Fatalf("ordered delivery wait produced INFO/ERROR noise: %s", logs.String())
	}
	deferred := 0
	for _, span := range recorder.Ended() {
		if span.Status().Code == codes.Error {
			t.Fatalf("expected delivery wait marked %s as failed", span.Name())
		}
		for _, attr := range span.Attributes() {
			if string(attr.Key) == "defer.reason" && attr.Value.AsString() == "delivery_pending" {
				deferred++
			}
		}
	}
	if deferred < 3 || len(f.messenger.sends) != 0 {
		t.Fatal("quiet waiting lost trace evidence or sent before its predecessor")
	}
}
