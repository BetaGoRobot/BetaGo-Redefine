package xhandler

import (
	"context"
	"fmt"
	"reflect"
	"sync"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/intentmeta"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xerror"
	"github.com/BetaGoRobot/go_utils/reflecting"
	"github.com/pkg/errors"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

type Stage[T, K any] interface {
	Name() string

	PreRun(context.Context, *T, *K) error
	Run(context.Context, *T, *K) error
	PostRun(context.Context, *T, *K) error

	MetaInit() *K

	// Depends 返回此 Stage 依赖的其他 Stage 实例列表
	Depends() []Stage[T, K]

	// FeatureInfo 返回功能信息，返回 nil 表示此 Stage 不受功能开关控制
	FeatureInfo() *FeatureInfo
}

type Operator[T, K any] = Stage[T, K]

// FeatureInfo 功能信息
type FeatureInfo struct {
	ID          string // 功能唯一标识
	Name        string // 功能名称
	Description string // 功能描述
	Default     bool   // 默认是否开启
}

// FeatureCheckFunc 功能检查函数类型
type FeatureCheckFunc func(ctx context.Context, featureID string, defaultEnabled bool, chatID, openID string) bool

// MetaDataWithOpenID 可以获取 chatID 和 openID 的 meta 接口
type MetaDataWithOpenID interface {
	GetChatID() string
	GetOpenID() string
}

type (
	StageBase[T, K any]    struct{}
	OperatorBase[T, K any] = StageBase[T, K]
	BaseMetaData           struct {
		mu sync.RWMutex

		ChatID      string
		OpenID      string
		IsP2P       bool
		Refresh     bool
		IsCommand   bool
		MainCommand string
		TraceID     string

		ForceReplyDirect   bool
		SkipDone           bool
		Extra              map[string]string
		LastReplyMessageID string
		LastReplyKind      string
		intentAnalysis     *intentmeta.IntentAnalysis

		// TODO: 暂时没有用上，后续改造替换掉st、et的反复解析，搞成通用参数
		StartTime string
		EndTime   string
	}
)

func (m *BaseMetaData) GetExtra(key string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.Extra == nil {
		return "", false
	}
	val, ok := m.Extra[key]
	return val, ok
}

func (m *BaseMetaData) SetExtra(key string, val string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Extra == nil {
		m.Extra = make(map[string]string)
	}
	m.Extra[key] = val
}

// SetIntentAnalysis stores the typed intent result and synchronizes the derived interaction mode.
func (m *BaseMetaData) SetIntentAnalysis(analysis *intentmeta.IntentAnalysis) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.intentAnalysis = cloneIntentAnalysis(analysis)
	if m.intentAnalysis == nil {
		return
	}
}

// GetIntentAnalysis returns a defensive copy of the stored intent result.
func (m *BaseMetaData) GetIntentAnalysis() (*intentmeta.IntentAnalysis, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.intentAnalysis == nil {
		return nil, false
	}
	return cloneIntentAnalysis(m.intentAnalysis), true
}

func cloneIntentAnalysis(src *intentmeta.IntentAnalysis) *intentmeta.IntentAnalysis {
	if src == nil {
		return nil
	}
	cloned := *src
	return &cloned
}

func (m *BaseMetaData) SetIsCommand(v bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.IsCommand = v
}

func (m *BaseMetaData) IsCommandMarked() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.IsCommand
}

func (m *BaseMetaData) SetMainCommand(command string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.MainCommand = command
}

func (m *BaseMetaData) GetMainCommand() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.MainCommand
}

func (m *BaseMetaData) SetSkipDone(v bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.SkipDone = v
}

func (m *BaseMetaData) ShouldSkipDone() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.SkipDone
}

func (m *BaseMetaData) SetLastReplyRef(messageID, kind string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.LastReplyMessageID = messageID
	m.LastReplyKind = kind
}

func (m *BaseMetaData) LastReplyRef() (messageID, kind string) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.LastReplyMessageID, m.LastReplyKind
}

// GetChatID 实现 MetaDataWithOpenID 接口
func (m *BaseMetaData) GetChatID() string {
	return m.ChatID
}

// GetOpenID 实现 MetaDataWithOpenID 接口
func (m *BaseMetaData) GetOpenID() string {
	return m.OpenID
}

type (
	ProcPanicFunc[T, K any] func(context.Context, error, *T, *K)
	ProcDeferFunc[T, K any] func(context.Context, *T, *K)
	MetaInitFunc[T, K any]  func(*T) *K
	Processor[T, K any]     struct {
		context.Context

		data           *T
		metaData       *K
		asyncStages    []Stage[T, K]
		features       map[string]FeatureInfo // 自动收集的功能信息
		onPanicFn      ProcPanicFunc[T, K]
		deferFn        []ProcDeferFunc[T, K]
		metaInitFn     MetaInitFunc[T, K]
		preRunFn       func(p *Processor[T, K])
		featureChecker FeatureCheckFunc // 功能检查函数（依赖注入）
	}
)

func (op *StageBase[T, K]) Name() string {
	return "NotImplementBaseName"
}

func (op *StageBase[T, K]) PreRun(context.Context, *T, *K) error {
	return nil
}

func (op *StageBase[T, K]) Run(context.Context, *T, *K) error {
	return nil
}

func (op *StageBase[T, K]) PostRun(context.Context, *T, *K) error {
	return nil
}

func (op *StageBase[T, K]) MetaInit() *K {
	return new(K)
}

// Depends 默认实现：无依赖
func (op *StageBase[T, K]) Depends() []Stage[T, K] {
	return nil
}

// FeatureInfo 默认实现：返回 nil，表示不受功能开关控制
func (op *StageBase[T, K]) FeatureInfo() *FeatureInfo {
	return nil
}

func (p *Processor[T, K]) WithCtx(ctx context.Context) *Processor[T, K] {
	p.Context = ctx
	return p
}

func (p *Processor[T, K]) Clone() *Processor[T, K] {
	if p == nil {
		return &Processor[T, K]{}
	}

	cloned := *p
	cloned.Context = nil
	cloned.data = nil
	cloned.metaData = nil

	if p.asyncStages != nil {
		cloned.asyncStages = append([]Operator[T, K](nil), p.asyncStages...)
	}
	if p.deferFn != nil {
		cloned.deferFn = append([]ProcDeferFunc[T, K](nil), p.deferFn...)
	}
	if p.features != nil {
		cloned.features = make(map[string]FeatureInfo, len(p.features))
		for key, value := range p.features {
			cloned.features[key] = value
		}
	}

	return &cloned
}

func (p *Processor[T, K]) NewExecution() *Processor[T, K] {
	return p.Clone()
}

// WithFeatureChecker 设置功能检查函数（依赖注入）
func (p *Processor[T, K]) WithFeatureChecker(checker FeatureCheckFunc) *Processor[T, K] {
	p.featureChecker = checker
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

func (p *Processor[T, K]) MetaData() *K {
	return p.metaData
}

func (p *Processor[T, K]) Clean() *Processor[T, K] {
	return p.NewExecution()
}

func (p *Processor[T, K]) Defer() {
	if recovered := recover(); recovered != nil {
		if p.onPanicFn != nil {
			p.onPanicFn(p.Context, panicAsError(recovered), p.data, p.metaData)
		}
	}
}

func panicAsError(recovered any) error {
	if err, ok := recovered.(error); ok {
		return err
	}
	return fmt.Errorf("panic: %v", recovered)
}

// AddAsync  添加并行处理阶段
//
//	@receiver p
//	@param stage
//	@return *Processor[T]
func (p *Processor[T, K]) AddAsync(stage Stage[T, K]) *Processor[T, K] {
	p.asyncStages = append(p.asyncStages, stage)
	p.collectFeatureInfo(stage)
	return p
}

// collectFeatureInfo 收集 Stage 的 FeatureInfo
func (p *Processor[T, K]) collectFeatureInfo(stage Stage[T, K]) {
	if fi := stage.FeatureInfo(); fi != nil {
		if p.features == nil {
			p.features = make(map[string]FeatureInfo)
		}
		p.features[fi.ID] = *fi
	}
}

// ListFeatures 列出所有收集到的功能信息
func (p *Processor[T, K]) ListFeatures() []FeatureInfo {
	list := make([]FeatureInfo, 0, len(p.features))
	for _, fi := range p.features {
		list = append(list, fi)
	}
	return list
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
	_ = p.RunParallelStages()
}

type stageWrapper[T, K any] struct {
	stage Stage[T, K]
	deps  []*stageWrapper[T, K]
	done  chan struct{}
	err   error
}

// RunParallelStages  运行并行处理阶段（考虑依赖关系）
//
//	@receiver p
//	@param ctx
//	@param event
//	@return error
func (p *Processor[T, K]) RunParallelStages() error {
	var span trace.Span
	p.Context, span = otel.Start(p.Context)
	defer span.End()

	stageMap, err := p.compileStageDAG()
	if err != nil {
		return err
	}

	wg := &sync.WaitGroup{}
	errorChan := make(chan error, len(stageMap))

	for name, wrapper := range stageMap {
		wg.Add(1)
		go func(w *stageWrapper[T, K], stageName string) {
			defer p.Defer()
			defer close(w.done)
			defer wg.Done()

			for _, dep := range w.deps {
				logs.L().Ctx(p).Info("Waiting for dependency",
					zap.String("stage", stageName),
					zap.String("dependency", dep.stage.Name()))
				<-dep.done
				if dep.err != nil {
					if errors.Is(dep.err, xerror.ErrStageSkip) {
						continue
					}
					w.err = errors.Wrap(dep.err, stageName+" blocked by dependency "+dep.stage.Name())
					errorChan <- w.err
					logs.L().Ctx(p).Warn("Dependency stage failed, blocking downstream stage",
						zap.String("stage", stageName),
						zap.String("dependency", dep.stage.Name()),
						zap.Error(dep.err))
					return
				}
			}

			logs.L().Ctx(p).Info("Starting stage", zap.String("stage", stageName))
			err := p.runSingleStage(p, w.stage)
			w.err = err
			if err != nil && !errors.Is(err, xerror.ErrStageSkip) {
				errorChan <- err
			}
		}(wrapper, name)
	}

	go func() {
		defer close(errorChan)
		wg.Wait()
	}()

	var mergedErr error
	for err := range errorChan {
		if err != nil {
			if mergedErr == nil {
				mergedErr = err
			} else {
				mergedErr = errors.Wrap(mergedErr, err.Error())
			}
			logs.L().Ctx(p).Warn("error in async stages", zap.Error(err))
		}
	}
	return mergedErr
}

func (p *Processor[T, K]) compileStageDAG() (map[string]*stageWrapper[T, K], error) {
	nodes := make(map[string]*stageWrapper[T, K], len(p.asyncStages))
	visiting := make(map[string]bool, len(p.asyncStages))

	var visit func(Stage[T, K]) error
	visit = func(stage Stage[T, K]) error {
		if stage == nil {
			return errors.New("nil stage")
		}
		name := stage.Name()
		if name == "" {
			return errors.New("stage name cannot be empty")
		}
		if visiting[name] {
			return errors.Errorf("dependency cycle detected at stage %q", name)
		}
		if existing, ok := nodes[name]; ok {
			if stageIdentity(existing.stage) != stageIdentity(stage) {
				return errors.Errorf("duplicate stage name %q with different instances", name)
			}
			return nil
		}

		visiting[name] = true
		node := &stageWrapper[T, K]{
			stage: stage,
			done:  make(chan struct{}),
		}
		nodes[name] = node
		for _, dep := range stage.Depends() {
			if err := visit(dep); err != nil {
				delete(visiting, name)
				return err
			}
			node.deps = append(node.deps, nodes[dep.Name()])
		}
		delete(visiting, name)
		return nil
	}

	for _, stage := range p.asyncStages {
		if err := visit(stage); err != nil {
			return nil, err
		}
	}
	return nodes, nil
}

func stageIdentity[T, K any](stage Stage[T, K]) string {
	value := reflect.ValueOf(any(stage))
	if !value.IsValid() {
		return "<nil>"
	}
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Map, reflect.Pointer, reflect.Slice, reflect.UnsafePointer:
		return fmt.Sprintf("%T@0x%x", stage, value.Pointer())
	default:
		return fmt.Sprintf("%T:%v", stage, stage)
	}
}

// runSingleStage 运行单个 Stage
func (p *Processor[T, K]) runSingleStage(ctx context.Context, stage Stage[T, K]) error {
	var err error

	// 自动检查功能开关
	if fi := stage.FeatureInfo(); fi != nil && p.featureChecker != nil {
		var chatID, openID string
		if metaWithOpenID, ok := any(p.metaData).(MetaDataWithOpenID); ok {
			chatID = metaWithOpenID.GetChatID()
			openID = metaWithOpenID.GetOpenID()
		}
		if !p.featureChecker(ctx, fi.ID, fi.Default, chatID, openID) {
			return errors.Wrap(xerror.ErrStageSkip, stage.Name()+" feature blocked")
		}
	}

	err = stage.PreRun(ctx, p.data, p.metaData)
	if err != nil {
		if errors.Is(err, xerror.ErrStageSkip) {
			logs.L().Ctx(ctx).Info("Skipped pre run stage", zap.String("stage", stage.Name()), zap.Error(err))
		} else {
			otel.RecordError(trace.SpanFromContext(ctx), err)
			logs.L().Ctx(ctx).Error("pre run stage error", zap.String("stage", stage.Name()), zap.Error(err))
		}
		return err
	}

	logs.L().Ctx(ctx).Info("Run Handler", zap.String("handler", reflecting.GetFunctionName(stage.Run)))
	err = stage.Run(ctx, p.data, p.metaData)
	if err != nil {
		if errors.Is(err, xerror.ErrStageSkip) {
			logs.L().Ctx(ctx).Info("run stage skipped", zap.String("stage", stage.Name()), zap.Error(err))
		} else {
			otel.RecordError(trace.SpanFromContext(ctx), err)
			logs.L().Ctx(ctx).Error("run stage error", zap.String("stage", stage.Name()), zap.Error(err), zap.Stack("stack"))
		}
		return err
	}

	err = stage.PostRun(ctx, p.data, p.metaData)
	if err != nil {
		otel.RecordError(trace.SpanFromContext(ctx), err)
		if errors.Is(err, xerror.ErrStageSkip) {
			logs.L().Ctx(ctx).Info("post run stage skipped", zap.String("stage", stage.Name()), zap.Error(err))
		} else {
			logs.L().Ctx(ctx).Error("post run stage error", zap.String("stage", stage.Name()), zap.Error(err))
		}
		return err
	}

	return nil
}
