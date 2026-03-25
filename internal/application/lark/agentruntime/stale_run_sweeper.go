package agentruntime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"go.uber.org/zap"
)

const (
	defaultStaleRunSweepInterval   = 5 * time.Second
	defaultStaleRunSweepBatchSize  = 100
	defaultLegacyRunStaleThreshold = 30 * time.Minute
	staleRunRepairReason           = "stale_run_timeout"
)

type staleRunSweepRepository interface {
	ListStaleActiveRuns(context.Context, time.Time, time.Time, int) ([]*AgentRun, error)
}

type staleRunRepairer interface {
	RepairStaleRun(context.Context, string, time.Time) (*AgentRun, error)
}

type StaleRunSweeper struct {
	repo     staleRunSweepRepository
	repairer staleRunRepairer
	now      func() time.Time

	interval         time.Duration
	batchSize        int
	legacyStaleAfter time.Duration

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	running atomic.Bool

	statsMu    sync.Mutex
	ticks      int64
	scanned    int64
	repaired   int64
	lastRunID  string
	lastStatus string
	lastError  string
}

func NewStaleRunSweeper(repo staleRunSweepRepository, repairer staleRunRepairer) *StaleRunSweeper {
	ctx, cancel := context.WithCancel(context.Background())
	return &StaleRunSweeper{
		repo:             repo,
		repairer:         repairer,
		now:              func() time.Time { return time.Now().UTC() },
		interval:         defaultStaleRunSweepInterval,
		batchSize:        defaultStaleRunSweepBatchSize,
		legacyStaleAfter: defaultLegacyRunStaleThreshold,
		ctx:              ctx,
		cancel:           cancel,
	}
}

func (w *StaleRunSweeper) Available() bool {
	return w != nil && w.repo != nil && w.repairer != nil
}

func (w *StaleRunSweeper) Start() {
	if w == nil || !w.Available() {
		return
	}
	if !w.running.CompareAndSwap(false, true) {
		return
	}
	w.wg.Add(1)
	go w.run()
}

func (w *StaleRunSweeper) Stop() {
	if w == nil {
		return
	}
	if !w.running.CompareAndSwap(true, false) {
		return
	}
	if w.cancel != nil {
		w.cancel()
	}
	w.wg.Wait()
}

func (w *StaleRunSweeper) Stats() map[string]any {
	if w == nil {
		return nil
	}
	w.statsMu.Lock()
	defer w.statsMu.Unlock()
	return map[string]any{
		"ticks":       w.ticks,
		"scanned":     w.scanned,
		"repaired":    w.repaired,
		"last_run_id": w.lastRunID,
		"last_status": w.lastStatus,
		"last_error":  w.lastError,
	}
}

func (w *StaleRunSweeper) RunOnce(ctx context.Context) {
	w.sweepOnce(ctx)
}

func (w *StaleRunSweeper) run() {
	defer w.wg.Done()
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-w.ctx.Done():
			return
		case <-ticker.C:
			w.sweepOnce(w.ctx)
		}
	}
}

func (w *StaleRunSweeper) sweepOnce(ctx context.Context) {
	if w == nil || !w.Available() {
		return
	}
	now := w.currentTime()
	legacyCutoff := now.Add(-w.normalizedLegacyStaleAfter())

	w.statsMu.Lock()
	w.ticks++
	w.statsMu.Unlock()

	runs, err := w.repo.ListStaleActiveRuns(ctx, now, legacyCutoff, w.normalizedBatchSize())
	if err != nil {
		w.recordError("", "", err)
		if !errors.Is(err, context.Canceled) {
			logs.L().Ctx(ctx).Warn("agent runtime stale run sweeper list failed", zap.Error(err))
		}
		return
	}
	for _, run := range runs {
		if run == nil {
			continue
		}
		w.recordScan(run)
		if !run.IsStaleActive(now, legacyCutoff) {
			continue
		}
		repaired, err := w.repairer.RepairStaleRun(ctx, run.ID, now)
		if err != nil {
			w.recordError(run.ID, string(run.Status), err)
			if !errors.Is(err, context.Canceled) {
				logs.L().Ctx(ctx).Warn("agent runtime stale run repair failed",
					zap.Error(err),
					zap.String("run_id", run.ID),
					zap.String("status", string(run.Status)),
				)
			}
			continue
		}
		if repaired == nil {
			continue
		}
		w.recordRepair(repaired)
	}
}

func (w *StaleRunSweeper) currentTime() time.Time {
	if w != nil && w.now != nil {
		return w.now().UTC()
	}
	return time.Now().UTC()
}

func (w *StaleRunSweeper) normalizedBatchSize() int {
	if w == nil || w.batchSize <= 0 {
		return defaultStaleRunSweepBatchSize
	}
	return w.batchSize
}

func (w *StaleRunSweeper) normalizedLegacyStaleAfter() time.Duration {
	if w == nil || w.legacyStaleAfter <= 0 {
		return defaultLegacyRunStaleThreshold
	}
	return w.legacyStaleAfter
}

func (w *StaleRunSweeper) recordScan(run *AgentRun) {
	if w == nil || run == nil {
		return
	}
	w.statsMu.Lock()
	defer w.statsMu.Unlock()
	w.scanned++
	w.lastRunID = strings.TrimSpace(run.ID)
	w.lastStatus = string(run.Status)
	w.lastError = ""
}

func (w *StaleRunSweeper) recordRepair(run *AgentRun) {
	if w == nil || run == nil {
		return
	}
	w.statsMu.Lock()
	defer w.statsMu.Unlock()
	w.repaired++
	w.lastRunID = strings.TrimSpace(run.ID)
	w.lastStatus = string(run.Status)
	w.lastError = ""
}

func (w *StaleRunSweeper) recordError(runID, status string, err error) {
	if w == nil || err == nil {
		return
	}
	w.statsMu.Lock()
	defer w.statsMu.Unlock()
	w.lastRunID = strings.TrimSpace(runID)
	w.lastStatus = strings.TrimSpace(status)
	w.lastError = err.Error()
}

var errStaleRunRepairNotNeeded = errors.New("agent runtime stale run repair not needed")

func (c *RunCoordinator) RepairStaleRun(ctx context.Context, runID string, repairedAt time.Time) (*AgentRun, error) {
	if c == nil || c.runRepo == nil {
		return nil, fmt.Errorf("run coordinator is nil")
	}
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return nil, nil
	}
	repairedAt = normalizeObservedAt(repairedAt)

	run, err := c.runRepo.GetByID(ctx, runID)
	if err != nil || run == nil {
		return nil, err
	}

	repaired, err := c.runRepo.UpdateStatus(ctx, run.ID, run.Revision, func(current *AgentRun) error {
		if current.Status.IsTerminal() {
			return errStaleRunRepairNotNeeded
		}
		if current.Status != RunStatusRunning {
			return errStaleRunRepairNotNeeded
		}
		current.Status = RunStatusFailed
		current.WaitingReason = WaitingReasonNone
		current.WaitingToken = ""
		current.ErrorText = staleRunRepairReason
		current.UpdatedAt = repairedAt
		current.FinishedAt = &repairedAt
		current.RepairAttempts++
		clearRunExecutionLiveness(current)
		return nil
	})
	if errors.Is(err, errStaleRunRepairNotNeeded) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if repaired == nil {
		return nil, nil
	}
	if err := c.clearFinishedRunState(ctx, repaired.ID, repaired.SessionID, repaired.ActorOpenID, repairedAt); err != nil {
		return nil, err
	}
	return repaired, nil
}
