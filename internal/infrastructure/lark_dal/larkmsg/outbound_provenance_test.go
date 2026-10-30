package larkmsg

import (
	"context"
	"errors"
	"testing"
	"time"

	infraConfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/larkeventstore"
	"go.opentelemetry.io/otel/trace"
)

type receiptLocationStore struct {
	locations []larkeventstore.Location
	err       error
	t         *testing.T
	traceID   trace.TraceID
}

func (s *receiptLocationStore) SaveEvent(context.Context, larkeventstore.Event) error { return nil }
func (s *receiptLocationStore) UpdateResolution(context.Context, larkeventstore.Scope, string, larkeventstore.Resolution) error {
	return nil
}
func (s *receiptLocationStore) FindLocation(context.Context, larkeventstore.Scope, string) (larkeventstore.Location, bool, error) {
	return larkeventstore.Location{}, false, nil
}
func (s *receiptLocationStore) PutLocation(ctx context.Context, l larkeventstore.Location) error {
	if ctx.Err() != nil {
		s.t.Error("provider receipt inherited canceled request")
	}
	if trace.SpanContextFromContext(ctx).TraceID() != s.traceID {
		s.t.Error("provider receipt lost trace")
	}
	if _, ok := ctx.Deadline(); !ok {
		s.t.Error("location write has no bounded deadline")
	}
	if s.err != nil {
		return s.err
	}
	s.locations = append(s.locations, l)
	return nil
}

func TestOutboundLocationPrecedesOptionalIndexing(t *testing.T) {
	useLarkMsgConfigPath(t)
	infraConfig.Get().LarkConfig.AppID = "cli_receipt"
	tid := trace.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{TraceID: tid, SpanID: trace.SpanID{1, 2, 3, 4, 5, 6, 7, 8}}))
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	s := &receiptLocationStore{t: t, traceID: tid}
	prev := larkeventstore.Default()
	larkeventstore.SetDefault(larkeventstore.NewRecorder(s))
	defer larkeventstore.SetDefault(prev)
	done := make(chan struct{})
	recordOutboundReceipt(ctx, "om_receipt", "oc_receipt", func(context.Context) {
		if len(s.locations) != 1 || s.locations[0].ChatID != "oc_receipt" {
			t.Error("indexing started before location was captured")
		}
		close(done)
	})
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("optional indexing not scheduled")
	}
}

func TestOutboundJournalFailureDoesNotSuppressIndexing(t *testing.T) {
	useLarkMsgConfigPath(t)
	infraConfig.Get().LarkConfig.AppID = "cli_receipt"
	s := &receiptLocationStore{t: t, err: errors.New("database unavailable")}
	r := larkeventstore.NewRecorder(s)
	prev := larkeventstore.Default()
	larkeventstore.SetDefault(r)
	defer larkeventstore.SetDefault(prev)
	done := make(chan struct{})
	recordOutboundReceipt(context.Background(), "om_receipt", "oc_receipt", func(context.Context) { close(done) })
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("failed locator write suppressed indexing")
	}
}
