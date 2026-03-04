package xhandler

import (
	"context"
	"sync"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xerror"
	"github.com/BetaGoRobot/go_utils/reflecting"
	"github.com/pkg/errors"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// Fetcher 定义需要提前获取数据的独立接口
type Fetcher[T, K any] interface {
	Name() string
	Fetch(ctx context.Context, event *T, meta *K) error
}

type Operator[T, K any] interface {
	Name() string

	PreRun(context.Context, *T, *K) error
	Run(context.Context, *T, *K) error
	PostRun(context.Context, *T, *K) error

	MetaInit() *K

	// Depends 返回此 Operator 依赖的 Fetcher 实例列表
	Depends() []Fetcher[T, K]
}

type (
	OperatorBase[T, K any] struct{}
	BaseMetaData           struct {
		ChatID      string
		UserID      string
		IsP2P       bool
		Refresh     bool
		IsCommand   bool
		MainCommand string
		TraceID     string

		ForceReplyDirect bool
		SkipDone         bool
		Extra            map[string]string

		// TODO: 暂时没有用上，后续改造替换掉st、et的反复解析，搞成通用参数
		StartTime string
		EndTime   string
	}
)

func (m *BaseMetaData) GetExtra(key string) (string, bool) {
	if m.Extra == nil {
		m.Extra = make(map[string]string)
		return "", false
	}
	val, ok := m.Extra[key]
	return val, ok
}

func (m *BaseMetaData) SetExtra(key string, val string) {
	if m.Extra == nil {
		m.Extra = make(map[string]string)
	}
	m.Extra[key] = val
}

type (
	ProcPanicFunc[T, K any] func(context.Context, error, *T, *K)
	ProcDeferFunc[T, K any] func(context.Context, *T, *K)
	MetaInitFunc[T, K any]  func(*T) *K
	Processor[T, K any]     struct {
		context.Context

		needBreak   bool
		data        *T
		metaData    *K
		syncStages  []Operator[T, K]
		asyncStages []Operator[T, K]
		onPanicFn   ProcPanicFunc[T, K]
		deferFn     []ProcDeferFunc[T, K]
		metaInitFn  MetaInitFunc[T, K]
		preRunFn    func(p *Processor[T, K])
	}
)

func (op *OperatorBase[T, K]) Name() string {
	return "NotImplementBaseName"
}

func (op *OperatorBase[T, K]) PreRun(context.Context, *T, *K) error {
	return nil
}

func (op *OperatorBase[T, K]) Run(context.Context, *T, *K) error {
	return nil
}

func (op *OperatorBase[T, K]) PostRun(context.Context, *T, *K) error {
	return nil
}

func (op *OperatorBase[T, K]) MetaInit() *K {
	return new(K)
}

// Depends 默认实现：无依赖
func (op *OperatorBase[T, K]) Depends() []Fetcher[T, K] {
	return nil
}

func (p *Processor[T, K]) WithCtx(ctx context.Context) *Processor[T, K] {
	p.Context = ctx
	return p
}

func (p *Processor[T, K]) OnPanic(fn ProcPanicFunc[T, K]) *Processor[T, K] {
	p.onPanicFn = fn
	return p
}

func (p *Processor[T, K]) WithDefer(fns ...ProcDeferFunc[T, K]) *Processor[T, K] {
	p.deferFn = append(p.deferFn, fns...)
	return p
}

func (p *Processor[T, K]) WithMetaDataProcess(fn MetaInitFunc[T, K]) *Processor[T, K] {
	p.metaInitFn = fn
	return p
}

func (p *Processor[T, K]) WithPreRun(f func(p *Processor[T, K])) *Processor[T, K] {
	p.preRunFn = f
	return p
}

func (p *Processor[T, K]) WithData(event *T) *Processor[T, K] {
	p.data = event
	return p
}

func (p *Processor[T, K]) Data() *T {
	return p.data
}

func (p *Processor[T, K]) Clean() *Processor[T, K] {
	p.data = nil
	p.Context = nil
	return p
}

func (p *Processor[T, K]) Defer() {
	if err := recover(); err != nil {
		if p.onPanicFn != nil {
			p.onPanicFn(p.Context, err.(error), p.data, p.metaData)
		}
	}
}

// AddSync  添加处理阶段
//
//	@receiver p
//	@param stage
//	@return *Processor[T]
func (p *Processor[T, K]) AddSync(stage Operator[T, K]) *Processor[T, K] {
	p.syncStages = append(p.syncStages, stage)
	return p
}

// AddAsync  添加并行处理阶段
//
//	@receiver p
//	@param stage
//	@return *Processor[T]
func (p *Processor[T, K]) AddAsync(stage Operator[T, K]) *Processor[T, K] {
	p.asyncStages = append(p.asyncStages, stage)
	return p
}

// AddParallelStages 兼容旧接口
func (p *Processor[T, K]) AddParallelStages(stage Operator[T, K]) *Processor[T, K] {
	return p.AddAsync(stage)
}

// AddStages 兼容旧接口
func (p *Processor[T, K]) AddStages(stage Operator[T, K]) *Processor[T, K] {
	return p.AddSync(stage)
}

// RunStages  运行处理阶段
//
//	@receiver p
//	@param ctx
//	@param event
func (p *Processor[T, K]) RunStages() (err error) {
	var span trace.Span
	p.Context, span = otel.T().Start(p.Context, reflecting.GetCurrentFunc())
	defer span.End()

	for _, s := range p.syncStages {
		defer p.Defer()
		err = s.PreRun(p.Context, p.data, p.metaData)
		if err != nil {
			trace.SpanFromContext(p.Context).RecordError(err)
			if errors.Is(err, xerror.ErrStageSkip) {
				logs.L().Ctx(p).Warn("Skipped pre run stage", zap.Error(err))
			} else {
				logs.L().Ctx(p).Error("Skipped pre run stage", zap.Error(err))
			}
			return
		}
		err = s.Run(p.Context, p.data, p.metaData)
		if err != nil {
			trace.SpanFromContext(p.Context).RecordError(err)
			if errors.Is(err, xerror.ErrStageSkip) {
				logs.L().Ctx(p).Warn("run stage skipped", zap.Error(err))
			} else {
				logs.L().Ctx(p).Error("run stage skipped", zap.Error(err))
			}
			return
		}
		err = s.PostRun(p.Context, p.data, p.metaData)
		if err != nil {
			trace.SpanFromContext(p.Context).RecordError(err)
			if errors.Is(err, xerror.ErrStageSkip) {
				logs.L().Ctx(p).Warn("post run stage skipped", zap.Error(err))
			} else {
				logs.L().Ctx(p).Error("post run stage skipped", zap.Error(err))
			}
			return
		}
	}
	return
}

// Run  运行
//
//	@receiver p
//	@param ctx
//	@param event
func (p *Processor[T, K]) Run() {
	if p.metaInitFn == nil {
		p.metaInitFn = func(*T) *K { return new(K) }
	}
	p.metaData = p.metaInitFn(p.Data())

	if p.preRunFn != nil {
		p.preRunFn(p)
	}
	for _, fn := range p.deferFn {
		if fn != nil {
			defer fn(p.Context, p.data, p.metaData)
		}
	}
	wg := sync.WaitGroup{}
	wg.Go(func() { p.RunStages() })
	wg.Go(func() { p.RunParallelStages() })
	wg.Wait()
}

// fetcherWrapper 包装 Fetcher 用于追踪执行状态
type fetcherWrapper[T, K any] struct {
	fetcher Fetcher[T, K]
	done    chan struct{}
	err     error
}

// operatorWrapper 包装 Operator 用于追踪执行状态
type operatorWrapper[T, K any] struct {
	operator Operator[T, K]
}

// RunParallelStages  运行并行处理阶段（考虑依赖关系）
//
//	@receiver p
//	@param ctx
//	@param event
//	@return error
func (p *Processor[T, K]) RunParallelStages() error {
	ctx, span := otel.T().Start(p.Context, reflecting.GetCurrentFunc())
	defer span.End()

	// 1. 收集所有依赖的 Fetcher（去重）
	fetcherMap := make(map[string]*fetcherWrapper[T, K])

	for _, op := range p.asyncStages {
		deps := op.Depends()
		for _, dep := range deps {
			name := dep.Name()
			if _, exists := fetcherMap[name]; !exists {
				fetcherMap[name] = &fetcherWrapper[T, K]{
					fetcher: dep,
					done:    make(chan struct{}),
				}
			}
		}
	}

	// 2. 启动所有 Fetchers（并行执行）
	wg := &sync.WaitGroup{}
	errorChan := make(chan error, len(p.asyncStages)+len(fetcherMap))

	for name, wrapper := range fetcherMap {
		wg.Add(1)
		go func(w *fetcherWrapper[T, K], fetcherName string) {
			defer p.Defer()
			defer close(w.done)
			defer wg.Done()

			logs.L().Ctx(ctx).Info("Starting fetcher", zap.String("fetcher", fetcherName))
			err := w.fetcher.Fetch(ctx, p.data, p.metaData)
			w.err = err
			if err != nil && !errors.Is(err, xerror.ErrStageSkip) {
				errorChan <- err
			}
		}(wrapper, name)
	}

	// 3. 启动所有 Operators，每个 Operator 等待自己依赖的 Fetcher 完成
	for _, op := range p.asyncStages {
		wg.Add(1)
		go func(operator Operator[T, K]) {
			defer p.Defer()
			defer wg.Done()

			// 等待此 Operator 依赖的 Fetcher 完成
			deps := operator.Depends()
			for _, dep := range deps {
				if wrapper, ok := fetcherMap[dep.Name()]; ok {
					logs.L().Ctx(ctx).Info("Waiting for dependency",
						zap.String("operator", operator.Name()),
						zap.String("dependency", dep.Name()))
					<-wrapper.done
					// 如果依赖的 Fetcher 出错了，记录日志但继续执行（使用回退机制）
					if wrapper.err != nil {
						logs.L().Ctx(ctx).Warn("Dependency fetcher failed, will use fallback",
							zap.String("operator", operator.Name()),
							zap.String("dependency", dep.Name()),
							zap.Error(wrapper.err))
					}
				}
			}

			// 执行 Operator
			logs.L().Ctx(ctx).Info("Starting operator", zap.String("operator", operator.Name()))
			err := p.runSingleOperator(ctx, operator)
			if err != nil && !errors.Is(err, xerror.ErrStageSkip) {
				errorChan <- err
			}
		}(op)
	}

	// 4. 等待所有完成
	go func() {
		defer close(errorChan)
		wg.Wait()
	}()

	var mergedErr error
	for err := range errorChan {
		if err != nil {
			mergedErr = errors.Wrap(mergedErr, err.Error())
			logs.L().Ctx(ctx).Warn("error in async stages", zap.Error(err))
		}
	}
	return mergedErr
}

// runSingleOperator 运行单个 Operator
func (p *Processor[T, K]) runSingleOperator(ctx context.Context, op Operator[T, K]) error {
	var err error

	err = op.PreRun(ctx, p.data, p.metaData)
	if err != nil {
		if errors.Is(err, xerror.ErrStageSkip) {
			logs.L().Ctx(ctx).Info("Skipped pre run stage", zap.String("stage", op.Name()), zap.Error(err))
		} else {
			trace.SpanFromContext(ctx).RecordError(err)
			logs.L().Ctx(ctx).Error("pre run stage error", zap.String("stage", op.Name()), zap.Error(err))
		}
		return err
	}

	logs.L().Ctx(ctx).Info("Run Handler", zap.String("handler", reflecting.GetFunctionName(op.Run)))
	err = op.Run(ctx, p.data, p.metaData)
	if err != nil {
		if errors.Is(err, xerror.ErrStageSkip) {
			logs.L().Ctx(ctx).Info("run stage skipped", zap.String("stage", op.Name()), zap.Error(err))
		} else {
			trace.SpanFromContext(ctx).RecordError(err)
			logs.L().Ctx(ctx).Error("run stage error", zap.String("stage", op.Name()), zap.Error(err), zap.Stack("stack"))
		}
		return err
	}

	err = op.PostRun(ctx, p.data, p.metaData)
	if err != nil {
		trace.SpanFromContext(ctx).RecordError(err)
		if errors.Is(err, xerror.ErrStageSkip) {
			logs.L().Ctx(ctx).Info("post run stage skipped", zap.String("stage", op.Name()), zap.Error(err))
		} else {
			logs.L().Ctx(ctx).Error("post run stage error", zap.String("stage", op.Name()), zap.Error(err))
		}
		return err
	}

	return nil
}
