package schedule

import (
	"context"
	"errors"
	"strings"

	apppermission "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/permission"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
)

var scheduleManageAllowed = apppermission.EnsureManageAllowed

func EnsureTaskMutationAllowed(ctx context.Context, actorOpenID string, task *model.ScheduledTask) error {
	actorOpenID = strings.TrimSpace(actorOpenID)
	if actorOpenID == "" {
		return errors.New("schedule mutation requires operator identity")
	}
	if task == nil {
		return errors.New("schedule task is nil")
	}

	if creatorOpenID := strings.TrimSpace(task.CreatorID); creatorOpenID != "" && creatorOpenID == actorOpenID {
		return nil
	}
	if err := scheduleManageAllowed(ctx, actorOpenID); err == nil {
		return nil
	}
	return errors.New("only schedule creator or privileged users can modify schedule")
}
