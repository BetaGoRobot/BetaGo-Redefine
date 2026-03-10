package main

import (
	"context"
	"errors"

	appruntime "github.com/BetaGoRobot/BetaGo-Redefine/internal/runtime"
	"gorm.io/gorm"
)

// newRecoverModule 用来包裹仍然依赖副作用初始化的旧模块，把 panic
// 转换成普通 error，避免启动阶段直接把进程炸掉。
func newRecoverModule(name string, critical bool, fn func()) *appruntime.FuncModule {
	return appruntime.NewFuncModule(appruntime.FuncModuleOptions{
		Name:     name,
		Critical: critical,
		Start: func(context.Context) error {
			return appruntime.RecoverError(name, fn)
		},
	})
}

// newOptionalModule 统一“尽力而为”的依赖语义：启动失败不阻断主流程，
// 但会在 readiness / health 中暴露 degraded 状态。
func newOptionalModule(name string, initFn func(), readyFn func(context.Context) error) *appruntime.FuncModule {
	return appruntime.NewFuncModule(appruntime.FuncModuleOptions{
		Name:     name,
		Critical: false,
		Start: func(context.Context) error {
			if err := appruntime.RecoverError(name, initFn); err != nil {
				return err
			}
			return nil
		},
		Ready: readyFn,
	})
}

// pingDB 是 GORM 底层连接池的运行时就绪探针。
func pingDB(ctx context.Context, database *gorm.DB) error {
	if database == nil {
		return errors.New("db unavailable")
	}
	sqlDB, err := database.DB()
	if err != nil {
		return err
	}
	return sqlDB.PingContext(ctx)
}

// closeDB 在运行时停止阶段关闭底层 sql.DB 连接池。
func closeDB(database *gorm.DB) error {
	if database == nil {
		return nil
	}
	sqlDB, err := database.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}
