package schedule

import (
	"testing"
	"time"
)

func TestLegacyPendingEditOldGenerationCannotExpireOverwrite(t *testing.T) {
	resetLegacyPendingEditState(t)
	const token = "same-token"
	first := &PendingEdit{TaskID: "task-old", CreatedAt: time.Now()}
	second := &PendingEdit{TaskID: "task-new", CreatedAt: time.Now()}

	if err := storeLegacyPendingEdit(token, first); err != nil {
		t.Fatalf("store first pending edit: %v", err)
	}
	oldGeneration := pendingEditGenerationForTest(token)
	if err := storeLegacyPendingEdit(token, second); err != nil {
		t.Fatalf("store overwritten pending edit: %v", err)
	}
	newGeneration := pendingEditGenerationForTest(token)
	if newGeneration <= oldGeneration {
		t.Fatalf("generation did not advance: old=%d new=%d", oldGeneration, newGeneration)
	}

	expireLegacyPendingEdit(token, oldGeneration)

	edit, ok := GetPendingEdit(token)
	if !ok || edit != second {
		t.Fatal("old expiry removed the overwritten pending edit")
	}
	assertLegacyPendingEditState(t, token, newGeneration)
}

func TestLegacyPendingEditOldGenerationCannotExpireDeleteAndRestore(t *testing.T) {
	resetLegacyPendingEditState(t)
	const token = "reused-token"

	if err := storeLegacyPendingEdit(token, &PendingEdit{TaskID: "task-old", CreatedAt: time.Now()}); err != nil {
		t.Fatalf("store first pending edit: %v", err)
	}
	oldGeneration := pendingEditGenerationForTest(token)
	DeletePendingEdit(token)
	assertLegacyPendingEditStateEmpty(t)

	restored := &PendingEdit{TaskID: "task-restored", CreatedAt: time.Now()}
	if err := storeLegacyPendingEdit(token, restored); err != nil {
		t.Fatalf("restore pending edit: %v", err)
	}
	newGeneration := pendingEditGenerationForTest(token)
	if newGeneration <= oldGeneration {
		t.Fatalf("generation did not advance after delete: old=%d new=%d", oldGeneration, newGeneration)
	}

	expireLegacyPendingEdit(token, oldGeneration)

	edit, ok := GetPendingEdit(token)
	if !ok || edit != restored {
		t.Fatal("pre-delete expiry removed the restored pending edit")
	}
	assertLegacyPendingEditState(t, token, newGeneration)
}

func TestLegacyPendingEditCurrentGenerationExpiryClearsAllState(t *testing.T) {
	resetLegacyPendingEditState(t)
	const token = "current-token"

	if err := storeLegacyPendingEdit(token, &PendingEdit{TaskID: "task-current", CreatedAt: time.Now()}); err != nil {
		t.Fatalf("store pending edit: %v", err)
	}
	generation := pendingEditGenerationForTest(token)

	expireLegacyPendingEdit(token, generation)

	assertLegacyPendingEditStateEmpty(t)
}

func pendingEditGenerationForTest(token string) uint64 {
	pendingEditsMu.RLock()
	defer pendingEditsMu.RUnlock()
	return pendingEditGenerations[token]
}

func assertLegacyPendingEditState(t *testing.T, token string, generation uint64) {
	t.Helper()
	pendingEditsMu.RLock()
	defer pendingEditsMu.RUnlock()
	if pendingEdits[token] == nil {
		t.Fatal("pending edit is missing")
	}
	if pendingEditTimers[token] == nil {
		t.Fatal("pending edit timer is missing")
	}
	if pendingEditGenerations[token] != generation {
		t.Fatalf("pending edit generation = %d, want %d", pendingEditGenerations[token], generation)
	}
}

func assertLegacyPendingEditStateEmpty(t *testing.T) {
	t.Helper()
	pendingEditsMu.RLock()
	defer pendingEditsMu.RUnlock()
	if len(pendingEdits) != 0 || len(pendingEditTimers) != 0 || len(pendingEditGenerations) != 0 {
		t.Fatalf(
			"pending edit state not empty: edits=%d timers=%d generations=%d",
			len(pendingEdits),
			len(pendingEditTimers),
			len(pendingEditGenerations),
		)
	}
}

func resetLegacyPendingEditState(t *testing.T) {
	t.Helper()
	cleanup := func() {
		pendingEditsMu.Lock()
		for _, timer := range pendingEditTimers {
			if timer != nil {
				timer.Stop()
			}
		}
		clear(pendingEdits)
		clear(pendingEditTimers)
		clear(pendingEditGenerations)
		pendingEditsMu.Unlock()
	}
	cleanup()
	t.Cleanup(cleanup)
}
