package mememodel

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/llmusage"
	arkmodel "github.com/volcengine/volcengine-go-sdk/service/arkruntime/model"
)

type embeddingGate struct {
	acquired atomic.Int32
	released atomic.Int32
	badRoute atomic.Bool
}

func (g *embeddingGate) Acquire(_ context.Context, route string) (func(), error) {
	if route != "model" {
		g.badRoute.Store(true)
	}
	g.acquired.Add(1)
	return func() { g.released.Add(1) }, nil
}

func TestEmbeddingConcurrencyOverlapsIndependentCallsAndPreservesOrder(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	gate := &embeddingGate{}
	firstStarted, secondFinished, releaseFirst := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var active, peak atomic.Int32
	e := &Embedder{Concurrency: 2, Gate: gate, Call: func(ctx context.Context, text string, scope llmusage.Scope) ([]float32, arkmodel.Usage, error) {
		n := active.Add(1)
		defer active.Add(-1)
		for p := peak.Load(); n > p && !peak.CompareAndSwap(p, n); p = peak.Load() {
		}
		if scope.Source != "meme_reply_embedding" || scope.BusinessOperation != llmusage.OperationRetrieverEmbedding {
			return nil, arkmodel.Usage{}, errors.New("embedding accounting scope changed")
		}
		if text == "first" {
			close(firstStarted)
			select {
			case <-releaseFirst:
			case <-ctx.Done():
				return nil, arkmodel.Usage{}, ctx.Err()
			}
			return []float32{1}, arkmodel.Usage{}, nil
		}
		if text == "second" {
			select {
			case <-firstStarted:
			case <-ctx.Done():
				return nil, arkmodel.Usage{}, ctx.Err()
			}
			close(secondFinished)
			return []float32{2}, arkmodel.Usage{}, nil
		}
		return []float32{3}, arkmodel.Usage{}, nil
	}}
	type result struct {
		vectors [][]float32
		err     error
	}
	done := make(chan result, 1)
	go func() { v, err := e.Embed(ctx, []string{"first", "second", "third"}); done <- result{v, err} }()
	select {
	case <-secondFinished:
		close(releaseFirst)
	case <-ctx.Done():
		t.Fatal("independent embeddings did not overlap")
	}
	r := <-done
	if r.err != nil || len(r.vectors) != 3 || r.vectors[0][0] != 1 || r.vectors[1][0] != 2 || r.vectors[2][0] != 3 {
		t.Fatalf("completion order changed input order: %v %v", r.vectors, r.err)
	}
	if peak.Load() != 2 || gate.badRoute.Load() || gate.acquired.Load() != 3 || gate.released.Load() != 3 {
		t.Fatalf("concurrency or per-call permits changed: peak=%d acquired=%d released=%d", peak.Load(), gate.acquired.Load(), gate.released.Load())
	}
}

func TestEmbeddingFailureCancelsPeersReleasesPermitsAndReturnsNoPartialResults(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	gate := &embeddingGate{}
	slowStarted := make(chan struct{})
	var canceled atomic.Bool
	e := &Embedder{Concurrency: 2, Gate: gate, Call: func(ctx context.Context, text string, _ llmusage.Scope) ([]float32, arkmodel.Usage, error) {
		if text == "slow" {
			close(slowStarted)
			<-ctx.Done()
			canceled.Store(true)
			return nil, arkmodel.Usage{}, ctx.Err()
		}
		<-slowStarted
		return nil, arkmodel.Usage{}, errors.New("private upstream error")
	}}
	v, err := e.Embed(ctx, []string{"slow", "fail"})
	if !errors.Is(err, ErrTransport) || v != nil || !canceled.Load() || gate.acquired.Load() != 2 || gate.released.Load() != 2 || gate.badRoute.Load() {
		t.Fatalf("failed batch leaked results or permits: vectors=%v err=%v acquired=%d released=%d canceled=%t", v, err, gate.acquired.Load(), gate.released.Load(), canceled.Load())
	}
}

func TestEmbeddingConcurrencyBoundsAndInvalidInputAreCheckedBeforeCalls(t *testing.T) {
	for _, concurrency := range []int{-1, 65} {
		calls := 0
		e := &Embedder{Concurrency: concurrency, Call: func(context.Context, string, llmusage.Scope) ([]float32, arkmodel.Usage, error) {
			calls++
			return []float32{1}, arkmodel.Usage{}, nil
		}}
		if v, err := e.Embed(context.Background(), []string{"valid"}); err == nil || v != nil || calls != 0 {
			t.Fatalf("invalid concurrency %d reached provider", concurrency)
		}
	}
	calls := 0
	e := &Embedder{Concurrency: 2, Call: func(context.Context, string, llmusage.Scope) ([]float32, arkmodel.Usage, error) {
		calls++
		return []float32{1}, arkmodel.Usage{}, nil
	}}
	if v, err := e.Embed(context.Background(), []string{"valid", ""}); err == nil || v != nil || calls != 0 {
		t.Fatal("partially invalid batch reached provider")
	}
}

func TestEmbeddingDefaultConcurrencyRemainsSequential(t *testing.T) {
	var active, peak atomic.Int32
	e := &Embedder{Call: func(context.Context, string, llmusage.Scope) ([]float32, arkmodel.Usage, error) {
		n := active.Add(1)
		defer active.Add(-1)
		if n > peak.Load() {
			peak.Store(n)
		}
		return []float32{1}, arkmodel.Usage{}, nil
	}}
	if _, err := e.Embed(context.Background(), []string{"one", "two"}); err != nil || peak.Load() != 1 {
		t.Fatal("online default changed from one in-flight embedding")
	}
}

func TestEmbeddingParentCancellationReleasesAllInFlightPermits(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	gate := &embeddingGate{}
	started := make(chan struct{})
	var calls atomic.Int32
	e := &Embedder{Concurrency: 2, Gate: gate, Call: func(ctx context.Context, _ string, _ llmusage.Scope) ([]float32, arkmodel.Usage, error) {
		if calls.Add(1) == 2 {
			close(started)
		}
		<-ctx.Done()
		return nil, arkmodel.Usage{}, ctx.Err()
	}}
	done := make(chan error, 1)
	go func() {
		vectors, err := e.Embed(ctx, []string{"one", "two", "pending"})
		if vectors != nil {
			done <- errors.New("canceled batch returned partial vectors")
			return
		}
		done <- err
	}()
	select {
	case <-started:
		cancel()
	case <-time.After(time.Second):
		t.Fatal("workers did not start")
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || gate.acquired.Load() != 2 || gate.released.Load() != 2 {
			t.Fatalf("cancellation or permits changed: %v acquired=%d released=%d", err, gate.acquired.Load(), gate.released.Load())
		}
	case <-time.After(time.Second):
		t.Fatal("embedding workers ignored parent cancellation")
	}
}
