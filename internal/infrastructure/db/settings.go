package db

import (
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/query"
	"gorm.io/gorm"
)

const bypassCacheSettingKey = "betago:bypass_query_cache"

func WithoutQueryCache(tx *gorm.DB) *gorm.DB {
	if tx == nil {
		return nil
	}
	return tx.Set(bypassCacheSettingKey, true)
}

func DBWithoutQueryCache() *gorm.DB {
	return WithoutQueryCache(DB())
}

func QueryWithoutCache() *query.Query {
	tx := DBWithoutQueryCache()
	if tx == nil {
		return nil
	}
	return query.Use(tx)
}

func IsQueryCacheBypassed(tx *gorm.DB) bool {
	if tx == nil {
		return false
	}
	value, ok := tx.Get(bypassCacheSettingKey)
	if !ok {
		return false
	}
	bypassed, _ := value.(bool)
	return bypassed
}
