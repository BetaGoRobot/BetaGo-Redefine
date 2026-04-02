package xhandler

import (
	"context"
	stderrors "errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"go.opentelemetry.io/otel/trace/noop"
)

type testEvent struct {
	id int
}

type gatedFetcher struct {
	StageBase[testEvent, BaseMetaData]

	name    string
	release <-chan struct{}

	calls atomic.Int32

	started chan struct{}
	done    chan struct{}

	startOnce sync.Once
	doneOnce  sync.Once
}

func newGatedFetcher(name string, release <-chan struct{}) *gatedFetcher {
	return &gatedFetcher{
		name:    name,
		release: release,
		started: make(chan struct{}),
		done:    make(chan struct{}),
	}
}

func (f *gatedFetcher) Name() string {
	return f.name
}

func (f *gatedFetcher) Run(ctx context.Context, event *testEvent, meta *BaseMetaData) error {
	f.calls.Add(1)
	f.startOnce.Do(func() { close(f.started) })
	if f.release != nil {
		<-f.release
	}
	f.doneOnce.Do(func() { close(f.done) })
	return nil
}

type chainedFetcher struct {
	*gatedFetcher
	deps []Stage[testEvent, BaseMetaData]
	run  func(context.Context, *testEvent, *BaseMetaData) error
}

func newChainedFetcher(name string, release <-chan struct{}) *chainedFetcher {
	return &chainedFetcher{
		gatedFetcher: newGatedFetcher(name, release),
	}
}

func (f *chainedFetcher) Depends() []Stage[testEvent, BaseMetaData] {
	return f.deps
}

func (f *chainedFetcher) Run(ctx context.Context, event *testEvent, meta *BaseMetaData) error {
	if err := f.gatedFetcher.Run(ctx, event, meta); err != nil {
		return err
	}
	if f.run != nil {
		return f.run(ctx, event, meta)
	}
	return nil
}

type testOperator struct {
	StageBase[testEvent, BaseMetaData]

	name string
	deps []Stage[testEvent, BaseMetaData]

	preRunFn  func(context.Context, *testEvent, *BaseMetaData) error
	runFn     func(context.Context, *testEvent, *BaseMetaData) error
	postRunFn func(context.Context, *testEvent, *BaseMetaData) error
}

func (o *testOperator) Name() string {
	return o.name
}

func (o *testOperator) Depends() []Stage[testEvent, BaseMetaData] {
	return o.deps
}

func (o *testOperator) PreRun(ctx context.Context, event *testEvent, meta *BaseMetaData) error {
	if o.preRunFn != nil {
		return o.preRunFn(ctx, event, meta)
	}
	return nil
}

func (o *testOperator) Run(ctx context.Context, event *testEvent, meta *BaseMetaData) error {
	if o.runFn != nil {
		return o.runFn(ctx, event, meta)
	}
	return nil
}

func (o *testOperator) PostRun(ctx context.Context, event *testEvent, meta *BaseMetaData) error {
	if o.postRunFn != nil {
		return o.postRunFn(ctx, event, meta)
	}
	return nil
}

func TestRunParallelStages_IndependentOperatorDoesNotWaitForDependency(t *testing.T) {
	initTestRuntime()

	release := make(chan struct{})
	fetcher := newGatedFetcher("intent", release)
	independentRan := make(chan struct{})
	dependentRan := make(chan struct{})

	processor := &Processor[testEvent, BaseMetaData]{
		Context:  context.Background(),
		data:     &testEvent{},
		metaData: &BaseMetaData{},
	}
	processor.
		AddAsync(&testOperator{
			name: "dependent",
			deps: []Stage[testEvent, BaseMetaData]{fetcher},
			runFn: func(ctx context.Context, event *testEvent, meta *BaseMetaData) error {
				close(dependentRan)
				return nil
			},
		}).
		AddAsync(&testOperator{
			name: "independent",
			runFn: func(ctx context.Context, event *testEvent, meta *BaseMetaData) error {
				close(independentRan)
				return nil
			},
		})

	runDone := make(chan error, 1)
	go func() {
		runDone <- processor.RunParallelStages()
	}()

	waitClosed(t, fetcher.started, time.Second, "fetcher started")
	waitClosed(t, independentRan, time.Second, "independent operator ran")
	ensureNotClosed(t, dependentRan, 100*time.Millisecond, "dependent operator should still wait for dependency")

	close(release)

	waitClosed(t, dependentRan, time.Second, "dependent operator ran after dependency")
	if err := <-runDone; err != nil {
		t.Fatalf("RunParallelStages returned error: %v", err)
	}
}

func TestRunParallelStages_DeduplicatesSharedFetcher(t *testing.T) {
	initTestRuntime()

	fetcher := newGatedFetcher("shared", nil)
	var ran atomic.Int32

	processor := &Processor[testEvent, BaseMetaData]{
		Context:  context.Background(),
		data:     &testEvent{},
		metaData: &BaseMetaData{},
	}
	processor.
		AddAsync(&testOperator{
			name: "op1",
			deps: []Stage[testEvent, BaseMetaData]{fetcher},
			runFn: func(ctx context.Context, event *testEvent, meta *BaseMetaData) error {
				ran.Add(1)
				return nil
			},
		}).
		AddAsync(&testOperator{
			name: "op2",
			deps: []Stage[testEvent, BaseMetaData]{fetcher},
			runFn: func(ctx context.Context, event *testEvent, meta *BaseMetaData) error {
				ran.Add(1)
				return nil
			},
		})

	if err := processor.RunParallelStages(); err != nil {
		t.Fatalf("RunParallelStages returned error: %v", err)
	}
	if got := fetcher.calls.Load(); got != 1 {
		t.Fatalf("shared fetcher called %d times, want 1", got)
	}
	if got := ran.Load(); got != 2 {
		t.Fatalf("operators ran %d times, want 2", got)
	}
}

func TestRunParallelStages_ExecutesTransitiveDependencies(t *testing.T) {
	initTestRuntime()

	leafRan := make(chan struct{})
	leaf := newChainedFetcher("leaf", nil)
	leaf.run = func(ctx context.Context, event *testEvent, meta *BaseMetaData) error {
		meta.SetExtra("leaf", "ready")
		close(leafRan)
		return nil
	}

	mid := newChainedFetcher("mid", nil)
	mid.deps = []Stage[testEvent, BaseMetaData]{leaf}

	rootRan := make(chan struct{})
	processor := &Processor[testEvent, BaseMetaData]{
		Context:  context.Background(),
		data:     &testEvent{},
		metaData: &BaseMetaData{},
	}
	processor.AddAsync(&testOperator{
		name: "root",
		deps: []Stage[testEvent, BaseMetaData]{mid},
		runFn: func(ctx context.Context, event *testEvent, meta *BaseMetaData) error {
			if got, ok := meta.GetExtra("leaf"); !ok || got != "ready" {
				t.Fatalf("transitive dependency result missing, got (%q, %v)", got, ok)
			}
			close(rootRan)
			return nil
		},
	})

	runDone := make(chan error, 1)
	go func() {
		runDone <- processor.RunParallelStages()
	}()

	waitClosed(t, mid.started, time.Second, "mid fetcher started")
	waitClosed(t, rootRan, time.Second, "root operator ran")
	waitClosed(t, leafRan, time.Second, "leaf fetcher ran")
	if err := <-runDone; err != nil {
		t.Fatalf("RunParallelStages returned error: %v", err)
	}
}

func TestRunParallelStages_DetectsDependencyCycles(t *testing.T) {
	initTestRuntime()

	a := newChainedFetcher("a", nil)
	b := newChainedFetcher("b", nil)
	a.deps = []Stage[testEvent, BaseMetaData]{b}
	b.deps = []Stage[testEvent, BaseMetaData]{a}

	processor := &Processor[testEvent, BaseMetaData]{
		Context:  context.Background(),
		data:     &testEvent{},
		metaData: &BaseMetaData{},
	}
	processor.AddAsync(&testOperator{
		name: "root",
		deps: []Stage[testEvent, BaseMetaData]{a},
	})

	if err := processor.RunParallelStages(); err == nil {
		t.Fatal("RunParallelStages() error = nil, want cycle detection error")
	}
}

func TestRunParallelStages_BlocksDependentsWhenDependencyFails(t *testing.T) {
	initTestRuntime()

	depErr := stderrors.New("dependency failed")
	failing := newChainedFetcher("failing", nil)
	failing.run = func(ctx context.Context, event *testEvent, meta *BaseMetaData) error {
		return depErr
	}

	dependentRan := make(chan struct{})
	processor := &Processor[testEvent, BaseMetaData]{
		Context:  context.Background(),
		data:     &testEvent{},
		metaData: &BaseMetaData{},
	}
	processor.AddAsync(&testOperator{
		name: "dependent",
		deps: []Stage[testEvent, BaseMetaData]{failing},
		runFn: func(ctx context.Context, event *testEvent, meta *BaseMetaData) error {
			close(dependentRan)
			return nil
		},
	})

	err := processor.RunParallelStages()
	if err == nil {
		t.Fatal("RunParallelStages() error = nil, want dependency failure returned")
	}
	if !stderrors.Is(err, depErr) {
		t.Fatalf("RunParallelStages() error = %v, want wrapped dependency error", err)
	}
	ensureNotClosed(t, dependentRan, 50*time.Millisecond, "dependent stage should not run when dependency fails")
}

func TestRun_InitializesMetaAndExecutesHooks(t *testing.T) {
	initTestRuntime()

	var (
		preRunCalled atomic.Bool
		runCalled    atomic.Bool
		deferCalled  atomic.Bool
	)

	event := &testEvent{}
	processor := (&Processor[testEvent, BaseMetaData]{}).
		WithCtx(context.Background()).
		WithData(event).
		WithMetaDataProcess(func(input *testEvent) *BaseMetaData {
			if input != event {
				t.Fatalf("meta init received unexpected event pointer")
			}
			return &BaseMetaData{ChatID: "chat-1"}
		}).
		WithPreRun(func(p *Processor[testEvent, BaseMetaData]) {
			preRunCalled.Store(true)
			if p.metaData == nil || p.metaData.ChatID != "chat-1" {
				t.Fatalf("metaData should be initialized before preRun")
			}
		}).
		WithDefer(func(ctx context.Context, input *testEvent, meta *BaseMetaData) {
			deferCalled.Store(true)
			if input != event {
				t.Fatalf("defer received unexpected event pointer")
			}
			if meta == nil || meta.ChatID != "chat-1" {
				t.Fatalf("defer received unexpected metaData: %#v", meta)
			}
		}).
		AddAsync(&testOperator{
			name: "op",
			runFn: func(ctx context.Context, input *testEvent, meta *BaseMetaData) error {
				runCalled.Store(true)
				if !preRunCalled.Load() {
					t.Fatalf("operator ran before preRun")
				}
				if input != event {
					t.Fatalf("operator received unexpected event pointer")
				}
				if meta == nil || meta.ChatID != "chat-1" {
					t.Fatalf("operator received unexpected metaData: %#v", meta)
				}
				return nil
			},
		})

	processor.Run()

	if !preRunCalled.Load() {
		t.Fatal("preRun hook was not called")
	}
	if !runCalled.Load() {
		t.Fatal("operator did not run")
	}
	if !deferCalled.Load() {
		t.Fatal("defer hook was not called")
	}
}

func TestNewExecutionDoesNotReuseRequestState(t *testing.T) {
	templateEvent := &testEvent{id: 1}
	execEvent := &testEvent{id: 2}

	template := (&Processor[testEvent, BaseMetaData]{}).
		WithCtx(context.Background()).
		WithData(templateEvent)

	exec := template.NewExecution().
		WithCtx(context.TODO()).
		WithData(execEvent)

	if template.Context == nil {
		t.Fatal("template context should remain unchanged")
	}
	if template.Data() == nil {
		t.Fatal("template data should remain unchanged")
	}
	if exec == template {
		t.Fatal("NewExecution should return a cloned processor")
	}
	if exec.Context == template.Context {
		t.Fatal("execution context should not alias template context")
	}
	if exec.Data() == template.Data() {
		t.Fatal("execution data should not alias template data")
	}
}

func TestBaseMetaDataAccessors(t *testing.T) {
	meta := &BaseMetaData{}

	meta.SetIsCommand(true)
	meta.SetMainCommand("bb")
	meta.SetSkipDone(true)
	meta.SetExtra("result", "ok")

	if !meta.IsCommandMarked() {
		t.Fatal("expected IsCommandMarked to return true")
	}
	if got := meta.GetMainCommand(); got != "bb" {
		t.Fatalf("GetMainCommand() = %q, want %q", got, "bb")
	}
	if !meta.ShouldSkipDone() {
		t.Fatal("expected ShouldSkipDone to return true")
	}
	if got, ok := meta.GetExtra("result"); !ok || got != "ok" {
		t.Fatalf("GetExtra() = (%q, %v), want (%q, true)", got, ok, "ok")
	}
}

func waitClosed(t *testing.T, ch <-chan struct{}, timeout time.Duration, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(timeout):
		t.Fatalf("timeout waiting for %s", what)
	}
}

func ensureNotClosed(t *testing.T, ch <-chan struct{}, timeout time.Duration, msg string) {
	t.Helper()
	select {
	case <-ch:
		t.Fatal(msg)
	case <-time.After(timeout):
	}
}

func initTestRuntime() {
	_ = os.Setenv("BETAGO_CONFIG_PATH", filepath.Clean("../../.dev/config.toml"))
	otel.Init(nil)
	otel.OtelTracer = noop.NewTracerProvider().Tracer("test")
	logs.Init()
}

func Example_dependencyUsage() {
	// 这是一个示例，展示如何在实际代码中使用新的依赖机制

	// 1. 定义一个 Stage
	/*
		type IntentRecognizeOperator struct {
			StageBase[T, K]
		}

		func (r *IntentRecognizeOperator) Name() string {
			return "IntentRecognizeOperator"
		}

		func (r *IntentRecognizeOperator) Run(ctx context.Context, event *T, meta *K) error {
			// 执行意图识别逻辑...
			return nil
		}

		// 全局单例
		var IntentRecognizeStage = &IntentRecognizeOperator{}
	*/

	// 2. 定义一个依赖该 Stage 的 Stage
	/*
		type ChatMsgOperator struct {
			StageBase[T, K]
		}

		// 声明依赖 - 返回 Stage 实例
		func (r *ChatMsgOperator) Depends() []Stage[T, K] {
			return []Stage[T, K]{
				IntentRecognizeStage,
			}
		}

		func (r *ChatMsgOperator) Name() string {
			return "ChatMsgOperator"
		}

		// ... 其他方法实现
	*/

	// 3. 注册到 Processor - 不需要手动注册依赖 Stage
	/*
		Handler = Handler.
			AddAsync(&ChatMsgOperator{})  // 只需要注册根 Stage
		// 依赖 Stage 会通过 ChatMsgOperator.Depends() 自动收集和执行
	*/

	fmt.Println("使用统一 Stage 和 Depends 的示例代码")
	fmt.Println("1. 定义 Stage 实现 Name() 和 Run() 方法")
	fmt.Println("2. Stage 通过 Depends() []Stage[T, K] 返回依赖实例")
	fmt.Println("3. Processor 自动编译 DAG、去重并执行所有依赖 Stage")
	fmt.Println("4. 每个 Stage 在自己的依赖完成后执行")
}
