package mcpstore

import (
	"context"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/luckin"
	infraDB "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/query"
	"gorm.io/gorm"
)

type OrderRepository struct {
	q *query.Query
}

func NewOrderRepository(db *gorm.DB) *OrderRepository {
	return &OrderRepository{q: query.Use(infraDB.WithoutQueryCache(db))}
}

func (r *OrderRepository) CreateOrder(ctx context.Context, record luckin.OrderRecord) error {
	return r.q.LuckinOrder.WithContext(ctx).Create(orderRow(record))
}

// ClaimDueOrders 原子领取到期可轮询的订单：把 next_poll_at 推后一个租约，避免多 worker 重复处理。
func (r *OrderRepository) ClaimDueOrders(ctx context.Context, now time.Time, lease time.Duration, limit int) ([]luckin.OrderRecord, error) {
	ins := r.q.LuckinOrder
	rows, err := ins.WithContext(ctx).
		Where(ins.Status.Eq(string(luckin.OrderRecordActive))).
		Where(ins.NextPollAt.Lte(now)).
		Order(ins.NextPollAt).
		Limit(limit).
		Find()
	if err != nil {
		return nil, err
	}
	claimed := make([]luckin.OrderRecord, 0, len(rows))
	for _, row := range rows {
		res, err := ins.WithContext(ctx).
			Where(ins.ID.Eq(row.ID)).
			Where(ins.Status.Eq(string(luckin.OrderRecordActive))).
			Where(ins.NextPollAt.Lte(now)).
			Updates(map[string]any{
				"next_poll_at": now.Add(lease),
				"updated_at":   now,
			})
		if err != nil {
			return nil, err
		}
		if res.RowsAffected == 0 {
			continue
		}
		claimed = append(claimed, orderRecordFromRow(row))
	}
	return claimed, nil
}

// ApplyUpdate 写入一次轮询后的状态更新（节点时间戳、状态、下次轮询时间或终止）。
func (r *OrderRepository) ApplyUpdate(ctx context.Context, orderRowID int64, update OrderUpdate, now time.Time) error {
	updates := map[string]any{"updated_at": now}
	if update.Status != "" {
		updates["status"] = string(update.Status)
	}
	if update.LastRemoteStatus != nil {
		updates["last_remote_status"] = *update.LastRemoteStatus
	}
	if update.UnpaidReminded != nil {
		updates["unpaid_reminded"] = *update.UnpaidReminded
	}
	if update.NextPollAt != nil {
		updates["next_poll_at"] = *update.NextPollAt
	}
	if update.StoppedReason != "" {
		updates["stopped_reason"] = update.StoppedReason
	}
	if update.FailCount != nil {
		updates["fail_count"] = *update.FailCount
	}
	for col, ts := range update.Timestamps {
		updates[col] = ts
	}
	ins := r.q.LuckinOrder
	_, err := ins.WithContext(ctx).Where(ins.ID.Eq(orderRowID)).Updates(updates)
	return err
}

// OrderUpdate 描述一次轮询后的字段变更。
type OrderUpdate struct {
	Status           luckin.OrderRecordStatus
	LastRemoteStatus *int
	UnpaidReminded   *bool
	NextPollAt       *time.Time
	FailCount        *int
	StoppedReason    string
	Timestamps       map[string]time.Time
}

func orderRow(record luckin.OrderRecord) *model.LuckinOrder {
	now := record.CreatedAt
	if now.IsZero() {
		now = time.Now()
	}
	status := record.Status
	if status == "" {
		status = luckin.OrderRecordActive
	}
	return &model.LuckinOrder{
		OrderID:             record.OrderID,
		AppID:               record.AppID,
		BotOpenID:           record.BotOpenID,
		ChatID:              record.ChatID,
		RequesterOpenID:     record.RequesterOpenID,
		InitiatorOpenID:     record.InitiatorOpenID,
		CartSnapshot:        marshalCartSnapshot(record.CartSnapshot),
		CredentialScopeType: string(record.CredentialScope.Type),
		CredentialScopeID:   record.CredentialScope.ID,
		MessageID:           record.MessageID,
		Status:              string(status),
		LastRemoteStatus:    int64(record.LastRemoteStatus),
		NeedPay:             record.NeedPay,
		PayURL:              record.PayURL,
		QrURL:               record.QRCodeURL,
		DiscountPrice:       record.DiscountPrice,
		UnpaidReminded:      record.UnpaidReminded,
		NextPollAt:          record.NextPollAt,
		PollDeadline:        record.PollDeadline,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
}

func orderRecordFromRow(row *model.LuckinOrder) luckin.OrderRecord {
	if row == nil {
		return luckin.OrderRecord{}
	}
	return luckin.OrderRecord{
		OrderID:          row.OrderID,
		AppID:            row.AppID,
		BotOpenID:        row.BotOpenID,
		ChatID:           row.ChatID,
		RequesterOpenID:  row.RequesterOpenID,
		InitiatorOpenID:  row.InitiatorOpenID,
		CartSnapshot:     unmarshalCartSnapshot(row.CartSnapshot),
		CredentialScope:  luckin.CredentialScope{Type: luckin.ScopeType(row.CredentialScopeType), ID: row.CredentialScopeID},
		MessageID:        row.MessageID,
		Status:           luckin.OrderRecordStatus(row.Status),
		LastRemoteStatus: int(row.LastRemoteStatus),
		NeedPay:          row.NeedPay,
		PayURL:           row.PayURL,
		QRCodeURL:        row.QrURL,
		DiscountPrice:    row.DiscountPrice,
		UnpaidReminded:   row.UnpaidReminded,
		NextPollAt:       row.NextPollAt,
		PollDeadline:     row.PollDeadline,
		FailCount:        int(row.FailCount),
		StoppedReason:    row.StoppedReason,
		CreatedAt:        row.CreatedAt,
	}
}

// FindRowID 通过 app/bot/orderID 找到行主键，供 ApplyUpdate 使用。
func (r *OrderRepository) FindRowID(ctx context.Context, appID, botOpenID, orderID string) (int64, bool, error) {
	ins := r.q.LuckinOrder
	rows, err := ins.WithContext(ctx).
		Where(ins.AppID.Eq(appID)).
		Where(ins.BotOpenID.Eq(botOpenID)).
		Where(ins.OrderID.Eq(orderID)).
		Limit(1).
		Find()
	if err != nil {
		return 0, false, err
	}
	if len(rows) == 0 {
		return 0, false, nil
	}
	return rows[0].ID, true, nil
}
