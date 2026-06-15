package mcpstore

import (
	"context"
	"errors"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/luckin"
	infraDB "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/query"
	"gorm.io/gen/field"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type CredentialRepository struct {
	q     *query.Query
	codec TokenCodec
}

func NewCredentialRepository(db *gorm.DB, codec TokenCodec) *CredentialRepository {
	return &CredentialRepository{q: query.Use(infraDB.WithoutQueryCache(db)), codec: codec}
}

func (r *CredentialRepository) FindToken(ctx context.Context, lookup luckin.CredentialLookup) (luckin.Credential, error) {
	ins := r.q.McpCredential
	row, err := ins.WithContext(ctx).
		Where(ins.Provider.Eq(lookup.Provider)).
		Where(ins.AppID.Eq(lookup.AppID)).
		Where(ins.BotOpenID.Eq(lookup.BotOpenID)).
		Where(ins.ScopeType.Eq(string(lookup.Scope.Type))).
		Where(ins.ScopeID.Eq(lookup.Scope.ID)).
		Where(field.NewField(ins.TableName(), "deleted_at").IsNull()).
		First()
	if err := normalizeCredentialFindError(err); err != nil {
		return luckin.Credential{}, err
	}
	return credentialFromRow(r.codec, lookup, row)
}

func (r *CredentialRepository) UpsertToken(ctx context.Context, lookup luckin.CredentialLookup, token, actorOpenID string) error {
	row, err := buildCredentialRow(r.codec, lookup, token, actorOpenID, time.Now())
	if err != nil {
		return err
	}
	ins := r.q.McpCredential
	return ins.WithContext(ctx).Clauses(credentialUpsertConflict(row)).Create(row)
}

func buildCredentialRow(codec TokenCodec, lookup luckin.CredentialLookup, token, actorOpenID string, now time.Time) (*model.McpCredential, error) {
	encrypted, err := codec.Encrypt(token)
	if err != nil {
		return nil, err
	}
	return &model.McpCredential{
		Provider:        lookup.Provider,
		AppID:           lookup.AppID,
		BotOpenID:       lookup.BotOpenID,
		ScopeType:       string(lookup.Scope.Type),
		ScopeID:         lookup.Scope.ID,
		EncryptedToken:  encrypted,
		TokenHint:       luckin.MaskToken(token),
		CreatedByOpenID: actorOpenID,
		UpdatedByOpenID: actorOpenID,
		CreatedAt:       now,
		UpdatedAt:       now,
	}, nil
}

func credentialFromRow(codec TokenCodec, lookup luckin.CredentialLookup, row *model.McpCredential) (luckin.Credential, error) {
	if row == nil {
		return luckin.Credential{}, luckin.ErrCredentialNotFound
	}
	token, err := codec.Decrypt(row.EncryptedToken)
	if err != nil {
		return luckin.Credential{}, err
	}
	return luckin.Credential{
		Provider:  lookup.Provider,
		Scope:     lookup.Scope,
		Token:     token,
		TokenHint: row.TokenHint,
	}, nil
}

func normalizeCredentialFindError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return luckin.ErrCredentialNotFound
	}
	return err
}

func credentialUpsertConflict(row *model.McpCredential) clause.OnConflict {
	return clause.OnConflict{
		Columns: []clause.Column{
			{Name: "provider"},
			{Name: "app_id"},
			{Name: "bot_open_id"},
			{Name: "scope_type"},
			{Name: "scope_id"},
		},
		DoUpdates: clause.Assignments(map[string]any{
			"encrypted_token":    row.EncryptedToken,
			"token_hint":         row.TokenHint,
			"updated_by_open_id": row.UpdatedByOpenID,
			"updated_at":         row.UpdatedAt,
			"deleted_at":         nil,
		}),
	}
}
