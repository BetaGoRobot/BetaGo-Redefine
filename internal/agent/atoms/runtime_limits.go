package atoms

import (
	"context"
)

// RuntimeLimits describes non-secret limits actually applied by the independent
// runtime. It deliberately does not invent counters from the retired limiter.
type RuntimeLimits struct {
	MaxConcurrentTasks     int `json:"max_concurrent_tasks"`
	MaxTurns               int `json:"max_turns"`
	MaxContextBytes        int `json:"max_context_bytes"`
	TaskTimeoutSeconds     int `json:"task_timeout_seconds"`
	TurnLeaseSeconds       int `json:"turn_lease_seconds"`
	WorkerConcurrency      int `json:"worker_concurrency"`
	WorkerPollMilliseconds int `json:"worker_poll_milliseconds"`
}

func RuntimeLimitsDefinition(limits RuntimeLimits) Definition {
	return Definition{Name: "runtime_limits_get", Description: "Read non-secret limits actually applied by this independent runtime: task concurrency, turn count, model context bytes, deadlines, leases and worker concurrency/poll interval. This is configuration state, not token/requests-per-minute quotas; no legacy rate limiter settings or counters are imported.", Run: func(context.Context, Invocation, string) (string, error) {
		return encodeCommunity(map[string]any{"limits": limits, "imported_legacy_limits": false})
	}}
}
