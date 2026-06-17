package chatmetrics

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal"
	appruntime "github.com/BetaGoRobot/BetaGo-Redefine/internal/runtime"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"go.uber.org/zap"
)

const (
	defaultInterval = 5 * time.Minute
	defaultTimeout  = 2 * time.Minute
)

type ModuleOptions struct {
	Interval     time.Duration
	Timeout      time.Duration
	RecentWindow time.Duration
	Collector    Collector
}

type Module struct {
	interval time.Duration
	timeout  time.Duration

	collector Collector

	mu          sync.Mutex
	cancel      context.CancelFunc
	lastRun     time.Time
	lastError   string
	lastChatNum int
}

func NewModule(opts ModuleOptions) *Module {
	interval := opts.Interval
	if interval <= 0 {
		interval = defaultInterval
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	collector := opts.Collector
	if collector.ListChats == nil {
		collector.ListChats = ListBotChats
	}
	if collector.CountMembers == nil {
		collector.CountMembers = CountChatMembers
	}
	if collector.CountRecentMessages == nil {
		collector.CountRecentMessages = func(context.Context, string, time.Time) (int, error) {
			return 0, errors.New("recent message counter is not configured")
		}
	}
	if collector.Record == nil {
		collector.Record = RecordVictoriaMetrics
	}
	if collector.RecentWindow <= 0 {
		collector.RecentWindow = opts.RecentWindow
	}
	if collector.RecentWindow <= 0 {
		collector.RecentWindow = DefaultRecentWindow
	}
	return &Module{
		interval:  interval,
		timeout:   timeout,
		collector: collector,
	}
}

func (m *Module) Name() string {
	return "lark_chat_metrics"
}

func (m *Module) Critical() bool {
	return false
}

func (m *Module) Init(context.Context) error {
	if lark_dal.Client() == nil {
		return errors.Join(appruntime.ErrDisabled, errors.New("lark client unavailable"))
	}
	return nil
}

func (m *Module) Start(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	m.mu.Lock()
	m.cancel = cancel
	m.mu.Unlock()

	go m.loop(runCtx)
	return nil
}

func (m *Module) Ready(context.Context) error {
	return nil
}

func (m *Module) Stop(context.Context) error {
	m.mu.Lock()
	cancel := m.cancel
	m.cancel = nil
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

func (m *Module) Stats() map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	return map[string]any{
		"interval":        m.interval.String(),
		"recent_window":   m.collector.RecentWindow.String(),
		"last_run":        m.lastRun.Format(time.RFC3339),
		"last_error":      m.lastError,
		"last_chat_count": m.lastChatNum,
	}
}

func (m *Module) loop(ctx context.Context) {
	m.runOnce(ctx)

	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.runOnce(ctx)
		}
	}
}

func (m *Module) runOnce(ctx context.Context) {
	collector := m.collector
	reported := 0
	record := collector.Record
	collector.Record = func(snapshot Snapshot) {
		reported++
		record(snapshot)
	}
	err := collector.Collect(ctx)

	m.mu.Lock()
	m.lastRun = time.Now()
	m.lastChatNum = reported
	if err != nil {
		m.lastError = err.Error()
	} else {
		m.lastError = ""
	}
	m.mu.Unlock()

	if err != nil {
		logs.L().Ctx(ctx).Warn("lark chat metrics collection finished with errors", zap.Error(err))
	}
}
