package main

import (
	"context"
	"os"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/miniodal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/opensearch"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/retriver"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/interfaces/lark"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"

	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"
)

func main() {
	path := ".dev/config.toml"
	if os.Getenv("BETAGO_CONFIG_PATH") != "" {
		path = os.Getenv("BETAGO_CONFIG_PATH")
	}
	config := config.LoadFile(path)

	otel.Init(config.OtelConfig)
	logs.Init() // 有先后顺序的.应当在otel之后
	db.Init(config.DBConfig)
	opensearch.Init(config.OpensearchConfig)
	ark_dal.Init(config.ArkConfig)
	miniodal.Init(config.MinioConfig)
	retriver.Init(config)

	go registerHandlers(config)
	select {}
}

func registerHandlers(config *config.BaseConfig) {
	eventHandler := dispatcher.
		NewEventDispatcher("", "").
		OnP2MessageReactionCreatedV1(lark.MessageReactionHandler).
		OnP2MessageReceiveV1(lark.MessageV2Handler).
		OnP2ApplicationAppVersionAuditV6(lark.AuditV6Handler).
		OnP2CardActionTrigger(lark.CardActionHandler)

	cli := larkws.NewClient(config.LarkConfig.AppID, config.LarkConfig.AppSecret,
		larkws.WithEventHandler(eventHandler),
		larkws.WithLogLevel(larkcore.LogLevelInfo),
	)

	// 启动客户端
	err := cli.Start(context.Background())
	if err != nil {
		panic(err)
	}
}
