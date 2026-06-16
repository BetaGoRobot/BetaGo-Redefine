package mcpstore

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/luckin"
	"gorm.io/datatypes"
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
		t.Fatalf("scope mismatch: %+v", row)
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
		t.Fatalf("identity mismatch: %+v", got)
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
