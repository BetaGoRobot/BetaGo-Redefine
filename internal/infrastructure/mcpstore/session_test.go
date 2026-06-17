package mcpstore

import (
	"context"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/luckin"
)

func TestSessionStoreSetGetClear(t *testing.T) {
	store := NewSessionStore()
	key := luckin.SessionKey{Provider: "luckin", AppID: "app", BotOpenID: "bot", ChatID: "chat", OpenID: "user"}

	if _, ok := store.GetShop(context.Background(), key); ok {
		t.Fatalf("expected empty session")
	}

	shop := luckin.ShopSelection{DeptID: 100, DeptName: "门店A", Longitude: 1.1, Latitude: 2.2}
	store.SetShop(context.Background(), key, shop)

	got, ok := store.GetShop(context.Background(), key)
	if !ok || got != shop {
		t.Fatalf("session mismatch: ok=%v got=%+v", ok, got)
	}
	recent := store.GetRecentShops(context.Background(), key, 3)
	if len(recent) != 1 || recent[0] != shop {
		t.Fatalf("recent shops mismatch: %+v", recent)
	}

	store.ClearShop(context.Background(), key)
	if _, ok := store.GetShop(context.Background(), key); ok {
		t.Fatalf("expected cleared session")
	}
	recent = store.GetRecentShops(context.Background(), key, 3)
	if len(recent) != 1 || recent[0] != shop {
		t.Fatalf("recent shops should survive current session clear: %+v", recent)
	}
}

func TestSessionStoreRecentShopsDedupesAndLimits(t *testing.T) {
	store := NewSessionStore()
	key := luckin.SessionKey{Provider: "luckin", AppID: "app", BotOpenID: "bot", ChatID: "chat", OpenID: "user"}

	store.SetShop(context.Background(), key, luckin.ShopSelection{DeptID: 1, DeptName: "门店A"})
	store.SetShop(context.Background(), key, luckin.ShopSelection{DeptID: 2, DeptName: "门店B"})
	store.SetShop(context.Background(), key, luckin.ShopSelection{DeptID: 1, DeptName: "门店A-新"})

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

func TestDefaultSessionStoreSingleton(t *testing.T) {
	if DefaultSessionStore() != DefaultSessionStore() {
		t.Fatalf("DefaultSessionStore should return a singleton")
	}
}
