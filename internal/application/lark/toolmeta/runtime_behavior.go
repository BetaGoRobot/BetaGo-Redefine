package toolmeta

type SideEffectLevel string

const (
	SideEffectLevelNone          SideEffectLevel = "none"
	SideEffectLevelChatWrite     SideEffectLevel = "chat_write"
	SideEffectLevelExternalWrite SideEffectLevel = "external_write"
	SideEffectLevelAdminWrite    SideEffectLevel = "admin_write"
)

type ApprovalBehavior struct {
	ResultKey         string
	PlaceholderOutput string
	ApprovalType      string
	ApprovalTitle     string
}

type RuntimeBehavior struct {
	SideEffectLevel       SideEffectLevel
	AllowCompatibleOutput bool
	Approval              *ApprovalBehavior
}

func (b RuntimeBehavior) RequiresApproval() bool {
	return b.Approval != nil
}

func LookupRuntimeBehavior(name string) (RuntimeBehavior, bool) {
	behavior, ok := runtimeBehaviors[name]
	if !ok {
		return RuntimeBehavior{}, false
	}
	return behavior, true
}

func SideEffectLevelOf(name string) SideEffectLevel {
	if behavior, ok := LookupRuntimeBehavior(name); ok && behavior.SideEffectLevel != "" {
		return behavior.SideEffectLevel
	}
	return SideEffectLevelNone
}

func AllowCompatibleOutput(name string) bool {
	behavior, ok := LookupRuntimeBehavior(name)
	return ok && behavior.AllowCompatibleOutput
}

func RequiresApproval(name string) bool {
	behavior, ok := LookupRuntimeBehavior(name)
	return ok && behavior.RequiresApproval()
}

var runtimeBehaviors = map[string]RuntimeBehavior{
	"send_message": {
		SideEffectLevel: SideEffectLevelChatWrite,
		Approval: &ApprovalBehavior{
			ResultKey:         "send_message_result",
			PlaceholderOutput: "已发起审批，等待确认后发送消息。",
			ApprovalType:      "capability",
			ApprovalTitle:     "审批发送消息",
		},
	},
	"revert_message": {
		SideEffectLevel: SideEffectLevelChatWrite,
		Approval: &ApprovalBehavior{
			ResultKey:         "revert_result",
			PlaceholderOutput: "已发起审批，等待确认后撤回消息。",
			ApprovalType:      "capability",
			ApprovalTitle:     "审批撤回消息",
		},
	},
	"oneword_get": {
		SideEffectLevel:       SideEffectLevelChatWrite,
		AllowCompatibleOutput: true,
		Approval: &ApprovalBehavior{
			ResultKey:         "oneword_result",
			PlaceholderOutput: "已发起审批，等待确认后发送一言。",
			ApprovalType:      "capability",
			ApprovalTitle:     "审批发送一言",
		},
	},
	"music_search": {
		SideEffectLevel:       SideEffectLevelChatWrite,
		AllowCompatibleOutput: true,
		Approval: &ApprovalBehavior{
			ResultKey:         "music_search_result",
			PlaceholderOutput: "已发起审批，等待确认后发送音乐卡片。",
			ApprovalType:      "capability",
			ApprovalTitle:     "审批发送音乐卡片",
		},
	},
	"gold_price_get": {
		SideEffectLevel:       SideEffectLevelChatWrite,
		AllowCompatibleOutput: true,
		Approval: &ApprovalBehavior{
			ResultKey:         "gold_result",
			PlaceholderOutput: "已发起审批，等待确认后发送金价走势卡。",
			ApprovalType:      "capability",
			ApprovalTitle:     "审批发送金价走势卡",
		},
	},
	"stock_zh_a_get": {
		SideEffectLevel:       SideEffectLevelChatWrite,
		AllowCompatibleOutput: true,
		Approval: &ApprovalBehavior{
			ResultKey:         "stock_result",
			PlaceholderOutput: "已发起审批，等待确认后发送股票走势卡。",
			ApprovalType:      "capability",
			ApprovalTitle:     "审批发送股票走势卡",
		},
	},
	"talkrate_get": {
		SideEffectLevel:       SideEffectLevelChatWrite,
		AllowCompatibleOutput: true,
		Approval: &ApprovalBehavior{
			ResultKey:         "talkrate_get_result",
			PlaceholderOutput: "已发起审批，等待确认后发送发言趋势图。",
			ApprovalType:      "capability",
			ApprovalTitle:     "审批发送发言趋势图",
		},
	},
	"word_cloud_get": {
		SideEffectLevel:       SideEffectLevelChatWrite,
		AllowCompatibleOutput: true,
		Approval: &ApprovalBehavior{
			ResultKey:         "word_cloud_get_result",
			PlaceholderOutput: "已发起审批，等待确认后发送词云卡片。",
			ApprovalType:      "capability",
			ApprovalTitle:     "审批发送词云卡片",
		},
	},
	"word_cloud_graph_get": {
		SideEffectLevel:       SideEffectLevelChatWrite,
		AllowCompatibleOutput: true,
		Approval: &ApprovalBehavior{
			ResultKey:         "word_cloud_graph_get_result",
			PlaceholderOutput: "已发起审批，等待确认后发送词云图。",
			ApprovalType:      "capability",
			ApprovalTitle:     "审批发送词云图",
		},
	},
	"word_chunks_get": {
		SideEffectLevel:       SideEffectLevelChatWrite,
		AllowCompatibleOutput: true,
		Approval: &ApprovalBehavior{
			ResultKey:         "word_chunks_get_result",
			PlaceholderOutput: "已发起审批，等待确认后发送 chunk 列表卡片。",
			ApprovalType:      "capability",
			ApprovalTitle:     "审批发送 chunk 列表卡片",
		},
	},
	"word_chunk_detail_get": {
		SideEffectLevel:       SideEffectLevelChatWrite,
		AllowCompatibleOutput: true,
		Approval: &ApprovalBehavior{
			ResultKey:         "word_chunk_detail_get_result",
			PlaceholderOutput: "已发起审批，等待确认后发送 chunk 详情卡片。",
			ApprovalType:      "capability",
			ApprovalTitle:     "审批发送 chunk 详情卡片",
		},
	},
	"word_get": {
		SideEffectLevel:       SideEffectLevelChatWrite,
		AllowCompatibleOutput: true,
		Approval: &ApprovalBehavior{
			ResultKey:         "word_get_result",
			PlaceholderOutput: "已发起审批，等待确认后发送复读词条卡片。",
			ApprovalType:      "capability",
			ApprovalTitle:     "审批发送复读词条卡片",
		},
	},
	"reply_get": {
		SideEffectLevel:       SideEffectLevelChatWrite,
		AllowCompatibleOutput: true,
		Approval: &ApprovalBehavior{
			ResultKey:         "reply_get_result",
			PlaceholderOutput: "已发起审批，等待确认后发送关键词回复卡片。",
			ApprovalType:      "capability",
			ApprovalTitle:     "审批发送关键词回复卡片",
		},
	},
	"image_get": {
		SideEffectLevel:       SideEffectLevelChatWrite,
		AllowCompatibleOutput: true,
		Approval: &ApprovalBehavior{
			ResultKey:         "image_get_result",
			PlaceholderOutput: "已发起审批，等待确认后发送图片素材卡片。",
			ApprovalType:      "capability",
			ApprovalTitle:     "审批发送图片素材卡片",
		},
	},
	"config_list": {
		SideEffectLevel:       SideEffectLevelChatWrite,
		AllowCompatibleOutput: true,
		Approval: &ApprovalBehavior{
			ResultKey:         "config_list_result",
			PlaceholderOutput: "已发起审批，等待确认后发送配置列表卡片。",
			ApprovalType:      "capability",
			ApprovalTitle:     "审批发送配置列表卡片",
		},
	},
	"feature_list": {
		SideEffectLevel:       SideEffectLevelChatWrite,
		AllowCompatibleOutput: true,
		Approval: &ApprovalBehavior{
			ResultKey:         "feature_list_result",
			PlaceholderOutput: "已发起审批，等待确认后发送功能开关卡片。",
			ApprovalType:      "capability",
			ApprovalTitle:     "审批发送功能开关卡片",
		},
	},
	"ratelimit_stats_get": {
		SideEffectLevel:       SideEffectLevelChatWrite,
		AllowCompatibleOutput: true,
		Approval: &ApprovalBehavior{
			ResultKey:         "ratelimit_stats_get_result",
			PlaceholderOutput: "已发起审批，等待确认后发送频控详情卡片。",
			ApprovalType:      "capability",
			ApprovalTitle:     "审批发送频控详情卡片",
		},
	},
	"ratelimit_list": {
		SideEffectLevel:       SideEffectLevelChatWrite,
		AllowCompatibleOutput: true,
		Approval: &ApprovalBehavior{
			ResultKey:         "ratelimit_list_result",
			PlaceholderOutput: "已发起审批，等待确认后发送频控概览卡片。",
			ApprovalType:      "capability",
			ApprovalTitle:     "审批发送频控概览卡片",
		},
	},
	"research_read_url": {
		SideEffectLevel: SideEffectLevelNone,
	},
	"research_extract_evidence": {
		SideEffectLevel: SideEffectLevelNone,
	},
	"research_source_ledger": {
		SideEffectLevel: SideEffectLevelNone,
	},
	"finance_tool_discover": {
		SideEffectLevel: SideEffectLevelNone,
	},
	"finance_market_data_get": {
		SideEffectLevel: SideEffectLevelNone,
	},
	"finance_news_get": {
		SideEffectLevel: SideEffectLevelNone,
	},
	"economy_indicator_get": {
		SideEffectLevel: SideEffectLevelNone,
	},
	"config_set": {
		SideEffectLevel: SideEffectLevelAdminWrite,
		Approval: &ApprovalBehavior{
			ResultKey:         "config_action_result",
			PlaceholderOutput: "已发起审批，等待确认后修改配置。",
			ApprovalType:      "capability",
			ApprovalTitle:     "审批修改配置",
		},
	},
	"config_delete": {
		SideEffectLevel: SideEffectLevelAdminWrite,
		Approval: &ApprovalBehavior{
			ResultKey:         "config_action_result",
			PlaceholderOutput: "已发起审批，等待确认后删除配置。",
			ApprovalType:      "capability",
			ApprovalTitle:     "审批删除配置",
		},
	},
	"feature_block": {
		SideEffectLevel: SideEffectLevelAdminWrite,
		Approval: &ApprovalBehavior{
			ResultKey:         "feature_action_result",
			PlaceholderOutput: "已发起审批，等待确认后屏蔽功能。",
			ApprovalType:      "capability",
			ApprovalTitle:     "审批屏蔽功能",
		},
	},
	"feature_unblock": {
		SideEffectLevel: SideEffectLevelAdminWrite,
		Approval: &ApprovalBehavior{
			ResultKey:         "feature_action_result",
			PlaceholderOutput: "已发起审批，等待确认后恢复功能。",
			ApprovalType:      "capability",
			ApprovalTitle:     "审批恢复功能",
		},
	},
	"mute_robot": {
		SideEffectLevel: SideEffectLevelAdminWrite,
		Approval: &ApprovalBehavior{
			ResultKey:         "mute_result",
			PlaceholderOutput: "已发起审批，等待确认后调整禁言。",
			ApprovalType:      "capability",
			ApprovalTitle:     "审批调整禁言",
		},
	},
	"permission_manage": {
		SideEffectLevel:       SideEffectLevelAdminWrite,
		AllowCompatibleOutput: true,
		Approval: &ApprovalBehavior{
			ResultKey:         "permission_manage_result",
			PlaceholderOutput: "已发起审批，等待确认后发送权限管理卡片。",
			ApprovalType:      "capability",
			ApprovalTitle:     "审批权限管理",
		},
	},
	"word_add": {
		SideEffectLevel: SideEffectLevelExternalWrite,
		Approval: &ApprovalBehavior{
			ResultKey:         "word_action_result",
			PlaceholderOutput: "已发起审批，等待确认后更新复读词条。",
			ApprovalType:      "capability",
			ApprovalTitle:     "审批更新复读词条",
		},
	},
	"reply_add": {
		SideEffectLevel: SideEffectLevelExternalWrite,
		Approval: &ApprovalBehavior{
			ResultKey:         "reply_action_result",
			PlaceholderOutput: "已发起审批，等待确认后添加关键词回复。",
			ApprovalType:      "capability",
			ApprovalTitle:     "审批添加关键词回复",
		},
	},
	"image_add": {
		SideEffectLevel: SideEffectLevelExternalWrite,
		Approval: &ApprovalBehavior{
			ResultKey:         "image_action_result",
			PlaceholderOutput: "已发起审批，等待确认后添加图片素材。",
			ApprovalType:      "capability",
			ApprovalTitle:     "审批添加图片素材",
		},
	},
	"image_delete": {
		SideEffectLevel: SideEffectLevelExternalWrite,
		Approval: &ApprovalBehavior{
			ResultKey:         "image_action_result",
			PlaceholderOutput: "已发起审批，等待确认后删除图片素材。",
			ApprovalType:      "capability",
			ApprovalTitle:     "审批删除图片素材",
		},
	},
	"create_schedule": {
		SideEffectLevel: SideEffectLevelExternalWrite,
		Approval: &ApprovalBehavior{
			ResultKey:         "schedule_tool_result",
			PlaceholderOutput: "已发起审批，等待确认后创建 schedule。",
			ApprovalType:      "capability",
			ApprovalTitle:     "审批创建 schedule",
		},
	},
	"delete_schedule": {
		SideEffectLevel: SideEffectLevelExternalWrite,
		Approval: &ApprovalBehavior{
			ResultKey:         "schedule_tool_result",
			PlaceholderOutput: "已发起审批，等待确认后删除 schedule。",
			ApprovalType:      "capability",
			ApprovalTitle:     "审批删除 schedule",
		},
	},
	"pause_schedule": {
		SideEffectLevel: SideEffectLevelExternalWrite,
		Approval: &ApprovalBehavior{
			ResultKey:         "schedule_tool_result",
			PlaceholderOutput: "已发起审批，等待确认后暂停 schedule。",
			ApprovalType:      "capability",
			ApprovalTitle:     "审批暂停 schedule",
		},
	},
	"resume_schedule": {
		SideEffectLevel: SideEffectLevelExternalWrite,
		Approval: &ApprovalBehavior{
			ResultKey:         "schedule_tool_result",
			PlaceholderOutput: "已发起审批，等待确认后恢复 schedule。",
			ApprovalType:      "capability",
			ApprovalTitle:     "审批恢复 schedule",
		},
	},
	"create_todo": {
		SideEffectLevel: SideEffectLevelExternalWrite,
		Approval: &ApprovalBehavior{
			ResultKey:         "todo_tool_result",
			PlaceholderOutput: "已发起审批，等待确认后创建待办。",
			ApprovalType:      "capability",
			ApprovalTitle:     "审批创建待办",
		},
	},
	"update_todo": {
		SideEffectLevel: SideEffectLevelExternalWrite,
		Approval: &ApprovalBehavior{
			ResultKey:         "todo_tool_result",
			PlaceholderOutput: "已发起审批，等待确认后更新待办。",
			ApprovalType:      "capability",
			ApprovalTitle:     "审批更新待办",
		},
	},
	"delete_todo": {
		SideEffectLevel: SideEffectLevelExternalWrite,
		Approval: &ApprovalBehavior{
			ResultKey:         "todo_tool_result",
			PlaceholderOutput: "已发起审批，等待确认后删除待办。",
			ApprovalType:      "capability",
			ApprovalTitle:     "审批删除待办",
		},
	},
}
