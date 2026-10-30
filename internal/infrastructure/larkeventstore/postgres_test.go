package larkeventstore

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	sqlmigrations "github.com/BetaGoRobot/BetaGo-Redefine/script/sql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestPostgresProvenanceInIsolatedRolledBackSchema(t *testing.T) {
	dsn := os.Getenv("BETAGO_EVENTSTORE_TEST_DSN")
	if dsn == "" {
		t.Skip("set BETAGO_EVENTSTORE_TEST_DSN to run isolated PostgreSQL contract test")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open PostgreSQL fixture: %T", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	tx := db.WithContext(ctx).Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	defer tx.Rollback()
	schemaName := "event_provenance_test_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if err := tx.Exec("CREATE SCHEMA " + schemaName).Error; err != nil {
		t.Fatal(err)
	}
	if err := tx.Exec("SELECT set_config('search_path', ?, true)", schemaName).Error; err != nil {
		t.Fatal(err)
	}
	body, err := sqlmigrations.Files.ReadFile("20261030_lark_event_provenance.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range strings.Split(strings.ReplaceAll(string(body), "betago.", schemaName+"."), ";") {
		if strings.TrimSpace(statement) != "" {
			if err := tx.Exec(statement).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	s := NewGormStore(tx)
	r := NewRecorder(s)
	scope := Scope{AppID: "cli_fixture", BotOpenID: "ou_fixture"}
	e := r.CaptureReaction(ctx, scope, testReaction())
	var stored Event
	if err := tx.Where("event_id = ?", e.EventID).Take(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.ActorOpenID != "ou_actor" || stored.ActionTime != "1793346959000" || stored.Payload == "" {
		t.Fatalf("event not durably captured: %#v", stored)
	}
	duplicate := e
	duplicate.ActorOpenID = "ou_different"
	duplicate.ReceivedAt = e.ReceivedAt.Add(time.Hour)
	if err := s.SaveEvent(ctx, duplicate); err != nil {
		t.Fatal(err)
	}
	stored = Event{}
	if err := tx.Where("event_id = ?", e.EventID).Take(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.ActorOpenID != e.ActorOpenID || stored.ReceivedAt.UnixMilli() != e.ReceivedAt.UnixMilli() {
		t.Fatal("duplicate replaced first receipt/actor")
	}
	r.RecordResolution(ctx, scope, e.EventID, Resolution{Status: "unresolved", Source: "feishu", ErrorCode: 230050, Error: "message invisible"})
	stored = Event{}
	if err := tx.Where("event_id = ?", e.EventID).Take(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != "unresolved" || stored.ErrorCode != 230050 || stored.ActorOpenID != "ou_actor" {
		t.Fatalf("resolution lost event identity: %#v", stored)
	}
	r.RecordResolution(ctx, scope, e.EventID, Resolution{Status: "resolved", Source: "feishu", ChatID: "oc_observed"})
	r.RecordResolution(ctx, scope, e.EventID, Resolution{Status: "unresolved", Source: "feishu", ErrorCode: 230050, Error: "message invisible"})
	stored = Event{}
	if err := tx.Where("event_id = ?", e.EventID).Take(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.ChatID != "oc_observed" {
		t.Fatal("later lookup failure erased a previously observed group")
	}
	r.ObserveMessage(ctx, scope, "om_known", "oc_known", false)
	r.ObserveMessage(ctx, scope, "om_known", "oc_known", true)
	r.ObserveMessage(ctx, scope, "om_known", "oc_known", false)
	loc, ok, err := s.FindLocation(ctx, scope, "om_known")
	if err != nil || !ok || loc.ChatID != "oc_known" || !loc.Recalled {
		t.Fatalf("recall locator regressed: %#v %v", loc, err)
	}
	conflict := loc
	conflict.ChatID = "oc_wrong"
	if err := s.PutLocation(ctx, conflict); err == nil {
		t.Fatal("conflicting group silently overwrote location")
	}
	_, ok, err = s.FindLocation(ctx, Scope{AppID: "cli_other", BotOpenID: "ou_other"}, "om_known")
	if err != nil || ok {
		t.Fatal("locator crossed bot scope")
	}
	old := e
	old.EventID = "old-event"
	old.ReceivedAt = time.Now().Add(-31 * 24 * time.Hour)
	if err := tx.Create(&old).Error; err != nil {
		t.Fatal(err)
	}
	other := old
	other.AppID = "cli_other"
	if err := tx.Create(&other).Error; err != nil {
		t.Fatal(err)
	}
	oldLoc := loc
	oldLoc.MessageID = "om_expired"
	oldLoc.LastSeenAt = time.Now().Add(-91 * 24 * time.Hour)
	if err := tx.Create(&oldLoc).Error; err != nil {
		t.Fatal(err)
	}
	cleanup := NewGormStore(tx)
	fresh := e
	fresh.EventID = "fresh-event"
	if err := cleanup.SaveEvent(ctx, fresh); err != nil {
		t.Fatal(err)
	}
	var n int64
	if err := tx.Model(&Event{}).Where("event_id = ? AND app_id = ?", "old-event", scope.AppID).Count(&n).Error; err != nil || n != 0 {
		t.Fatalf("expired audit retained: %d %v", n, err)
	}
	if err := tx.Model(&Event{}).Where("event_id = ? AND app_id = ?", "old-event", "cli_other").Count(&n).Error; err != nil || n != 1 {
		t.Fatalf("cleanup crossed scope: %d %v", n, err)
	}
	if err := tx.Model(&Location{}).Where("message_id = ?", "om_expired").Count(&n).Error; err != nil || n != 0 {
		t.Fatalf("expired location retained: %d %v", n, err)
	}
}
