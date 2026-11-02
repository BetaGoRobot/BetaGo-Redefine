package handlers

import (
	"context"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/memereference"
)

func TestMemeCorrectionAvailableInStandardModes(t *testing.T) {
	for _, mode := range []standardPromptMode{standardPromptModeDirect, standardPromptModeAmbient} {
		if !strings.Contains(buildStandardChatSystemPrompt(context.Background(), mode, ""), memereference.CorrectionInstruction) {
			t.Fatal("standard generator lost the explicit-feedback correction path")
		}
	}
}
