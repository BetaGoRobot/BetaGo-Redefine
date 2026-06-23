package xfuture

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"sync"
	"sync/atomic"
)

var ErrNilFunc = errors.New("future: nil func")

type PanicError struct {
	Value any
	Stack []byte
}

func (e *PanicError) Error() string {
	return fmt.Sprintf("future: panic: %v", e.Value)
}

func (e *PanicError) Unwrap() error {
	if err, ok := e.Value.(error); ok {
		return err
	}
	return nil
}

type Future[T any] interface {
	Start() bool
	Started() bool

	Done() <-chan struct{}

	Wait() error
	Val() (T, error)

	TryVal() (T, error, bool)

	WaitContext(ctx context.Context) error
	ValContext(ctx context.Context) (T, error)
}

type Waiter interface {
	Start() bool
	Started() bool

	Done() <-chan struct{}

	Wait() error
	WaitContext(ctx context.Context) error
}

type future[T any] struct {
	fn func() (T, error)

	once    sync.Once
	started atomic.Bool
	done    chan struct{}

	val T
	err error
}

func Go[T any](fn func() (T, error)) Future[T] {
	f := newLazy(fn)
	f.Start()
	return f
}

func GoContext[T any](ctx context.Context, fn func(context.Context) (T, error)) Future[T] {
	if fn == nil {
		return Failed[T](ErrNilFunc)
	}
	return Go(func() (T, error) {
		return fn(ctx)
	})
}

func Lazy[T any](fn func() (T, error)) Future[T] {
	return newLazy(fn)
}

func LazyContext[T any](ctx context.Context, fn func(context.Context) (T, error)) Future[T] {
	if fn == nil {
		return Failed[T](ErrNilFunc)
	}
	return Lazy(func() (T, error) {
		return fn(ctx)
	})
}

func Value[T any](v T) Future[T] {
	f := &future[T]{
		done: make(chan struct{}),
		val:  v,
	}
	close(f.done)
	f.started.Store(true)
	return f
}

func Failed[T any](err error) Future[T] {
	f := &future[T]{
		done: make(chan struct{}),
		err:  err,
	}
	close(f.done)
	f.started.Store(true)
	return f
}

func From[T any](v T, err error) Future[T] {
	if err != nil {
		return Failed[T](err)
	}
	return Value(v)
}

func newLazy[T any](fn func() (T, error)) *future[T] {
	if fn == nil {
		return Failed[T](ErrNilFunc).(*future[T])
	}
	return &future[T]{
		fn:   fn,
		done: make(chan struct{}),
	}
}

func (f *future[T]) Start() bool {
	if f.started.Load() {
		return false
	}
	started := false
	f.once.Do(func() {
		started = true
		f.started.Store(true)
		if f.fn == nil {
			return
		}
		go f.run()
	})
	return started
}

func (f *future[T]) run() {
	defer close(f.done)

	defer func() {
		if r := recover(); r != nil {
			f.err = &PanicError{
				Value: r,
				Stack: debug.Stack(),
			}
		}
	}()

	f.val, f.err = f.fn()
}

func (f *future[T]) Started() bool {
	return f.started.Load()
}

func (f *future[T]) Done() <-chan struct{} {
	return f.done
}

func (f *future[T]) Wait() error {
	f.Start()
	<-f.done
	return f.err
}

func (f *future[T]) Val() (T, error) {
	f.Start()
	<-f.done
	if f.err != nil {
		var zero T
		return zero, f.err
	}
	return f.val, nil
}

func (f *future[T]) TryVal() (T, error, bool) {
	select {
	case <-f.done:
		if f.err != nil {
			var zero T
			return zero, f.err, true
		}
		return f.val, nil, true
	default:
		var zero T
		return zero, nil, false
	}
}

func (f *future[T]) WaitContext(ctx context.Context) error {
	if ctx == nil {
		panic("future: nil context")
	}
	f.Start()
	select {
	case <-f.done:
		return f.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (f *future[T]) ValContext(ctx context.Context) (T, error) {
	if ctx == nil {
		panic("future: nil context")
	}
	f.Start()
	select {
	case <-f.done:
		if f.err != nil {
			var zero T
			return zero, f.err
		}
		return f.val, nil
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	}
}

func WaitAll(futures ...Waiter) error {
	for _, f := range futures {
		if f == nil {
			continue
		}
		f.Start()
	}
	for _, f := range futures {
		if f == nil {
			continue
		}
		if err := f.Wait(); err != nil {
			return err
		}
	}
	return nil
}

func WaitAllContext(ctx context.Context, futures ...Waiter) error {
	if ctx == nil {
		panic("future: nil context")
	}
	for _, f := range futures {
		if f == nil {
			continue
		}
		f.Start()
	}
	for _, f := range futures {
		if f == nil {
			continue
		}
		if err := f.WaitContext(ctx); err != nil {
			return err
		}
	}
	return nil
}

func All[T any](futures ...Future[T]) ([]T, error) {
	for _, f := range futures {
		if f == nil {
			continue
		}
		f.Start()
	}
	results := make([]T, len(futures))
	for i, f := range futures {
		if f == nil {
			continue
		}
		v, err := f.Val()
		if err != nil {
			return nil, err
		}
		results[i] = v
	}
	return results, nil
}

func AllContext[T any](ctx context.Context, futures ...Future[T]) ([]T, error) {
	if ctx == nil {
		panic("future: nil context")
	}
	for _, f := range futures {
		if f == nil {
			continue
		}
		f.Start()
	}
	results := make([]T, len(futures))
	for i, f := range futures {
		if f == nil {
			continue
		}
		v, err := f.ValContext(ctx)
		if err != nil {
			return nil, err
		}
		results[i] = v
	}
	return results, nil
}
