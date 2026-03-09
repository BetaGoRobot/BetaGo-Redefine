package xhttp

import (
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/go-resty/resty/v2"
	"go.uber.org/zap"
)

var (
	HttpClient          = resty.New()
	HttpClientWithProxy = resty.New()
)

func Init() {
	cfg := config.Get().ProxyConfig
	if cfg == nil || cfg.PrivateProxy == "" {
		HttpClientWithProxy = resty.New()
		logs.L().Warn("Proxy HTTP client disabled, using direct client",
			zap.String("reason", "proxy config missing or empty"),
		)
		return
	}
	HttpClientWithProxy = resty.New().SetProxy(cfg.PrivateProxy)
}
