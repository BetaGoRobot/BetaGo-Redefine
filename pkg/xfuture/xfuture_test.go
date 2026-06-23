package xfuture

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ------------------------------------------------------------------
// 21.1 基础测试
// ------------------------------------------------------------------

func TestGoExecutesImmediately(t *testing.T) {
	done := make(chan struct{})
	_ = Go(func() (int, error) {
		close(done)
		return 1, nil
	})
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatalf("Go() did not start immediately")
	}
}

func TestLazyDoesNotExecuteUntilTriggered(t *testing.T) {
	started := make(chan struct{}, 1)
	f := Lazy(func() (int, error) {
		started <- struct{}{}
		return 1, nil
	})
	select {
	case <-started:
		t.Fatalf("Lazy executed immediately before any trigger")
	case <-time.After(30 * time.Millisecond):
	}
	if f.Started() {
		t.Fatalf("Lazy reported started before trigger")
	}
	f.Start()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatalf("Lazy did not execute after Start()")
	}
}

func TestLazyValTriggersExecution(t *testing.T) {
	var run atomic.Int64
	f := Lazy(func() (int, error) {
		run.Add(1)
		return 42, nil
	})
	v, err := f.Val()
	if err != nil {
		t.Fatalf("Val() err = %v", err)
	}
	if v != 42 {
		t.Fatalf("Val() = %d, want 42", v)
	}
	if run.Load() != 1 {
		t.Fatalf("fn ran %d times, want 1", run.Load())
	}
}

func TestLazyWaitTriggersExecution(t *testing.T) {
	var run atomic.Int64
	f := Lazy(func() (int, error) {
		run.Add(1)
		return 0, nil
	})
	if err := f.Wait(); err != nil {
		t.Fatalf("Wait() err = %v", err)
	}
	if run.Load() != 1 {
		t.Fatalf("fn ran %d times, want 1", run.Load())
	}
}

func TestLazyStartTriggersExecution(t *testing.T) {
	done := make(chan struct{})
	f := Lazy(func() (int, error) {
		close(done)
		return 0, nil
	})
	ok := f.Start()
	if !ok {
		t.Fatalf("Start() returned false on first call of lazy")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatalf("Start() did not trigger execution")
	}
}

func TestTryValDoesNotTriggerLazy(t *testing.T) {
	f := Lazy(func() (int, error) {
		t.Fatalf("TryVal must not trigger lazy future")
		return 1, nil
	})
	v, err, ok := f.TryVal()
	if ok {
		t.Fatalf("TryVal returned ok=true on unstarted lazy future")
	}
	if v != 0 || err != nil {
		t.Fatalf("TryVal on unstarted lazy should return zero/nil, got v=%v err=%v", v, err)
	}
	if f.Started() {
		t.Fatalf("TryVal caused Started()=true")
	}
}

func TestDoneDoesNotTriggerLazy(t *testing.T) {
	f := Lazy(func() (int, error) {
		t.Fatalf("Done must not trigger lazy future")
		return 1, nil
	})
	ch := f.Done()
	select {
	case <-ch:
		t.Fatalf("Done channel closed on unstarted lazy future")
	case <-time.After(20 * time.Millisecond):
	}
	if f.Started() {
		t.Fatalf("Done caused Started()=true")
	}
}

// ------------------------------------------------------------------
// 21.2 exactly-once 测试
// ------------------------------------------------------------------

func TestStartExactlyOnce_ConcurrentStart(t *testing.T) {
	var run atomic.Int64
	f := Lazy(func() (int, error) {
		run.Add(1)
		time.Sleep(10 * time.Millisecond)
		return 1, nil
	})
	var startedTrue atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if f.Start() {
				startedTrue.Add(1)
			}
		}()
	}
	wg.Wait()
	if startedTrue.Load() != 1 {
		t.Fatalf("exactly one Start should return true, got %d", startedTrue.Load())
	}
	if run.Load() != 1 {
		t.Fatalf("fn ran %d times, want 1", run.Load())
	}
}

func TestStartExactlyOnce_ConcurrentVal(t *testing.T) {
	var run atomic.Int64
	f := Lazy(func() (int, error) {
		run.Add(1)
		return 7, nil
	})
	var wg sync.WaitGroup
	results := make([]int, 100)
	errs := make([]error, 100)
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = f.Val()
		}(i)
	}
	wg.Wait()
	if run.Load() != 1 {
		t.Fatalf("fn ran %d times, want 1", run.Load())
	}
	for i, r := range results {
		if r != 7 || errs[i] != nil {
			t.Fatalf("result[%d] = (%v, %v), want (7, nil)", i, r, errs[i])
		}
	}
}

func TestStartExactlyOnce_ConcurrentWait(t *testing.T) {
	var run atomic.Int64
	f := Lazy(func() (int, error) {
		run.Add(1)
		return 0, nil
	})
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = f.Wait()
		}()
	}
	wg.Wait()
	if run.Load() != 1 {
		t.Fatalf("fn ran %d times, want 1", run.Load())
	}
}

func TestStartExactlyOnce_MixedConcurrentCalls(t *testing.T) {
	var run atomic.Int64
	f := Lazy(func() (int, error) {
		run.Add(1)
		time.Sleep(5 * time.Millisecond)
		return 9, nil
	})
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(4)
		go func() { defer wg.Done(); f.Start() }()
		go func() { defer wg.Done(); _, _ = f.Val() }()
		go func() { defer wg.Done(); _ = f.Wait() }()
		go func() { defer wg.Done(); _, _, _ = f.TryVal() }()
	}
	wg.Wait()
	if run.Load() != 1 {
		t.Fatalf("fn ran %d times, want 1", run.Load())
	}
}

// ------------------------------------------------------------------
// 21.3 WaitAll 测试
// ------------------------------------------------------------------

func TestWaitAll_ReturnsFirstError(t *testing.T) {
	boom := errors.New("boom")
	f1 := Go(func() (int, error) { return 1, nil })
	f2 := Go(func() (string, error) { return "", boom })
	f3 := Go(func() (bool, error) { return true, nil })
	err := WaitAll(f1, f2, f3)
	if !errors.Is(err, boom) {
		t.Fatalf("WaitAll err = %v, want %v", err, boom)
	}
}

func TestWaitAll_Success(t *testing.T) {
	f1 := Go(func() (int, error) { return 1, nil })
	f2 := Go(func() (string, error) { return "a", nil })
	if err := WaitAll(f1, f2); err != nil {
		t.Fatalf("WaitAll err = %v", err)
	}
}

// Lazy future 不能退化成串行：全部 Start 之后再依次 Wait。
// 验证方式：每个任务 sleep 30ms，3 个任务总耗时应该 < 80ms（否则是串行）。
func TestWaitAll_LazyFuturesRunConcurrently(t *testing.T) {
	start := time.Now()
	f1 := Lazy(func() (int, error) { time.Sleep(30 * time.Millisecond); return 1, nil })
	f2 := Lazy(func() (string, error) { time.Sleep(30 * time.Millisecond); return "x", nil })
	f3 := Lazy(func() (bool, error) { time.Sleep(30 * time.Millisecond); return true, nil })
	if err := WaitAll(f1, f2, f3); err != nil {
		t.Fatalf("WaitAll err = %v", err)
	}
	elapsed := time.Since(start)
	if elapsed > 90*time.Millisecond {
		t.Fatalf("WaitAll took %v, lazy futures likely ran serially (expected ~30ms)", elapsed)
	}
}

func TestWaitAll_SupportsDifferentReturnTypes(t *testing.T) {
	fi := Go(func() (int, error) { return 1, nil })
	fs := Go(func() (string, error) { return "a", nil })
	fb := Go(func() (bool, error) { return false, nil })
	if err := WaitAll(fi, fs, fb); err != nil {
		t.Fatalf("WaitAll err = %v", err)
	}
	i, _ := fi.Val()
	s, _ := fs.Val()
	b, _ := fb.Val()
	if i != 1 || s != "a" || b != false {
		t.Fatalf("values wrong: (%d,%s,%t)", i, s, b)
	}
}

func TestWaitAll_SkipsNil(t *testing.T) {
	f1 := Go(func() (int, error) { return 1, nil })
	if err := WaitAll(nil, f1, nil); err != nil {
		t.Fatalf("WaitAll err = %v", err)
	}
}

// ------------------------------------------------------------------
// 21.4 All 测试
// ------------------------------------------------------------------

func TestAll_ReturnsResultsInOrder(t *testing.T) {
	f1 := Go(func() (int, error) { time.Sleep(20 * time.Millisecond); return 1, nil })
	f2 := Go(func() (int, error) { return 2, nil })
	f3 := Go(func() (int, error) { time.Sleep(10 * time.Millisecond); return 3, nil })
	got, err := All(f1, f2, f3)
	if err != nil {
		t.Fatalf("All err = %v", err)
	}
	if len(got) != 3 || got[0] != 1 || got[1] != 2 || got[2] != 3 {
		t.Fatalf("All = %v, want [1 2 3]", got)
	}
}

func TestAll_PropagatesError(t *testing.T) {
	boom := errors.New("x")
	f1 := Go(func() (int, error) { return 1, nil })
	f2 := Go(func() (int, error) { return 0, boom })
	got, err := All(f1, f2)
	if !errors.Is(err, boom) {
		t.Fatalf("All err = %v, want %v", err, boom)
	}
	if got != nil {
		t.Fatalf("All results = %v on error, want nil", got)
	}
}

func TestAll_LazyFuturesRunConcurrently(t *testing.T) {
	start := time.Now()
	f1 := Lazy(func() (int, error) { time.Sleep(30 * time.Millisecond); return 1, nil })
	f2 := Lazy(func() (int, error) { time.Sleep(30 * time.Millisecond); return 2, nil })
	f3 := Lazy(func() (int, error) { time.Sleep(30 * time.Millisecond); return 3, nil })
	_, err := All(f1, f2, f3)
	if err != nil {
		t.Fatalf("All err = %v", err)
	}
	if elapsed := time.Since(start); elapsed > 90*time.Millisecond {
		t.Fatalf("All took %v, lazy futures likely ran serially", elapsed)
	}
}

// ------------------------------------------------------------------
// 21.5 Context 测试
// ------------------------------------------------------------------

func TestValContext_TimeoutReturnsContextError(t *testing.T) {
	f := Go(func() (int, error) {
		time.Sleep(200 * time.Millisecond)
		return 1, nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := f.ValContext(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ValContext err = %v, want DeadlineExceeded", err)
	}
}

func TestValContext_TimeoutDoesNotCancelFuture(t *testing.T) {
	f := Go(func() (int, error) {
		time.Sleep(80 * time.Millisecond)
		return 77, nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err := f.ValContext(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ValContext err = %v, want DeadlineExceeded", err)
	}
	// 之后依然可以取到结果。
	v, err := f.Val()
	if err != nil {
		t.Fatalf("Val() after timeout err = %v", err)
	}
	if v != 77 {
		t.Fatalf("Val() after timeout = %d, want 77", v)
	}
}

func TestGoContext_ExecutionContextCancelsTask(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	f := GoContext(ctx, func(ctx context.Context) (int, error) {
		select {
		case <-time.After(5 * time.Second):
			return 1, nil
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	})
	cancel()
	_, err := f.Val()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Val() err = %v, want Canceled", err)
	}
}

func TestWaitAllContext_Timeout(t *testing.T) {
	f1 := Go(func() (int, error) { time.Sleep(200 * time.Millisecond); return 1, nil })
	f2 := Go(func() (string, error) { time.Sleep(200 * time.Millisecond); return "", nil })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	err := WaitAllContext(ctx, f1, f2)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("WaitAllContext err = %v, want DeadlineExceeded", err)
	}
}

// ------------------------------------------------------------------
// 21.6 Panic 测试
// ------------------------------------------------------------------

func TestPanic_ConvertedToPanicError(t *testing.T) {
	f := Go(func() (int, error) {
		panic("boom")
	})
	_, err := f.Val()
	var pErr *PanicError
	if !errors.As(err, &pErr) {
		t.Fatalf("Val() err = %T %v, want *PanicError", err, err)
	}
	if pErr.Value != "boom" {
		t.Fatalf("PanicError.Value = %v, want 'boom'", pErr.Value)
	}
	if len(pErr.Stack) == 0 {
		t.Fatalf("PanicError.Stack empty")
	}
}

func TestPanic_WaitAlsoReturnsPanicError(t *testing.T) {
	f := Go(func() (int, error) { panic("x") })
	err := f.Wait()
	var pErr *PanicError
	if !errors.As(err, &pErr) {
		t.Fatalf("Wait() err = %T %v, want *PanicError", err, err)
	}
}

func TestPanic_WrappedErrorUnwrap(t *testing.T) {
	boom := errors.New("wrapped")
	f := Go(func() (int, error) { panic(boom) })
	err := f.Wait()
	if !errors.Is(err, boom) {
		t.Fatalf("errors.Is did not find wrapped boom: %v", err)
	}
}

// ------------------------------------------------------------------
// Misc：Value/Failed/From/nil function/nil context panic
// ------------------------------------------------------------------

func TestValue_CompletedSuccess(t *testing.T) {
	f := Value(42)
	if !f.Started() {
		t.Fatalf("Value should report started=true")
	}
	v, err := f.Val()
	if err != nil || v != 42 {
		t.Fatalf("Val() = (%v,%v), want (42,nil)", v, err)
	}
	select {
	case <-f.Done():
	default:
		t.Fatalf("Done channel not closed for Value future")
	}
}

func TestFailed_CompletedError(t *testing.T) {
	boom := errors.New("boom")
	f := Failed[int](boom)
	v, err := f.Val()
	if !errors.Is(err, boom) {
		t.Fatalf("Val() err = %v, want boom", err)
	}
	var zero int
	if v != zero {
		t.Fatalf("Val() value = %v, want zero", v)
	}
}

func TestFrom_SuccessAndError(t *testing.T) {
	a, errA := From(1, nil).Val()
	if a != 1 || errA != nil {
		t.Fatalf("From success: (%v,%v)", a, errA)
	}
	boom := errors.New("x")
	_, errB := From(0, boom).Val()
	if !errors.Is(errB, boom) {
		t.Fatalf("From error: %v", errB)
	}
}

func TestNilFunc_ReturnsFailedFuture(t *testing.T) {
	f := Go[int](nil)
	_, err := f.Val()
	if !errors.Is(err, ErrNilFunc) {
		t.Fatalf("nil fn err = %v, want ErrNilFunc", err)
	}
	fl := Lazy[int](nil)
	_, err = fl.Val()
	if !errors.Is(err, ErrNilFunc) {
		t.Fatalf("Lazy nil fn err = %v, want ErrNilFunc", err)
	}
}

func TestNilContext_Panics(t *testing.T) {
	f := Go(func() (int, error) { return 1, nil })
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic for nil context, did not panic")
		}
	}()
	_, _ = f.ValContext(nil)
}

func TestStarted_ReturnsTrueAfterGo(t *testing.T) {
	f := Go(func() (int, error) { time.Sleep(20 * time.Millisecond); return 1, nil })
	if !f.Started() {
		t.Fatalf("Go future Started() = false immediately after construction")
	}
}

func TestStart_ReturnsFalseOnSecondCall(t *testing.T) {
	f := Lazy(func() (int, error) { return 1, nil })
	if !f.Start() {
		t.Fatalf("first Start returned false")
	}
	if f.Start() {
		t.Fatalf("second Start returned true")
	}
}

func TestDoneChannel_ReturnsSameInstance(t *testing.T) {
	f := Lazy(func() (int, error) { return 1, nil })
	c1 := f.Done()
	c2 := f.Done()
	if c1 != c2 {
		t.Fatalf("Done() returned different channels on repeated calls")
	}
}

func TestAll_SkipsNilInSlice(t *testing.T) {
	f1 := Go(func() (int, error) { return 1, nil })
	got, err := All[int](nil, f1)
	if err != nil {
		t.Fatalf("All(nil,f1) err = %v", err)
	}
	if len(got) != 2 || got[0] != 0 || got[1] != 1 {
		t.Fatalf("All(nil,f1) = %v, want [0 1]", got)
	}
}
