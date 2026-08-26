package schedule

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestLarkTaskNotifierRepliesToSourceMessage(t *testing.T) {
	task := &model.ScheduledTask{ID: "task-1", ChatID: "oc_chat", SourceMessageID: "om_source"}
	createCalls := 0
	notifier := &larkTaskNotifier{
		replyText: func(_ context.Context, text, msgID, suffix string, replyInThread bool) (*larkim.ReplyMessageResp, error) {
			if text != "审核后的播报" || msgID != task.SourceMessageID || suffix != "_scheduleNotify" || replyInThread {
				t.Fatalf("reply args = %q/%q/%q/%v", text, msgID, suffix, replyInThread)
			}
			return &larkim.ReplyMessageResp{}, nil
		},
		createText: func(context.Context, string, string, string) error {
			createCalls++
			return nil
		},
	}

	if err := notifier.Notify(context.Background(), task, "审核后的播报"); err != nil {
		t.Fatalf("Notify() error = %v", err)
	}
	if createCalls != 0 {
		t.Fatalf("create calls = %d, want 0", createCalls)
	}
}

func TestLarkTaskNotifierFallsBackToChatWhenReplyFails(t *testing.T) {
	task := &model.ScheduledTask{ID: "task-2", ChatID: "oc_chat", SourceMessageID: "om_source"}
	replyErr := errors.New("source message unavailable")
	createCalls := 0
	notifier := &larkTaskNotifier{
		replyText: func(context.Context, string, string, string, bool) (*larkim.ReplyMessageResp, error) {
			return nil, replyErr
		},
		createText: func(_ context.Context, content, msgID, chatID string) error {
			createCalls++
			if content != `{"text":"审核后的播报"}` {
				t.Fatalf("fallback content = %q", content)
			}
			if !strings.HasPrefix(msgID, "schedule-notify-task-2-") {
				t.Fatalf("fallback message ID = %q", msgID)
			}
			if chatID != task.ChatID {
				t.Fatalf("fallback chat ID = %q, want %q", chatID, task.ChatID)
			}
			return nil
		},
	}

	if err := notifier.Notify(context.Background(), task, "审核后的播报"); err != nil {
		t.Fatalf("Notify() error = %v", err)
	}
	if createCalls != 1 {
		t.Fatalf("create calls = %d, want 1", createCalls)
	}
}

func TestLarkTaskNotifierReturnsErrorWhenReplyAndFallbackFail(t *testing.T) {
	replyErr := errors.New("reply failed")
	createErr := errors.New("create failed")
	notifier := &larkTaskNotifier{
		replyText: func(context.Context, string, string, string, bool) (*larkim.ReplyMessageResp, error) {
			return nil, replyErr
		},
		createText: func(context.Context, string, string, string) error {
			return createErr
		},
	}

	err := notifier.Notify(context.Background(), &model.ScheduledTask{
		ID: "task-3", ChatID: "oc_chat", SourceMessageID: "om_source",
	}, "审核后的播报")
	if !errors.Is(err, replyErr) || !errors.Is(err, createErr) {
		t.Fatalf("Notify() error = %v, want joined reply/create errors", err)
	}
}

func TestLarkTaskNotifierRejectsMissingChatOrContent(t *testing.T) {
	notifier := &larkTaskNotifier{
		replyText: func(context.Context, string, string, string, bool) (*larkim.ReplyMessageResp, error) {
			t.Fatal("reply must not be called")
			return nil, nil
		},
		createText: func(context.Context, string, string, string) error {
			t.Fatal("create must not be called")
			return nil
		},
	}

	for _, tt := range []struct {
		name    string
		task    *model.ScheduledTask
		content string
	}{
		{name: "nil task", task: nil, content: "content"},
		{name: "missing chat", task: &model.ScheduledTask{ID: "task"}, content: "content"},
		{name: "missing content", task: &model.ScheduledTask{ID: "task", ChatID: "oc"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := notifier.Notify(context.Background(), tt.task, tt.content); err == nil {
				t.Fatal("Notify() error = nil, want validation error")
			}
		})
	}
}
