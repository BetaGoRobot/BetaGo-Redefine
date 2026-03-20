package ops

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
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

func TestReplyChatOperatorUsesStandardInvokerInStandardMode(t *testing.T) {
	prevStandardInvoke := standardChatInvoker
	prevAgenticInvoke := agenticChatInvoker
	prevProgress := progressReactionHandler
	prevDone := doneReactionHandler
	defer func() {
		standardChatInvoker = prevStandardInvoke
		agenticChatInvoker = prevAgenticInvoke
		progressReactionHandler = prevProgress
		doneReactionHandler = prevDone
	}()
	progressReactionHandler = func(context.Context, string) func() { return func() {} }
	doneReactionHandler = func(context.Context, string, *xhandler.BaseMetaData) {}

	invokeCount := 0
	var seenArgs []string
	standardChatInvoker = func(ctx context.Context, event *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData, args ...string) error {
		invokeCount++
		seenArgs = append([]string(nil), args...)
		return nil
	}
	agenticChatInvoker = func(context.Context, *larkim.P2MessageReceiveV1, *xhandler.BaseMetaData, ...string) error {
		t.Fatal("agentic invoker should not be called in standard mode")
		return nil
	}

	event := testMessageEvent("p2p", "oc_chat", "ou_actor")
	msgID := "om_reply_runtime"
	event.Event.Message.MessageId = &msgID
	meta := &xhandler.BaseMetaData{ChatID: "oc_chat", OpenID: "ou_actor"}

	op := &ReplyChatOperator{}
	if err := op.PreRun(context.Background(), event, meta); err != nil {
		t.Fatalf("PreRun() error = %v", err)
	}
	if err := op.Run(context.Background(), event, meta); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if invokeCount != 1 {
		t.Fatalf("standard chat invoke count = %d, want 1", invokeCount)
	}
	if len(seenArgs) == 0 {
		t.Fatal("expected trimmed reply args to be forwarded")
	}
}

func TestAgenticReplyChatOperatorPassesRuntimeOwnershipToAgenticInvoker(t *testing.T) {
	prevObserve := runtimeMessageObservation
	prevStandardInvoke := standardChatInvoker
	prevAgenticInvoke := agenticChatInvoker
	prevProgress := progressReactionHandler
	prevDone := doneReactionHandler
	defer func() {
		runtimeMessageObservation = prevObserve
		standardChatInvoker = prevStandardInvoke
		agenticChatInvoker = prevAgenticInvoke
		progressReactionHandler = prevProgress
		doneReactionHandler = prevDone
	}()
	runtimeMessageObservation = func(context.Context, *larkim.P2MessageReceiveV1, *xhandler.BaseMetaData) (agentruntime.ShadowObservation, bool) {
		return agentruntime.ShadowObservation{
			PolicyDecision: agentruntime.PolicyDecision{
				EnterRuntime:   true,
				TriggerType:    agentruntime.TriggerTypeMention,
				SupersedeRunID: "run_active",
				Reason:         "supersede_active_run",
			},
		}, true
	}
	progressReactionHandler = func(context.Context, string) func() { return func() {} }
	doneReactionHandler = func(context.Context, string, *xhandler.BaseMetaData) {}
	standardChatInvoker = func(context.Context, *larkim.P2MessageReceiveV1, *xhandler.BaseMetaData, ...string) error {
		t.Fatal("standard invoker should not be called in agentic mode")
		return nil
	}

	var seenOwnership agentruntime.InitialRunOwnership
	var seenOK bool
	var seenArgs []string
	agenticChatInvoker = func(ctx context.Context, event *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData, args ...string) error {
		seenOwnership, seenOK = agentruntime.InitialRunOwnershipFromContext(ctx)
		seenArgs = append([]string(nil), args...)
		return nil
	}

	event := testMessageEvent("p2p", "oc_chat", "ou_actor")
	msgID := "om_reply_runtime"
	event.Event.Message.MessageId = &msgID
	meta := &xhandler.BaseMetaData{ChatID: "oc_chat", OpenID: "ou_actor"}

	op := &AgenticReplyChatOperator{}
	if err := op.PreRun(context.Background(), event, meta); err != nil {
		t.Fatalf("PreRun() error = %v", err)
	}
	if err := op.Run(context.Background(), event, meta); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if !seenOK {
		t.Fatal("expected runtime ownership in context")
	}
	if seenOwnership.TriggerType != agentruntime.TriggerTypeMention {
		t.Fatalf("trigger type = %q, want %q", seenOwnership.TriggerType, agentruntime.TriggerTypeMention)
	}
	if seenOwnership.SupersedeRunID != "run_active" {
		t.Fatalf("supersede run id = %q, want %q", seenOwnership.SupersedeRunID, "run_active")
	}
	if len(seenArgs) == 0 {
		t.Fatal("expected trimmed reply args to be forwarded")
	}
}

func TestAgenticChatMsgOperatorRoutesFollowUpMessagesIntoRuntime(t *testing.T) {
	prevObserve := runtimeMessageObservation
	prevStandardInvoke := standardChatInvoker
	prevAgenticInvoke := agenticChatInvoker
	defer func() {
		runtimeMessageObservation = prevObserve
		standardChatInvoker = prevStandardInvoke
		agenticChatInvoker = prevAgenticInvoke
	}()
	runtimeMessageObservation = func(context.Context, *larkim.P2MessageReceiveV1, *xhandler.BaseMetaData) (agentruntime.ShadowObservation, bool) {
		return agentruntime.ShadowObservation{
			PolicyDecision: agentruntime.PolicyDecision{
				EnterRuntime:  true,
				TriggerType:   agentruntime.TriggerTypeFollowUp,
				AttachToRunID: "run_active",
				Reason:        "attach_follow_up",
			},
		}, true
	}
	standardChatInvoker = func(context.Context, *larkim.P2MessageReceiveV1, *xhandler.BaseMetaData, ...string) error {
		t.Fatal("standard invoker should not be called in agentic mode")
		return nil
	}

	invokeCount := 0
	var seenOwnership agentruntime.InitialRunOwnership
	var seenOK bool
	agenticChatInvoker = func(ctx context.Context, event *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData, args ...string) error {
		invokeCount++
		seenOwnership, seenOK = agentruntime.InitialRunOwnershipFromContext(ctx)
		return nil
	}

	event := testMessageEvent("group", "oc_chat", "ou_actor")
	text := `{"text":"继续把刚才那个审批流程走完"}`
	event.Event.Message.Content = &text
	meta := &xhandler.BaseMetaData{ChatID: "oc_chat", OpenID: "ou_actor"}

	op := &AgenticChatMsgOperator{}
	if err := op.PreRun(context.Background(), event, meta); err != nil {
		t.Fatalf("PreRun() error = %v", err)
	}
	if err := op.Run(context.Background(), event, meta); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if invokeCount != 1 {
		t.Fatalf("agentic chat invoke count = %d, want 1", invokeCount)
	}
	if !seenOK {
		t.Fatal("expected runtime ownership in context")
	}
	if seenOwnership.TriggerType != agentruntime.TriggerTypeFollowUp {
		t.Fatalf("trigger type = %q, want %q", seenOwnership.TriggerType, agentruntime.TriggerTypeFollowUp)
	}
	if seenOwnership.AttachToRunID != "run_active" {
		t.Fatalf("attach run id = %q, want %q", seenOwnership.AttachToRunID, "run_active")
	}
}

func TestExecuteFromRawCommandPassesRuntimeOwnershipToBBCommand(t *testing.T) {
	prevObserve := runtimeMessageObservation
	prevExecute := agenticRootCommandExecutor
	prevProgress := progressReactionHandler
	prevDone := doneReactionHandler
	defer func() {
		runtimeMessageObservation = prevObserve
		agenticRootCommandExecutor = prevExecute
		progressReactionHandler = prevProgress
		doneReactionHandler = prevDone
	}()

	runtimeMessageObservation = func(context.Context, *larkim.P2MessageReceiveV1, *xhandler.BaseMetaData) (agentruntime.ShadowObservation, bool) {
		return agentruntime.ShadowObservation{
			PolicyDecision: agentruntime.PolicyDecision{
				EnterRuntime:   true,
				TriggerType:    agentruntime.TriggerTypeCommandBridge,
				SupersedeRunID: "run_active",
				Reason:         "supersede_active_run",
			},
		}, true
	}
	progressReactionHandler = func(context.Context, string) func() { return func() {} }
	doneReactionHandler = func(context.Context, string, *xhandler.BaseMetaData) {}

	var seenOwnership agentruntime.InitialRunOwnership
	var seenOK bool
	var seenCommands []string
	agenticRootCommandExecutor = func(ctx context.Context, event *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData, commands []string) error {
		seenOwnership, seenOK = agentruntime.InitialRunOwnershipFromContext(ctx)
		seenCommands = append([]string(nil), commands...)
		return nil
	}

	event := testMessageEvent("group", "oc_chat", "ou_actor")
	text := `{"text":"/bb 帮我总结"}`
	event.Event.Message.Content = &text
	msgID := "om_bb"
	event.Event.Message.MessageId = &msgID
	meta := &xhandler.BaseMetaData{ChatID: "oc_chat", OpenID: "ou_actor"}

	if err := executeAgenticRawCommand(context.Background(), event, meta, "/bb 帮我总结"); err != nil {
		t.Fatalf("executeAgenticRawCommand() error = %v", err)
	}

	if !seenOK {
		t.Fatal("expected runtime ownership in command context")
	}
	if seenOwnership.TriggerType != agentruntime.TriggerTypeCommandBridge {
		t.Fatalf("trigger type = %q, want %q", seenOwnership.TriggerType, agentruntime.TriggerTypeCommandBridge)
	}
	if seenOwnership.SupersedeRunID != "run_active" {
		t.Fatalf("supersede run id = %q, want %q", seenOwnership.SupersedeRunID, "run_active")
	}
	if len(seenCommands) == 0 || seenCommands[0] != "bb" {
		t.Fatalf("commands = %+v, want bb command", seenCommands)
	}
}
