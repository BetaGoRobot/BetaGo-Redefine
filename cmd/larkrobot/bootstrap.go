package main

import (
	"errors"

	appcardaction "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/cardaction"
	infraConfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	appruntime "github.com/BetaGoRobot/BetaGo-Redefine/internal/runtime"
)

// buildApp 是当前单体进程的装配根。这里集中完成：
// 1. 构造受控执行器和 handler 入口；
// 2. 按依赖顺序注册基础设施模块；
// 3. 注册应用服务、管理面和 websocket ingress。
func buildApp(cfg *infraConfig.BaseConfig) (*appruntime.App, error) {
	if cfg == nil {
		return nil, errors.New("config is nil")
	}
	if cfg.LarkConfig == nil {
		return nil, errors.New("lark config is nil")
	}

	appcardaction.RegisterBuiltins()

	components, err := newAppComponents(cfg)
	if err != nil {
		return nil, err
	}
	app := appruntime.NewApp()

	addInfrastructureModules(app, cfg, components)
	addSearchSchemaModule(app, cfg, components)
	addExecutorModules(app, components)
	addApplicationModules(app, cfg, components)

	return app, nil
}
