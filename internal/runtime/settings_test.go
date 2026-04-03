package runtime

import (
	"testing"
	"time"

	infraConfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
)

func TestShutdownTimeoutDefaults(t *testing.T) {
	if got := ShutdownTimeout(&infraConfig.BaseConfig{}); got != 15*time.Second {
		t.Fatalf("ShutdownTimeout() = %s, want %s", got, 15*time.Second)
	}
	if got := ManagementShutdownTimeout(&infraConfig.BaseConfig{}); got != 10*time.Second {
		t.Fatalf("ManagementShutdownTimeout() = %s, want %s", got, 10*time.Second)
	}
}

func TestShutdownTimeoutRespectsConfig(t *testing.T) {
	cfg := &infraConfig.BaseConfig{
		RuntimeConfig: &infraConfig.RuntimeConfig{
			ShutdownTimeoutSeconds: 21,
		},
		ManagementHTTPConfig: &infraConfig.ManagementHTTPConfig{
			ShutdownTimeoutSeconds: 7,
		},
	}

	if got := ShutdownTimeout(cfg); got != 21*time.Second {
		t.Fatalf("ShutdownTimeout() = %s, want %s", got, 21*time.Second)
	}
	if got := ManagementShutdownTimeout(cfg); got != 7*time.Second {
		t.Fatalf("ManagementShutdownTimeout() = %s, want %s", got, 7*time.Second)
	}
}

func TestExecutorConfigsDefaults(t *testing.T) {
	configs := ExecutorConfigs(&infraConfig.BaseConfig{})

	if got := configs["message"]; got.Workers != 8 || got.QueueSize != 256 || got.TaskTimeout != 10*time.Minute {
		t.Fatalf("message executor config = %+v", got)
	}
	if got := configs["reaction"]; got.Workers != 4 || got.QueueSize != 128 || got.TaskTimeout != 30*time.Second {
		t.Fatalf("reaction executor config = %+v", got)
	}
	if got := configs["recording"]; got.Workers != 4 || got.QueueSize != 128 || got.TaskTimeout != 2*time.Minute {
		t.Fatalf("recording executor config = %+v", got)
	}
	if got := configs["chunk"]; got.Workers != 2 || got.QueueSize != 64 || got.TaskTimeout != 5*time.Minute {
		t.Fatalf("chunk executor config = %+v", got)
	}
	if got := configs["schedule"]; got.Workers != 4 || got.QueueSize != 128 || got.TaskTimeout != 10*time.Minute {
		t.Fatalf("schedule executor config = %+v", got)
	}
}

func TestExecutorConfigsRespectsRuntimeConfig(t *testing.T) {
	configs := ExecutorConfigs(&infraConfig.BaseConfig{
		RuntimeConfig: &infraConfig.RuntimeConfig{
			MessageWorkers:             3,
			MessageQueueSize:           16,
			MessageTimeoutSeconds:      45,
			ReactionWorkers:            2,
			ReactionQueueSize:          18,
			ReactionTimeoutSeconds:     12,
			RecordingWorkers:           5,
			RecordingQueueSize:         22,
			RecordingTimeoutSeconds:    90,
			ChunkWorkers:               6,
			ChunkQueueSize:             24,
			ChunkTimeoutSeconds:        33,
			ScheduleWorkers:            7,
			ScheduleQueueSize:          26,
			ScheduleTaskTimeoutSeconds: 77,
		},
	})

	if got := configs["message"]; got.Workers != 3 || got.QueueSize != 16 || got.TaskTimeout != 45*time.Second {
		t.Fatalf("message executor config = %+v", got)
	}
	if got := configs["reaction"]; got.Workers != 2 || got.QueueSize != 18 || got.TaskTimeout != 12*time.Second {
		t.Fatalf("reaction executor config = %+v", got)
	}
	if got := configs["recording"]; got.Workers != 5 || got.QueueSize != 22 || got.TaskTimeout != 90*time.Second {
		t.Fatalf("recording executor config = %+v", got)
	}
	if got := configs["chunk"]; got.Workers != 6 || got.QueueSize != 24 || got.TaskTimeout != 33*time.Second {
		t.Fatalf("chunk executor config = %+v", got)
	}
	if got := configs["schedule"]; got.Workers != 7 || got.QueueSize != 26 || got.TaskTimeout != 77*time.Second {
		t.Fatalf("schedule executor config = %+v", got)
	}
}
