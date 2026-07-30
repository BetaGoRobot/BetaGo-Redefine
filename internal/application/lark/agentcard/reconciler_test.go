package agentcard

import (
	"context"
	"testing"
	"time"
)

type patchCatalogStub struct {
	targets []PatchTarget
}

func (s patchCatalogStub) ListDuePatches(
	context.Context,
	time.Time,
	int,
) ([]PatchTarget, error) {
	return append([]PatchTarget(nil), s.targets...), nil
}

type patchProcessorStub struct {
	got []PatchTarget
}

func (s *patchProcessorStub) Process(
	_ context.Context,
	surfaceID string,
	revision int64,
) error {
	s.got = append(s.got, PatchTarget{SurfaceID: surfaceID, Revision: revision})
	return nil
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
