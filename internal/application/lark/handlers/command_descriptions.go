package handlers

// CommandDescription keeps CLI-facing help text close to the command handlers
// without reusing tool descriptions that are often longer and tool-oriented.
func (configSetHandler) CommandDescription() string {
	return "设置配置项"
}

func (configListHandler) CommandDescription() string {
	return "查看配置项"
}

func (configDeleteHandler) CommandDescription() string {
	return "删除配置项"
}

func (featureListHandler) CommandDescription() string {
	return "查看功能开关"
}

func (featureBlockHandler) CommandDescription() string {
	return "屏蔽功能"
}

func (featureUnblockHandler) CommandDescription() string {
	return "取消屏蔽功能"
}

func (wordAddHandler) CommandDescription() string {
	return "新增复读词条"
}

func (wordGetHandler) CommandDescription() string {
	return "查看复读词条"
}

func (replyAddHandler) CommandDescription() string {
	return "新增关键词回复"
}

func (replyGetHandler) CommandDescription() string {
	return "查看关键词回复"
}

func (imageAddHandler) CommandDescription() string {
	return "新增图片素材"
}

func (imageGetHandler) CommandDescription() string {
	return "查看图片素材"
}

func (imageDeleteHandler) CommandDescription() string {
	return "删除图片素材"
}

func (musicSearchHandler) CommandDescription() string {
	return "搜索音乐"
}

func (oneWordHandler) CommandDescription() string {
	return "发送一言或诗词"
}

func (muteHandler) CommandDescription() string {
	return "设置或解除禁言"
}

func (goldHandler) CommandDescription() string {
	return "查看金价走势"
}

func (zhAStockHandler) CommandDescription() string {
	return "查看 A 股走势"
}

func (trendHandler) CommandDescription() string {
	return "查看发言趋势"
}

func (wordCloudHandler) CommandDescription() string {
	return "生成词云和热点摘要"
}

func (rateLimitStatsHandler) CommandDescription() string {
	return "查看频控详情"
}

func (rateLimitListHandler) CommandDescription() string {
	return "查看频控概览"
}

func (debugGetIDHandler) CommandDescription() string {
	return "查看引用消息 ID"
}

func (debugGetGroupIDHandler) CommandDescription() string {
	return "查看当前会话 ID"
}

func (debugTryPanicHandler) CommandDescription() string {
	return "触发 panic 调试"
}

func (debugTraceHandler) CommandDescription() string {
	return "查看消息 trace"
}

func (debugRevertHandler) CommandDescription() string {
	return "撤回机器人消息"
}

func (debugRepeatHandler) CommandDescription() string {
	return "复发引用消息"
}

func (debugImageHandler) CommandDescription() string {
	return "分析引用图片"
}

func (debugConversationHandler) CommandDescription() string {
	return "查看对话上下文"
}
