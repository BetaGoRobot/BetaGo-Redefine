package handlers

import (
	scheduleapp "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/schedule"
	todoapp "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/todo"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xcommand"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func BuildLarkTools() *tools.Impl[larkim.P2MessageReceiveV1] {
	ins := buildTools(true, true, true, true)
	xcommand.RegisterTool(ins, PermissionManage)
	return ins
}

func BuildInjectableFinanceTools() *tools.Impl[larkim.P2MessageReceiveV1] {
	ins := tools.New[larkim.P2MessageReceiveV1]()
	registerInjectableFinanceTools(ins)
	return ins
}

func BuildRuntimeCapabilityTools() *tools.Impl[larkim.P2MessageReceiveV1] {
	ins := BuildLarkTools()
	registerInjectableFinanceTools(ins)
	return ins
}

func larktools() *tools.Impl[larkim.P2MessageReceiveV1] {
	return BuildLarkTools()
}

func BuildSchedulableTools() *tools.Impl[larkim.P2MessageReceiveV1] {
	ins := buildTools(false, false, false, false)
	scheduleapp.RegisterRuntimeTools(ins)
	return ins
}

func buildTools(enableWebSearch, includeDebugRevert, includeScheduleTools, allowTargetChatOverride bool) *tools.Impl[larkim.P2MessageReceiveV1] {
	ins := tools.New[larkim.P2MessageReceiveV1]()
	if enableWebSearch {
		ins.WebSearch()
	}

	registerBaseTools(ins, allowTargetChatOverride)
	if includeDebugRevert {
		xcommand.RegisterTool(ins, DebugRevert)
	}
	if includeScheduleTools {
		scheduleapp.RegisterTools(ins)
	}
	return ins
}

func registerBaseTools(ins *tools.Impl[larkim.P2MessageReceiveV1], allowTargetChatOverride bool) {
	xcommand.RegisterTool(ins, ChatMembers)
	xcommand.RegisterTool(ins, RecentActiveMembers)
	xcommand.RegisterTool(ins, SearchHistory)
	xcommand.RegisterTool(ins, ResearchReadURL)
	xcommand.RegisterTool(ins, ResearchExtractEvidence)
	xcommand.RegisterTool(ins, ResearchSourceLedger)
	xcommand.RegisterTool(ins, FinanceToolDiscover)
	xcommand.RegisterTool(ins, MusicSearch)
	xcommand.RegisterTool(ins, Mute)
	xcommand.RegisterTool(ins, Gold)
	xcommand.RegisterTool(ins, OneWord)
	xcommand.RegisterTool(ins, ZhAStock)
	xcommand.RegisterTool(ins, Trend)
	xcommand.RegisterTool(ins, WordCloud)
	xcommand.RegisterTool(ins, WordCloudGraph)
	xcommand.RegisterTool(ins, WordChunks)
	xcommand.RegisterTool(ins, WordChunkDetail)

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

	if allowTargetChatOverride {
		xcommand.RegisterTool(ins, SendMessage)
	} else {
		xcommand.RegisterTool(ins, ScheduledSendMessage)
	}
	todoapp.RegisterTools(ins)
}

func registerInjectableFinanceTools(ins *tools.Impl[larkim.P2MessageReceiveV1]) {
	xcommand.RegisterTool(ins, FinanceMarketData)
	xcommand.RegisterTool(ins, FinanceNews)
	xcommand.RegisterTool(ins, EconomyIndicator)
}
