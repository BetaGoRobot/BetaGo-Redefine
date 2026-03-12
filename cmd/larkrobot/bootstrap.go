package main

import (
	"context"
	"errors"
	"fmt"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	appcardaction "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/cardaction"
	larkchunking "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/chunking"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/handlers"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/messages"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/messages/recording"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/reaction"
	scheduleapp "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/schedule"
	todoapp "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/todo"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/aktool"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	infraConfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/gotify"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/miniodal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/neteaseapi"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/opensearch"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	redis_dal "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/redis"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/retriever"
	larkiface "github.com/BetaGoRobot/BetaGo-Redefine/internal/interfaces/lark"
	appruntime "github.com/BetaGoRobot/BetaGo-Redefine/internal/runtime"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhttp"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
)

type appComponents struct {
	messageExecutor   *appruntime.Executor
	reactionExecutor  *appruntime.Executor
	recordingExecutor *appruntime.Executor
	chunkExecutor     *appruntime.Executor
	scheduleExecutor  *appruntime.Executor
	handlerSet        *larkiface.HandlerSet
	eventDispatcher   *dispatcher.EventDispatcher
}

// scheduler 仍保留为包级句柄，是因为当前调度器本身还没有实现
// runtime.Module。真正的生命周期仍由装配阶段注册的模块接管。
var scheduler *scheduleapp.Scheduler

// buildApp 是当前单体进程的装配根。这里集中完成：
// 1. 构造受控执行器和 handler 入口；
// 2. 按依赖顺序注册基础设施模块；
// 3. 注册应用服务、管理面和 websocket ingress。
func buildApp(cfg *infraConfig.BaseConfig) (*appruntime.App, error) {
	if cfg == nil {
		return nil, errors.New("config is nil")
	}

	appcardaction.RegisterBuiltins()

	components := newAppComponents(cfg)
	app := appruntime.NewApp()

	addInfrastructureModules(app, cfg)
	addExecutorModules(app, components)
	addApplicationModules(app, cfg, components)

	return app, nil
}

// newAppComponents 只负责构造“会被多个模块共享”的装配对象，不直接向
// App 注册模块，避免对象创建和生命周期注册混在一起。
func newAppComponents(cfg *infraConfig.BaseConfig) *appComponents {
	executorConfigs := appruntime.ExecutorConfigs(cfg)

	messageExecutor := appruntime.NewExecutor(executorConfigs["message"])
	reactionExecutor := appruntime.NewExecutor(executorConfigs["reaction"])
	recordingExecutor := appruntime.NewExecutor(executorConfigs["recording"])
	chunkExecutor := appruntime.NewExecutor(executorConfigs["chunk"])
	scheduleExecutor := appruntime.NewExecutor(executorConfigs["schedule"])

	messageProcessor := messages.NewMessageProcessor(appconfig.GetManager())
	reactionProcessor := reaction.NewReactionProcessor()
	handlerSet := larkiface.NewHandlerSet(larkiface.HandlerSetOptions{
		MessageProcessor:  messageProcessor,
		ReactionProcessor: reactionProcessor,
		MessageExecutor:   messageExecutor,
		ReactionExecutor:  reactionExecutor,
	})

	return &appComponents{
		messageExecutor:   messageExecutor,
		reactionExecutor:  reactionExecutor,
		recordingExecutor: recordingExecutor,
		chunkExecutor:     chunkExecutor,
		scheduleExecutor:  scheduleExecutor,
		handlerSet:        handlerSet,
		eventDispatcher:   newEventDispatcher(handlerSet),
	}
}

// addInfrastructureModules 注册基础设施层模块。顺序严格反映依赖方向：
// 先准备底层连接和客户端，再让上层应用服务接入它们。
func addInfrastructureModules(app *appruntime.App, cfg *infraConfig.BaseConfig) {
	app.AddModule(newRecoverModule("otel", false, func() {
		otel.Init(cfg.OtelConfig)
	}))
	app.AddModule(newRecoverModule("logging", true, func() {
		logs.Init()
	}))
	app.AddModule(appruntime.NewFuncModule(appruntime.FuncModuleOptions{
		Name:     "db",
		Critical: true,
		Init: func(context.Context) error {
			return appruntime.RecoverError("db.Init", func() {
				db.Init(cfg.DBConfig)
			})
		},
		Ready: func(ctx context.Context) error {
			return pingDB(ctx, db.DB())
		},
		Stop: func(context.Context) error {
			return closeDB(db.DB())
		},
	}))
	app.AddModule(appruntime.NewFuncModule(appruntime.FuncModuleOptions{
		Name:     "redis",
		Critical: true,
		Start: func(ctx context.Context) error {
			return redis_dal.Init(ctx)
		},
		Ready: func(ctx context.Context) error {
			return redis_dal.Ping(ctx)
		},
		Stop: func(context.Context) error {
			return redis_dal.Close()
		},
	}))
	app.AddModule(newOptionalModule("lark_api", func() {
		lark_dal.Init()
	}, func(context.Context) error {
		if lark_dal.Client() == nil {
			return errors.New("lark client unavailable")
		}
		return nil
	}))
	app.AddModule(newOptionalModule("opensearch", func() {
		opensearch.Init(cfg.OpensearchConfig)
	}, func(context.Context) error {
		if ok, reason := opensearch.Status(); !ok {
			return errors.New(reason)
		}
		return nil
	}))
	app.AddModule(newOptionalModule("ark_runtime", func() {
		ark_dal.Init(cfg.ArkConfig)
	}, func(context.Context) error {
		if ok, reason := ark_dal.Status(); !ok {
			return errors.New(reason)
		}
		return nil
	}))
	app.AddModule(newOptionalModule("minio", func() {
		miniodal.Init(cfg.MinioConfig)
	}, func(context.Context) error {
		if ok, reason := miniodal.Status(); !ok {
			return errors.New(reason)
		}
		return nil
	}))
	app.AddModule(newOptionalModule("gotify", func() {
		gotify.Init()
	}, func(context.Context) error {
		return gotify.ErrUnavailable()
	}))
	app.AddModule(newOptionalModule("aktool", func() {
		aktool.Init()
	}, func(context.Context) error {
		if ok, reason := aktool.Status(); !ok {
			return errors.New(reason)
		}
		return nil
	}))
	app.AddModule(newRecoverModule("xhttp", false, func() {
		xhttp.Init()
	}))
	app.AddModule(newRecoverModule("netease_music", false, func() {
		neteaseapi.Init()
	}))
	app.AddModule(newOptionalModule("retriever", func() {
		retriever.Init()
	}, func(context.Context) error {
		if ok, reason := retriever.Status(); !ok {
			return errors.New(reason)
		}
		return nil
	}))
}

// addExecutorModules 把受控执行器作为一等运行时模块接入健康检查和关闭
// 流程，避免“工作池存在但运行时看不见”。
func addExecutorModules(app *appruntime.App, components *appComponents) {
	app.AddModule(components.messageExecutor)
	app.AddModule(components.reactionExecutor)
	app.AddModule(components.recordingExecutor)
	app.AddModule(components.chunkExecutor)
	app.AddModule(components.scheduleExecutor)
}

// addApplicationModules 注册依赖基础设施和执行器的上层模块。这里把遗留
// 包级初始化收敛成有序的运行时阶段。
func addApplicationModules(app *appruntime.App, cfg *infraConfig.BaseConfig, components *appComponents) {
	app.AddModule(appruntime.NewFuncModule(appruntime.FuncModuleOptions{
		Name:     "application_services",
		Critical: true,
		Start: func(context.Context) error {
			recording.SetBackgroundSubmitter(components.recordingExecutor)
			larkchunking.SetExecutor(components.chunkExecutor)
			todoapp.Init(db.DB())
			scheduleapp.Init(db.DB(), handlers.BuildSchedulableTools())
			return nil
		},
		Ready: func(context.Context) error {
			if !scheduleapp.GetService().Available() {
				return errors.New("schedule service unavailable")
			}
			if !todoapp.GetService().Available() {
				return errors.New("todo service unavailable")
			}
			return nil
		},
	}))
	app.AddModule(appruntime.NewFuncModule(appruntime.FuncModuleOptions{
		Name:     "chunking",
		Critical: false,
		Start: func(ctx context.Context) error {
			larkchunking.Start(ctx)
			if !larkchunking.Enabled() {
				return fmt.Errorf("%w: %s", appruntime.ErrDisabled, larkchunking.DisableReason())
			}
			return nil
		},
		Ready: func(context.Context) error {
			if !larkchunking.Enabled() {
				return errors.New(larkchunking.DisableReason())
			}
			return nil
		},
		Stop: func(context.Context) error {
			larkchunking.Stop()
			return nil
		},
	}))
	app.AddModule(appruntime.NewHealthHTTPModule(
		managementAddr(cfg),
		appruntime.ManagementShutdownTimeout(cfg),
		app.Registry(),
	))
	app.AddModule(appruntime.NewFuncModule(appruntime.FuncModuleOptions{
		Name:     "scheduler",
		Critical: false,
		Start: func(context.Context) error {
			service := scheduleapp.GetService()
			if !service.Available() {
				return fmt.Errorf("%w: schedule service unavailable", appruntime.ErrDisabled)
			}
			scheduler = scheduleapp.NewSchedulerWithExecutor(service, components.scheduleExecutor)
			scheduler.Start()
			return nil
		},
		Stop: func(context.Context) error {
			if scheduler != nil {
				scheduler.Stop()
			}
			return nil
		},
		Stats: components.scheduleExecutor.Stats,
	}))
	app.AddModule(appruntime.NewLarkWSModule(
		cfg.LarkConfig.AppID,
		cfg.LarkConfig.AppSecret,
		components.eventDispatcher,
	))
}

// newEventDispatcher 负责把运行时管理的 HandlerSet 绑定到当前订阅的
// Lark 事件类型上。
func newEventDispatcher(handlerSet *larkiface.HandlerSet) *dispatcher.EventDispatcher {
	return dispatcher.
		NewEventDispatcher("", "").
		OnP2MessageReactionCreatedV1(handlerSet.MessageReactionHandler).
		OnP2MessageReceiveV1(handlerSet.MessageV2Handler).
		OnP2ApplicationAppVersionAuditV6(handlerSet.AuditV6Handler).
		OnP2CardActionTrigger(handlerSet.CardActionHandler)
}
