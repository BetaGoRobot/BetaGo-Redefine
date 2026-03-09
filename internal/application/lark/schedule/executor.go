package schedule

import (
	"context"
	"fmt"

	toolkit "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

type ToolExecutor struct {
	tools *toolkit.Impl[larkim.P2MessageReceiveV1]
}

func NewToolExecutor(tools *toolkit.Impl[larkim.P2MessageReceiveV1]) *ToolExecutor {
	return &ToolExecutor{tools: tools}
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
	unit, ok := e.tools.Get(task.ToolName)
	if !ok {
		return "", fmt.Errorf("tool %q is not schedulable", task.ToolName)
	}

	result := unit.Function(ctx, task.ToolArgs, toolkit.FCMeta[larkim.P2MessageReceiveV1]{
		ChatID: task.ChatID,
		UserID: task.CreatorID,
	})
	if result.IsErr() {
		return "", result.Err()
	}
	return result.Value(), nil
}
