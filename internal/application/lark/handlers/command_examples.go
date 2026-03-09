package handlers

// CommandExamples provides concise CLI examples that `/help` and inline
// `--help` can render directly without maintaining a separate help registry.
func (configSetHandler) CommandExamples() []string {
	return []string{
		"/config set --key=intent_recognition_enabled --value=true",
		"/config set --key=intent_recognition_enabled --value=false --scope=global",
	}
}

func (configListHandler) CommandExamples() []string {
	return []string{
		"/config list",
		"/config list --scope=user",
	}
}

func (configDeleteHandler) CommandExamples() []string {
	return []string{
		"/config delete --key=intent_recognition_enabled",
		"/config delete --key=intent_recognition_enabled --scope=global",
	}
}

func (featureListHandler) CommandExamples() []string {
	return []string{
		"/feature list",
	}
}

func (featureBlockHandler) CommandExamples() []string {
	return []string{
		"/feature block --feature=chat",
		"/feature block --feature=chat --scope=user",
	}
}

func (featureUnblockHandler) CommandExamples() []string {
	return []string{
		"/feature unblock --feature=chat",
		"/feature unblock --feature=chat --scope=user",
	}
}

func (wordAddHandler) CommandExamples() []string {
	return []string{
		"/word add --word=收到 --rate=80",
	}
}

func (wordGetHandler) CommandExamples() []string {
	return []string{
		"/word get",
	}
}

func (replyAddHandler) CommandExamples() []string {
	return []string{
		"/reply add --word=早安 --reply=早安早安",
		"/reply add --word=天气 --reply=今天天气不错 --type=substr",
	}
}

func (replyGetHandler) CommandExamples() []string {
	return []string{
		"/reply get",
	}
}

func (imageAddHandler) CommandExamples() []string {
	return []string{
		"/image add --url=https://example.com/demo.png",
	}
}

func (imageGetHandler) CommandExamples() []string {
	return []string{
		"/image get",
	}
}

func (imageDeleteHandler) CommandExamples() []string {
	return []string{
		"/image del",
	}
}

func (musicSearchHandler) CommandExamples() []string {
	return []string{
		"/music 稻香",
		"/music --type=album 范特西",
	}
}

func (oneWordHandler) CommandExamples() []string {
	return []string{
		"/oneword",
		"/oneword --type=诗词",
	}
}

func (muteHandler) CommandExamples() []string {
	return []string{
		"/mute --t=10m",
		"/mute --cancel",
	}
}

func (goldHandler) CommandExamples() []string {
	return []string{
		"/stock gold --d=7",
		"/stock gold --h=12",
	}
}

func (zhAStockHandler) CommandExamples() []string {
	return []string{
		"/stock zh_a --code=600519",
		"/stock zh_a --code=000001 --days=5",
	}
}

func (trendHandler) CommandExamples() []string {
	return []string{
		"/talkrate --days=7 --interval=1d",
		"/talkrate --play=bar --st=2026-03-01T00:00:00+08:00 --et=2026-03-07T23:59:59+08:00",
	}
}

func (wordCloudHandler) CommandExamples() []string {
	return []string{
		"/wc --days=7 --mtop=10 --ctop=5",
		"/wc --sort=time --chat_id=oc_xxx",
	}
}

func (rateLimitStatsHandler) CommandExamples() []string {
	return []string{
		"/ratelimit stats",
		"/ratelimit stats --chat_id=oc_xxx",
	}
}

func (rateLimitListHandler) CommandExamples() []string {
	return []string{
		"/ratelimit list",
	}
}

func (chatHandler) CommandExamples() []string {
	return []string{
		"/bb 今天天气怎么样",
		"/bb --r 帮我总结一下这周讨论",
	}
}

func (debugGetIDHandler) CommandExamples() []string {
	return []string{
		"/debug msgid",
	}
}

func (debugGetGroupIDHandler) CommandExamples() []string {
	return []string{
		"/debug chatid",
	}
}

func (debugTryPanicHandler) CommandExamples() []string {
	return []string{
		"/debug panic",
	}
}

func (debugTraceHandler) CommandExamples() []string {
	return []string{
		"/debug trace",
	}
}

func (debugRevertHandler) CommandExamples() []string {
	return []string{
		"/debug revert",
	}
}

func (debugRepeatHandler) CommandExamples() []string {
	return []string{
		"/debug repeat",
	}
}

func (debugImageHandler) CommandExamples() []string {
	return []string{
		"/debug image",
		"/debug image 这张图里有什么",
	}
}

func (debugConversationHandler) CommandExamples() []string {
	return []string{
		"/debug conver",
	}
}
