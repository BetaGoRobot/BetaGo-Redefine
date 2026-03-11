package config

import infraConfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"

func CurrentRateLimitConfig() *infraConfig.RateLimitConfig {
	cfg := currentBaseConfig()
	if cfg == nil {
		return nil
	}
	return cfg.RateLimitConfig
}
