package schedule

import (
	"context"
	"errors"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/query"
	"gorm.io/gorm"
)

type Repository struct {
	q        *query.Query
	identity botidentity.Identity
}

func NewRepository(db *gorm.DB, identity botidentity.Identity) *Repository {
	return &Repository{q: query.Use(db), identity: identity}
}

func (r *Repository) scopedScheduledTask(ctx context.Context) query.IScheduledTaskDo {
	ins := r.q.ScheduledTask
	do := ins.WithContext(ctx)
	if r.identity.AppID != "" {
		do = do.Where(ins.AppID.Eq(r.identity.AppID))
	}
	if r.identity.BotOpenID != "" {
		do = do.Where(ins.BotOpenID.Eq(r.identity.BotOpenID))
	}
	return do
}

func (r *Repository) CreateTask(ctx context.Context, task *model.ScheduledTask) error {
	return r.q.ScheduledTask.WithContext(ctx).Create(task)
}

func (r *Repository) GetTaskByID(ctx context.Context, id string) (*model.ScheduledTask, error) {
	ins := r.q.ScheduledTask
	tasks, err := r.scopedScheduledTask(ctx).Where(ins.ID.Eq(id)).Limit(1).Find()
	if err != nil {
		return nil, err
	}
	if len(tasks) == 0 {
		return nil, errors.New("task not found")
	}
	return tasks[0], nil
}

func (r *Repository) ListTasksByChatID(ctx context.Context, chatID string, limit, offset int) ([]*model.ScheduledTask, error) {
	ins := r.q.ScheduledTask
	return r.scopedScheduledTask(ctx).
		Where(ins.ChatID.Eq(chatID)).
		Order(ins.CreatedAt.Desc()).
		Limit(limit).
		Offset(offset).
		Find()
}

func (r *Repository) ListDueTasks(ctx context.Context, now time.Time, limit int) ([]*model.ScheduledTask, error) {
	ins := r.q.ScheduledTask
	return r.scopedScheduledTask(ctx).
		Where(ins.Status.Eq(model.ScheduleTaskStatusEnabled)).
		Where(ins.NextRunAt.Lte(now)).
		Order(ins.NextRunAt.Asc()).
		Limit(limit).
		Find()
}

func (r *Repository) ClaimTaskRun(ctx context.Context, id string, now time.Time, updates map[string]any) (bool, error) {
	if updates == nil {
		updates = make(map[string]any)
	}
	updates["last_run_at"] = now
	updates["run_count"] = gorm.Expr("run_count + 1")
	updates["updated_at"] = now

	ins := r.q.ScheduledTask
	result, err := r.scopedScheduledTask(ctx).
		Where(ins.ID.Eq(id)).
		Where(ins.Status.Eq(model.ScheduleTaskStatusEnabled)).
		Where(ins.NextRunAt.Lte(now)).
		Updates(updates)
	return result.RowsAffected == 1, err
}

func (r *Repository) DeleteTask(ctx context.Context, id string) error {
	ins := r.q.ScheduledTask
	_, err := r.scopedScheduledTask(ctx).Where(ins.ID.Eq(id)).Delete()
	return err
}

func (r *Repository) PauseTask(ctx context.Context, id string) error {
	return r.UpdateTaskFields(ctx, id, map[string]any{
		"status": model.ScheduleTaskStatusPaused,
	})
}

func (r *Repository) ResumeTask(ctx context.Context, id string, nextRunAt time.Time) error {
	return r.UpdateTaskFields(ctx, id, map[string]any{
		"status":      model.ScheduleTaskStatusEnabled,
		"next_run_at": nextRunAt,
	})
}

func (r *Repository) UpdateTaskFields(ctx context.Context, id string, updates map[string]any) error {
	if updates == nil {
		updates = make(map[string]any)
	}
	updates["updated_at"] = time.Now()

	ins := r.q.ScheduledTask
	_, err := r.scopedScheduledTask(ctx).Where(ins.ID.Eq(id)).Updates(updates)
	return err
}
