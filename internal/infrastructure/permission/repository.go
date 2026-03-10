package permission

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"go.opentelemetry.io/otel/attribute"
	"gorm.io/gorm"
)

const SubjectTypeUser = "user"

type Grant struct {
	ID              int64          `gorm:"column:id;primaryKey" json:"id"`
	SubjectType     string         `gorm:"column:subject_type" json:"subject_type"`
	SubjectID       string         `gorm:"column:subject_id" json:"subject_id"`
	PermissionPoint string         `gorm:"column:permission_point" json:"permission_point"`
	Scope           string         `gorm:"column:scope" json:"scope"`
	AppID           string         `gorm:"column:app_id;not null" json:"app_id"`
	BotOpenID       string         `gorm:"column:bot_open_id;not null" json:"bot_open_id"`
	ResourceChatID  *string        `gorm:"column:resource_chat_id" json:"resource_chat_id"`
	ResourceUserID  *string        `gorm:"column:resource_user_id" json:"resource_user_id"`
	Remark          string         `gorm:"column:remark" json:"remark"`
	CreatedAt       time.Time      `gorm:"column:created_at" json:"created_at"`
	UpdatedAt       time.Time      `gorm:"column:updated_at" json:"updated_at"`
	DeletedAt       gorm.DeletedAt `gorm:"column:deleted_at" json:"deleted_at"`
}

func (*Grant) TableName() string {
	return "permission_grants"
}

type GrantFilter struct {
	SubjectType     string
	SubjectID       string
	PermissionPoint string
	Scope           string
	AppID           string
	BotOpenID       string
	ResourceChatID  string
	ResourceUserID  string
}

func Exists(ctx context.Context, filter GrantFilter) (bool, error) {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(
		attribute.String("subject.type", strings.TrimSpace(filter.SubjectType)),
		attribute.String("subject.id", otel.PreviewString(strings.TrimSpace(filter.SubjectID), 128)),
		attribute.String("permission.point", strings.TrimSpace(filter.PermissionPoint)),
		attribute.String("permission.scope", strings.TrimSpace(filter.Scope)),
	)
	defer span.End()

	subjectType := strings.TrimSpace(filter.SubjectType)
	subjectID := strings.TrimSpace(filter.SubjectID)
	permissionPoint := strings.TrimSpace(filter.PermissionPoint)
	scope := strings.TrimSpace(filter.Scope)
	if subjectType == "" || subjectID == "" || permissionPoint == "" || scope == "" {
		err := errors.New("permission grant filter is incomplete")
		otel.RecordError(span, err)
		return false, err
	}
	q, err := permissionGrantQueryWithoutCache()
	if err != nil {
		otel.RecordError(span, err)
		return false, err
	}
	fields := permissionGrantFieldSet(q)

	_, err = applyGrantFilter(q.PermissionGrant.WithContext(ctx), fields, GrantFilter{
		SubjectType:     subjectType,
		SubjectID:       subjectID,
		PermissionPoint: permissionPoint,
		Scope:           scope,
		AppID:           filter.AppID,
		BotOpenID:       filter.BotOpenID,
		ResourceChatID:  filter.ResourceChatID,
		ResourceUserID:  filter.ResourceUserID,
	}).
		Select(fields.ID).
		Limit(1).
		Take()
	switch {
	case err == nil:
		span.SetAttributes(attribute.Bool("permission.exists", true))
		return true, nil
	case errors.Is(err, gorm.ErrRecordNotFound):
		span.SetAttributes(attribute.Bool("permission.exists", false))
		return false, nil
	default:
		otel.RecordError(span, err)
		return false, err
	}
}
