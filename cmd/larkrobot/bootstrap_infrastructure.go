package main

import (
	"context"
	"errors"
	"runtime/debug"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/akshareapi"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	infraConfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/gotify"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/llmusage"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/miniodal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/neteaseapi"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/opensearch"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	redis_dal "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/redis"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/retriever"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/schema"
	appruntime "github.com/BetaGoRobot/BetaGo-Redefine/internal/runtime"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhttp"
)

// addInfrastructureModules 注册基础设施层模块。顺序严格反映依赖方向：
// 先准备底层连接和客户端，再让上层应用服务接入它们。
func addInfrastructureModules(
	app *appruntime.App,
	cfg *infraConfig.BaseConfig,
	components *appComponents,
) {
	app.AddModule(newRecoverModule("otel", false, func() {
		otel.Init(cfg.OtelConfig)
	}))
	app.AddModule(newRecoverModule("vm_metrics", false, func() {
		initVMMetrics(cfg.VMConfig)
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
				llmusage.SetDefaultBotIDProvider(func() string {
					id := botidentity.Current()
					if id.AppID != "" {
						return "lark:" + id.AppID
					}
					return ""
				})
				llmusage.SetDefaultRecorder(llmusage.NewRecorder(db.DB()))
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
		Name:     "runtime_schema",
		Critical: true,
		Init: func(ctx context.Context) (initErr error) {
			if components != nil && components.schemaBootstrap != nil {
				defer func() {
					components.schemaBootstrap.Complete(initErr)
				}()
			}
			runner := &schema.Runner{
				DB:         db.DB(),
				Schema:     runtimeSchemaName(cfg),
				Revision:   runtimeSchemaRevision(),
				Migrations: schema.DefaultMigrations(),
			}
			report, err := runner.Apply(ctx)
			if components != nil && components.schemaBootstrap != nil {
				components.schemaBootstrap.Update(map[string]any{
					"latest_version":  report.LatestVersion,
					"latest_checksum": report.LatestChecksum,
					"applied_count":   len(report.Applied),
					"skipped_count":   len(report.Skipped),
				})
			}
			return err
		},
		Stats: func() map[string]any {
			if components == nil {
				return nil
			}
			return components.schemaBootstrap.Stats()
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
	app.AddModule(newOptionalModule("akshareapi", func() {
		akshareapi.Init()
	}, func(context.Context) error {
		if ok, reason := akshareapi.Status(); !ok {
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

func runtimeSchemaName(cfg *infraConfig.BaseConfig) string {
	if cfg == nil || cfg.DBConfig == nil {
		return "betago"
	}
	searchPath := strings.TrimSpace(cfg.DBConfig.SearchPath)
	if searchPath == "" {
		return "betago"
	}
	name := strings.Trim(strings.TrimSpace(strings.Split(searchPath, ",")[0]), `"`)
	if name == "" || name == "$user" {
		return "betago"
	}
	return name
}

func runtimeSchemaRevision() string {
	build, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	revision := ""
	modified := false
	for _, setting := range build.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = strings.TrimSpace(setting.Value)
		case "vcs.modified":
			modified = setting.Value == "true"
		}
	}
	if revision == "" {
		revision = "unknown"
	}
	if modified {
		revision += "-dirty"
	}
	return revision
}

func initVMMetrics(cfg *infraConfig.VMConfig) {
	if cfg == nil {
		return
	}
	pushInterval := time.Duration(cfg.PushInterval) * time.Second
	xhandler.InitMetrics(cfg.PushURL, pushInterval, cfg.Instance)
}
