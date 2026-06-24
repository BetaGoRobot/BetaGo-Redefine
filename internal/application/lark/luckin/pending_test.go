package luckin

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"
)

func TestNewPendingOrderComputesHashAndExpiry(t *testing.T) {
	payload := json.RawMessage(`{"deptId":1,"productList":[{"amount":1,"productId":2,"skuCode":"s"}]}`)
	order := NewPendingOrder(NewPendingOrderRequest{
		AppID:              "app",
		BotOpenID:          "bot",
		ChatID:             "chat",
		InitiatorOpenID:    "user",
		Credential:         Credential{Scope: CredentialScope{Type: ScopePersonal, ID: "user"}},
		CreateOrderPayload: payload,
		PreviewResult:      json.RawMessage(`{"discountPrice":9.9}`),
		Now:                time.Unix(100, 0),
	})
	if order.ID == "" || order.PayloadHash == "" {
		t.Fatalf("missing id/hash: id_empty=%t hash_empty=%t", order.ID == "", order.PayloadHash == "")
	}
	wantHashBytes := sha256.Sum256(payload)
	if order.PayloadHash != hex.EncodeToString(wantHashBytes[:]) {
		t.Fatalf("PayloadHash = %q", order.PayloadHash)
	}
	if !order.ExpiresAt.Equal(time.Unix(100, 0).Add(10 * time.Minute)) {
		t.Fatalf("ExpiresAt = %s", order.ExpiresAt)
	}
	if order.Status != PendingStatusPending {
		t.Fatalf("Status = %q", order.Status)
	}
	if order.MCPServerName != ServerName {
		t.Fatalf("MCPServerName = %q", order.MCPServerName)
	}
	if order.AppID != "app" || order.BotOpenID != "bot" {
		t.Fatalf("app/bot mismatch: app=%q bot=%q", order.AppID, order.BotOpenID)
	}
}

func TestScopeLabelMapsKnownScopes(t *testing.T) {
	tests := []struct {
		name  string
		scope CredentialScope
		want  string
	}{
		{name: "personal", scope: CredentialScope{Type: ScopePersonal}, want: "个人瑞幸账号"},
		{name: "chat", scope: CredentialScope{Type: ScopeChat}, want: "群聊默认瑞幸账号"},
		{name: "system", scope: CredentialScope{Type: ScopeSystem}, want: "系统默认瑞幸账号"},
		{name: "unknown", scope: CredentialScope{Type: ScopeType("other")}, want: "未知瑞幸账号"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ScopeLabel(tt.scope); got != tt.want {
				t.Fatalf("ScopeLabel() = %q, want %q", got, tt.want)
			}
		})
	}
}
