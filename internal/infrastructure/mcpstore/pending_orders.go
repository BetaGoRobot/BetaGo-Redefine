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
	rows, err := ins.WithContext(ctx).Where(ins.ID.Eq(id)).Limit(1).Find()
	if err != nil {
		return luckin.PendingOrder{}, err
	}
	if len(rows) == 0 {
		return luckin.PendingOrder{}, luckin.ErrPendingOrderNotFound
	}
	return pendingOrderFromRow(rows[0]), nil
}

func (r *PendingOrderRepository) MarkConfirmed(ctx context.Context, id, payloadHash, confirmedByOpenID string, resultJSON json.RawMessage, now time.Time) error {
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
	result, err := ins.WithContext(ctx).
		Where(ins.ID.Eq(id)).
		Where(ins.PayloadHash.Eq(payloadHash)).
		Where(ins.Status.Eq(string(luckin.PendingStatusPending))).
		Where(ins.ExpiresAt.Gt(now)).
		Updates(updates)
	if err != nil {
		return err
	}
	if result.RowsAffected == 0 {
		return luckin.ErrPendingOrderNotConfirmable
	}
	return nil
}

func (r *PendingOrderRepository) MarkCancelled(ctx context.Context, id, payloadHash, operatorOpenID, chatID string, now time.Time) error {
	if now.IsZero() {
		now = time.Now()
	}
	updates := map[string]any{
		"status":               string(luckin.PendingStatusCancelled),
		"confirmed_by_open_id": operatorOpenID,
		"updated_at":           now,
	}
	ins := r.q.LuckinPendingOrder
	result, err := ins.WithContext(ctx).
		Where(ins.ID.Eq(id)).
		Where(ins.PayloadHash.Eq(payloadHash)).
		Where(ins.ChatID.Eq(chatID)).
		Where(ins.RequesterOpenID.Eq(operatorOpenID)).
		Where(ins.Status.Eq(string(luckin.PendingStatusPending))).
		Where(ins.ExpiresAt.Gt(now)).
		Updates(updates)
	if err != nil {
		return err
	}
	if result.RowsAffected == 0 {
		return luckin.ErrPendingOrderNotConfirmable
	}
	return nil
}

func (r *PendingOrderRepository) UpdateDraft(ctx context.Context, order luckin.PendingOrder, now time.Time) error {
	if now.IsZero() {
		now = time.Now()
	}
	updates := map[string]any{
		"create_order_payload": datatypes.JSON(defaultJSON(order.CreateOrderPayload)),
		"payload_hash":         order.PayloadHash,
		"preview_result":       datatypes.JSON(defaultJSON(order.PreviewResult)),
		"updated_at":           now,
	}
	ins := r.q.LuckinPendingOrder
	result, err := ins.WithContext(ctx).
		Where(ins.ID.Eq(order.ID)).
		Where(ins.Status.Eq(string(luckin.PendingStatusPending))).
		Where(ins.ExpiresAt.Gt(now)).
		Updates(updates)
	if err != nil {
		return err
	}
	if result.RowsAffected == 0 {
		return luckin.ErrPendingOrderNotConfirmable
	}
	return nil
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
		InitiatorOpenID:     order.InitiatorOpenID,
		CredentialScopeType: string(order.CredentialScope.Type),
		CredentialScopeID:   order.CredentialScope.ID,
		McpServerName:       order.MCPServerName,
		CreateOrderPayload:  datatypes.JSON(defaultJSON(order.CreateOrderPayload)),
		PayloadHash:         order.PayloadHash,
		PreviewResult:       datatypes.JSON(defaultJSON(order.PreviewResult)),
		CartSnapshot:        marshalCartSnapshot(order.CartSnapshot),
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
		InitiatorOpenID:    row.InitiatorOpenID,
		CredentialScope:    luckin.CredentialScope{Type: luckin.ScopeType(row.CredentialScopeType), ID: row.CredentialScopeID},
		MCPServerName:      row.McpServerName,
		CreateOrderPayload: json.RawMessage(row.CreateOrderPayload),
		PayloadHash:        row.PayloadHash,
		PreviewResult:      json.RawMessage(row.PreviewResult),
		CartSnapshot:       unmarshalCartSnapshot(row.CartSnapshot),
		Status:             luckin.PendingStatus(row.Status),
		ResultJSON:         json.RawMessage(row.ResultJSON),
		ErrorText:          row.ErrorText,
		ExpiresAt:          row.ExpiresAt,
		ConfirmedByOpenID:  row.ConfirmedByOpenID,
		ConfirmedAt:        confirmedAtPtr,
	}
}

// marshalCartSnapshot 把 []CartItem 落库成 jsonb 列内容；空 cart 用 "[]" 以匹配 NOT NULL DEFAULT '[]'。
func marshalCartSnapshot(items []luckin.CartItem) string {
	if len(items) == 0 {
		return "[]"
	}
	raw, err := json.Marshal(items)
	if err != nil {
		return "[]"
	}
	return string(raw)
}

func unmarshalCartSnapshot(raw string) []luckin.CartItem {
	if raw == "" {
		return nil
	}
	var items []luckin.CartItem
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return nil
	}
	return items
}

func defaultJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`{}`)
	}
	return raw
}
