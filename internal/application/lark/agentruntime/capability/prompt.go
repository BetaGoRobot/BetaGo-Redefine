package capability

import (
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/toolmeta"
)

// DecoratePrompt implements capability runtime behavior.
func DecoratePrompt(name, desc string) string {
	behavior, ok := toolmeta.LookupRuntimeBehavior(strings.TrimSpace(name))
	if !ok {
		return desc
	}

	switch {
	case behavior.RequiresApproval():
		return appendPromptNote(desc, "该工具属于有副作用动作，调用后会先进入审批等待，不会立刻执行。只有在用户明确要求执行这个动作时才使用。")
	case behavior.SideEffectLevel == toolmeta.SideEffectLevelNone:
		return appendPromptNote(desc, "该工具是只读查询工具，不会修改群聊、配置或共享状态。遇到金价、股价、历史检索等需要事实的问题时应优先使用。")
	default:
		return desc
	}
}

func appendPromptNote(base, note string) string {
	base = strings.TrimSpace(base)
	note = strings.TrimSpace(note)
	if note == "" || strings.Contains(base, note) {
		return base
	}
	if base == "" {
		return note
	}
	if strings.HasSuffix(base, "。") || strings.HasSuffix(base, ".") {
		return base + " " + note
	}
	return base + "。 " + note
}
