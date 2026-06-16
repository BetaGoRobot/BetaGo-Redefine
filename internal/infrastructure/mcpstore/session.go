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
	cache *ttlcache.Cache[string, luckin.ShopSelection]
}

func NewSessionStore() *SessionStore {
	c := ttlcache.New(
		ttlcache.WithTTL[string, luckin.ShopSelection](sessionTTL),
		ttlcache.WithCapacity[string, luckin.ShopSelection](2000),
	)
	go c.Start()
	return &SessionStore{cache: c}
}

func (s *SessionStore) GetShop(_ context.Context, key luckin.SessionKey) (luckin.ShopSelection, bool) {
	item := s.cache.Get(key.String())
	if item == nil {
		return luckin.ShopSelection{}, false
	}
	return item.Value(), true
}

func (s *SessionStore) SetShop(_ context.Context, key luckin.SessionKey, shop luckin.ShopSelection) {
	s.cache.Set(key.String(), shop, ttlcache.DefaultTTL)
}

func (s *SessionStore) ClearShop(_ context.Context, key luckin.SessionKey) {
	s.cache.Delete(key.String())
}
