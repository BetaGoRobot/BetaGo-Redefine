package llmusage

import (
	"context"
	"sync"
	"testing"
)

func TestCollectorObservesConcurrentRequestUsage(t *testing.T) {
	collector := NewCollector()
	ctx := WithObserver(context.Background(), collector)

	const count = 64
	var group sync.WaitGroup
	for index := 0; index < count; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			observerFromContext(ctx).ObserveUsage(ctx, Record{
				PromptTokens:     2,
				CompletionTokens: 3,
				TotalTokens:      5,
			})
		}()
	}
	group.Wait()

	records := collector.Records()
	if len(records) != count {
		t.Fatalf("Records() count = %d, want %d", len(records), count)
	}
	totals := collector.Totals()
	if totals.Records != count ||
		totals.PromptTokens != count*2 ||
		totals.CompletionTokens != count*3 ||
		totals.TotalTokens != count*5 {
		t.Fatalf("Totals() = %+v", totals)
	}

	records[0].PromptTokens = 999
	if got := collector.Records()[0].PromptTokens; got != 2 {
		t.Fatalf("Records() leaked mutable state: prompt tokens = %d", got)
	}
}

func TestRecordUsageNotifiesRequestObserverWithoutReplacingDefaultRecorder(t *testing.T) {
	store := &fakeStore{}
	previous := DefaultRecorder()
	SetDefaultRecorder(NewRecorderWithStore(store))
	t.Cleanup(func() { SetDefaultRecorder(previous) })

	collector := NewCollector()
	ctx := WithObserver(context.Background(), collector)
	record := Record{PromptTokens: 11, CompletionTokens: 7, TotalTokens: 18}

	if err := RecordUsage(ctx, record); err != nil {
		t.Fatalf("RecordUsage() error = %v", err)
	}
	if len(store.rows) != 1 {
		t.Fatalf("default recorder rows = %d, want 1", len(store.rows))
	}
	if got := collector.Totals(); got.PromptTokens != 11 || got.TotalTokens != 18 || got.Records != 1 {
		t.Fatalf("collector totals = %+v", got)
	}
}

func TestWithObserverIsNilSafeNoOp(t *testing.T) {
	ctx := WithObserver(nil, nil)
	observerFromContext(ctx).ObserveUsage(ctx, Record{TotalTokens: 3})
}
