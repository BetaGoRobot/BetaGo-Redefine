package initial

import (
	"context"
	"errors"
	"iter"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	initialcore "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime/initialcore"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	uuid "github.com/satori/go.uuid"
	"go.uber.org/zap"
)

const (
	defaultPollTimeout  = time.Second
	defaultScopeLockTTL = 30 * time.Second
	defaultRetryDelay   = 200 * time.Millisecond
	defaultMaxQueueAge  = 48 * time.Hour
)

// RunStore defines a initial-run flow contract.
type RunStore interface {
	DequeuePendingInitialScope(context.Context, time.Duration) (string, string, error)
	ConsumePendingInitialRun(context.Context, string, string) ([]byte, int64, error)
	PrependPendingInitialRun(context.Context, string, string, []byte) error
	NotifyPendingInitialRun(context.Context, string, string) error
	ClearPendingInitialScopeIfEmpty(context.Context, string, string) error
	PendingInitialRunCount(context.Context, string, string) (int64, error)
	AcquirePendingInitialScopeLock(context.Context, string, string, string, time.Duration) (bool, error)
	ReleasePendingInitialScopeLock(context.Context, string, string, string) (bool, error)
}

// RunProcessor defines a initial-run flow contract.
type RunProcessor interface {
	ProcessRun(context.Context, agentruntime.RunProcessorInput) error
}

// RunStatusUpdater defines a initial-run flow contract.
type RunStatusUpdater interface {
	MarkStarted(context.Context, PendingRun) error
	MarkExpired(context.Context, PendingRun) error
}

type lastActivity struct {
	chatID      string
	actorOpenID string
	lastError   string
}

func (a lastActivity) stats() map[string]any {
	return map[string]any{
		"last_chat_id":    a.chatID,
		"last_actor_open": a.actorOpenID,
		"last_error":      a.lastError,
	}
}

func (a *lastActivity) record(chatID, actorOpenID string, err error) {
	if a == nil {
		return
	}
	a.chatID = chatID
	a.actorOpenID = actorOpenID
	if err == nil {
		a.lastError = ""
		return
	}
	a.lastError = err.Error()
}

// RunWorker carries initial-run flow state.
type RunWorker struct {
	core    *initialcore.Worker[PendingRun]
	workers int

	metrics *PendingInitialMetrics
	wg      sync.WaitGroup

	running atomic.Bool

	statsMu       sync.Mutex
	processed     int64
	skippedLocked int64
	last          lastActivity
}

// NewRunWorker implements initial-run flow behavior.
func NewRunWorker(store RunStore, processor RunProcessor) *RunWorker {
	return NewRunWorkerWithMetricsAndStatusUpdater(store, processor, nil, nil)
}

// NewRunWorkerWithMetrics implements initial-run flow behavior.
func NewRunWorkerWithMetrics(store RunStore, processor RunProcessor, metrics *PendingInitialMetrics) *RunWorker {
	return NewRunWorkerWithMetricsAndStatusUpdater(store, processor, metrics, nil)
}

// NewRunWorkerWithMetricsAndStatusUpdater implements initial-run flow behavior.
func NewRunWorkerWithMetricsAndStatusUpdater(
	store RunStore,
	processor RunProcessor,
	metrics *PendingInitialMetrics,
	statusUpdater RunStatusUpdater,
) *RunWorker {
	worker := &RunWorker{
		metrics: metrics,
		workers: 1,
	}
	worker.core = initialcore.NewWorker[PendingRun](
		store,
		newWorkerCallbacks(worker, processor, statusUpdater),
		"pending_initial_worker_"+uuid.NewV4().String(),
		defaultPollTimeout,
		defaultScopeLockTTL,
		defaultRetryDelay,
	)
	return worker
}

// Available implements initial-run flow behavior.
func (w *RunWorker) Available() bool {
	return w != nil && w.core != nil && w.core.Available()
}

func (w *RunWorker) WithWorkers(workers int) *RunWorker {
	if w == nil {
		return nil
	}
	if workers > 0 {
		w.workers = workers
		return w
	}
	w.workers = 1
	return w
}

// Start launches the resume worker loop in the background.
func (w *RunWorker) Start() {
	if w == nil || !w.Available() {
		return
	}
	if !w.running.CompareAndSwap(false, true) {
		return
	}

	workers := w.configuredWorkers()
	w.wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer w.wg.Done()
			w.core.Run()
		}()
	}
}

// Stop stops the resume worker loop and waits for in-flight polling to exit.
func (w *RunWorker) Stop() {
	if w == nil {
		return
	}
	if !w.running.CompareAndSwap(true, false) {
		return
	}
	if w.core != nil {
		w.core.Stop()
	}
	w.wg.Wait()
}

// Stats implements initial-run flow behavior.
func (w *RunWorker) Stats() map[string]any {
	if w == nil {
		return nil
	}

	w.statsMu.Lock()
	defer w.statsMu.Unlock()
	stats := map[string]any{
		"workers":        int64(w.configuredWorkers()),
		"processed":      w.processed,
		"skipped_locked": w.skippedLocked,
	}
	for key, value := range w.last.stats() {
		stats[key] = value
	}
	return stats
}

func (w *RunWorker) configuredWorkers() int {
	if w == nil || w.workers <= 0 {
		return 5
	}
	return w.workers
}

func (w *RunWorker) recordProcessed(chatID, actorOpenID string) {
	w.statsMu.Lock()
	defer w.statsMu.Unlock()
	w.processed++
	w.last.record(chatID, actorOpenID, nil)
}

func (w *RunWorker) recordSkipped(chatID, actorOpenID string) {
	w.statsMu.Lock()
	defer w.statsMu.Unlock()
	w.skippedLocked++
	w.last.record(chatID, actorOpenID, nil)
}

func (w *RunWorker) recordError(chatID, actorOpenID string, err error) {
	if w == nil || err == nil {
		return
	}
	w.statsMu.Lock()
	defer w.statsMu.Unlock()
	w.last.record(chatID, actorOpenID, err)
}

// LarkRunStatusUpdater carries initial-run flow state.
type LarkRunStatusUpdater struct {
	patchAgentic func(context.Context, larkmsg.AgentStreamingCardRefs, iter.Seq[*ark_dal.ModelStreamRespReasoning]) (larkmsg.AgentStreamingCardRefs, error)
}

// NewLarkRunStatusUpdater implements initial-run flow behavior.
func NewLarkRunStatusUpdater() *LarkRunStatusUpdater {
	return &LarkRunStatusUpdater{patchAgentic: larkmsg.PatchAgentStreamingCardWithRefs}
}

// NewLarkRunStatusUpdaterForTest implements initial-run flow behavior.
func NewLarkRunStatusUpdaterForTest(
	patchAgentic func(context.Context, larkmsg.AgentStreamingCardRefs, iter.Seq[*ark_dal.ModelStreamRespReasoning]) (larkmsg.AgentStreamingCardRefs, error),
) *LarkRunStatusUpdater {
	return &LarkRunStatusUpdater{patchAgentic: patchAgentic}
}

// MarkStarted implements initial-run flow behavior.
func (u *LarkRunStatusUpdater) MarkStarted(ctx context.Context, item PendingRun) error {
	if u == nil || u.patchAgentic == nil {
		return nil
	}
	root := RootTarget{
		MessageID: strings.TrimSpace(item.RootTarget.MessageID),
		CardID:    strings.TrimSpace(item.RootTarget.CardID),
	}
	if root.CardID == "" {
		return nil
	}
	_, err := u.patchAgentic(ctx, larkmsg.AgentStreamingCardRefs{
		MessageID: root.MessageID,
		CardID:    root.CardID,
	}, initialcore.StartedSeq(initialcore.StartedThought, initialcore.StartedReply))
	return err
}

// MarkExpired patches the pending card so users can see the queue item timed out
// instead of waiting indefinitely.
func (u *LarkRunStatusUpdater) MarkExpired(ctx context.Context, item PendingRun) error {
	if u == nil || u.patchAgentic == nil {
		return nil
	}
	root := RootTarget{
		MessageID: strings.TrimSpace(item.RootTarget.MessageID),
		CardID:    strings.TrimSpace(item.RootTarget.CardID),
	}
	if root.CardID == "" {
		return nil
	}
	_, err := u.patchAgentic(ctx, larkmsg.AgentStreamingCardRefs{
		MessageID: root.MessageID,
		CardID:    root.CardID,
	}, expiredSeq("排队超时。", "等待时间过长，已停止继续排队，请重新发起任务。"))
	return err
}

func newWorkerCallbacks(
	worker *RunWorker,
	processor RunProcessor,
	statusUpdater RunStatusUpdater,
) initialcore.WorkerCallbacks[PendingRun] {
	return initialcore.WorkerCallbacks[PendingRun]{
		Decode:   UnmarshalPendingRun,
		Validate: func(item PendingRun) error { return item.Validate() },
		ApplyContext: func(ctx context.Context, item PendingRun) context.Context {
			return item.ApplyRootTarget(ctx)
		},
		MarkStarted:      markWorkerStarted(statusUpdater),
		Process:          processWorkerItemWithStatusUpdater(processor, statusUpdater),
		RequestedAt:      func(item PendingRun) time.Time { return item.RequestedAt },
		Retryable:        func(err error) bool { return errors.Is(err, agentruntime.ErrRunSlotOccupied) },
		OnWakeupConsumed: func() { worker.incWakeupConsumed() },
		OnProcessed: func(scope initialcore.Scope, waitDuration time.Duration) {
			worker.recordProcessedScope(scope, waitDuration)
		},
		OnLockSkipped:  func(scope initialcore.Scope) { worker.recordSkippedScope(scope) },
		OnError:        func(scope initialcore.Scope, err error) { worker.recordWorkerError(scope, err) },
		OnRequeued:     func(scope initialcore.Scope) { worker.incWorkerRequeued() },
		OnScopeCleared: func(scope initialcore.Scope) { worker.incWorkerScopeCleared() },
	}
}

func markWorkerStarted(statusUpdater RunStatusUpdater) func(context.Context, PendingRun) error {
	return func(ctx context.Context, item PendingRun) error {
		if statusUpdater == nil {
			return nil
		}
		return statusUpdater.MarkStarted(ctx, item)
	}
}

func processWorkerItem(processor RunProcessor) func(context.Context, PendingRun) error {
	return processWorkerItemWithStatusUpdater(processor, nil)
}

func processWorkerItemWithStatusUpdater(processor RunProcessor, statusUpdater RunStatusUpdater) func(context.Context, PendingRun) error {
	return func(ctx context.Context, item PendingRun) error {
		if pendingRunExpired(item, time.Now().UTC()) {
			if statusUpdater != nil {
				_ = statusUpdater.MarkExpired(ctx, item)
			}
			return nil
		}
		if processor == nil {
			return nil
		}
		initial := item.BuildInitialRunInput(time.Now().UTC())
		return processor.ProcessRun(ctx, agentruntime.RunProcessorInput{Initial: &initial})
	}
}

func pendingRunExpired(item PendingRun, now time.Time) bool {
	requestedAt := item.RequestedAt
	if requestedAt.IsZero() {
		return false
	}
	return now.UTC().Sub(requestedAt.UTC()) >= defaultMaxQueueAge
}

func expiredSeq(thoughtText, replyText string) iter.Seq[*ark_dal.ModelStreamRespReasoning] {
	return func(yield func(*ark_dal.ModelStreamRespReasoning) bool) {
		yield(&ark_dal.ModelStreamRespReasoning{
			ContentStruct: ark_dal.ContentStruct{
				Thought: strings.TrimSpace(thoughtText),
				Reply:   strings.TrimSpace(replyText),
			},
		})
	}
}

func (w *RunWorker) incWakeupConsumed() {
	if w.metrics != nil {
		w.metrics.IncWakeupConsumed()
	}
}

func (w *RunWorker) recordProcessedScope(scope initialcore.Scope, waitDuration time.Duration) {
	if w.metrics != nil {
		w.metrics.IncWorkerProcessed(waitDuration)
	}
	w.recordProcessed(scope.ChatID, scope.ActorOpenID)
}

func (w *RunWorker) recordSkippedScope(scope initialcore.Scope) {
	if w.metrics != nil {
		w.metrics.IncWorkerLockSkipped()
	}
	w.recordSkipped(scope.ChatID, scope.ActorOpenID)
}

func (w *RunWorker) recordWorkerError(scope initialcore.Scope, err error) {
	if w.metrics != nil {
		w.metrics.IncWorkerErrors()
	}
	w.recordError(scope.ChatID, scope.ActorOpenID, err)
	if err != nil && !errors.Is(err, context.Canceled) {
		logs.L().Warn("agent runtime pending initial worker error",
			zap.Error(err),
			zap.String("chat_id", scope.ChatID),
			zap.String("actor_open_id", scope.ActorOpenID),
		)
	}
}

func (w *RunWorker) incWorkerRequeued() {
	if w.metrics != nil {
		w.metrics.IncWorkerRequeued()
	}
}

func (w *RunWorker) incWorkerScopeCleared() {
	if w.metrics != nil {
		w.metrics.IncWorkerScopeCleared()
	}
}
