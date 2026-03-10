package main

import (
	"os"

	infraConfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
)

// loadConfig 负责读取进程启动时使用的主配置文件。
func loadConfig() (*infraConfig.BaseConfig, error) {
	return infraConfig.LoadFileE(loadConfigPath())
}

// loadConfigPath 提供唯一的配置文件覆盖入口，方便本地开发、测试和
// 部署系统注入不同配置。
func loadConfigPath() string {
	if path := os.Getenv("BETAGO_CONFIG_PATH"); path != "" {
		return path
	}
	return ".dev/config.toml"
}

// managementAddr 返回管理面 HTTP 的监听地址。空字符串表示显式关闭该
// 模块，此时运行时会把它标记为 disabled，而不是启动失败。
func managementAddr(cfg *infraConfig.BaseConfig) string {
	if cfg == nil || cfg.ManagementHTTPConfig == nil {
		return ""
	}
	return cfg.ManagementHTTPConfig.Addr
}
