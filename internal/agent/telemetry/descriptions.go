package telemetry

import "strings"

func description(event string) string {
	for suffix, label := range map[string]string{".started": "开始", ".completed": "完成", ".failed": "失败", ".deferred": "等待后重试"} {
		if strings.HasSuffix(event, suffix) {
			return description(strings.TrimSuffix(event, suffix)) + "：" + label
		}
	}
	switch event {
	case "agent.ingress":
		return "接收并保存飞书事件"
	case "agent.message.received":
		return "收到群消息"
	case "agent.route.decision":
		return "判断消息参与方式"
	case "agent.turn":
		return "处理一轮群聊请求"
	case "agent.turn.refresh":
		return "重新核对消息、任务和上下文有效性"
	case "agent.turn.start":
		return "领取并开始本轮对话"
	case "agent.turn.set_context":
		return "保存本轮上下文与消息引用"
	case "agent.turn.prompt", "agent.turn.prompt.built", "agent.turn.prompt.content":
		return "构建模型上下文与召回材料"
	case "agent.turn.decision":
		return "模型决定旁听、回复或展开回答"
	case "agent.turn.native", "agent.turn.native.result":
		return "执行模型与工具循环"
	case "agent.model.generate", "agent.model.call", "model.request":
		return "调用模型"
	case "agent.model.request", "agent.model.request.content", "model.request.encoded", "model.request.content":
		return "发送模型输入、工具定义与输出协议"
	case "agent.model.response", "agent.model.response.content", "model.response.decoded", "model.response.content", "model.provider.response":
		return "收到模型输出与用量"
	case "agent.tool.invoke", "agent.tool.arguments":
		return "调用工具并记录参数"
	case "agent.tool.result", "agent.tool.output":
		return "工具执行结果"
	case "agent.tool.cache_hit":
		return "复用已有工具回执"
	case "agent.tool.validate":
		return "校验工具参数"
	case "agent.tool.refresh":
		return "执行前再次核对工具权限和来源"
	case "agent.tool.begin_ledger":
		return "登记工具执行以避免重复操作"
	case "agent.tool.execute":
		return "执行工具请求"
	case "agent.tool.bind_sources":
		return "核验并绑定工具返回的来源"
	case "agent.tool.finish_ledger":
		return "保存工具执行结果与回执"
	case "agent.delivery.queued":
		return "回答已进入发送队列"
	case "agent.delivery.send", "agent.delivery.content":
		return "向群聊发送回答"
	case "agent.delivery.update":
		return "更新已有回答卡片"
	case "agent.delivery.receipt":
		return "收到飞书发送回执"
	case "agent.delivery.persisted":
		return "发送回执已保存"
	case "agent.route":
		return "路由消息并创建任务"
	case "agent.delivery":
		return "检查并交付已排队的回答"
	case "agent.retrieval", "agent.retrieval.query":
		return "检索群聊历史或长期记忆"
	case "agent.retrieval.channel", "agent.retrieval.candidates":
		return "读取召回通道的原始候选"
	case "agent.retrieval.summary":
		return "核验、排序并筛选召回结果"
	case "agent.retrieval.results":
		return "召回历史或记忆材料"
	case "agent.memory.response":
		return "长期记忆召回的完整响应"
	case "agent.message.content":
		return "收到消息的明文内容"
	case "agent.clock":
		return "刷新模型可见的北京时间"
	}
	if strings.HasPrefix(event, "agent.job.") {
		return "执行异步任务（" + strings.TrimPrefix(event, "agent.job.") + "）"
	}
	if strings.HasSuffix(event, ".http") {
		return "请求外部服务（" + strings.TrimSuffix(event, ".http") + "）"
	}
	return event
}
