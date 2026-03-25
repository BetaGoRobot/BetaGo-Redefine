package initial

import (
	"context"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// PendingInitialBacklog reports the current backlog size for pending initial
// scopes and runs.
type PendingInitialBacklog struct {
	PendingScopes int64
	PendingRuns   int64
}

// PendingInitialBacklogSnapshotter loads the current pending backlog for metrics export.
type PendingInitialBacklogSnapshotter interface {
	SnapshotPendingInitialBacklog(context.Context) (PendingInitialBacklog, error)
}

// PendingInitialMetrics accumulates counters for queueing, worker execution, and
// sweeper activity in the pending initial-run pipeline.
type PendingInitialMetrics struct {
	enqueued             atomic.Int64
	enqueueRejected      atomic.Int64
	wakeupEmitted        atomic.Int64
	wakeupConsumed       atomic.Int64
	workerProcessed      atomic.Int64
	workerLockSkipped    atomic.Int64
	workerBusySkipped    atomic.Int64
	workerRequeued       atomic.Int64
	workerErrors         atomic.Int64
	workerScopeCleared   atomic.Int64
	workerWaitNanosTotal atomic.Int64
	workerWaitSamples    atomic.Int64
	sweepTicks           atomic.Int64
	sweepScopesScanned   atomic.Int64
	sweepRescheduled     atomic.Int64
	sweepBusySkipped     atomic.Int64
	sweepStaleCleared    atomic.Int64
	sweepErrors          atomic.Int64
}

// PendingInitialMetricsSnapshot is an immutable copy of the current metrics counters.
type PendingInitialMetricsSnapshot struct {
	Enqueued             int64
	EnqueueRejected      int64
	WakeupEmitted        int64
	WakeupConsumed       int64
	WorkerProcessed      int64
	WorkerLockSkipped    int64
	WorkerBusySkipped    int64
	WorkerRequeued       int64
	WorkerErrors         int64
	WorkerScopeCleared   int64
	WorkerWaitNanosTotal int64
	WorkerWaitSamples    int64
	SweepTicks           int64
	SweepScopesScanned   int64
	SweepRescheduled     int64
	SweepBusySkipped     int64
	SweepStaleCleared    int64
	SweepErrors          int64
}

// PendingInitialMetricsProvider renders the metrics snapshot into the
// application-runtime metrics provider interface.
type PendingInitialMetricsProvider struct {
	metrics     *PendingInitialMetrics
	snapshotter PendingInitialBacklogSnapshotter
}

// NewPendingInitialMetrics constructs a zeroed in-memory metrics accumulator.
func NewPendingInitialMetrics() *PendingInitialMetrics {
	return &PendingInitialMetrics{}
}

// NewPendingInitialMetricsProvider constructs a metrics provider from an
// accumulator and an optional live backlog snapshotter.
func NewPendingInitialMetricsProvider(metrics *PendingInitialMetrics, snapshotter PendingInitialBacklogSnapshotter) *PendingInitialMetricsProvider {
	if metrics == nil {
		metrics = NewPendingInitialMetrics()
	}
	return &PendingInitialMetricsProvider{
		metrics:     metrics,
		snapshotter: snapshotter,
	}
}

// Snapshot copies the current in-memory counters into an immutable snapshot.
func (m *PendingInitialMetrics) Snapshot() PendingInitialMetricsSnapshot {
	if m == nil {
		return PendingInitialMetricsSnapshot{}
	}
	return PendingInitialMetricsSnapshot{
		Enqueued:             m.enqueued.Load(),
		EnqueueRejected:      m.enqueueRejected.Load(),
		WakeupEmitted:        m.wakeupEmitted.Load(),
		WakeupConsumed:       m.wakeupConsumed.Load(),
		WorkerProcessed:      m.workerProcessed.Load(),
		WorkerLockSkipped:    m.workerLockSkipped.Load(),
		WorkerBusySkipped:    m.workerBusySkipped.Load(),
		WorkerRequeued:       m.workerRequeued.Load(),
		WorkerErrors:         m.workerErrors.Load(),
		WorkerScopeCleared:   m.workerScopeCleared.Load(),
		WorkerWaitNanosTotal: m.workerWaitNanosTotal.Load(),
		WorkerWaitSamples:    m.workerWaitSamples.Load(),
		SweepTicks:           m.sweepTicks.Load(),
		SweepScopesScanned:   m.sweepScopesScanned.Load(),
		SweepRescheduled:     m.sweepRescheduled.Load(),
		SweepBusySkipped:     m.sweepBusySkipped.Load(),
		SweepStaleCleared:    m.sweepStaleCleared.Load(),
		SweepErrors:          m.sweepErrors.Load(),
	}
}

// IncEnqueued records that one pending initial run was successfully enqueued.
func (m *PendingInitialMetrics) IncEnqueued() {
	if m != nil {
		m.enqueued.Add(1)
	}
}

// IncEnqueueRejected records that enqueueing a pending initial run was rejected.
func (m *PendingInitialMetrics) IncEnqueueRejected() {
	if m != nil {
		m.enqueueRejected.Add(1)
	}
}

// IncWakeupEmitted records that a wakeup signal was emitted for a pending scope.
func (m *PendingInitialMetrics) IncWakeupEmitted() {
	if m != nil {
		m.wakeupEmitted.Add(1)
	}
}

// IncWakeupConsumed records that a worker consumed a pending-scope wakeup signal.
func (m *PendingInitialMetrics) IncWakeupConsumed() {
	if m != nil {
		m.wakeupConsumed.Add(1)
	}
}

// IncWorkerProcessed records that a worker completed one pending initial run and,
// when available, captures how long that run waited in the queue.
func (m *PendingInitialMetrics) IncWorkerProcessed(wait time.Duration) {
	if m == nil {
		return
	}
	m.workerProcessed.Add(1)
	if wait > 0 {
		m.workerWaitNanosTotal.Add(wait.Nanoseconds())
		m.workerWaitSamples.Add(1)
	}
}

// IncWorkerLockSkipped records that a worker skipped a scope because another worker held the lock.
func (m *PendingInitialMetrics) IncWorkerLockSkipped() {
	if m != nil {
		m.workerLockSkipped.Add(1)
	}
}

// IncWorkerBusySkipped records that a worker deferred a scope because execution capacity was exhausted.
func (m *PendingInitialMetrics) IncWorkerBusySkipped() {
	if m != nil {
		m.workerBusySkipped.Add(1)
	}
}

// IncWorkerRequeued records that a pending run was requeued after a retryable failure.
func (m *PendingInitialMetrics) IncWorkerRequeued() {
	if m != nil {
		m.workerRequeued.Add(1)
	}
}

// IncWorkerErrors records that worker processing failed with a non-success outcome.
func (m *PendingInitialMetrics) IncWorkerErrors() {
	if m != nil {
		m.workerErrors.Add(1)
	}
}

// IncWorkerScopeCleared records that an empty pending scope was cleared.
func (m *PendingInitialMetrics) IncWorkerScopeCleared() {
	if m != nil {
		m.workerScopeCleared.Add(1)
	}
}

// IncSweepTick records one sweeper pass over pending scopes.
func (m *PendingInitialMetrics) IncSweepTick() {
	if m != nil {
		m.sweepTicks.Add(1)
	}
}

// AddSweepScopesScanned records how many scopes were scanned during a sweeper pass.
func (m *PendingInitialMetrics) AddSweepScopesScanned(delta int64) {
	if m != nil && delta > 0 {
		m.sweepScopesScanned.Add(delta)
	}
}

// IncSweepRescheduled records that a scope was rescheduled by the sweeper.
func (m *PendingInitialMetrics) IncSweepRescheduled() {
	if m != nil {
		m.sweepRescheduled.Add(1)
	}
}

// IncSweepBusySkipped records that the sweeper skipped a scope because capacity was still busy.
func (m *PendingInitialMetrics) IncSweepBusySkipped() {
	if m != nil {
		m.sweepBusySkipped.Add(1)
	}
}

// IncSweepStaleCleared records that the sweeper cleared a stale or empty scope.
func (m *PendingInitialMetrics) IncSweepStaleCleared() {
	if m != nil {
		m.sweepStaleCleared.Add(1)
	}
}

// IncSweepErrors records that sweeper processing encountered an error.
func (m *PendingInitialMetrics) IncSweepErrors() {
	if m != nil {
		m.sweepErrors.Add(1)
	}
}

// PrometheusMetrics renders the current pending-initial metrics and backlog
// snapshot as Prometheus text exposition format.
func (p *PendingInitialMetricsProvider) PrometheusMetrics(ctx context.Context) (string, error) {
	if p == nil {
		return "", nil
	}

	snapshot := PendingInitialMetricsSnapshot{}
	if p.metrics != nil {
		snapshot = p.metrics.Snapshot()
	}

	backlog := PendingInitialBacklog{}
	if p.snapshotter != nil {
		current, err := p.snapshotter.SnapshotPendingInitialBacklog(ctx)
		if err != nil {
			return "", err
		}
		backlog = current
	}

	var builder strings.Builder
	writeCounter(&builder, "betago_agent_runtime_pending_initial_enqueued_total", snapshot.Enqueued)
	writeCounter(&builder, "betago_agent_runtime_pending_initial_enqueue_rejected_total", snapshot.EnqueueRejected)
	writeCounter(&builder, "betago_agent_runtime_pending_initial_wakeup_emitted_total", snapshot.WakeupEmitted)
	writeCounter(&builder, "betago_agent_runtime_pending_initial_wakeup_consumed_total", snapshot.WakeupConsumed)
	writeCounter(&builder, "betago_agent_runtime_pending_initial_worker_processed_total", snapshot.WorkerProcessed)
	writeCounter(&builder, "betago_agent_runtime_pending_initial_worker_lock_skipped_total", snapshot.WorkerLockSkipped)
	writeCounter(&builder, "betago_agent_runtime_pending_initial_worker_busy_skipped_total", snapshot.WorkerBusySkipped)
	writeCounter(&builder, "betago_agent_runtime_pending_initial_worker_requeued_total", snapshot.WorkerRequeued)
	writeCounter(&builder, "betago_agent_runtime_pending_initial_worker_errors_total", snapshot.WorkerErrors)
	writeCounter(&builder, "betago_agent_runtime_pending_initial_worker_scope_cleared_total", snapshot.WorkerScopeCleared)
	writeCounter(&builder, "betago_agent_runtime_pending_initial_worker_wait_seconds_count", snapshot.WorkerWaitSamples)
	writeGauge(&builder, "betago_agent_runtime_pending_initial_worker_wait_seconds_sum", float64(snapshot.WorkerWaitNanosTotal)/float64(time.Second))
	writeCounter(&builder, "betago_agent_runtime_pending_initial_sweep_ticks_total", snapshot.SweepTicks)
	writeCounter(&builder, "betago_agent_runtime_pending_initial_sweep_scopes_scanned_total", snapshot.SweepScopesScanned)
	writeCounter(&builder, "betago_agent_runtime_pending_initial_sweep_rescheduled_total", snapshot.SweepRescheduled)
	writeCounter(&builder, "betago_agent_runtime_pending_initial_sweep_busy_skipped_total", snapshot.SweepBusySkipped)
	writeCounter(&builder, "betago_agent_runtime_pending_initial_sweep_stale_cleared_total", snapshot.SweepStaleCleared)
	writeCounter(&builder, "betago_agent_runtime_pending_initial_sweep_errors_total", snapshot.SweepErrors)
	writeGauge(&builder, "betago_agent_runtime_pending_initial_scopes", float64(backlog.PendingScopes))
	writeGauge(&builder, "betago_agent_runtime_pending_initial_runs", float64(backlog.PendingRuns))
	return builder.String(), nil
}

func writeCounter(builder *strings.Builder, name string, value int64) {
	builder.WriteString(name)
	builder.WriteByte(' ')
	builder.WriteString(strconv.FormatInt(value, 10))
	builder.WriteByte('\n')
}

func writeGauge(builder *strings.Builder, name string, value float64) {
	builder.WriteString(name)
	builder.WriteByte(' ')
	builder.WriteString(strconv.FormatFloat(value, 'f', -1, 64))
	builder.WriteByte('\n')
}
