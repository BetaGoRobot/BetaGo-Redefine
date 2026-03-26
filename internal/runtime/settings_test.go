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

func TestAgentRuntimeWorkerConfigsUsesRuntimeConfigOverrides(t *testing.T) {
	settings := AgentRuntimeWorkerConfigs(&infraConfig.BaseConfig{
		RuntimeConfig: &infraConfig.RuntimeConfig{
			AgentRuntimeResumeWorkers:         4,
			AgentRuntimePendingInitialWorkers: 6,
		},
	})
	if settings.ResumeWorkers != 4 {
		t.Fatalf("ResumeWorkers = %d, want 4", settings.ResumeWorkers)
	}
	if settings.PendingInitialRunWorkers != 6 {
		t.Fatalf("PendingInitialRunWorkers = %d, want 6", settings.PendingInitialRunWorkers)
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

func TestAgentRuntimeTimingConfigsUsesRuntimeConfigOverrides(t *testing.T) {
	settings := AgentRuntimeTimingConfigs(&infraConfig.BaseConfig{
		RuntimeConfig: &infraConfig.RuntimeConfig{
			AgentRuntimeExecutionLeaseTimeoutSeconds:      210,
			AgentRuntimeExecutionHeartbeatIntervalSeconds: 21,
			AgentRuntimeStaleRunLegacyTimeoutSeconds:      2700,
			AgentRuntimeStaleRunSweepIntervalSeconds:      9,
		},
	})
	if settings.ExecutionLeaseTTL != 210*time.Second {
		t.Fatalf("ExecutionLeaseTTL = %s, want %s", settings.ExecutionLeaseTTL, 210*time.Second)
	}
	if settings.ExecutionHeartbeatInterval != 21*time.Second {
		t.Fatalf("ExecutionHeartbeatInterval = %s, want %s", settings.ExecutionHeartbeatInterval, 21*time.Second)
	}
	if settings.LegacyRunStaleTimeout != 2700*time.Second {
		t.Fatalf("LegacyRunStaleTimeout = %s, want %s", settings.LegacyRunStaleTimeout, 2700*time.Second)
	}
	if settings.StaleRunSweepInterval != 9*time.Second {
		t.Fatalf("StaleRunSweepInterval = %s, want %s", settings.StaleRunSweepInterval, 9*time.Second)
	}
}
