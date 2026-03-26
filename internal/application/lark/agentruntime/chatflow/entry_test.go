package chatflow

import (
	"context"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/intent"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime/model/responses"
)

func TestAgenticEntryBuildRequestUsesReasoningModel(t *testing.T) {
	now := time.Date(2026, 3, 19, 12, 0, 0, 0, time.UTC)
	handler := NewAgenticEntryHandler()
	handler.now = func() time.Time { return now }
	handler.accessorBuilder = func(context.Context, string, string) ConfigAccessor {
		return fakeAccessor{
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
	meta := &xhandler.BaseMetaData{}
	meta.SetIntentAnalysis(&intent.IntentAnalysis{
		InteractionMode: intent.InteractionModeAgentic,
		ReasoningEffort: responses.ReasoningEffort_high,
	})
	req, err := handler.BuildRequest(context.Background(), testEvent(), meta, "reason", &size, "帮我总结")
	if err != nil {
		t.Fatalf("BuildRequest() error = %v", err)
	}
	if req == nil {
		t.Fatal("expected request")
	}
	if req.Plan.ModelID != "deep-reasoner" {
		t.Fatalf("model id = %q, want %q", req.Plan.ModelID, "deep-reasoner")
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
	if req.Plan.ReasoningEffort != responses.ReasoningEffort_high {
		t.Fatalf("reasoning effort = %v, want %v", req.Plan.ReasoningEffort, responses.ReasoningEffort_high)
	}
	if req.StartedAt != now {
		t.Fatalf("started at = %v, want %v", req.StartedAt, now)
	}
}

func TestAgenticEntryBuildRequestSkipsMutedNonMentionMessage(t *testing.T) {
	handler := NewAgenticEntryHandler()
	handler.accessorBuilder = func(context.Context, string, string) ConfigAccessor {
		return fakeAccessor{normalModel: "fast-chat"}
	}
	handler.mentionChecker = func(*larkim.P2MessageReceiveV1) bool { return false }
	handler.muteChecker = func(context.Context, string) (bool, error) { return true, nil }
	handler.fileCollector = func(context.Context, *larkim.P2MessageReceiveV1) ([]string, error) {
		t.Fatal("expected file collector not to be called")
		return nil, nil
	}

	req, err := handler.BuildRequest(context.Background(), testEvent(), nil, "normal", nil, "帮我总结")
	if err != nil {
		t.Fatalf("BuildRequest() error = %v", err)
	}
	if req != nil {
		t.Fatal("expected nil request when chat is muted")
	}
}

func TestAgenticEntryBuildRequestAllowsMentionedMessageWithoutMuteCheck(t *testing.T) {
	muteChecks := 0
	handler := NewAgenticEntryHandler()
	handler.accessorBuilder = func(context.Context, string, string) ConfigAccessor {
		return fakeAccessor{normalModel: "fast-chat"}
	}
	handler.mentionChecker = func(*larkim.P2MessageReceiveV1) bool { return true }
	handler.muteChecker = func(context.Context, string) (bool, error) {
		muteChecks++
		return true, nil
	}
	handler.fileCollector = func(context.Context, *larkim.P2MessageReceiveV1) ([]string, error) {
		return nil, nil
	}

	req, err := handler.BuildRequest(context.Background(), testEvent(), nil, "normal", nil, "帮我总结")
	if err != nil {
		t.Fatalf("BuildRequest() error = %v", err)
	}
	if req == nil {
		t.Fatal("expected request")
	}
	if muteChecks != 0 {
		t.Fatalf("mute checks = %d, want 0", muteChecks)
	}
}

type fakeAccessor struct {
	reasoningModel string
	normalModel    string
}

func (a fakeAccessor) ChatReasoningModel() string { return a.reasoningModel }
func (a fakeAccessor) ChatNormalModel() string    { return a.normalModel }

func testEvent() *larkim.P2MessageReceiveV1 {
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
