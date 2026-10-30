package larkeventstore

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	appdb "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type GormStore struct {
	db        *gorm.DB
	mu        sync.Mutex
	nextPrune map[Scope]time.Time
}

func NewGormStore(db *gorm.DB) *GormStore {
	return &GormStore{db: db, nextPrune: make(map[Scope]time.Time)}
}

func (s *GormStore) SaveEvent(ctx context.Context, e Event) error {
	if s == nil || s.db == nil {
		return errors.New("event journal database unavailable")
	}
	if !e.Scope().Valid() || e.EventID == "" || e.MessageID == "" || !json.Valid([]byte(e.Payload)) {
		return errors.New("invalid event journal record")
	}
	err := s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&e).Error
	if err == nil {
		s.prune(ctx, e.Scope())
	}
	return err
}
func (s *GormStore) UpdateResolution(ctx context.Context, scope Scope, eventID string, r Resolution) error {
	if s == nil || s.db == nil {
		return errors.New("event journal database unavailable")
	}
	updates := map[string]any{"status": r.Status, "resolution_source": r.Source, "error_code": r.ErrorCode, "error": r.Error}
	if r.ChatID != "" {
		updates["chat_id"] = r.ChatID
	}
	return s.db.WithContext(ctx).Model(&Event{}).Where("app_id = ? AND bot_open_id = ? AND event_id = ?", scope.AppID, scope.BotOpenID, eventID).Updates(updates).Error
}
func (s *GormStore) PutLocation(ctx context.Context, l Location) error {
	if s == nil || s.db == nil {
		return errors.New("message locator database unavailable")
	}
	if !(Scope{l.AppID, l.BotOpenID}).Valid() || l.MessageID == "" || l.ChatID == "" {
		return errors.New("invalid message location")
	}
	result := s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "app_id"}, {Name: "bot_open_id"}, {Name: "message_id"}},
		DoUpdates: clause.Assignments(map[string]any{"last_seen_at": gorm.Expr("GREATEST(lark_message_locations.last_seen_at, EXCLUDED.last_seen_at)"), "recalled": gorm.Expr("lark_message_locations.recalled OR EXCLUDED.recalled")}),
		Where:     clause.Where{Exprs: []clause.Expression{gorm.Expr("lark_message_locations.chat_id = EXCLUDED.chat_id")}},
	}).Create(&l)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("message location conflicts with previously observed chat")
	}
	s.prune(ctx, Scope{l.AppID, l.BotOpenID})
	return nil
}
func (s *GormStore) FindLocation(ctx context.Context, scope Scope, mid string) (Location, bool, error) {
	if s == nil || s.db == nil {
		return Location{}, false, errors.New("message locator database unavailable")
	}
	var l Location
	// Find, rather than First, avoids emitting an error for an ordinary cache miss.
	r := appdb.WithoutQueryCache(s.db.WithContext(ctx)).Where("app_id = ? AND bot_open_id = ? AND message_id = ? AND last_seen_at >= ?", scope.AppID, scope.BotOpenID, mid, time.Now().Add(-90*24*time.Hour)).Limit(1).Find(&l)
	return l, r.RowsAffected > 0, r.Error
}

// A bounded batch once per scope/hour avoids an unbounded retention table or a
// background worker with a separate shutdown lifecycle. Indexes bound each scan.
func (s *GormStore) prune(ctx context.Context, scope Scope) {
	now := time.Now().UTC()
	s.mu.Lock()
	if now.Before(s.nextPrune[scope]) {
		s.mu.Unlock()
		return
	}
	s.nextPrune[scope] = now.Add(time.Hour)
	s.mu.Unlock()
	db := s.db.WithContext(ctx)
	events := db.Model(&Event{}).Select("app_id,bot_open_id,event_id").Where("app_id = ? AND bot_open_id = ? AND received_at < ?", scope.AppID, scope.BotOpenID, now.Add(-30*24*time.Hour)).Order("received_at").Limit(1000)
	if err := db.Where("(app_id,bot_open_id,event_id) IN (?)", events).Delete(&Event{}).Error; err != nil {
		msg, _ := safeError(err)
		logs.L().Ctx(ctx).Warn("event journal retention batch failed", zap.String("error", msg))
		return
	}
	locations := db.Model(&Location{}).Select("app_id,bot_open_id,message_id").Where("app_id = ? AND bot_open_id = ? AND last_seen_at < ?", scope.AppID, scope.BotOpenID, now.Add(-90*24*time.Hour)).Order("last_seen_at").Limit(1000)
	if err := db.Where("(app_id,bot_open_id,message_id) IN (?)", locations).Delete(&Location{}).Error; err != nil {
		msg, _ := safeError(err)
		logs.L().Ctx(ctx).Warn("message locator retention batch failed", zap.String("error", msg))
	}
}
