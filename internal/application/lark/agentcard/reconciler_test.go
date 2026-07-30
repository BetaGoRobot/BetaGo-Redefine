package agentcard

import (
	"context"
	"errors"
	"testing"
	"time"
)

type patchCatalogStub struct {
	targets []PatchTarget
	err     error
}

func (s patchCatalogStub) ListDuePatches(
	context.Context,
	time.Time,
	int,
) ([]PatchTarget, error) {
	return append([]PatchTarget(nil), s.targets...), s.err
}

type patchProcessorStub struct {
	got []PatchTarget
	err error
}

func (s *patchProcessorStub) Process(
	_ context.Context,
	surfaceID string,
	revision int64,
) error {
	s.got = append(s.got, PatchTarget{SurfaceID: surfaceID, Revision: revision})
	return s.err
}

func TestPatchReconcilerStatsAndHealthRecoverAfterSuccessfulSweep(t *testing.T) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	catalog := &patchCatalogStub{err: errors.New("postgres unavailable")}
	processor := &patchProcessorStub{}
	reconciler, err := NewPatchReconciler(PatchReconcilerOptions{
		Catalog: catalog, Processors: []PatchProcessor{processor},
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.ReconcileOnce(context.Background(), 0); err == nil {
		t.Fatal("catalog failure was hidden")
	}
	stats := reconciler.Stats()
	if stats["failed"] != uint64(1) ||
		stats["last_error"] != "postgres unavailable" {
		t.Fatalf("failure stats = %#v", stats)
	}
	if healthy, _ := reconciler.Health(); healthy {
		t.Fatal("failed reconciler reported healthy")
	}

	catalog.err = nil
	catalog.targets = []PatchTarget{{SurfaceID: "surface-1", Revision: 1}}
	if _, err := reconciler.ReconcileOnce(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	stats = reconciler.Stats()
	if stats["scanned"] != uint64(1) ||
		stats["completed"] != uint64(1) ||
		stats["last_error"] != "" ||
		stats["last_success_at"] != now {
		t.Fatalf("recovered stats = %#v", stats)
	}
	if healthy, _ := reconciler.Health(); !healthy {
		t.Fatal("successful sweep did not recover health")
	}
}

func TestPatchReconcilerProcessesDueTargets(t *testing.T) {
	processor := &patchProcessorStub{}
	reconciler, err := NewPatchReconciler(PatchReconcilerOptions{
		Catalog: patchCatalogStub{targets: []PatchTarget{
			{SurfaceID: "surface-1", Revision: 2},
			{SurfaceID: "surface-2", Revision: 3},
		}},
		Processors: []PatchProcessor{processor},
		BatchSize:  16,
		Interval:   time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	count, err := reconciler.ReconcileOnce(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 || len(processor.got) != 2 ||
		processor.got[1].SurfaceID != "surface-2" {
		t.Fatalf("processed = %d %#v", count, processor.got)
	}
}
