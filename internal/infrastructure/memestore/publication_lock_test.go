package memestore

import (
	"context"
	"errors"
	"testing"
	"time"
)

type publicationLocker interface {
	WithPublicationLock(context.Context, string, func(context.Context) error) error
}

func TestPublicationLockRejectsInvalidHandleWithoutDatabaseAccess(t *testing.T) {
	var scoped *Scoped
	locker, ok := any(scoped).(publicationLocker)
	if !ok {
		t.Fatal("scoped store has no publication lock")
	}
	if err := locker.WithPublicationLock(context.Background(), "generation", func(context.Context) error { return nil }); !errors.Is(err, ErrInvalid) {
		t.Fatalf("nil store accepted: %v", err)
	}
}

func TestPublicationLockSerializesIndependentHandlesAndReleasesAfterCancellation(t *testing.T) {
	store := fixture(t)
	first, ok := any(store.ForScope("wiki")).(publicationLocker)
	if !ok {
		t.Fatal("scoped store has no publication lock")
	}
	independent, err := New(store.db, store.schema)
	if err != nil {
		t.Fatal(err)
	}
	second := any(independent.ForScope("wiki")).(publicationLocker)
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- first.WithPublicationLock(context.Background(), "generation", func(context.Context) error { close(entered); <-release; return nil })
	}()
	<-entered
	wait, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	called := false
	err = second.WithPublicationLock(wait, "generation", func(context.Context) error { called = true; return nil })
	if !errors.Is(err, context.DeadlineExceeded) || called {
		close(release)
		<-done
		t.Fatalf("concurrent publisher entered same generation: called=%t err=%v", called, err)
	}
	other := any(independent.ForScope("other")).(publicationLocker)
	if err = other.WithPublicationLock(context.Background(), "generation", func(context.Context) error { return nil }); err != nil {
		close(release)
		<-done
		t.Fatalf("lock unexpectedly crossed scopes: %v", err)
	}
	close(release)
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	expected := errors.New("callback stopped")
	if err = second.WithPublicationLock(context.Background(), "generation", func(context.Context) error { return expected }); !errors.Is(err, expected) {
		t.Fatalf("callback failure lost: %v", err)
	}
	cancelled, cancelCall := context.WithCancel(context.Background())
	if err = second.WithPublicationLock(cancelled, "generation", func(context.Context) error { cancelCall(); return cancelled.Err() }); !errors.Is(err, context.Canceled) {
		t.Fatalf("callback cancellation lost: %v", err)
	}
	retry, cancelRetry := context.WithTimeout(context.Background(), time.Second)
	defer cancelRetry()
	if err = first.WithPublicationLock(retry, "generation", func(context.Context) error { return nil }); err != nil {
		t.Fatalf("publication lock leaked after callback failure/cancellation: %v", err)
	}
}
