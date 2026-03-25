package initial

import (
	"context"
	"strings"
	"testing"
	"time"
)

type staticPendingInitialBacklogSnapshotter struct {
	backlog PendingInitialBacklog
}

func (s staticPendingInitialBacklogSnapshotter) SnapshotPendingInitialBacklog(context.Context) (PendingInitialBacklog, error) {
	return s.backlog, nil
}

func TestPendingInitialMetricsProviderPrometheusMetricsIncludesCountersAndBacklog(t *testing.T) {
	metrics := NewPendingInitialMetrics()
	metrics.IncEnqueued()
	metrics.IncWakeupEmitted()
	metrics.IncWakeupConsumed()
	metrics.IncWorkerProcessed(2 * time.Second)
	metrics.IncSweepTick()
	metrics.IncSweepRescheduled()

	provider := NewPendingInitialMetricsProvider(metrics, staticPendingInitialBacklogSnapshotter{
		backlog: PendingInitialBacklog{
			PendingScopes: 2,
			PendingRuns:   3,
		},
	})

	body, err := provider.PrometheusMetrics(context.Background())
	if err != nil {
		t.Fatalf("PrometheusMetrics() error = %v", err)
	}
	for _, want := range []string{
		"betago_agent_runtime_pending_initial_enqueued_total 1",
		"betago_agent_runtime_pending_initial_wakeup_emitted_total 1",
		"betago_agent_runtime_pending_initial_wakeup_consumed_total 1",
		"betago_agent_runtime_pending_initial_worker_processed_total 1",
		"betago_agent_runtime_pending_initial_worker_wait_seconds_sum 2",
		"betago_agent_runtime_pending_initial_worker_wait_seconds_count 1",
		"betago_agent_runtime_pending_initial_sweep_ticks_total 1",
		"betago_agent_runtime_pending_initial_sweep_rescheduled_total 1",
		"betago_agent_runtime_pending_initial_scopes 2",
		"betago_agent_runtime_pending_initial_runs 3",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("PrometheusMetrics() body = %q, want contain %q", body, want)
		}
	}
}
