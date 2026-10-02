// Copyright IBM Corp. 2014, 2025
// SPDX-License-Identifier: MPL-2.0

package lru

import (
	"github.com/hashicorp/golang-lru/v2/simplelru"
)

// Snapshot returns the cache contents as an ordered snapshot, from oldest
// to newest, without updating the recent-ness of any entry.
//
// The snapshot is taken under the cache lock, so it reflects a single
// consistent point in time even while other goroutines read and write the
// cache. The returned snapshot is plain data owned by the caller; the
// library does not bind it to any file or serialization format. See
// simplelru.Snapshot for the ownership rules applying to mutable values.
func (c *Cache[K, V]) Snapshot() simplelru.Snapshot[K, V] {
	c.lock.RLock()
	s := c.lru.Snapshot()
	c.lock.RUnlock()
	return s
}

// Restore atomically replaces the entire contents of the cache with the
// given snapshot, preserving its oldest-to-newest ordering. It is intended
// for rebuilding a cache from a previously taken Snapshot without the
// order-distorting side effects of re-inserting entries one by one.
//
// The snapshot is validated before anything is modified: if any key appears
// more than once, Restore returns an error wrapping
// simplelru.ErrDuplicateKey and the cache is left completely unchanged.
//
// If the snapshot holds more entries than the cache's capacity, the excess
// is discarded from the oldest end of the snapshot and only the newest
// Cap() entries are restored; discarded entries do not trigger the eviction
// callback.
//
// Every entry of the previous cache contents is reported to the eviction
// callback (if any), from oldest to newest, outside the cache lock, exactly
// as if it had been evicted. After a successful Restore the cache owns the
// snapshot's values; see simplelru.Snapshot for the ownership rules
// applying to mutable values.
func (c *Cache[K, V]) Restore(s simplelru.Snapshot[K, V]) error {
	var ks []K
	var vs []V
	c.lock.Lock()
	err := c.lru.Restore(s)
	if err == nil && c.onEvictedCB != nil && len(c.evictedKeys) > 0 {
		ks, vs = c.evictedKeys, c.evictedVals
		c.initEvictBuffers()
	}
	c.lock.Unlock()
	// invoke callbacks outside of critical section
	if c.onEvictedCB != nil {
		for i := 0; i < len(ks); i++ {
			c.onEvictedCB(ks[i], vs[i])
		}
	}
	return err
}
