package main

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"

	appruntime "github.com/BetaGoRobot/BetaGo-Redefine/internal/runtime"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"go.uber.org/zap"
)

// main 只保留进程级控制流：
// 1. 读取配置；
// 2. 构造运行时 App；
// 3. 在信号上下文中启动；
// 4. 收到退出信号后按统一生命周期关闭。
//
// 具体的模块装配和依赖顺序已经拆到同目录下的其他文件中，避免入口文件
// 同时承担配置、依赖初始化、探针和业务装配职责。
func main() {
	cfg, err := loadConfig()
	if err != nil {
		panic(err)
	}

	app, err := buildApp(cfg)
	if err != nil {
		panic(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := app.Start(ctx); err != nil {
		panic(err)
	}

	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), appruntime.ShutdownTimeout(cfg))
	defer cancel()
	if err := app.Stop(shutdownCtx); err != nil && !errors.Is(err, context.Canceled) {
		logs.L().Error("app stop failed", zap.Error(err))
	}
}
