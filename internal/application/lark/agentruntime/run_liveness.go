package agentruntime

import (
	"context"
	"strings"
	"time"
)

// RunLivenessState classifies whether an active run still has a live executor.
type RunLivenessState string

const (
	RunLivenessUnknown RunLivenessState = "unknown"
	RunLivenessHealthy RunLivenessState = "healthy"
	RunLivenessExpired RunLivenessState = "expired"
)

// RunLeasePolicy describes how long one executor may keep a run active without
// refreshing its heartbeat.
type RunLeasePolicy struct {
	TTL               time.Duration `json:"ttl,omitempty"`
	HeartbeatInterval time.Duration `json:"heartbeat_interval,omitempty"`
}

func DefaultRunLeasePolicy() RunLeasePolicy {
	return RunLeasePolicy{
		TTL:               defaultExecutionLeaseTTL,
		HeartbeatInterval: defaultExecutionLeaseRenewInterval,
	}
}

func (p RunLeasePolicy) Normalize() RunLeasePolicy {
	normalized := p
	if normalized.TTL <= 0 {
		normalized.TTL = defaultExecutionLeaseTTL
	}
	if normalized.HeartbeatInterval <= 0 {
		normalized.HeartbeatInterval = defaultExecutionLeaseRenewInterval
	}
	if normalized.HeartbeatInterval >= normalized.TTL {
		normalized.HeartbeatInterval = normalized.TTL / 2
		if normalized.HeartbeatInterval <= 0 {
			normalized.HeartbeatInterval = normalized.TTL
		}
	}
	return normalized
}

func (r *AgentRun) HasExecutionLease() bool {
	if r == nil {
		return false
	}
	return strings.TrimSpace(r.WorkerID) != "" || r.HeartbeatAt != nil || r.LeaseExpiresAt != nil
}

func (r *AgentRun) IsLeaseExpired(now time.Time) bool {
	if r == nil || r.LeaseExpiresAt == nil {
		return false
	}
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	return !now.Before(r.LeaseExpiresAt.UTC())
}

func (r *AgentRun) NeedsHeartbeat(now time.Time, policy RunLeasePolicy) bool {
	if r == nil || !r.HasExecutionLease() {
		return false
	}
	policy = policy.Normalize()
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	if r.HeartbeatAt == nil {
		return true
	}
	return !now.Before(r.HeartbeatAt.UTC().Add(policy.HeartbeatInterval))
}

func (r *AgentRun) LivenessState(now time.Time) RunLivenessState {
	if r == nil || !r.HasExecutionLease() {
		return RunLivenessUnknown
	}
	if r.IsLeaseExpired(now) {
		return RunLivenessExpired
	}
	return RunLivenessHealthy
}

func (r *AgentRun) IsStaleActive(now, legacyCutoff time.Time) bool {
	if r == nil {
		return false
	}
	if r.Status != RunStatusRunning {
		return false
	}
	if r.HasExecutionLease() {
		return r.LivenessState(now) == RunLivenessExpired
	}
	if legacyCutoff.IsZero() {
		return false
	}
	legacyCutoff = legacyCutoff.UTC()
	return !r.UpdatedAt.IsZero() && !r.UpdatedAt.UTC().After(legacyCutoff)
}

func applyRunExecutionLiveness(run *AgentRun, workerID string, observedAt time.Time, policy RunLeasePolicy) {
	if run == nil {
		return
	}
	policy = policy.Normalize()
	observedAt = normalizeObservedAt(observedAt)
	leaseExpiresAt := observedAt.Add(policy.TTL)
	run.WorkerID = strings.TrimSpace(workerID)
	run.HeartbeatAt = &observedAt
	run.LeaseExpiresAt = &leaseExpiresAt
}

func clearRunExecutionLiveness(run *AgentRun) {
	if run == nil {
		return
	}
	run.WorkerID = ""
	run.HeartbeatAt = nil
	run.LeaseExpiresAt = nil
}

func seedQueuedRunLiveness(run *AgentRun, observedAt time.Time, policy RunLeasePolicy) {
	if run == nil || run.Status != RunStatusQueued || run.HasExecutionLease() {
		return
	}
	applyRunExecutionLiveness(run, "", observedAt, policy)
}

type executionWorkerIDContextKey struct{}

func withExecutionWorkerID(ctx context.Context, workerID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	workerID = strings.TrimSpace(workerID)
	if workerID == "" {
		return ctx
	}
	return context.WithValue(ctx, executionWorkerIDContextKey{}, workerID)
}

func executionWorkerIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	workerID, _ := ctx.Value(executionWorkerIDContextKey{}).(string)
	return strings.TrimSpace(workerID)
}
