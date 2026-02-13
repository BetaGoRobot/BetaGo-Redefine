package lark_dal

import (
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	lark "github.com/larksuite/oapi-sdk-go/v3"
)

var client *lark.Client

func Client() *lark.Client { // for 外部调用
	return client
}

func InitLarkClient(conf config.LarkConfig) {
	client = lark.NewClient(conf.AppID, conf.AppSecret)
}
