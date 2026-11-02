package memestore

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestQuotaCoordinatesIndependentHandlesAndCancellation(t *testing.T) {
	s := fixture(t)
	a, err := s.Gate("provider-key-hash", 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	other, err := New(s.db, s.schema)
	if err != nil {
		t.Fatal(err)
	}
	b, err := other.Gate("provider-key-hash", 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	release, err := a.Acquire(context.Background(), "ark")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err = b.Acquire(ctx, "ark"); !errors.Is(err, context.DeadlineExceeded) {
		release()
		t.Fatalf("in-flight permit not global: %v", err)
	}
	release()
	release()
	ctx2, cancel2 := context.WithTimeout(context.Background(), time.Second)
	defer cancel2()
	release2, err := b.Acquire(ctx2, "ark")
	if err != nil {
		t.Fatal(err)
	}
	release2()
}

func TestQuotaStartIntervalIsSeparateFromConcurrency(t *testing.T) {
	s := fixture(t)
	gate, err := s.Gate("provider", 60*time.Millisecond, 2)
	if err != nil {
		t.Fatal(err)
	}
	release, err := gate.Acquire(context.Background(), "ark")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	// Compare the database's admission times. A busy test runner may resume the
	// first Acquire after its interval has already elapsed; timing only the
	// second call would then report a false bypass.
	var firstNext time.Time
	if err := s.db.QueryRow(`SELECT next_start FROM ` + s.table("meme_remote_quota")).Scan(&firstNext); err != nil {
		t.Fatal(err)
	}
	release2, err := gate.Acquire(context.Background(), "ark")
	if err != nil {
		t.Fatal(err)
	}
	defer release2()
	var secondNext time.Time
	if err := s.db.QueryRow(`SELECT next_start FROM ` + s.table("meme_remote_quota")).Scan(&secondNext); err != nil {
		t.Fatal(err)
	}
	if spacing := secondNext.Sub(firstNext); spacing < 60*time.Millisecond {
		t.Fatalf("second admission bypassed shared interval: %v", spacing)
	}
}
