package runtime

import (
	"slices"
	"sync"
	"time"
)

// State 表示运行时为每个模块记录的生命周期/健康状态。
type State string

const (
	// StateUnknown 是启动前的占位状态：模块已经注册，但生命周期钩子还没执行。
	StateUnknown      State = "unknown"
	StateInitializing State = "initializing"
	StateStarting     State = "starting"
	StateReady        State = "ready"
	StateDegraded     State = "degraded"
	StateFailed       State = "failed"
	StateDisabled     State = "disabled"
	StateStopped      State = "stopped"
)

// ComponentStatus 是管理面 HTTP 返回的单模块视图。它保留了 critical
// 属性，便于调用方区分“只是降级的非关键依赖”和“会阻断 readiness 的故障”。
type ComponentStatus struct {
	Name      string         `json:"name"`
	State     State          `json:"state"`
	Critical  bool           `json:"critical"`
	Message   string         `json:"message,omitempty"`
	UpdatedAt time.Time      `json:"updated_at"`
	Stats     map[string]any `json:"stats,omitempty"`
}

// Snapshot 是聚合后的运行时健康视图。
//
// 语义如下：
// - Live：App.Start 全部成功后为 true，App.Stop 前先切回 false；
// - Ready：只有进程 live 且所有 critical 模块都 ready 才为 true；
// - Degraded：只要任意组件是 degraded 或 failed，就会置为 true。
type Snapshot struct {
	Live       bool              `json:"live"`
	Ready      bool              `json:"ready"`
	Degraded   bool              `json:"degraded"`
	UpdatedAt  time.Time         `json:"updated_at"`
	Components []ComponentStatus `json:"components"`
}

// Registry 是进程内运行时健康状态的唯一事实来源。当前部署仍以单实例为主，
// 因此这里刻意保持简单，只维护进程本地内存态。
type Registry struct {
	mu        sync.RWMutex
	live      bool
	updatedAt time.Time
	items     map[string]ComponentStatus
}

// NewRegistry 创建一个空注册表，并初始化时间戳，避免第一份快照出现零值时间。
func NewRegistry() *Registry {
	return &Registry{
		items:     make(map[string]ComponentStatus),
		updatedAt: time.Now(),
	}
}

// SetLive 设置进程级 liveness 标记。
func (r *Registry) SetLive(live bool) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.live = live
	r.updatedAt = time.Now()
}

// Register 在启动前预注册组件，并把它标记为 StateUnknown，使其能提前
// 出现在 health 输出中。
func (r *Registry) Register(name string, critical bool) {
	if r == nil || name == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.items[name]; ok {
		return
	}
	r.items[name] = ComponentStatus{
		Name:      name,
		State:     StateUnknown,
		Critical:  critical,
		UpdatedAt: time.Now(),
	}
	r.updatedAt = time.Now()
}

// Update 更新组件当前状态，并复制一份 stats，避免模块后续继续修改 map
// 时和读取方发生数据竞争。
func (r *Registry) Update(name string, state State, message string, stats map[string]any) {
	if r == nil || name == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	item := r.items[name]
	item.Name = name
	item.State = state
	item.Message = message
	item.UpdatedAt = time.Now()
	item.Stats = cloneStats(stats)
	r.items[name] = item
	r.updatedAt = time.Now()
}

// Snapshot 计算 /readyz 和 /healthz 使用的聚合视图。
// critical 模块参与 readiness 判定；optional 模块只影响 degraded 标志和
// 自己的组件状态。
func (r *Registry) Snapshot() Snapshot {
	if r == nil {
		return Snapshot{}
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	components := make([]ComponentStatus, 0, len(r.items))
	ready := r.live
	degraded := false
	for _, item := range r.items {
		copied := item
		copied.Stats = cloneStats(item.Stats)
		components = append(components, copied)
		if item.Critical && item.State != StateReady {
			ready = false
		}
		if item.State == StateDegraded || item.State == StateFailed {
			degraded = true
		}
	}
	slices.SortFunc(components, func(a, b ComponentStatus) int {
		switch {
		case a.Name < b.Name:
			return -1
		case a.Name > b.Name:
			return 1
		default:
			return 0
		}
	})

	return Snapshot{
		Live:       r.live,
		Ready:      ready,
		Degraded:   degraded,
		UpdatedAt:  r.updatedAt,
		Components: components,
	}
}

// cloneStats 对 stats map 做一次防御性复制，因为模块可能在快照生成后
// 继续并发更新内部计数器。
func cloneStats(stats map[string]any) map[string]any {
	if len(stats) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(stats))
	for key, value := range stats {
		cloned[key] = value
	}
	return cloned
}
