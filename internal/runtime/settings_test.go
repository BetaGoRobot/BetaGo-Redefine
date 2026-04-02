package runtime

import (
	"testing"
	"time"

	infraConfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
)

func TestAgentRuntimeWorkerConfigsDefaultsToSingleWorker(t *testing.T) {
	settings := AgentRuntimeWorkerConfigs(&infraConfig.BaseConfig{})
	if settings.ResumeWorkers != 1 {
		t.Fatalf("ResumeWorkers = %d, want 1", settings.ResumeWorkers)
	}
	if settings.PendingInitialRunWorkers != 1 {
		t.Fatalf("PendingInitialRunWorkers = %d, want 1", settings.PendingInitialRunWorkers)
	}
}

func TestAgentRuntimeTimingConfigsDefaults(t *testing.T) {
	settings := AgentRuntimeTimingConfigs(&infraConfig.BaseConfig{})
	if settings.ExecutionLeaseTTL != 3*time.Minute {
		t.Fatalf("ExecutionLeaseTTL = %s, want %s", settings.ExecutionLeaseTTL, 3*time.Minute)
	}
	if settings.ExecutionHeartbeatInterval != 15*time.Second {
		t.Fatalf("ExecutionHeartbeatInterval = %s, want %s", settings.ExecutionHeartbeatInterval, 15*time.Second)
	}
	if settings.LegacyRunStaleTimeout != 30*time.Minute {
		t.Fatalf("LegacyRunStaleTimeout = %s, want %s", settings.LegacyRunStaleTimeout, 30*time.Minute)
	}
	if settings.StaleRunSweepInterval != 5*time.Second {
		t.Fatalf("StaleRunSweepInterval = %s, want %s", settings.StaleRunSweepInterval, 5*time.Second)
	}
}
