package mcpstore

import (
	"context"
	"encoding/json"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/luckin"
	infraDB "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/query"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type PendingOrderRepository struct {
	q *query.Query
}

func NewPendingOrderRepository(db *gorm.DB) *PendingOrderRepository {
	return &PendingOrderRepository{q: query.Use(infraDB.WithoutQueryCache(db))}
}

func (r *PendingOrderRepository) CreatePendingOrder(ctx context.Context, order luckin.PendingOrder) error {
	return r.q.LuckinPendingOrder.WithContext(ctx).Create(buildPendingOrderRow(order))
}

func (r *PendingOrderRepository) FindPendingOrder(ctx context.Context, id string) (luckin.PendingOrder, error) {
	ins := r.q.LuckinPendingOrder
	row, err := ins.WithContext(ctx).Where(ins.ID.Eq(id)).Take()
	if err != nil {
		return luckin.PendingOrder{}, err
	}
	return pendingOrderFromRow(row), nil
}

func (r *PendingOrderRepository) MarkConfirmed(ctx context.Context, id, confirmedByOpenID string, resultJSON json.RawMessage, now time.Time) error {
	if now.IsZero() {
		now = time.Now()
	}
	updates := map[string]any{
		"status":               string(luckin.PendingStatusConfirmed),
		"confirmed_by_open_id": confirmedByOpenID,
		"confirmed_at":         now,
		"result_json":          datatypes.JSON(defaultJSON(resultJSON)),
		"updated_at":           now,
	}
	ins := r.q.LuckinPendingOrder
	_, err := ins.WithContext(ctx).Where(ins.ID.Eq(id)).Updates(updates)
	return err
}

func buildPendingOrderRow(order luckin.PendingOrder) *model.LuckinPendingOrder {
	confirmedAt := time.Time{}
	if order.ConfirmedAt != nil {
		confirmedAt = *order.ConfirmedAt
	}
	return &model.LuckinPendingOrder{
		ID:                  order.ID,
		AppID:               order.AppID,
		BotOpenID:           order.BotOpenID,
		ChatID:              order.ChatID,
		RequesterOpenID:     order.RequesterOpenID,
		CredentialScopeType: string(order.CredentialScope.Type),
		CredentialScopeID:   order.CredentialScope.ID,
		McpServerName:       order.MCPServerName,
		CreateOrderPayload:  datatypes.JSON(defaultJSON(order.CreateOrderPayload)),
		PayloadHash:         order.PayloadHash,
		PreviewResult:       datatypes.JSON(defaultJSON(order.PreviewResult)),
		Status:              string(order.Status),
		ResultJSON:          datatypes.JSON(defaultJSON(order.ResultJSON)),
		ErrorText:           order.ErrorText,
		ExpiresAt:           order.ExpiresAt,
		ConfirmedByOpenID:   order.ConfirmedByOpenID,
		ConfirmedAt:         confirmedAt,
	}
}

func pendingOrderFromRow(row *model.LuckinPendingOrder) luckin.PendingOrder {
	if row == nil {
		return luckin.PendingOrder{}
	}
	confirmedAt := row.ConfirmedAt
	var confirmedAtPtr *time.Time
	if !confirmedAt.IsZero() {
		confirmedAtPtr = &confirmedAt
	}
	return luckin.PendingOrder{
		ID:                 row.ID,
		AppID:              row.AppID,
		BotOpenID:          row.BotOpenID,
		ChatID:             row.ChatID,
		RequesterOpenID:    row.RequesterOpenID,
		CredentialScope:    luckin.CredentialScope{Type: luckin.ScopeType(row.CredentialScopeType), ID: row.CredentialScopeID},
		MCPServerName:      row.McpServerName,
		CreateOrderPayload: json.RawMessage(row.CreateOrderPayload),
		PayloadHash:        row.PayloadHash,
		PreviewResult:      json.RawMessage(row.PreviewResult),
		Status:             luckin.PendingStatus(row.Status),
		ResultJSON:         json.RawMessage(row.ResultJSON),
		ErrorText:          row.ErrorText,
		ExpiresAt:          row.ExpiresAt,
		ConfirmedByOpenID:  row.ConfirmedByOpenID,
		ConfirmedAt:        confirmedAtPtr,
	}
}

func defaultJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`{}`)
	}
	return raw
}
