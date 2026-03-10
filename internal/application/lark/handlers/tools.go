package handlers

import (
	scheduleapp "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/schedule"
	todoapp "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/todo"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xcommand"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func larktools() *tools.Impl[larkim.P2MessageReceiveV1] {
	return buildTools(true, true, true)
}

func BuildSchedulableTools() *tools.Impl[larkim.P2MessageReceiveV1] {
	return buildTools(false, false, false)
}

func buildTools(enableWebSearch, includeDebugRevert, includeScheduleTools bool) *tools.Impl[larkim.P2MessageReceiveV1] {
	ins := tools.New[larkim.P2MessageReceiveV1]()
	if enableWebSearch {
		ins.WebSearch()
	}

	registerBaseTools(ins)
	if includeDebugRevert {
		xcommand.RegisterTool(ins, DebugRevert)
	}
	if includeScheduleTools {
		scheduleapp.RegisterTools(ins)
	}
	return ins
}

func registerBaseTools(ins *tools.Impl[larkim.P2MessageReceiveV1]) {
	xcommand.RegisterTool(ins, SearchHistory)
	xcommand.RegisterTool(ins, MusicSearch)
	xcommand.RegisterTool(ins, Mute)
	xcommand.RegisterTool(ins, Gold)
	xcommand.RegisterTool(ins, OneWord)
	xcommand.RegisterTool(ins, ZhAStock)
	xcommand.RegisterTool(ins, Trend)
	xcommand.RegisterTool(ins, WordCloud)

	xcommand.RegisterTool(ins, ConfigList)
	xcommand.RegisterTool(ins, ConfigSet)
	xcommand.RegisterTool(ins, ConfigDelete)

	xcommand.RegisterTool(ins, FeatureList)
	xcommand.RegisterTool(ins, FeatureBlock)
	xcommand.RegisterTool(ins, FeatureUnblock)

	xcommand.RegisterTool(ins, WordAdd)
	xcommand.RegisterTool(ins, WordGet)

	xcommand.RegisterTool(ins, ReplyAdd)
	xcommand.RegisterTool(ins, ReplyGet)

	xcommand.RegisterTool(ins, ImageAdd)
	xcommand.RegisterTool(ins, ImageGet)
	xcommand.RegisterTool(ins, ImageDelete)

	xcommand.RegisterTool(ins, RateLimitStats)
	xcommand.RegisterTool(ins, RateLimitList)

	xcommand.RegisterTool(ins, SendMessage)
	todoapp.RegisterTools(ins)
}
