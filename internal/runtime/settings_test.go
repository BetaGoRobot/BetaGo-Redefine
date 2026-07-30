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
	if got := configs["conversation"]; got.Workers != 4 || got.QueueSize != 128 || got.TaskTimeout != 2*time.Minute {
		t.Fatalf("conversation executor config = %+v", got)
	}
	if got := configs["projection"]; got.Workers != 2 || got.QueueSize != 128 || got.TaskTimeout != time.Minute {
		t.Fatalf("projection executor config = %+v", got)
	}
	if got := ConversationEventIndex(&infraConfig.BaseConfig{}); got != "agent_conversation_events" {
		t.Fatalf("ConversationEventIndex() = %q", got)
	}
}

func TestExecutorConfigsRespectsRuntimeConfig(t *testing.T) {
	configs := ExecutorConfigs(&infraConfig.BaseConfig{
		RuntimeConfig: &infraConfig.RuntimeConfig{
			MessageWorkers:                       3,
			MessageQueueSize:                     16,
			MessageTimeoutSeconds:                45,
			ReactionWorkers:                      2,
			ReactionQueueSize:                    18,
			ReactionTimeoutSeconds:               12,
			RecordingWorkers:                     5,
			RecordingQueueSize:                   22,
			RecordingTimeoutSeconds:              90,
			ChunkWorkers:                         6,
			ChunkQueueSize:                       24,
			ChunkTimeoutSeconds:                  33,
			ScheduleWorkers:                      7,
			ScheduleQueueSize:                    26,
			ScheduleTaskTimeoutSeconds:           77,
			ConversationWorkers:                  3,
			ConversationQueueSize:                31,
			ConversationTimeoutSeconds:           119,
			ConversationProjectionWorkers:        5,
			ConversationProjectionQueueSize:      37,
			ConversationProjectionTimeoutSeconds: 53,
			ConversationEventIndex:               "custom_conversation_events",
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
	if got := configs["conversation"]; got.Workers != 3 || got.QueueSize != 31 || got.TaskTimeout != 119*time.Second {
		t.Fatalf("conversation executor config = %+v", got)
	}
	if got := configs["projection"]; got.Workers != 5 || got.QueueSize != 37 || got.TaskTimeout != 53*time.Second {
		t.Fatalf("projection executor config = %+v", got)
	}
	if got := ConversationEventIndex(&infraConfig.BaseConfig{RuntimeConfig: &infraConfig.RuntimeConfig{
		ConversationEventIndex: "custom_conversation_events",
	}}); got != "custom_conversation_events" {
		t.Fatalf("ConversationEventIndex() = %q", got)
	}
}

func TestAgentCardRolloutDefaultsOffAndValidatesModes(t *testing.T) {
	settings, err := AgentCardRolloutSettings(&infraConfig.BaseConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if settings.Mode != AgentCardModeOff || settings.ToolsAvailable() ||
		settings.CanSend("chat-1") {
		t.Fatalf("default settings = %#v", settings)
	}
	if _, err := AgentCardRolloutSettings(&infraConfig.BaseConfig{
		AgentCardConfig: &infraConfig.AgentCardConfig{
			Enabled: true, Mode: "invalid",
		},
	}); err == nil {
		t.Fatal("invalid agent card mode was accepted")
	}
}

func TestAgentCardRolloutModesEnforceShadowAllowlistAndOn(t *testing.T) {
	tests := []struct {
		name           string
		config         *infraConfig.AgentCardConfig
		chatID         string
		wantTools      bool
		wantSend       bool
		wantShadow     bool
		wantRepairs    int
		wantExpiry     time.Duration
		wantWorkers    int
		wantPatchLease time.Duration
	}{
		{
			name: "shadow",
			config: &infraConfig.AgentCardConfig{
				Enabled: true, Mode: "shadow",
			},
			chatID: "chat-1", wantTools: true, wantShadow: true,
			wantRepairs: 2, wantExpiry: 10 * time.Minute,
			wantWorkers: 1, wantPatchLease: 30 * time.Second,
		},
		{
			name: "allowlist allowed",
			config: &infraConfig.AgentCardConfig{
				Enabled: true, Mode: "allowlist",
				AllowChatIDs:      []string{"chat-1"},
				MaxRepairAttempts: 3, DefaultExpirySeconds: 900,
				PatchWorkerCount: 2, PatchLeaseSeconds: 45,
			},
			chatID: "chat-1", wantTools: true, wantSend: true,
			wantRepairs: 3, wantExpiry: 15 * time.Minute,
			wantWorkers: 2, wantPatchLease: 45 * time.Second,
		},
		{
			name: "allowlist denied",
			config: &infraConfig.AgentCardConfig{
				Enabled: true, Mode: "allowlist",
				AllowChatIDs: []string{"chat-2"},
			},
			chatID: "chat-1", wantTools: true,
			wantRepairs: 2, wantExpiry: 10 * time.Minute,
			wantWorkers: 1, wantPatchLease: 30 * time.Second,
		},
		{
			name: "on",
			config: &infraConfig.AgentCardConfig{
				Enabled: true, Mode: "on",
			},
			chatID: "chat-any", wantTools: true, wantSend: true,
			wantRepairs: 2, wantExpiry: 10 * time.Minute,
			wantWorkers: 1, wantPatchLease: 30 * time.Second,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			settings, err := AgentCardRolloutSettings(&infraConfig.BaseConfig{
				AgentCardConfig: test.config,
			})
			if err != nil {
				t.Fatal(err)
			}
			if settings.ToolsAvailable() != test.wantTools ||
				settings.CanSend(test.chatID) != test.wantSend ||
				settings.Shadow() != test.wantShadow ||
				settings.MaxRepairAttempts != test.wantRepairs ||
				settings.DefaultExpiry != test.wantExpiry ||
				settings.PatchWorkerCount != test.wantWorkers ||
				settings.PatchLease != test.wantPatchLease {
				t.Fatalf("settings = %#v", settings)
			}
		})
	}
}
