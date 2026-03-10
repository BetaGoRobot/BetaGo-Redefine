package schedule

import (
	"context"
	"errors"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	infraDB "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/query"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"go.opentelemetry.io/otel/attribute"
	"gorm.io/gorm"
)

type Repository struct {
	q        *query.Query
	identity botidentity.Identity
}

func NewRepository(db *gorm.DB, identity botidentity.Identity) *Repository {
	return &Repository{q: query.Use(infraDB.WithoutQueryCache(db)), identity: identity}
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
	ctx, span := otel.Start(ctx)
	span.SetAttributes(
		attribute.String("schedule.id", task.ID),
		attribute.String("chat.id", task.ChatID),
		attribute.String("schedule.status", string(task.Status)),
	)
	defer span.End()
	err := r.q.ScheduledTask.WithContext(ctx).Create(task)
	otel.RecordError(span, err)
	return err
}

func (r *Repository) GetTaskByID(ctx context.Context, id string) (*model.ScheduledTask, error) {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(attribute.String("schedule.id", id))
	defer span.End()

	ins := r.q.ScheduledTask
	tasks, err := r.scopedScheduledTask(ctx).Where(ins.ID.Eq(id)).Limit(1).Find()
	if err != nil {
		otel.RecordError(span, err)
		return nil, err
	}
	if len(tasks) == 0 {
		err := errors.New("task not found")
		otel.RecordError(span, err)
		return nil, err
	}
	return tasks[0], nil
}

func (r *Repository) ListTasksByChatID(ctx context.Context, chatID string, limit, offset int) ([]*model.ScheduledTask, error) {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(
		attribute.String("chat.id", chatID),
		attribute.Int("limit", limit),
		attribute.Int("offset", offset),
	)
	defer span.End()

	ins := r.q.ScheduledTask
	tasks, err := r.scopedScheduledTask(ctx).
		Where(ins.ChatID.Eq(chatID)).
		Order(ins.CreatedAt.Desc()).
		Limit(limit).
		Offset(offset).
		Find()
	if err != nil {
		otel.RecordError(span, err)
		return nil, err
	}
	span.SetAttributes(attribute.Int("schedule.count", len(tasks)))
	return tasks, nil
}

func (r *Repository) ListDueTasks(ctx context.Context, now time.Time, limit int) ([]*model.ScheduledTask, error) {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(
		attribute.String("due.before", now.Format(time.RFC3339)),
		attribute.Int("limit", limit),
	)
	defer span.End()

	ins := r.q.ScheduledTask
	tasks, err := r.scopedScheduledTask(ctx).
		Where(ins.Status.Eq(model.ScheduleTaskStatusEnabled)).
		Where(ins.NextRunAt.Lte(now)).
		Order(ins.NextRunAt.Asc()).
		Limit(limit).
		Find()
	if err != nil {
		otel.RecordError(span, err)
		return nil, err
	}
	span.SetAttributes(attribute.Int("schedule.count", len(tasks)))
	return tasks, nil
}

func (r *Repository) ClaimTaskRun(ctx context.Context, id string, now time.Time, updates map[string]any) (bool, error) {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(
		attribute.String("schedule.id", id),
		attribute.String("claim.at", now.Format(time.RFC3339)),
	)
	defer span.End()

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
	otel.RecordError(span, err)
	claimed := result.RowsAffected == 1
	span.SetAttributes(attribute.Bool("schedule.claimed", claimed))
	return claimed, err
}

func (r *Repository) DeleteTask(ctx context.Context, id string) error {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(attribute.String("schedule.id", id))
	defer span.End()
	ins := r.q.ScheduledTask
	_, err := r.scopedScheduledTask(ctx).Where(ins.ID.Eq(id)).Delete()
	otel.RecordError(span, err)
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
	ctx, span := otel.Start(ctx)
	span.SetAttributes(
		attribute.String("schedule.id", id),
		attribute.Int("updates.count", len(updates)),
	)
	defer span.End()

	if updates == nil {
		updates = make(map[string]any)
	}
	updates["updated_at"] = time.Now()

	ins := r.q.ScheduledTask
	_, err := r.scopedScheduledTask(ctx).Where(ins.ID.Eq(id)).Updates(updates)
	otel.RecordError(span, err)
	return err
}
