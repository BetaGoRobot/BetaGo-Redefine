package schedule

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/mention"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

type TaskNotifier interface {
	Notify(context.Context, *model.ScheduledTask, string, string) error
}

type taskNotificationKind string

const (
	taskNotificationKindResult         taskNotificationKind = "result"
	taskNotificationKindExecutionError taskNotificationKind = "execution-error"
	taskNotificationKindReviewError    taskNotificationKind = "review-error"
)

type larkTaskNotifier struct {
	replyText  func(context.Context, string, string, string, bool) (*larkim.ReplyMessageResp, error)
	createText func(context.Context, string, string, string) error
}

func newLarkTaskNotifier() TaskNotifier {
	return &larkTaskNotifier{
		replyText:  larkmsg.ReplyMsgText,
		createText: larkmsg.CreateMsgTextRaw,
	}
}

func taskNotificationDeliveryKey(task *model.ScheduledTask, kind taskNotificationKind, finishedAt time.Time) string {
	if task == nil || strings.TrimSpace(task.ID) == "" || kind == "" || finishedAt.IsZero() {
		return ""
	}
	return fmt.Sprintf(
		"schedule-notify-%s-%d-%s",
		strings.TrimSpace(task.ID),
		finishedAt.UTC().UnixNano(),
		kind,
	)
}

func (n *larkTaskNotifier) Notify(
	ctx context.Context,
	task *model.ScheduledTask,
	content string,
	deliveryKey string,
) error {
	if task == nil {
		return errors.New("task is nil")
	}
	chatID := strings.TrimSpace(task.ChatID)
	if chatID == "" {
		return errors.New("task chat_id is required")
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return errors.New("notification content is required")
	}
	deliveryKey = strings.TrimSpace(deliveryKey)
	if deliveryKey == "" {
		return errors.New("notification delivery key is required")
	}
	if n == nil || n.replyText == nil || n.createText == nil {
		return errors.New("task notifier is not configured")
	}
	if normalized, err := mention.NormalizeOutgoingText(ctx, chatID, content); err == nil {
		content = normalized
	}

	var replyErr error
	if sourceMessageID := strings.TrimSpace(task.SourceMessageID); sourceMessageID != "" {
		if _, err := n.replyText(ctx, content, sourceMessageID, "_"+deliveryKey, false); err == nil {
			return nil
		} else {
			replyErr = fmt.Errorf("reply to schedule source message: %w", err)
		}
	}

	createErr := n.createText(
		ctx,
		larkmsg.NewTextMsgBuilder().Text(content).Build(),
		deliveryKey,
		chatID,
	)
	if createErr == nil {
		return nil
	}
	createErr = fmt.Errorf("create schedule chat notification: %w", createErr)
	if replyErr != nil {
		return errors.Join(replyErr, createErr)
	}
	return createErr
}
