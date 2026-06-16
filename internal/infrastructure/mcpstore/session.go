package mcpstore

import (
	"context"
	"sync"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/luckin"
	"github.com/jellydator/ttlcache/v3"
)

const sessionTTL = 30 * time.Minute

var (
	defaultSessionStore *SessionStore
	defaultSessionOnce  sync.Once
)

// DefaultSessionStore 返回进程级共享的门店会话缓存，供工具层与卡片回调共用。
func DefaultSessionStore() *SessionStore {
	defaultSessionOnce.Do(func() {
		defaultSessionStore = NewSessionStore()
	})
	return defaultSessionStore
}

type SessionStore struct {
	shops *ttlcache.Cache[string, luckin.ShopSelection]
	carts *ttlcache.Cache[string, luckin.Cart]
}

func NewSessionStore() *SessionStore {
	shops := ttlcache.New(
		ttlcache.WithTTL[string, luckin.ShopSelection](sessionTTL),
		ttlcache.WithCapacity[string, luckin.ShopSelection](2000),
	)
	carts := ttlcache.New(
		ttlcache.WithTTL[string, luckin.Cart](sessionTTL),
		ttlcache.WithCapacity[string, luckin.Cart](2000),
	)
	go shops.Start()
	go carts.Start()
	return &SessionStore{shops: shops, carts: carts}
}

func (s *SessionStore) GetShop(_ context.Context, key luckin.SessionKey) (luckin.ShopSelection, bool) {
	item := s.shops.Get(key.String())
	if item == nil {
		return luckin.ShopSelection{}, false
	}
	return item.Value(), true
}

func (s *SessionStore) SetShop(_ context.Context, key luckin.SessionKey, shop luckin.ShopSelection) {
	s.shops.Set(key.String(), shop, ttlcache.DefaultTTL)
}

func (s *SessionStore) ClearShop(_ context.Context, key luckin.SessionKey) {
	s.shops.Delete(key.String())
}

func (s *SessionStore) GetCart(_ context.Context, key luckin.SessionKey) (luckin.Cart, bool) {
	item := s.carts.Get(key.String())
	if item == nil {
		return luckin.Cart{}, false
	}
	return item.Value(), true
}

func (s *SessionStore) SetCart(_ context.Context, key luckin.SessionKey, cart luckin.Cart) {
	s.carts.Set(key.String(), cart, ttlcache.DefaultTTL)
}

func (s *SessionStore) ClearCart(_ context.Context, key luckin.SessionKey) {
	s.carts.Delete(key.String())
}
