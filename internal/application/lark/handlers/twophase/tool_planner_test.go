package twophase

import (
	"strings"
	"testing"
)

func TestToolPlannerSystemPromptMentionsMessageContextSignals(t *testing.T) {
	for _, want := range []string{
		"MessageContext",
		"direct_addressing",
		"mentioned_bot",
		"这些字段是可靠信号",
		"工具类型仍必须由消息语义本身决定",
	} {
		if !strings.Contains(toolPlannerSystemPrompt, want) {
			t.Fatalf("toolPlannerSystemPrompt = %q, want contain %q", toolPlannerSystemPrompt, want)
		}
	}
}

func TestBuildToolPlannerUserPromptIncludesDirectSignals(t *testing.T) {
	msgCtx := PlannerMessageContext{
		Direct:       true,
		MentionedBot: true,
	}

	prompt := buildToolPlannerUserPrompt("来杯生椰拿铁", []string{
		"[2026-06-29 14:00:01](ou_a) <甲>: 下午好困",
	}, msgCtx)

	for _, want := range []string{
		"消息元信息",
		"direct_addressing: true",
		"mentioned_bot: true",
		"最近对话",
		"当前用户消息",
		"来杯生椰拿铁",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt = %q, want contain %q", prompt, want)
		}
	}
}
