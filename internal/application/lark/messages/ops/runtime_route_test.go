package ops

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestRuntimeIsMentionedOnlyTreatsBotMentionAsExplicit(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(configPath, []byte("[lark_config]\nbot_open_id = \"ou_bot\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if cfg := config.LoadFile(configPath); cfg == nil || cfg.LarkConfig == nil || cfg.LarkConfig.BotOpenID != "ou_bot" {
		t.Fatalf("LoadFile() returned unexpected config: %+v", cfg)
	}
	otherOpenID := "ou_alice"

	event := testMessageEvent("group", "oc_chat", "ou_actor")
	event.Event.Message.Mentions = []*larkim.MentionEvent{
		{
			Id: &larkim.UserId{
				OpenId: &otherOpenID,
			},
		},
	}

	if runtimeIsMentioned(event) {
		t.Fatalf("runtimeIsMentioned() = true, want false for non-bot mention %q", otherOpenID)
	}

	botOpenID := "ou_bot"
	event.Event.Message.Mentions = []*larkim.MentionEvent{
		{
			Id: &larkim.UserId{
				OpenId: &botOpenID,
			},
		},
	}
	if !runtimeIsMentioned(event) {
		t.Fatalf("runtimeIsMentioned() = false, want true for bot mention %q", botOpenID)
	}
}

func TestRuntimeContextForObservedMessageSkipsStandardMode(t *testing.T) {
	ctx := context.Background()
	ctx = runtimeContextForObservedMessage(ctx, appconfig.ChatModeStandard, agentruntime.ShadowObservation{
		PolicyDecision: agentruntime.PolicyDecision{
			EnterRuntime:   true,
			TriggerType:    agentruntime.TriggerTypeMention,
			SupersedeRunID: "run_active",
		},
	}, true, agentruntime.TriggerTypeMention)

	if _, ok := agentruntime.InitialRunOwnershipFromContext(ctx); ok {
		t.Fatal("expected standard mode not to carry runtime ownership")
	}
}

func TestRuntimeContextForObservedMessageAttachesOwnershipInAgenticMode(t *testing.T) {
	ctx := runtimeContextForObservedMessage(context.Background(), appconfig.ChatModeAgentic, agentruntime.ShadowObservation{
		PolicyDecision: agentruntime.PolicyDecision{
			EnterRuntime:  true,
			TriggerType:   agentruntime.TriggerTypeFollowUp,
			AttachToRunID: "run_active",
		},
	}, true, agentruntime.TriggerTypeFollowUp, agentruntime.TriggerTypeReplyToBot)

	ownership, ok := agentruntime.InitialRunOwnershipFromContext(ctx)
	if !ok {
		t.Fatal("expected runtime ownership in context")
	}
	if ownership.TriggerType != string(agentruntime.TriggerTypeFollowUp) {
		t.Fatalf("trigger type = %q, want %q", ownership.TriggerType, agentruntime.TriggerTypeFollowUp)
	}
	if ownership.AttachToRunID != "run_active" {
		t.Fatalf("attach run id = %q, want %q", ownership.AttachToRunID, "run_active")
	}
}

func TestRuntimeContextForObservedMessageSkipsUnmatchedTrigger(t *testing.T) {
	ctx := runtimeContextForObservedMessage(context.Background(), appconfig.ChatModeAgentic, agentruntime.ShadowObservation{
		PolicyDecision: agentruntime.PolicyDecision{
			EnterRuntime:   true,
			TriggerType:    agentruntime.TriggerTypeMention,
			SupersedeRunID: "run_active",
		},
	}, true, agentruntime.TriggerTypeCommandBridge)

	if _, ok := agentruntime.InitialRunOwnershipFromContext(ctx); ok {
		t.Fatal("expected unmatched trigger not to carry runtime ownership")
	}
}
