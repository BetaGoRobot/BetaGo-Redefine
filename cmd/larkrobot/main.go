package main

import (
	"context"
	"os"

	appcardaction "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/cardaction"
	larkchunking "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/chunking"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/handlers"
	scheduleapp "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/schedule"
	todoapp "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/todo"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/aktool"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/gotify"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/miniodal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/neteaseapi"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/opensearch"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/retriever"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/interfaces/lark"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhttp"

	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"
)

func main() {
	cfg := loadConfig()
	initInfrastructure(cfg)
	initApplications()
	go scheduleapp.StartScheduler()
	go startLarkClient(cfg)
	select {}
}

func loadConfig() *config.BaseConfig {
	return config.LoadFile(loadConfigPath())
}

func loadConfigPath() string {
	if path := os.Getenv("BETAGO_CONFIG_PATH"); path != "" {
		return path
	}
	return ".dev/config.toml"
}

func initInfrastructure(cfg *config.BaseConfig) {
	otel.Init(cfg.OtelConfig)
	logs.Init() // 有先后顺序的.应当在otel之后
	db.Init(cfg.DBConfig)
	opensearch.Init(cfg.OpensearchConfig)
	ark_dal.Init(cfg.ArkConfig)
	miniodal.Init(cfg.MinioConfig)
	retriever.Init()
	neteaseapi.Init()
	aktool.Init()
	gotify.Init()
	larkchunking.Init()
	lark_dal.Init()
	xhttp.Init()
}

func initApplications() {
	appcardaction.RegisterBuiltins()
	todoapp.Init(db.DB())
	scheduleapp.Init(db.DB(), handlers.BuildSchedulableTools())
}

func newEventDispatcher() *dispatcher.EventDispatcher {
	return dispatcher.
		NewEventDispatcher("", "").
		OnP2MessageReactionCreatedV1(lark.MessageReactionHandler).
		OnP2MessageReceiveV1(lark.MessageV2Handler).
		OnP2ApplicationAppVersionAuditV6(lark.AuditV6Handler).
		OnP2CardActionTrigger(lark.CardActionHandler)
}

func startLarkClient(cfg *config.BaseConfig) {
	cli := larkws.NewClient(cfg.LarkConfig.AppID, cfg.LarkConfig.AppSecret,
		larkws.WithEventHandler(newEventDispatcher()),
		larkws.WithLogLevel(larkcore.LogLevelInfo),
	)

	err := cli.Start(context.Background())
	if err != nil {
		panic(err)
	}
}
