package mcpstore

import (
	"context"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/luckin"
)

func TestSessionStoreSetGetDelete(t *testing.T) {
	store := NewSessionStore()
	key := luckin.SessionKey{Provider: "luckin", AppID: "app", BotOpenID: "bot", MessageID: "msg-1"}

	if _, ok := store.GetSession(context.Background(), key); ok {
		t.Fatalf("expected empty session")
	}

	sess := luckin.OrderSession{
		InitiatorOpenID: "ou_a",
		ChatID:          "chat",
		Shop:            luckin.ShopSelection{DeptID: 100, DeptName: "门店A"},
		Cart:            luckin.Cart{Items: []luckin.CartItem{{LineID: "L1", AddedByOpenID: "ou_a", ProductID: 1, SkuCode: "S", Amount: 2, UnitPrice: 16}}},
	}
	store.SetSession(context.Background(), key, sess)

	got, ok := store.GetSession(context.Background(), key)
	if !ok || got.InitiatorOpenID != "ou_a" || got.Shop.DeptID != 100 || len(got.Cart.Items) != 1 {
		t.Fatalf("session mismatch: ok=%v got=%+v", ok, got)
	}

	store.DeleteSession(context.Background(), key)
	if _, ok := store.GetSession(context.Background(), key); ok {
		t.Fatalf("expected cleared session")
	}
}

func TestSessionStoreRejectsKeyWithoutMessageID(t *testing.T) {
	store := NewSessionStore()
	key := luckin.SessionKey{Provider: "luckin", AppID: "app", BotOpenID: "bot"}
	store.SetSession(context.Background(), key, luckin.OrderSession{InitiatorOpenID: "ou_a"})
	if _, ok := store.GetSession(context.Background(), key); ok {
		t.Fatalf("session without message id must not persist")
	}
}

func TestSessionStoreRecentShopsDedupesAndLimits(t *testing.T) {
	store := NewSessionStore()
	key := luckin.UserHistoryKey{Provider: "luckin", AppID: "app", BotOpenID: "bot", ChatID: "chat", OpenID: "user"}

	store.AddRecentShop(context.Background(), key, luckin.ShopSelection{DeptID: 1, DeptName: "门店A"})
	store.AddRecentShop(context.Background(), key, luckin.ShopSelection{DeptID: 2, DeptName: "门店B"})
	store.AddRecentShop(context.Background(), key, luckin.ShopSelection{DeptID: 1, DeptName: "门店A-新"})

	recent := store.GetRecentShops(context.Background(), key, 2)
	if len(recent) != 2 {
		t.Fatalf("recent len = %d, want 2: %+v", len(recent), recent)
	}
	if recent[0].DeptID != 1 || recent[0].DeptName != "门店A-新" {
		t.Fatalf("latest deduped shop mismatch: %+v", recent)
	}
	if recent[1].DeptID != 2 {
		t.Fatalf("second shop mismatch: %+v", recent)
	}
}

func TestSessionStoreSeen(t *testing.T) {
	store := NewSessionStore()
	key := luckin.UserHistoryKey{Provider: "luckin", AppID: "app", BotOpenID: "bot", ChatID: "chat", OpenID: "user"}
	if store.Seen(context.Background(), key) {
		t.Fatalf("seen should be false initially")
	}
	store.MarkSeen(context.Background(), key)
	if !store.Seen(context.Background(), key) {
		t.Fatalf("seen should be true after MarkSeen")
	}
}

func TestDefaultSessionStoreSingleton(t *testing.T) {
	if DefaultSessionStore() != DefaultSessionStore() {
		t.Fatalf("DefaultSessionStore should return a singleton")
	}
}
