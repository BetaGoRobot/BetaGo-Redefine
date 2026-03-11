package schedule

import (
	"context"
	"fmt"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	toolkit "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"github.com/bytedance/gg/gptr"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

type ToolExecutor struct {
	tools    *toolkit.Impl[larkim.P2MessageReceiveV1]
	identity botidentity.Identity
}

func NewToolExecutor(tools *toolkit.Impl[larkim.P2MessageReceiveV1], identity botidentity.Identity) *ToolExecutor {
	return &ToolExecutor{tools: tools, identity: identity}
}

func (e *ToolExecutor) AvailableTools() []string {
	if e == nil || e.tools == nil {
		return nil
	}
	names := make([]string, 0, len(e.tools.FunctionCallMap))
	for name := range e.tools.FunctionCallMap {
		names = append(names, name)
	}
	return uniqueSorted(names)
}

func (e *ToolExecutor) CanExecute(name string) bool {
	if e == nil || e.tools == nil {
		return false
	}
	_, ok := e.tools.Get(name)
	return ok
}

func (e *ToolExecutor) Execute(ctx context.Context, task *model.ScheduledTask) (string, error) {
	if task == nil {
		return "", fmt.Errorf("task is nil")
	}
	if e == nil || e.tools == nil {
		return "", fmt.Errorf("scheduled task executor not initialized")
	}
	if err := e.identity.EnsureMatch(task.AppID, task.BotOpenID); err != nil {
		return "", err
	}
	unit, ok := e.tools.Get(task.ToolName)
	if !ok {
		return "", fmt.Errorf("tool %q is not schedulable", task.ToolName)
	}

	result := unit.Function(ctx, task.ToolArgs, toolkit.FCMeta[larkim.P2MessageReceiveV1]{
		ChatID: task.ChatID,
		OpenID: task.CreatorID,
		Data:   buildScheduledTaskEvent(task),
	})
	if result.IsErr() {
		return "", result.Err()
	}
	return result.Value(), nil
}

func buildScheduledTaskEvent(task *model.ScheduledTask) *larkim.P2MessageReceiveV1 {
	if task == nil {
		return nil
	}

	sourceMessageID := strings.TrimSpace(task.SourceMessageID)
	chatID := strings.TrimSpace(task.ChatID)
	if sourceMessageID == "" && chatID == "" {
		return nil
	}

	event := &larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Message: &larkim.EventMessage{},
		},
	}
	if sourceMessageID != "" {
		event.Event.Message.MessageId = gptr.Of(sourceMessageID)
	}
	if chatID != "" {
		event.Event.Message.ChatId = gptr.Of(chatID)
	}
	return event
}
