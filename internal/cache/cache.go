// Package cache provides a tiny TTL cache abstraction. The interface keeps the
// backing library behind two methods so it can be swapped (e.g. for a shared
// cross-replica backend) without touching call sites.
package cache

import (
	"time"

	"github.com/jellydator/ttlcache/v3"
)

// Cache is the minimal surface prove needs.
type Cache[K comparable, V any] interface {
	Get(key K) (V, bool)
	Set(key K, value V)
}

// ttlAdapter wraps jellydator/ttlcache with a fixed (non-sliding) TTL and a hard
// capacity bound, so per-repo/team growth stays capped.
type ttlAdapter[K comparable, V any] struct {
	c *ttlcache.Cache[K, V]
}

// NewTTL returns a Cache where every entry expires after ttl, evicting the
// least-recently-used entry once size is exceeded.
func NewTTL[K comparable, V any](ttl time.Duration, size int) Cache[K, V] {
	c := ttlcache.New[K, V](
		ttlcache.WithTTL[K, V](ttl),
		ttlcache.WithCapacity[K, V](uint64(size)),
		ttlcache.WithDisableTouchOnHit[K, V](), // fixed TTL, not sliding
	)
	return &ttlAdapter[K, V]{c: c}
}

func (a *ttlAdapter[K, V]) Get(key K) (V, bool) {
	if item := a.c.Get(key); item != nil {
		return item.Value(), true
	}
	var zero V
	return zero, false
}

func (a *ttlAdapter[K, V]) Set(key K, value V) {
	a.c.Set(key, value, ttlcache.DefaultTTL)
}
