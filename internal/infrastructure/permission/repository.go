package permission

import (
	"context"
	"errors"
	"strings"
	"time"

	infraDB "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db"
	"gorm.io/gorm"
)

const SubjectTypeUser = "user"

type Grant struct {
	ID              int64          `gorm:"column:id;primaryKey" json:"id"`
	SubjectType     string         `gorm:"column:subject_type" json:"subject_type"`
	SubjectID       string         `gorm:"column:subject_id" json:"subject_id"`
	PermissionPoint string         `gorm:"column:permission_point" json:"permission_point"`
	Scope           string         `gorm:"column:scope" json:"scope"`
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
	ResourceChatID  string
	ResourceUserID  string
}

func Exists(ctx context.Context, filter GrantFilter) (bool, error) {
	db := infraDB.DBWithoutQueryCache()
	if db == nil {
		return false, errors.New("db is not initialized")
	}

	subjectType := strings.TrimSpace(filter.SubjectType)
	subjectID := strings.TrimSpace(filter.SubjectID)
	permissionPoint := strings.TrimSpace(filter.PermissionPoint)
	scope := strings.TrimSpace(filter.Scope)
	if subjectType == "" || subjectID == "" || permissionPoint == "" || scope == "" {
		return false, errors.New("permission grant filter is incomplete")
	}

	query := db.WithContext(ctx).
		Model(&Grant{}).
		Where("subject_type = ?", subjectType).
		Where("subject_id = ?", subjectID).
		Where("permission_point = ?", permissionPoint).
		Where("scope = ?", scope)

	resourceChatID := strings.TrimSpace(filter.ResourceChatID)
	if resourceChatID == "" {
		query = query.Where("resource_chat_id IS NULL")
	} else {
		query = query.Where("resource_chat_id = ?", resourceChatID)
	}

	resourceUserID := strings.TrimSpace(filter.ResourceUserID)
	if resourceUserID == "" {
		query = query.Where("resource_user_id IS NULL")
	} else {
		query = query.Where("resource_user_id = ?", resourceUserID)
	}

	var grant Grant
	err := query.Select("id").Limit(1).Take(&grant).Error
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, gorm.ErrRecordNotFound):
		return false, nil
	default:
		return false, err
	}
}
