package mcpstore

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/luckin"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"gorm.io/datatypes"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestBuildPendingOrderRowMapsJSONFields(t *testing.T) {
	confirmedAt := time.Unix(200, 0)
	order := luckin.PendingOrder{
		ID:                 "po_1",
		AppID:              "app",
		BotOpenID:          "bot",
		ChatID:             "chat",
		RequesterOpenID:    "user",
		CredentialScope:    luckin.CredentialScope{Type: luckin.ScopeChat, ID: "chat"},
		MCPServerName:      luckin.ServerName,
		CreateOrderPayload: json.RawMessage(`{"deptId":1}`),
		PayloadHash:        "hash_1",
		PreviewResult:      json.RawMessage(`{"price":9.9}`),
		Status:             luckin.PendingStatusConfirmed,
		ResultJSON:         json.RawMessage(`{"orderNo":"123"}`),
		ExpiresAt:          time.Unix(100, 0),
		ConfirmedByOpenID:  "user",
		ConfirmedAt:        &confirmedAt,
	}

	row := buildPendingOrderRow(order)
	if row.CreateOrderPayload.String() != datatypes.JSON(order.CreateOrderPayload).String() {
		t.Fatalf("CreateOrderPayload = %s", row.CreateOrderPayload)
	}
	if row.PreviewResult.String() != datatypes.JSON(order.PreviewResult).String() {
		t.Fatalf("PreviewResult = %s", row.PreviewResult)
	}
	if row.ResultJSON.String() != datatypes.JSON(order.ResultJSON).String() {
		t.Fatalf("ResultJSON = %s", row.ResultJSON)
	}
	if row.CredentialScopeType != string(luckin.ScopeChat) || row.CredentialScopeID != "chat" {
		t.Fatalf("scope mismatch: type=%q id=%q", row.CredentialScopeType, row.CredentialScopeID)
	}
	if !row.ConfirmedAt.Equal(confirmedAt) {
		t.Fatalf("ConfirmedAt = %s", row.ConfirmedAt)
	}
}

func TestPendingOrderFromRowMapsDomainFields(t *testing.T) {
	order := luckin.PendingOrder{
		ID:                 "po_1",
		AppID:              "app",
		BotOpenID:          "bot",
		ChatID:             "chat",
		RequesterOpenID:    "user",
		CredentialScope:    luckin.CredentialScope{Type: luckin.ScopePersonal, ID: "user"},
		MCPServerName:      luckin.ServerName,
		CreateOrderPayload: json.RawMessage(`{"deptId":1}`),
		PayloadHash:        "hash_1",
		PreviewResult:      json.RawMessage(`{"price":9.9}`),
		Status:             luckin.PendingStatusPending,
		ExpiresAt:          time.Unix(100, 0),
	}

	got := pendingOrderFromRow(buildPendingOrderRow(order))
	if got.ID != order.ID || got.AppID != order.AppID || got.BotOpenID != order.BotOpenID {
		t.Fatalf("identity mismatch: id=%q app=%q bot=%q", got.ID, got.AppID, got.BotOpenID)
	}
	if string(got.CreateOrderPayload) != string(order.CreateOrderPayload) {
		t.Fatalf("CreateOrderPayload = %s", got.CreateOrderPayload)
	}
	if string(got.PreviewResult) != string(order.PreviewResult) {
		t.Fatalf("PreviewResult = %s", got.PreviewResult)
	}
	if got.CredentialScope != order.CredentialScope {
		t.Fatalf("CredentialScope = %+v", got.CredentialScope)
	}
}

func TestPendingOrderRepositoryIntegrationCreateFindAndMarkConfirmed(t *testing.T) {
	if os.Getenv("BETAGO_RUN_MCPSTORE_INTEGRATION") != "1" {
		t.Skip("set BETAGO_RUN_MCPSTORE_INTEGRATION=1 to run mcpstore repository integration test")
	}
	cfg, err := config.LoadFileE("../../../.dev/config.toml")
	if err != nil || cfg.DBConfig == nil {
		t.Skipf("database config is unavailable: %v", err)
	}
	db, err := gorm.Open(postgres.Open(cfg.DBConfig.DSN()), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}

	ctx := context.Background()
	repo := NewPendingOrderRepository(db)
	now := time.Now().UTC().Truncate(time.Second)
	ids := []string{
		"test_pending_confirm",
		"test_pending_wrong_hash",
		"test_pending_expired",
		"test_pending_cancelled",
	}
	cleanupPendingOrders(t, db, ids)
	t.Cleanup(func() { cleanupPendingOrders(t, db, ids) })

	order := testPendingOrder(ids[0], luckin.PendingStatusPending, now.Add(10*time.Minute))
	if err := repo.CreatePendingOrder(ctx, order); err != nil {
		t.Fatalf("CreatePendingOrder error = %v", err)
	}
	found, err := repo.FindPendingOrder(ctx, order.ID)
	if err != nil {
		t.Fatalf("FindPendingOrder error = %v", err)
	}
	if found.ID != order.ID || found.PayloadHash != order.PayloadHash || found.Status != luckin.PendingStatusPending {
		t.Fatalf("found order mismatch: id=%q status=%q hash_match=%t", found.ID, found.Status, found.PayloadHash == order.PayloadHash)
	}

	resultJSON := json.RawMessage(`{"orderNo":"123"}`)
	if err := repo.MarkConfirmed(ctx, order.ID, order.PayloadHash, "actor-open-id", resultJSON, now); err != nil {
		t.Fatalf("MarkConfirmed error = %v", err)
	}
	var row model.LuckinPendingOrder
	if err := db.First(&row, "id = ?", order.ID).Error; err != nil {
		t.Fatalf("load confirmed row: %v", err)
	}
	if row.Status != string(luckin.PendingStatusConfirmed) || row.ConfirmedByOpenID != "actor-open-id" {
		t.Fatalf("confirmed fields mismatch: status=%q actor=%q", row.Status, row.ConfirmedByOpenID)
	}
	if !jsonEqual(row.ResultJSON, datatypes.JSON(resultJSON)) {
		t.Fatalf("ResultJSON = %s", row.ResultJSON)
	}
	if row.ConfirmedAt.IsZero() {
		t.Fatalf("ConfirmedAt is zero")
	}
	if err := repo.MarkConfirmed(ctx, order.ID, order.PayloadHash, "actor-open-id", resultJSON, now); !errors.Is(err, luckin.ErrPendingOrderNotConfirmable) {
		t.Fatalf("second MarkConfirmed err = %v, want ErrPendingOrderNotConfirmable", err)
	}

	wrongHashOrder := testPendingOrder(ids[1], luckin.PendingStatusPending, now.Add(10*time.Minute))
	if err := repo.CreatePendingOrder(ctx, wrongHashOrder); err != nil {
		t.Fatalf("CreatePendingOrder wrong hash fixture error = %v", err)
	}
	if err := repo.MarkConfirmed(ctx, wrongHashOrder.ID, "wrong-hash", "actor-open-id", resultJSON, now); !errors.Is(err, luckin.ErrPendingOrderNotConfirmable) {
		t.Fatalf("wrong hash MarkConfirmed err = %v, want ErrPendingOrderNotConfirmable", err)
	}

	expiredOrder := testPendingOrder(ids[2], luckin.PendingStatusPending, now.Add(-time.Minute))
	if err := repo.CreatePendingOrder(ctx, expiredOrder); err != nil {
		t.Fatalf("CreatePendingOrder expired fixture error = %v", err)
	}
	if err := repo.MarkConfirmed(ctx, expiredOrder.ID, expiredOrder.PayloadHash, "actor-open-id", resultJSON, now); !errors.Is(err, luckin.ErrPendingOrderNotConfirmable) {
		t.Fatalf("expired MarkConfirmed err = %v, want ErrPendingOrderNotConfirmable", err)
	}

	cancelledOrder := testPendingOrder(ids[3], luckin.PendingStatusCancelled, now.Add(10*time.Minute))
	if err := repo.CreatePendingOrder(ctx, cancelledOrder); err != nil {
		t.Fatalf("CreatePendingOrder cancelled fixture error = %v", err)
	}
	if err := repo.MarkConfirmed(ctx, cancelledOrder.ID, cancelledOrder.PayloadHash, "actor-open-id", resultJSON, now); !errors.Is(err, luckin.ErrPendingOrderNotConfirmable) {
		t.Fatalf("cancelled MarkConfirmed err = %v, want ErrPendingOrderNotConfirmable", err)
	}

	if _, err := repo.FindPendingOrder(ctx, "test_pending_missing"); !errors.Is(err, luckin.ErrPendingOrderNotFound) {
		t.Fatalf("FindPendingOrder missing err = %v, want ErrPendingOrderNotFound", err)
	}
	if err := repo.MarkConfirmed(ctx, "test_pending_missing", "hash", "actor-open-id", resultJSON, now); !errors.Is(err, luckin.ErrPendingOrderNotConfirmable) {
		t.Fatalf("missing MarkConfirmed err = %v, want ErrPendingOrderNotConfirmable", err)
	}
}

func testPendingOrder(id string, status luckin.PendingStatus, expiresAt time.Time) luckin.PendingOrder {
	return luckin.PendingOrder{
		ID:                 id,
		AppID:              "test-app",
		BotOpenID:          "test-bot",
		ChatID:             "test-chat",
		RequesterOpenID:    "test-user",
		CredentialScope:    luckin.CredentialScope{Type: luckin.ScopePersonal, ID: "test-user"},
		MCPServerName:      luckin.ServerName,
		CreateOrderPayload: json.RawMessage(`{"deptId":1}`),
		PayloadHash:        id + "-hash",
		PreviewResult:      json.RawMessage(`{"discountPrice":9.9}`),
		Status:             status,
		ResultJSON:         json.RawMessage(`{}`),
		ExpiresAt:          expiresAt,
	}
}

func cleanupPendingOrders(t *testing.T, db *gorm.DB, ids []string) {
	t.Helper()
	if len(ids) == 0 {
		return
	}
	if err := db.Unscoped().Where("id IN ?", ids).Delete(&model.LuckinPendingOrder{}).Error; err != nil {
		t.Fatalf("cleanup pending orders: %v", err)
	}
}

func jsonEqual(left, right []byte) bool {
	var leftValue any
	var rightValue any
	if err := json.Unmarshal(left, &leftValue); err != nil {
		return false
	}
	if err := json.Unmarshal(right, &rightValue); err != nil {
		return false
	}
	return reflect.DeepEqual(leftValue, rightValue)
}
