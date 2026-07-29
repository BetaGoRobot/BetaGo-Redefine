package llmusage

import (
	"context"
	"sync"
)

// Observer receives only the usage records emitted through the request context
// carrying it. It intentionally does not replace or mutate the process recorder.
type Observer interface {
	ObserveUsage(context.Context, Record)
}

type Totals struct {
	PromptTokens     int64
	CompletionTokens int64
	TotalTokens      int64
	Records          int
}

type Collector struct {
	mu      sync.RWMutex
	records []Record
}

func NewCollector() *Collector {
	return &Collector{}
}

func (c *Collector) ObserveUsage(_ context.Context, record Record) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.records = append(c.records, record)
}

func (c *Collector) Records() []Record {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]Record(nil), c.records...)
}

func (c *Collector) Totals() Totals {
	var totals Totals
	for _, record := range c.Records() {
		totals.PromptTokens += record.PromptTokens
		totals.CompletionTokens += record.CompletionTokens
		totals.TotalTokens += record.TotalTokens
		totals.Records++
	}
	return totals
}

type observerContextKey struct{}

func WithObserver(ctx context.Context, observer Observer) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if observer == nil {
		observer = noopObserver{}
	}
	return context.WithValue(ctx, observerContextKey{}, observer)
}

func observerFromContext(ctx context.Context) Observer {
	if ctx != nil {
		if observer, ok := ctx.Value(observerContextKey{}).(Observer); ok && observer != nil {
			return observer
		}
	}
	return noopObserver{}
}

type noopObserver struct{}

func (noopObserver) ObserveUsage(context.Context, Record) {}
