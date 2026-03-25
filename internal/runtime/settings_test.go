package runtime

import (
	"testing"

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
