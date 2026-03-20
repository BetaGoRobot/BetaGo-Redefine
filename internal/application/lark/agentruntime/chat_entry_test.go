package agentruntime

import (
	"context"
	"testing"
	"time"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestAgenticChatEntryBuildRequestUsesReasoningModel(t *testing.T) {
	now := time.Date(2026, 3, 19, 12, 0, 0, 0, time.UTC)
	handler := NewAgenticChatEntryHandler()
	handler.now = func() time.Time { return now }
	handler.accessorBuilder = func(context.Context, string, string) agenticChatEntryConfigAccessor {
		return fakeChatEntryAccessor{
			reasoningModel: "deep-reasoner",
			normalModel:    "fast-chat",
		}
	}
	handler.mentionChecker = func(*larkim.P2MessageReceiveV1) bool { return false }
	handler.muteChecker = func(context.Context, string) (bool, error) { return false, nil }
	handler.fileCollector = func(context.Context, *larkim.P2MessageReceiveV1) ([]string, error) {
		return []string{"https://example.com/a.png", "https://example.com/b.png"}, nil
	}

	size := 18
	req, err := handler.buildRequest(context.Background(), testChatEntryEvent(), "reason", &size, "帮我总结")
	if err != nil {
		t.Fatalf("buildRequest() error = %v", err)
	}
	if req == nil {
		t.Fatal("expected request")
	}
	if req.Plan.ModelID != "deep-reasoner" {
		t.Fatalf("model id = %q, want %q", req.Plan.ModelID, "deep-reasoner")
	}
	if req.Plan.Mode != appconfig.ChatModeAgentic {
		t.Fatalf("plan mode = %q, want %q", req.Plan.Mode, appconfig.ChatModeAgentic)
	}
	if req.Plan.Size != 18 {
		t.Fatalf("plan size = %d, want %d", req.Plan.Size, 18)
	}
	if len(req.Plan.Files) != 2 {
		t.Fatalf("file count = %d, want %d", len(req.Plan.Files), 2)
	}
	if len(req.Plan.Args) != 1 || req.Plan.Args[0] != "帮我总结" {
		t.Fatalf("args = %+v, want %+v", req.Plan.Args, []string{"帮我总结"})
	}
	if !req.Plan.EnableDeferredToolCollector {
		t.Fatal("expected deferred tool collector to be enabled")
	}
	if req.StartedAt != now {
		t.Fatalf("started at = %v, want %v", req.StartedAt, now)
	}
}

func TestAgenticChatEntryBuildRequestSkipsMutedNonMentionMessage(t *testing.T) {
	handler := NewAgenticChatEntryHandler()
	handler.accessorBuilder = func(context.Context, string, string) agenticChatEntryConfigAccessor {
		return fakeChatEntryAccessor{normalModel: "fast-chat"}
	}
	handler.mentionChecker = func(*larkim.P2MessageReceiveV1) bool { return false }
	handler.muteChecker = func(context.Context, string) (bool, error) { return true, nil }
	handler.fileCollector = func(context.Context, *larkim.P2MessageReceiveV1) ([]string, error) {
		t.Fatal("expected file collector not to be called")
		return nil, nil
	}

	req, err := handler.buildRequest(context.Background(), testChatEntryEvent(), "normal", nil, "帮我总结")
	if err != nil {
		t.Fatalf("buildRequest() error = %v", err)
	}
	if req != nil {
		t.Fatal("expected nil request when chat is muted")
	}
}

func TestAgenticChatEntryBuildRequestAllowsMentionedMessageWithoutMuteCheck(t *testing.T) {
	muteChecks := 0
	handler := NewAgenticChatEntryHandler()
	handler.accessorBuilder = func(context.Context, string, string) agenticChatEntryConfigAccessor {
		return fakeChatEntryAccessor{normalModel: "fast-chat"}
	}
	handler.mentionChecker = func(*larkim.P2MessageReceiveV1) bool { return true }
	handler.muteChecker = func(context.Context, string) (bool, error) {
		muteChecks++
		return true, nil
	}
	handler.fileCollector = func(context.Context, *larkim.P2MessageReceiveV1) ([]string, error) {
		return nil, nil
	}

	req, err := handler.buildRequest(context.Background(), testChatEntryEvent(), "normal", nil, "帮我总结")
	if err != nil {
		t.Fatalf("buildRequest() error = %v", err)
	}
	if req == nil {
		t.Fatal("expected request")
	}
	if muteChecks != 0 {
		t.Fatalf("mute checks = %d, want 0", muteChecks)
	}
}

func TestAgenticChatEntryBuildRequestForwardsInitialRunOwnership(t *testing.T) {
	handler := NewAgenticChatEntryHandler()
	handler.accessorBuilder = func(context.Context, string, string) agenticChatEntryConfigAccessor {
		return fakeChatEntryAccessor{
			reasoningModel: "deep-reasoner",
			normalModel:    "fast-chat",
		}
	}
	handler.mentionChecker = func(*larkim.P2MessageReceiveV1) bool { return true }
	handler.fileCollector = func(context.Context, *larkim.P2MessageReceiveV1) ([]string, error) {
		return nil, nil
	}

	ctx := WithInitialRunOwnership(context.Background(), InitialRunOwnership{
		TriggerType:   TriggerTypeFollowUp,
		AttachToRunID: "run_active",
	})
	req, err := handler.buildRequest(ctx, testChatEntryEvent(), "reason", nil, "继续")
	if err != nil {
		t.Fatalf("buildRequest() error = %v", err)
	}
	if req == nil {
		t.Fatal("expected request")
	}
	if req.Ownership.TriggerType != TriggerTypeFollowUp {
		t.Fatalf("trigger type = %q, want %q", req.Ownership.TriggerType, TriggerTypeFollowUp)
	}
	if req.Ownership.AttachToRunID != "run_active" {
		t.Fatalf("attach run id = %q, want %q", req.Ownership.AttachToRunID, "run_active")
	}
}

type fakeChatEntryAccessor struct {
	reasoningModel string
	normalModel    string
}

func (a fakeChatEntryAccessor) ChatReasoningModel() string { return a.reasoningModel }

func (a fakeChatEntryAccessor) ChatNormalModel() string { return a.normalModel }

func testChatEntryEvent() *larkim.P2MessageReceiveV1 {
	chatID := "oc_chat"
	openID := "ou_actor"
	msgID := "om_runtime_entry"
	chatType := "group"
	return &larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Message: &larkim.EventMessage{
				ChatId:    &chatID,
				MessageId: &msgID,
				ChatType:  &chatType,
			},
			Sender: &larkim.EventSender{
				SenderId: &larkim.UserId{
					OpenId: &openID,
				},
			},
		},
	}
}
