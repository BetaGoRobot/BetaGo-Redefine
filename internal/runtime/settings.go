package runtime

import (
	"fmt"
	"strings"
	"time"

	infraConfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
)

type AgentCardMode string

const (
	AgentCardModeOff       AgentCardMode = "off"
	AgentCardModeShadow    AgentCardMode = "shadow"
	AgentCardModeAllowlist AgentCardMode = "allowlist"
	AgentCardModeOn        AgentCardMode = "on"
)

type EvaluationMode string

const (
	EvaluationModeOff       EvaluationMode = "off"
	EvaluationModeAllowlist EvaluationMode = "allowlist"
	EvaluationModeOn        EvaluationMode = "on"
)

// AgentCardSettings is the normalized, fail-closed runtime policy used by
// both tool exposure and delivery. Callers must not inspect raw config values.
type AgentCardSettings struct {
	Mode              AgentCardMode
	MaxRepairAttempts int
	DefaultExpiry     time.Duration
	PatchWorkerCount  int
	PatchLease        time.Duration

	allowedChats map[string]struct{}
}

type EvaluationSettings struct {
	Mode           EvaluationMode
	CohortDuration time.Duration

	allowedChats map[string]struct{}
}

func EvaluationRolloutSettings(
	cfg *infraConfig.BaseConfig,
) (EvaluationSettings, error) {
	settings := EvaluationSettings{
		Mode:           EvaluationModeOff,
		CohortDuration: 24 * time.Hour,
		allowedChats:   make(map[string]struct{}),
	}
	if cfg == nil || cfg.RuntimeConfig == nil {
		return settings, nil
	}

	raw := cfg.RuntimeConfig
	mode := EvaluationMode(strings.ToLower(strings.TrimSpace(raw.EvaluationMode)))
	if mode == "" {
		mode = EvaluationModeOff
	}
	switch mode {
	case EvaluationModeOff, EvaluationModeAllowlist, EvaluationModeOn:
		settings.Mode = mode
	default:
		return EvaluationSettings{}, fmt.Errorf(
			"unsupported evaluation mode %q",
			raw.EvaluationMode,
		)
	}

	if raw.EvaluationCohortDurationHours < 0 ||
		raw.EvaluationCohortDurationHours > 168 {
		return EvaluationSettings{}, fmt.Errorf(
			"evaluation cohort duration hours must be between 1 and 168",
		)
	}
	if raw.EvaluationCohortDurationHours > 0 {
		settings.CohortDuration =
			time.Duration(raw.EvaluationCohortDurationHours) * time.Hour
	}
	for _, chatID := range raw.EvaluationChatIDs {
		if chatID = strings.TrimSpace(chatID); chatID != "" {
			settings.allowedChats[chatID] = struct{}{}
		}
	}
	return settings, nil
}

func (s EvaluationSettings) Enabled() bool {
	return s.Mode == EvaluationModeAllowlist || s.Mode == EvaluationModeOn
}

func (s EvaluationSettings) Allows(chatID string) bool {
	switch s.Mode {
	case EvaluationModeOn:
		return true
	case EvaluationModeAllowlist:
		_, allowed := s.allowedChats[strings.TrimSpace(chatID)]
		return allowed
	default:
		return false
	}
}

func (s EvaluationSettings) AllowedChatCount() int {
	return len(s.allowedChats)
}

func AgentCardRolloutSettings(cfg *infraConfig.BaseConfig) (AgentCardSettings, error) {
	settings := AgentCardSettings{
		Mode:              AgentCardModeOff,
		MaxRepairAttempts: 2,
		DefaultExpiry:     10 * time.Minute,
		PatchWorkerCount:  1,
		PatchLease:        30 * time.Second,
		allowedChats:      make(map[string]struct{}),
	}
	if cfg == nil || cfg.AgentCardConfig == nil || !cfg.AgentCardConfig.Enabled {
		return settings, nil
	}

	raw := cfg.AgentCardConfig
	mode := AgentCardMode(strings.ToLower(strings.TrimSpace(raw.Mode)))
	if mode == "" {
		mode = AgentCardModeOff
	}
	switch mode {
	case AgentCardModeOff, AgentCardModeShadow, AgentCardModeAllowlist, AgentCardModeOn:
		settings.Mode = mode
	default:
		return AgentCardSettings{}, fmt.Errorf("unsupported agent_card mode %q", raw.Mode)
	}

	settings.MaxRepairAttempts = defaultInt(raw.MaxRepairAttempts, settings.MaxRepairAttempts)
	settings.DefaultExpiry = defaultDuration(raw.DefaultExpirySeconds, settings.DefaultExpiry)
	settings.PatchWorkerCount = defaultInt(raw.PatchWorkerCount, settings.PatchWorkerCount)
	settings.PatchLease = defaultDuration(raw.PatchLeaseSeconds, settings.PatchLease)
	for _, chatID := range raw.AllowChatIDs {
		if chatID = strings.TrimSpace(chatID); chatID != "" {
			settings.allowedChats[chatID] = struct{}{}
		}
	}
	return settings, nil
}

func (s AgentCardSettings) ToolsAvailable() bool {
	return s.Mode != AgentCardModeOff
}

func (s AgentCardSettings) Shadow() bool {
	return s.Mode == AgentCardModeShadow
}

func (s AgentCardSettings) CanSend(chatID string) bool {
	switch s.Mode {
	case AgentCardModeOn:
		return true
	case AgentCardModeAllowlist:
		_, allowed := s.allowedChats[strings.TrimSpace(chatID)]
		return allowed
	default:
		return false
	}
}

// ShutdownTimeout 返回 App.Stop 的总优雅关闭时间。默认值故意保守一些，
// 让工作池有机会把已接收任务尽量处理完。
func ShutdownTimeout(cfg *infraConfig.BaseConfig) time.Duration {
	if cfg != nil && cfg.RuntimeConfig != nil && cfg.RuntimeConfig.ShutdownTimeoutSeconds > 0 {
		return time.Duration(cfg.RuntimeConfig.ShutdownTimeoutSeconds) * time.Second
	}
	return 15 * time.Second
}

// ManagementShutdownTimeout 返回管理面 HTTP 的关闭超时，它可以比整个应用
// 的关闭超时更短。
func ManagementShutdownTimeout(cfg *infraConfig.BaseConfig) time.Duration {
	if cfg != nil && cfg.ManagementHTTPConfig != nil && cfg.ManagementHTTPConfig.ShutdownTimeoutSeconds > 0 {
		return time.Duration(cfg.ManagementHTTPConfig.ShutdownTimeoutSeconds) * time.Second
	}
	return 10 * time.Second
}

// ExecutorConfigs 把 runtime TOML 中的配置转换成实际执行器参数。不同工作
// 类别拥有独立预算，避免某一条链路过载时直接吃掉所有 worker。
func ExecutorConfigs(cfg *infraConfig.BaseConfig) map[string]ExecutorConfig {
	runtimeCfg := &infraConfig.RuntimeConfig{}
	if cfg != nil && cfg.RuntimeConfig != nil {
		runtimeCfg = cfg.RuntimeConfig
	}
	return map[string]ExecutorConfig{
		"message": {
			Name:        "message_executor",
			Workers:     defaultInt(runtimeCfg.MessageWorkers, 8),
			QueueSize:   defaultInt(runtimeCfg.MessageQueueSize, 256),
			TaskTimeout: defaultDuration(runtimeCfg.MessageTimeoutSeconds, 10*time.Minute),
		},
		"reaction": {
			Name:        "reaction_executor",
			Workers:     defaultInt(runtimeCfg.ReactionWorkers, 4),
			QueueSize:   defaultInt(runtimeCfg.ReactionQueueSize, 128),
			TaskTimeout: defaultDuration(runtimeCfg.ReactionTimeoutSeconds, 30*time.Second),
		},
		"recording": {
			Name:        "recording_executor",
			Workers:     defaultInt(runtimeCfg.RecordingWorkers, 4),
			QueueSize:   defaultInt(runtimeCfg.RecordingQueueSize, 128),
			TaskTimeout: defaultDuration(runtimeCfg.RecordingTimeoutSeconds, 2*time.Minute),
		},
		"chunk": {
			Name:        "chunk_executor",
			Workers:     defaultInt(runtimeCfg.ChunkWorkers, 2),
			QueueSize:   defaultInt(runtimeCfg.ChunkQueueSize, 64),
			TaskTimeout: defaultDuration(runtimeCfg.ChunkTimeoutSeconds, 5*time.Minute),
		},
		"schedule": {
			Name:        "schedule_executor",
			Workers:     defaultInt(runtimeCfg.ScheduleWorkers, 4),
			QueueSize:   defaultInt(runtimeCfg.ScheduleQueueSize, 128),
			TaskTimeout: defaultDuration(runtimeCfg.ScheduleTaskTimeoutSeconds, 10*time.Minute),
		},
		"conversation": {
			Name:        "conversation_executor",
			Workers:     defaultInt(runtimeCfg.ConversationWorkers, 4),
			QueueSize:   defaultInt(runtimeCfg.ConversationQueueSize, 128),
			TaskTimeout: defaultDuration(runtimeCfg.ConversationTimeoutSeconds, 2*time.Minute),
		},
		"projection": {
			Name:        "conversation_projection_executor",
			Workers:     defaultInt(runtimeCfg.ConversationProjectionWorkers, 2),
			QueueSize:   defaultInt(runtimeCfg.ConversationProjectionQueueSize, 128),
			TaskTimeout: defaultDuration(runtimeCfg.ConversationProjectionTimeoutSeconds, time.Minute),
		},
	}
}

func ConversationEventIndex(cfg *infraConfig.BaseConfig) string {
	if cfg != nil && cfg.RuntimeConfig != nil {
		if index := strings.TrimSpace(cfg.RuntimeConfig.ConversationEventIndex); index != "" {
			return index
		}
	}
	return "agent_conversation_events"
}

// defaultInt 为正整数配置应用兜底值。
func defaultInt(value, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}

// defaultDuration 为正秒数配置应用兜底值。
func defaultDuration(seconds int, fallback time.Duration) time.Duration {
	if seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	return fallback
}
