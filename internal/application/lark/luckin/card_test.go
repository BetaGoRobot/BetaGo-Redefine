package luckin

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildPendingOrderCardContainsScopeAndActions(t *testing.T) {
	card := BuildPendingOrderCard(PendingOrder{
		ID:              "po_1",
		PayloadHash:     "hash_1",
		CredentialScope: CredentialScope{Type: ScopeChat, ID: "chat"},
	})
	text := mustMarshalForTest(card)
	if !containsAll(text, "群聊默认瑞幸账号", "luckin_order_confirm", "luckin_order_cancel") {
		t.Fatalf("card missing required content: %s", text)
	}
	if !containsAll(text, "po_1", "hash_1", "pending_order_id", "payload_hash") {
		t.Fatalf("card missing callback fields: %s", text)
	}
}

func TestBuildPendingOrderCardDoesNotExposePayload(t *testing.T) {
	card := BuildPendingOrderCard(PendingOrder{
		ID:                 "po_1",
		PayloadHash:        "hash_1",
		CredentialScope:    CredentialScope{Type: ScopePersonal, ID: "user"},
		CreateOrderPayload: []byte(`{"secret":"full-order-payload"}`),
	})
	text := mustMarshalForTest(card)
	if containsAll(text, "full-order-payload") {
		t.Fatalf("card exposes order payload: %s", text)
	}
}

func mustMarshalForTest(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func containsAll(s string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(s, part) {
			return false
		}
	}
	return true
}
