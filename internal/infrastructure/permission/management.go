package permission

import (
	"context"
	"errors"
	"strings"
	"time"

	infraDB "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/query"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"go.opentelemetry.io/otel/attribute"
	"gorm.io/gen/field"
	"gorm.io/gorm"
)

type ListFilter struct {
	SubjectType string
	SubjectID   string
	AppID       string
	BotOpenID   string
}

func Upsert(ctx context.Context, grant Grant) error {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(
		attribute.String("subject.type", strings.TrimSpace(grant.SubjectType)),
		attribute.String("subject.id", otel.PreviewString(strings.TrimSpace(grant.SubjectID), 128)),
		attribute.String("permission.point", strings.TrimSpace(grant.PermissionPoint)),
		attribute.String("permission.scope", strings.TrimSpace(grant.Scope)),
	)
	defer span.End()

	q, err := permissionGrantQueryWithoutCache()
	if err != nil {
		otel.RecordError(span, err)
		return err
	}
	normalized, err := normalizeGrant(grant)
	if err != nil {
		otel.RecordError(span, err)
		return err
	}
	fields := permissionGrantFieldSet(q)

	existing, err := applyExactGrantFilter(q.PermissionGrant.WithContext(ctx).Unscoped(), fields, normalized).Take()
	switch {
	case err == nil:
		updates := map[string]any{
			"remark":     normalized.Remark,
			"updated_at": time.Now(),
			"deleted_at": nil,
		}
		_, err = q.PermissionGrant.WithContext(ctx).Unscoped().
			Where(fields.ID.Eq(existing.ID)).
			Updates(updates)
		otel.RecordError(span, err)
		return err
	case errors.Is(err, gorm.ErrRecordNotFound):
		err = q.PermissionGrant.WithContext(ctx).Create(toPermissionGrantModel(normalized))
		otel.RecordError(span, err)
		return err
	default:
		otel.RecordError(span, err)
		return err
	}
}

func Revoke(ctx context.Context, filter GrantFilter) error {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(
		attribute.String("subject.type", strings.TrimSpace(filter.SubjectType)),
		attribute.String("subject.id", otel.PreviewString(strings.TrimSpace(filter.SubjectID), 128)),
		attribute.String("permission.point", strings.TrimSpace(filter.PermissionPoint)),
		attribute.String("permission.scope", strings.TrimSpace(filter.Scope)),
	)
	defer span.End()

	q, err := permissionGrantQueryWithoutCache()
	if err != nil {
		otel.RecordError(span, err)
		return err
	}
	if err := validateStrictFilter(filter); err != nil {
		otel.RecordError(span, err)
		return err
	}
	fields := permissionGrantFieldSet(q)

	_, err = applyGrantFilter(q.PermissionGrant.WithContext(ctx), fields, filter).Delete()
	otel.RecordError(span, err)
	return err
}

func ListBySubject(ctx context.Context, filter ListFilter) ([]Grant, error) {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(
		attribute.String("subject.type", strings.TrimSpace(filter.SubjectType)),
		attribute.String("subject.id", otel.PreviewString(strings.TrimSpace(filter.SubjectID), 128)),
	)
	defer span.End()

	q, err := permissionGrantQueryWithoutCache()
	if err != nil {
		otel.RecordError(span, err)
		return nil, err
	}
	subjectType := strings.TrimSpace(filter.SubjectType)
	subjectID := strings.TrimSpace(filter.SubjectID)
	if subjectType == "" || subjectID == "" {
		err := errors.New("permission list filter is incomplete")
		otel.RecordError(span, err)
		return nil, err
	}
	fields := permissionGrantFieldSet(q)

	grantQuery := q.PermissionGrant.WithContext(ctx).
		Where(fields.SubjectType.Eq(subjectType)).
		Where(fields.SubjectID.Eq(subjectID))

	if appID := strings.TrimSpace(filter.AppID); appID != "" {
		grantQuery = grantQuery.Where(fields.AppID.Eq(appID))
	}
	if botOpenID := strings.TrimSpace(filter.BotOpenID); botOpenID != "" {
		grantQuery = grantQuery.Where(fields.BotOpenID.Eq(botOpenID))
	}

	grantModels, err := grantQuery.
		Order(
			fields.PermissionPoint.Asc(),
			fields.Scope.Asc(),
			field.NewUnsafeFieldRaw("? ASC NULLS FIRST", fields.ResourceChatID),
			field.NewUnsafeFieldRaw("? ASC NULLS FIRST", fields.ResourceUserID),
		).
		Find()
	if err != nil {
		otel.RecordError(span, err)
		return nil, err
	}

	grants := make([]Grant, 0, len(grantModels))
	for _, grantModel := range grantModels {
		grants = append(grants, fromPermissionGrantModel(grantModel))
	}
	span.SetAttributes(attribute.Int("permission.count", len(grants)))
	return grants, nil
}

func HasAnyGrant(ctx context.Context, appID, botOpenID string) (bool, error) {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(
		attribute.String("app.id", strings.TrimSpace(appID)),
		attribute.String("bot.open_id", otel.PreviewString(strings.TrimSpace(botOpenID), 128)),
	)
	defer span.End()

	q, err := permissionGrantQueryWithoutCache()
	if err != nil {
		otel.RecordError(span, err)
		return false, err
	}
	fields := permissionGrantFieldSet(q)

	grantQuery := q.PermissionGrant.WithContext(ctx)
	if appID = strings.TrimSpace(appID); appID != "" {
		grantQuery = grantQuery.Where(fields.AppID.Eq(appID))
	}
	if botOpenID = strings.TrimSpace(botOpenID); botOpenID != "" {
		grantQuery = grantQuery.Where(fields.BotOpenID.Eq(botOpenID))
	}

	_, err = grantQuery.Select(fields.ID).Limit(1).Take()
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

func normalizeGrant(grant Grant) (Grant, error) {
	normalized := Grant{
		SubjectType:     strings.TrimSpace(grant.SubjectType),
		SubjectID:       strings.TrimSpace(grant.SubjectID),
		PermissionPoint: strings.TrimSpace(grant.PermissionPoint),
		Scope:           strings.TrimSpace(grant.Scope),
		AppID:           strings.TrimSpace(grant.AppID),
		BotOpenID:       strings.TrimSpace(grant.BotOpenID),
		Remark:          strings.TrimSpace(grant.Remark),
	}
	if normalized.SubjectType == "" || normalized.SubjectID == "" || normalized.PermissionPoint == "" || normalized.Scope == "" {
		return Grant{}, errors.New("permission grant is incomplete")
	}

	normalized.ResourceChatID = normalizeNullableString(grant.ResourceChatID)
	normalized.ResourceUserID = normalizeNullableString(grant.ResourceUserID)
	return normalized, nil
}

func normalizeNullableString(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func validateStrictFilter(filter GrantFilter) error {
	subjectType := strings.TrimSpace(filter.SubjectType)
	subjectID := strings.TrimSpace(filter.SubjectID)
	permissionPoint := strings.TrimSpace(filter.PermissionPoint)
	scope := strings.TrimSpace(filter.Scope)
	if subjectType == "" || subjectID == "" || permissionPoint == "" || scope == "" {
		return errors.New("permission grant filter is incomplete")
	}
	return nil
}

type permissionGrantFields struct {
	ID              field.Int64
	SubjectType     field.String
	SubjectID       field.String
	PermissionPoint field.String
	Scope           field.String
	AppID           field.String
	BotOpenID       field.String
	ResourceChatID  field.String
	ResourceUserID  field.String
}

func permissionGrantQueryWithoutCache() (*query.Query, error) {
	q := infraDB.QueryWithoutCache()
	if q == nil {
		return nil, errors.New("db is not initialized")
	}
	return q, nil
}

func permissionGrantFieldSet(q *query.Query) permissionGrantFields {
	ins := q.PermissionGrant
	return permissionGrantFields{
		ID:              ins.ID,
		SubjectType:     ins.SubjectType,
		SubjectID:       ins.SubjectID,
		PermissionPoint: ins.PermissionPoint,
		Scope:           ins.Scope,
		AppID:           ins.AppID,
		BotOpenID:       ins.BotOpenID,
		ResourceChatID:  ins.ResourceChatID,
		ResourceUserID:  ins.ResourceUserID,
	}
}

func applyGrantFilter(grantQuery query.IPermissionGrantDo, fields permissionGrantFields, filter GrantFilter) query.IPermissionGrantDo {
	grantQuery = grantQuery.
		Where(fields.SubjectType.Eq(strings.TrimSpace(filter.SubjectType))).
		Where(fields.SubjectID.Eq(strings.TrimSpace(filter.SubjectID))).
		Where(fields.PermissionPoint.Eq(strings.TrimSpace(filter.PermissionPoint))).
		Where(fields.Scope.Eq(strings.TrimSpace(filter.Scope)))

	if appID := strings.TrimSpace(filter.AppID); appID != "" {
		grantQuery = grantQuery.Where(fields.AppID.Eq(appID))
	}
	if botOpenID := strings.TrimSpace(filter.BotOpenID); botOpenID != "" {
		grantQuery = grantQuery.Where(fields.BotOpenID.Eq(botOpenID))
	}

	resourceChatID := strings.TrimSpace(filter.ResourceChatID)
	if resourceChatID == "" {
		grantQuery = grantQuery.Where(fields.ResourceChatID.IsNull())
	} else {
		grantQuery = grantQuery.Where(fields.ResourceChatID.Eq(resourceChatID))
	}

	resourceUserID := strings.TrimSpace(filter.ResourceUserID)
	if resourceUserID == "" {
		grantQuery = grantQuery.Where(fields.ResourceUserID.IsNull())
	} else {
		grantQuery = grantQuery.Where(fields.ResourceUserID.Eq(resourceUserID))
	}
	return grantQuery
}

func applyExactGrantFilter(grantQuery query.IPermissionGrantDo, fields permissionGrantFields, grant Grant) query.IPermissionGrantDo {
	grantQuery = grantQuery.
		Where(fields.SubjectType.Eq(grant.SubjectType)).
		Where(fields.SubjectID.Eq(grant.SubjectID)).
		Where(fields.PermissionPoint.Eq(grant.PermissionPoint)).
		Where(fields.Scope.Eq(grant.Scope)).
		Where(fields.AppID.Eq(grant.AppID)).
		Where(fields.BotOpenID.Eq(grant.BotOpenID))

	if grant.ResourceChatID == nil {
		grantQuery = grantQuery.Where(fields.ResourceChatID.IsNull())
	} else {
		grantQuery = grantQuery.Where(fields.ResourceChatID.Eq(*grant.ResourceChatID))
	}
	if grant.ResourceUserID == nil {
		grantQuery = grantQuery.Where(fields.ResourceUserID.IsNull())
	} else {
		grantQuery = grantQuery.Where(fields.ResourceUserID.Eq(*grant.ResourceUserID))
	}
	return grantQuery
}

func toPermissionGrantModel(grant Grant) *model.PermissionGrant {
	return &model.PermissionGrant{
		ID:              grant.ID,
		SubjectType:     grant.SubjectType,
		SubjectID:       grant.SubjectID,
		PermissionPoint: grant.PermissionPoint,
		Scope:           grant.Scope,
		AppID:           grant.AppID,
		BotOpenID:       grant.BotOpenID,
		ResourceChatID:  cloneNullableString(grant.ResourceChatID),
		ResourceUserID:  cloneNullableString(grant.ResourceUserID),
		Remark:          grant.Remark,
		CreatedAt:       grant.CreatedAt,
		UpdatedAt:       grant.UpdatedAt,
		DeletedAt:       grant.DeletedAt,
	}
}

func fromPermissionGrantModel(grant *model.PermissionGrant) Grant {
	if grant == nil {
		return Grant{}
	}
	return Grant{
		ID:              grant.ID,
		SubjectType:     grant.SubjectType,
		SubjectID:       grant.SubjectID,
		PermissionPoint: grant.PermissionPoint,
		Scope:           grant.Scope,
		AppID:           grant.AppID,
		BotOpenID:       grant.BotOpenID,
		ResourceChatID:  cloneNullableString(grant.ResourceChatID),
		ResourceUserID:  cloneNullableString(grant.ResourceUserID),
		Remark:          grant.Remark,
		CreatedAt:       grant.CreatedAt,
		UpdatedAt:       grant.UpdatedAt,
		DeletedAt:       grant.DeletedAt,
	}
}

func cloneNullableString(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
