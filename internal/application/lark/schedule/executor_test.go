package schedule

import (
	"context"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	toolkit "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"github.com/bytedance/gg/gresult"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestToolExecutorRejectsTaskFromAnotherBot(t *testing.T) {
	tools := toolkit.New[larkim.P2MessageReceiveV1]()
	tools.Add(
		toolkit.NewUnit[larkim.P2MessageReceiveV1]().
			Name("mock").
			Desc("mock").
			Params(toolkit.NewParams("object")).
			Func(func(ctx context.Context, args string, input toolkit.FCMeta[larkim.P2MessageReceiveV1]) gresult.R[string] {
				return gresult.OK("ok")
			}),
	)

	executor := NewToolExecutor(tools, botidentity.Identity{
		AppID:     "app-self",
		BotOpenID: "bot-self",
	})
	task := model.NewScheduledTask("test", model.ScheduleTaskTypeOnce, "chat-1", "user-1", "mock", `{}`, model.ScheduleTaskDefaultTimezone, "app-other", "bot-other")

	_, err := executor.Execute(context.Background(), task)
	if err == nil {
		t.Fatal("expected bot identity mismatch error")
	}
	if !strings.Contains(err.Error(), "bot identity mismatch") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestToolExecutorExecutesOwnedTask(t *testing.T) {
	tools := toolkit.New[larkim.P2MessageReceiveV1]()
	called := false
	tools.Add(
		toolkit.NewUnit[larkim.P2MessageReceiveV1]().
			Name("mock").
			Desc("mock").
			Params(toolkit.NewParams("object")).
			Func(func(ctx context.Context, args string, input toolkit.FCMeta[larkim.P2MessageReceiveV1]) gresult.R[string] {
				called = true
				if input.ChatID != "chat-1" {
					return gresult.Err[string](context.Canceled)
				}
				if input.OpenID != "user-1" {
					return gresult.Err[string](context.DeadlineExceeded)
				}
				if input.Data == nil || input.Data.Event == nil || input.Data.Event.Message == nil || input.Data.Event.Message.MessageId == nil {
					return gresult.Err[string](context.DeadlineExceeded)
				}
				if *input.Data.Event.Message.MessageId != "om_source" {
					return gresult.Err[string](context.DeadlineExceeded)
				}
				if input.Data.Event.Message.ChatId == nil || *input.Data.Event.Message.ChatId != "chat-1" {
					return gresult.Err[string](context.Canceled)
				}
				return gresult.OK("ok")
			}),
	)

	executor := NewToolExecutor(tools, botidentity.Identity{
		AppID:     "app-self",
		BotOpenID: "bot-self",
	})
	task := model.NewScheduledTask("test", model.ScheduleTaskTypeOnce, "chat-1", "user-1", "mock", `{}`, model.ScheduleTaskDefaultTimezone, "app-self", "bot-self")
	task.SourceMessageID = "om_source"

	result, err := executor.Execute(context.Background(), task)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("expected tool handler to be called")
	}
	if result != "ok" {
		t.Fatalf("unexpected result: %q", result)
	}
}

func TestBuildScheduledTaskEventReturnsNilWithoutMessageAndChat(t *testing.T) {
	if got := buildScheduledTaskEvent(&model.ScheduledTask{}); got != nil {
		t.Fatalf("expected nil event, got %+v", got)
	}
}
