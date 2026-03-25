package runtime

import (
	"time"

	infraConfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
)

type AgentRuntimeWorkerSettings struct {
	ResumeWorkers            int
	PendingInitialRunWorkers int
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

func AgentRuntimeWorkerConfigs(cfg *infraConfig.BaseConfig) AgentRuntimeWorkerSettings {
	runtimeCfg := &infraConfig.RuntimeConfig{}
	if cfg != nil && cfg.RuntimeConfig != nil {
		runtimeCfg = cfg.RuntimeConfig
	}
	return AgentRuntimeWorkerSettings{
		ResumeWorkers:            defaultInt(runtimeCfg.AgentRuntimeResumeWorkers, 1),
		PendingInitialRunWorkers: defaultInt(runtimeCfg.AgentRuntimePendingInitialWorkers, 1),
	}
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
	}
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
