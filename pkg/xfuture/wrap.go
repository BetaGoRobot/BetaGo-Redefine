package xfuture

import (
	"context"

	"github.com/sourcegraph/conc/pool"
)

type P2[T, K any] struct {
	T T
	K K
}

type XPool[T any] struct {
	p *pool.ResultContextPool[T]
}

func (p *XPool[T]) Wait() ([]T, error) {
	return p.p.Wait()
}

func (p *XPool[T]) WaitFirst() (T, error) {
	r, err := p.p.Wait()
	return r[0], err
}

func New[T any](ctx context.Context, fn func(ctx context.Context) (T, error)) *XPool[T] {
	p := pool.NewWithResults[T]().WithContext(ctx)
	p.Go(fn)
	return &XPool[T]{p: p}
}

func New2[T, K any](ctx context.Context, fn func(ctx context.Context) (T, K, error)) *XPool[P2[T, K]] {
	p := pool.NewWithResults[P2[T, K]]().WithContext(ctx)
	p.Go(func(ctx context.Context) (P2[T, K], error) {
		t, k, err := fn(ctx)
		if err != nil {
			return P2[T, K]{}, err
		}
		return P2[T, K]{t, k}, nil
	})
	return &XPool[P2[T, K]]{p: p}
}
