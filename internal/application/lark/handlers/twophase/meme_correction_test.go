package twophase

import (
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/memereference"
)

func TestMemeCorrectionAvailableWithoutToolHint(t *testing.T) {
	if !strings.Contains(BuildReplyGeneratorPrompt("direct", "", nil), memereference.CorrectionInstruction) {
		t.Fatal("two-phase generator lost the correction path without a tool hint")
	}
	if !strings.Contains(toolPlannerSystemPrompt, "用户明确纠正机器人对梗的理解或召回") || !strings.Contains(toolPlannerSystemPrompt, "不必先查群历史") {
		t.Fatal("tool planner still blocks direct public correction behind history")
	}
}
