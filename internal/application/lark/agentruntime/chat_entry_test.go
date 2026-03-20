package agentruntime

import (
	"context"
	"testing"
	"time"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestResolveChatResponseMode(t *testing.T) {
	if got := resolveChatResponseMode(appconfig.ChatModeStandard, "normal"); got != chatResponseModeStandard {
		t.Fatalf("resolveChatResponseMode(standard, normal) = %q, want %q", got, chatResponseModeStandard)
	}
	if got := resolveChatResponseMode(appconfig.ChatModeAgentic, "normal"); got != chatResponseModeAgentic {
		t.Fatalf("resolveChatResponseMode(agentic, normal) = %q, want %q", got, chatResponseModeAgentic)
	}
	if got := resolveChatResponseMode(appconfig.ChatModeStandard, "reason"); got != chatResponseModeAgentic {
		t.Fatalf("resolveChatResponseMode(standard, reason) = %q, want %q", got, chatResponseModeAgentic)
	}
}

func TestChatEntryHandlerRoutesAgenticReasonModeWithReasoningModel(t *testing.T) {
	now := time.Date(2026, 3, 19, 12, 0, 0, 0, time.UTC)
	handler := NewChatEntryHandler(ChatEntryHandlerOptions{
		Now: func() time.Time { return now },
		AccessorBuilder: func(context.Context, string, string) chatEntryConfigAccessor {
			return fakeChatEntryAccessor{
				mode:           appconfig.ChatModeStandard,
				runtimeEnabled: true,
				cutoverEnabled: true,
				reasoningModel: "deep-reasoner",
				normalModel:    "fast-chat",
			}
		},
		MentionChecker: func(*larkim.P2MessageReceiveV1) bool { return false },
		MuteChecker:    func(context.Context, string) (bool, error) { return false, nil },
		FileCollector: func(context.Context, *larkim.P2MessageReceiveV1) ([]string, error) {
			return []string{"https://example.com/a.png", "https://example.com/b.png"}, nil
		},
		AgenticResponder: func(ctx context.Context, req ChatResponseRequest) error {
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
			if !req.RuntimeEnabled {
				t.Fatal("expected runtime enabled to be forwarded")
			}
			if !req.CutoverEnabled {
				t.Fatal("expected runtime cutover flag to be forwarded")
			}
			if req.StartedAt != now {
				t.Fatalf("started at = %v, want %v", req.StartedAt, now)
			}
			return nil
		},
		StandardResponder: func(context.Context, ChatResponseRequest) error {
			t.Fatal("expected standard responder not to be called")
			return nil
		},
	})

	size := 18
	if err := handler.Handle(context.Background(), testChatEntryEvent(), "reason", &size, "帮我总结"); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
}

func TestChatEntryHandlerSkipsMutedNonMentionMessage(t *testing.T) {
	responderCalls := 0
	handler := NewChatEntryHandler(ChatEntryHandlerOptions{
		AccessorBuilder: func(context.Context, string, string) chatEntryConfigAccessor {
			return fakeChatEntryAccessor{
				mode:        appconfig.ChatModeAgentic,
				normalModel: "fast-chat",
			}
		},
		MentionChecker: func(*larkim.P2MessageReceiveV1) bool { return false },
		MuteChecker: func(context.Context, string) (bool, error) {
			return true, nil
		},
		FileCollector: func(context.Context, *larkim.P2MessageReceiveV1) ([]string, error) {
			t.Fatal("expected file collector not to be called")
			return nil, nil
		},
		AgenticResponder: func(context.Context, ChatResponseRequest) error {
			responderCalls++
			return nil
		},
		StandardResponder: func(context.Context, ChatResponseRequest) error {
			responderCalls++
			return nil
		},
	})

	if err := handler.Handle(context.Background(), testChatEntryEvent(), "normal", nil, "帮我总结"); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if responderCalls != 0 {
		t.Fatalf("responder calls = %d, want 0", responderCalls)
	}
}

func TestChatEntryHandlerAllowsMentionedMessageWithoutMuteCheck(t *testing.T) {
	muteChecks := 0
	handler := NewChatEntryHandler(ChatEntryHandlerOptions{
		AccessorBuilder: func(context.Context, string, string) chatEntryConfigAccessor {
			return fakeChatEntryAccessor{
				mode:        appconfig.ChatModeStandard,
				normalModel: "fast-chat",
			}
		},
		MentionChecker: func(*larkim.P2MessageReceiveV1) bool { return true },
		MuteChecker: func(context.Context, string) (bool, error) {
			muteChecks++
			return true, nil
		},
		FileCollector: func(context.Context, *larkim.P2MessageReceiveV1) ([]string, error) {
			return nil, nil
		},
		StandardResponder: func(context.Context, ChatResponseRequest) error {
			return nil
		},
	})

	if err := handler.Handle(context.Background(), testChatEntryEvent(), "normal", nil, "帮我总结"); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if muteChecks != 0 {
		t.Fatalf("mute checks = %d, want 0", muteChecks)
	}
}

func TestChatEntryHandlerForwardsInitialRunOwnershipFromContext(t *testing.T) {
	handler := NewChatEntryHandler(ChatEntryHandlerOptions{
		AccessorBuilder: func(context.Context, string, string) chatEntryConfigAccessor {
			return fakeChatEntryAccessor{
				mode:           appconfig.ChatModeAgentic,
				runtimeEnabled: true,
				cutoverEnabled: true,
				reasoningModel: "deep-reasoner",
				normalModel:    "fast-chat",
			}
		},
		MentionChecker: func(*larkim.P2MessageReceiveV1) bool { return true },
		FileCollector: func(context.Context, *larkim.P2MessageReceiveV1) ([]string, error) {
			return nil, nil
		},
		AgenticResponder: func(ctx context.Context, req ChatResponseRequest) error {
			if req.Ownership.TriggerType != TriggerTypeFollowUp {
				t.Fatalf("trigger type = %q, want %q", req.Ownership.TriggerType, TriggerTypeFollowUp)
			}
			if req.Ownership.AttachToRunID != "run_active" {
				t.Fatalf("attach run id = %q, want %q", req.Ownership.AttachToRunID, "run_active")
			}
			return nil
		},
	})

	ctx := WithInitialRunOwnership(context.Background(), InitialRunOwnership{
		TriggerType:   TriggerTypeFollowUp,
		AttachToRunID: "run_active",
	})
	if err := handler.Handle(ctx, testChatEntryEvent(), "reason", nil, "继续"); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
}

type fakeChatEntryAccessor struct {
	mode           appconfig.ChatMode
	runtimeEnabled bool
	cutoverEnabled bool
	reasoningModel string
	normalModel    string
}

func (a fakeChatEntryAccessor) ChatMode() appconfig.ChatMode { return a.mode }

func (a fakeChatEntryAccessor) AgentRuntimeEnabled() bool { return a.runtimeEnabled }

func (a fakeChatEntryAccessor) AgentRuntimeChatCutover() bool { return a.cutoverEnabled }

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
