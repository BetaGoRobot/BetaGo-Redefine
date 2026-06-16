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

	store.ClearShop(context.Background(), key)
	if _, ok := store.GetShop(context.Background(), key); ok {
		t.Fatalf("expected cleared session")
	}
}

func TestDefaultSessionStoreSingleton(t *testing.T) {
	if DefaultSessionStore() != DefaultSessionStore() {
		t.Fatalf("DefaultSessionStore should return a singleton")
	}
}
