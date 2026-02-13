package lark

import (
	"context"
	"fmt"
	"math/rand/v2"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/bytedance/gg/gresult"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

// 这里定义和注册一些functioncall的handler
// 举例，我们先定义一个
func TestLarkBotFunctionCallTools(t *testing.T) {
	// 注册一个垃圾函数
	config := config.LoadFile("../../../.dev/config.toml")
	otel.Init(config.OtelConfig)
	logs.Init()
	ark_dal.Init(config.ArkConfig)

	ins := tools.New[*larkim.P2MessageReceiveV1]()
	unit := tools.NewUnit[*larkim.P2MessageReceiveV1]()
	params := tools.NewParams("object").
		AddProp("city", &tools.Prop{
			Type: "string",
			Desc: "城市",
			Items: []*tools.Prop{
				{
					Type: "string",
					Desc: "城市名称",
				},
			},
		}).AddRequired("city")
	ins.Add(unit.Name("get_weather").Desc("根据城市获取天气").Params(params)).Handle(handleGetWeather)

	iter, err := ark_dal.New[*larkim.P2MessageReceiveV1](
		"chat_id", "user_id", nil,
	).WithTools(ins).Do(
		context.Background(), "你是一个气象分析专家,根据用户输入的城市名称,查询该城市的天气", "帮我查询一下绵阳市的天气；因为天气查询可能有不稳定的情况，请多查几次告诉我所有结果",
	)
	if err != nil {
		panic(err)
	}

	for item := range iter {
		fmt.Print(item)
	}
	fmt.Println()
}

func handleGetWeather(ctx context.Context, args string, input tools.FCMeta[*larkim.P2MessageReceiveV1]) gresult.R[string] {
	return gresult.OK(fmt.Sprintf("天气晴朗,温度25.%d摄氏度", rand.IntN(10)))
}
