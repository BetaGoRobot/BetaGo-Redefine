package db

import (
	"time"

	"github.com/jellydator/ttlcache/v3"
)

var cache = ttlcache.New(
	ttlcache.WithTTL[string, any](60*time.Second), // 默认 TTL
	ttlcache.WithCapacity[string, any](1000),      // 最大容量
)
