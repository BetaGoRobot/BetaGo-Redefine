package handlers

import (
	scheduleapp "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/schedule"
	todoapp "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/todo"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xcommand"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func larktools() *tools.Impl[larkim.P2MessageReceiveV1] {
	ins := tools.New[larkim.P2MessageReceiveV1]().WebSearch()
	registerBaseTools(ins)
	revert(ins)
	scheduleapp.RegisterTools(ins)
	return ins
}

func registerBaseTools(ins *tools.Impl[larkim.P2MessageReceiveV1]) {
	musicSearch(ins)
	oneWordTool(ins)
	muteBot(ins)
	goldReport(ins)
	stockZhATool(ins)
	hybridSearch(ins)
	trendReportTool(ins)
	wordCloudTool(ins)
	configTools(ins)
	featureTools(ins)
	wordTools(ins)
	replyTools(ins)
	imageTools(ins)
	rateLimitTools(ins)
	sendMessageTool(ins)
	todoapp.RegisterTools(ins)
}

func BuildSchedulableTools() *tools.Impl[larkim.P2MessageReceiveV1] {
	ins := tools.New[larkim.P2MessageReceiveV1]()
	registerBaseTools(ins)
	return ins
}

func hybridSearch(ins *tools.Impl[larkim.P2MessageReceiveV1]) {
	xcommand.RegisterTool(ins, SearchHistory)
}

func musicSearch(ins *tools.Impl[larkim.P2MessageReceiveV1]) {
	xcommand.RegisterTool(ins, MusicSearch)
}

func muteBot(ins *tools.Impl[larkim.P2MessageReceiveV1]) {
	xcommand.RegisterTool(ins, Mute)
}

func goldReport(ins *tools.Impl[larkim.P2MessageReceiveV1]) {
	xcommand.RegisterTool(ins, Gold)
}

func revert(ins *tools.Impl[larkim.P2MessageReceiveV1]) {
	xcommand.RegisterTool(ins, DebugRevert)
}

func oneWordTool(ins *tools.Impl[larkim.P2MessageReceiveV1]) {
	xcommand.RegisterTool(ins, OneWord)
}

func stockZhATool(ins *tools.Impl[larkim.P2MessageReceiveV1]) {
	xcommand.RegisterTool(ins, ZhAStock)
}

func trendReportTool(ins *tools.Impl[larkim.P2MessageReceiveV1]) {
	xcommand.RegisterTool(ins, Trend)
}

func wordCloudTool(ins *tools.Impl[larkim.P2MessageReceiveV1]) {
	xcommand.RegisterTool(ins, WordCloud)
}

func configTools(ins *tools.Impl[larkim.P2MessageReceiveV1]) {
	xcommand.RegisterTool(ins, ConfigList)
	xcommand.RegisterTool(ins, ConfigSet)
	xcommand.RegisterTool(ins, ConfigDelete)
}

func featureTools(ins *tools.Impl[larkim.P2MessageReceiveV1]) {
	xcommand.RegisterTool(ins, FeatureList)
	xcommand.RegisterTool(ins, FeatureBlock)
	xcommand.RegisterTool(ins, FeatureUnblock)
}

func wordTools(ins *tools.Impl[larkim.P2MessageReceiveV1]) {
	xcommand.RegisterTool(ins, WordAdd)
	xcommand.RegisterTool(ins, WordGet)
}

func replyTools(ins *tools.Impl[larkim.P2MessageReceiveV1]) {
	xcommand.RegisterTool(ins, ReplyAdd)
	xcommand.RegisterTool(ins, ReplyGet)
}

func imageTools(ins *tools.Impl[larkim.P2MessageReceiveV1]) {
	xcommand.RegisterTool(ins, ImageAdd)
	xcommand.RegisterTool(ins, ImageGet)
	xcommand.RegisterTool(ins, ImageDelete)
}

func rateLimitTools(ins *tools.Impl[larkim.P2MessageReceiveV1]) {
	xcommand.RegisterTool(ins, RateLimitStats)
	xcommand.RegisterTool(ins, RateLimitList)
}

func sendMessageTool(ins *tools.Impl[larkim.P2MessageReceiveV1]) {
	xcommand.RegisterTool(ins, SendMessage)
}
