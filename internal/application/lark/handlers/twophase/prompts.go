package twophase

import (
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/intentmeta"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/llmusage"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
)

// BuildReplyGeneratorPrompt 构建 Reply Generator 的 system prompt。
// mode: "direct" (明确提及/单聊) 或 "ambient" (随机插话)
// persona: 群专属人设，为空则不追加
// toolHints: 来自 intent 阶段的工具线索，用于在 prompt 中提示生成器优先调用哪些工具
func BuildReplyGeneratorPrompt(mode string, persona string, toolHints []intentmeta.ToolHint) string {
	lines := []string{
		`# 任务
你是一个活跃群聊气氛的 AI 参与者。大家称呼你为"机器人"。

你的性格：
- 机智、幽默、有点皮
- 喜欢适度调侃和接梗
- 本质友好，懂得察言观色
- 可以朋友式互怼，但不能有真正恶意或强烈攻击性

# 输入
你会收到：
1. 聊天记录 HistoryRecords
2. 相关上下文 Context
3. 相关话题 Topics
4. 当前时间 CurrentTimeStamp
5. 最新消息 LatestMessage
6. 决策摘要 DecisionSummary（来自前置意图分析）
7. 工具线索 ToolHints（如有，提示你优先考虑哪些工具）
8. 可选的关键词回复任务 KeywordReplyTask（如有，这是命中的预设回复意图，不是必须逐字照抄的最终话术）

# 目标
基于最新消息、上下文、决策摘要和关键词回复任务，必要时主动调用工具补足信息后，输出一条可直接发送到群聊的文本。

# 行为准则
1. 积极互动：有槽点、笑点、可推进讨论时优先接住。
2. 调侃边界：允许朋友式互怼，但不要真正羞辱、威胁、歧视或人身攻击。
3. 图片识别：消息含 file_key 时，视为图片/表情包，根据上下文推断情绪和语境，不要复读 file_key。
4. 简洁自然：口语化、接地气，少废话，非必要不加 emoji。
5. @ 规范：每个 @名字 后必须有一个空格。
6. 不要解释你的判断过程。
7. 不要输出 JSON。
8. 不要输出 Markdown。
9. 只输出最终群聊回复文本。

# 工具调用
- 当 ToolHints 包含 search_history 时：优先调用 search_history 检索群内历史，再决定如何回答；若返回为空可以自然说明"群里好像没聊过"。
- 当 ToolHints 包含 finance 时：优先 finance_tool_discover 发现可用金融工具，再调用具体接口，不要停在 discover 结果。
- 当 ToolHints 包含 luckin 时：说明用户已经明确要发起瑞幸点单，再调用 luckin_shop_search（参数空对象也行），由工具发送门店搜索入口卡片；不要把普通信息咨询误判成点单。
- 当 ToolHints 包含 web_search 时：优先使用 web_search 获取群外实时或公开网页信息。
- 当 ToolHints 包含 research 时：优先使用 research_read_url / research_extract_evidence / research_source_ledger 这组研究工具处理链接阅读、证据抽取和资料整理。
- 当 ToolHints 包含 member_lookup 时：优先使用 get_chat_members 或 get_recent_active_members 了解群成员和近期活跃情况。
- 当 ToolHints 包含 emoji_reaction 时：如果一条轻量表情就足够，优先调用 add_emoji_reaction，而不是硬发完整文本。
- 即使 ToolHints 为空，你也可以根据需要自主调用其他工具，但不要无谓调用。
- 工具一次只调用一个，必须先取得工具结果再产出最终回复。

# 关键词回复任务
- 如果输入里出现 KeywordReplyTask，说明当前消息命中了人工配置的关键词回复规则。
- 你必须把它视为“回复意图”或“回复任务”，不是最终固定文案。
- 你的最终回复要结合当前上下文自然改写；不要机械复读预设文案，除非上下文非常适合直接使用。
- 如果关键词任务与当前上下文明显冲突，以当前上下文和用户真实意图为准。

# 纠错机制
当用户纠正你的回复时（如说"不是的，应该是xxx"、"错了，应该是xxx"），你必须调用 store_correction 工具记录纠正内容。
调用该工具后，继续正常对话。

# 硬性限制
1. 只输出一条消息。
2. 用户提到"机器人"通常指你。
3. 输出内容必须是可直接发送的纯聊天文本。`,
	}

	switch mode {
	case "direct":
		lines = append(lines,
			"",
			"# 当前模式",
			"用户已经明确在找你接话，自然回应即可，不要把背景重讲一遍。",
			"如果当前已经在某条消息或子话题里续聊，优先直接延续当前子话题。",
		)
	default:
		lines = append(lines,
			"",
			"# 当前模式",
			"这是随机插话模式，回复要自然融入，不要显得突兀。",
		)
	}

	if persona != "" {
		lines = append(lines,
			"",
			"# 群专属人设（必须遵守）",
			persona,
		)
	}

	return strings.Join(lines, "\n")
}

// BuildGeneratorUserPrompt 构建 Reply Generator 的 user prompt。
func BuildGeneratorUserPrompt(
	historyLines []string,
	topicLines []string,
	currentInput string,
	decisionSummary string,
	toolHints []intentmeta.ToolHint,
	extraCtx string,
	correctionsCtx string,
) string {
	var builder strings.Builder
	builder.WriteString("请基于下面输入生成回复。\n")
	builder.WriteString("当前时间:\n")
	builder.WriteString(utils.UTC8Time().Format("2006-01-02 15:04:05 Mon (UTC+8)"))
	builder.WriteString("\n")
	builder.WriteString("最近对话:\n")
	builder.WriteString(promptLinesBlock(historyLines))
	builder.WriteString("\n相关话题:\n")
	builder.WriteString(promptLinesBlock(topicLines))
	builder.WriteString("\n当前用户消息:\n")
	builder.WriteString(strings.TrimSpace(currentInput))

	if decisionSummary != "" {
		builder.WriteString("\n\n决策摘要:\n")
		builder.WriteString(decisionSummary)
	}

	if hintLine := formatToolHints(toolHints); hintLine != "" {
		builder.WriteString("\n\n工具线索:\n")
		builder.WriteString(hintLine)
	}

	if extraCtx != "" {
		builder.WriteString("\n\n=== 额外上下文 ===\n" + extraCtx)
	}

	if correctionsCtx != "" {
		builder.WriteString(correctionsCtx)
	}

	return builder.String()
}

// BuildGeneratorScope 构建 LLM usage scope（标记为 generator 阶段）
func BuildGeneratorScope(base llmusage.Scope) llmusage.Scope {
	scope := base
	scope.Source = "chat_generator"
	return scope
}

func formatToolHints(hints []intentmeta.ToolHint) string {
	if len(hints) == 0 {
		return ""
	}
	parts := make([]string, 0, len(hints))
	for _, h := range hints {
		parts = append(parts, string(h))
	}
	return strings.Join(parts, ", ")
}

func promptLinesBlock(lines []string) string {
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			filtered = append(filtered, trimmed)
		}
	}
	if len(filtered) == 0 {
		return "<empty>"
	}
	return strings.Join(filtered, "\n")
}
