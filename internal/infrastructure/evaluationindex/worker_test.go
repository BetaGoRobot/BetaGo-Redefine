package evaluationindex

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestProjectionProcessorAdvancesOnlyAfterSuccessfulUpsert(t *testing.T) {
	first := evaluationSnapshotFixture()
	first.EpisodeID = "episode-1"
	first.UpdatedAt = first.UpdatedAt.Add(time.Second)
	second := evaluationSnapshotFixture()
	second.EpisodeID = "episode-2"
	second.UpdatedAt = first.UpdatedAt.Add(time.Second)
	source := &projectionSourceFake{pages: [][]EvaluationSnapshot{{first, second}}}
	owner, index := evaluationIndexTestTenant(t)
	failID, err := owner.DocumentID(second.EpisodeID)
	if err != nil {
		t.Fatal(err)
	}
	backend := &projectionBackendFake{failID: failID}
	store, err := NewStoreWithBackend(owner, index, backend)
	if err != nil {
		t.Fatalf("NewStoreWithBackend() error = %v", err)
	}
	processor, err := NewProjectionProcessor(source, store, 10)
	if err != nil {
		t.Fatalf("NewProjectionProcessor() error = %v", err)
	}
	if err := processor.ProcessNext(context.Background()); err == nil {
		t.Fatal("ProcessNext() error = nil")
	}
	if processor.Cursor().EpisodeID != first.EpisodeID {
		t.Fatalf("cursor = %#v, want first successful snapshot", processor.Cursor())
	}
	backend.failID = ""
	source.pages = [][]EvaluationSnapshot{{second}}
	if err := processor.ProcessNext(context.Background()); err != nil {
		t.Fatalf("ProcessNext(retry) error = %v", err)
	}
	if processor.Cursor().EpisodeID != second.EpisodeID {
		t.Fatalf("retry cursor = %#v", processor.Cursor())
	}
}

func TestProjectionWorkerStopsIdleLoop(t *testing.T) {
	source := &projectionSourceFake{}
	owner, index := evaluationIndexTestTenant(t)
	store, err := NewStoreWithBackend(owner, index, &projectionBackendFake{})
	if err != nil {
		t.Fatalf("NewStoreWithBackend() error = %v", err)
	}
	processor, err := NewProjectionProcessor(source, store, 10)
	if err != nil {
		t.Fatalf("NewProjectionProcessor() error = %v", err)
	}
	worker, err := NewProjectionWorker(processor, ProjectionWorkerOptions{
		Interval: time.Millisecond, MaxBackoff: 2 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewProjectionWorker() error = %v", err)
	}
	if err := worker.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for source.calls() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := worker.Stop(stopCtx); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if source.calls() == 0 {
		t.Fatal("worker did not poll projection source")
	}
}

type projectionSourceFake struct {
	mu        sync.Mutex
	pages     [][]EvaluationSnapshot
	err       error
	callCount int
}

func (f *projectionSourceFake) EvaluationSnapshotsAfter(
	_ context.Context,
	_ ProjectionCursor,
	_ int,
) ([]EvaluationSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.callCount++
	if f.err != nil {
		return nil, f.err
	}
	if len(f.pages) == 0 {
		return nil, nil
	}
	page := f.pages[0]
	f.pages = f.pages[1:]
	return page, nil
}

func (f *projectionSourceFake) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.callCount
}

type projectionBackendFake struct {
	mu     sync.Mutex
	failID string
}

func (f *projectionBackendFake) Upsert(
	_ context.Context,
	_ string,
	id string,
	_ any,
) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id == f.failID {
		return errors.New("injected projection failure")
	}
	return nil
}

func (f *projectionBackendFake) Search(
	context.Context,
	string,
	map[string]any,
) ([]json.RawMessage, error) {
	return nil, nil
}
