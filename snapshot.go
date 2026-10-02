// Copyright IBM Corp. 2014, 2026
// SPDX-License-Identifier: MPL-2.0

package lru

import "github.com/hashicorp/golang-lru/v2/simplelru"

// Entry is a key/value pair as held by a Cache. Snapshot returns a slice of
// Entry values ordered from oldest to newest; Restore consumes the same
// representation to rebuild a cache.
type Entry[K comparable, V any] struct {
	Key   K
	Value V
}

// ErrDuplicateKey is returned by Restore when the supplied entries contain the
// same key more than once. Use errors.Is to detect it.
var ErrDuplicateKey = simplelru.ErrDuplicateKey

// Snapshot returns the cache contents as an ordered slice of entries, from
// oldest to newest.
//
// It is a non-mutating read: unlike fetching each value with Get, Snapshot
// never changes the "recently used"-ness of any entry. The whole snapshot is
// taken while holding the cache's read lock, so under concurrent access the
// returned entries reflect one consistent point in time.
//
// Values are shallow-copied. If V is a pointer, slice, map, or other reference
// type, the returned Entry and the cache alias the same underlying value;
// mutating it through the snapshot is visible to the cache. Deep copy yourself
// when you need isolation.
func (c *Cache[K, V]) Snapshot() []Entry[K, V] {
	c.lock.RLock()
	snapshot := c.lru.Snapshot()
	c.lock.RUnlock()

	entries := make([]Entry[K, V], len(snapshot))
	for i := range snapshot {
		entries[i] = Entry[K, V]{Key: snapshot[i].Key, Value: snapshot[i].Value}
	}
	return entries
}

// Restore atomically replaces the cache contents with the supplied entries.
//
// entries must be ordered from oldest to newest: the first element becomes the
// oldest entry and the last element the most recently used one. Passing an
// empty slice (or nil) clears the cache.
//
// All validation happens before the existing cache is modified. If the same
// key occurs more than once, Restore returns an error wrapping ErrDuplicateKey
// and leaves the old cache, including its ordering and contents, completely
// intact.
//
// When entries holds more items than Cap, the oldest surplus entries are
// dropped and only the newest Cap entries are kept, matching the capacity
// behavior of Add. Dropped entries are never inserted and never trigger the
// eviction callback.
//
// Every entry removed from the replaced cache is reported to the cache's
// onEvict callback, as with Purge. The callbacks are invoked after Restore has
// released its lock, so they may safely re-enter the cache.
//
// Values are shallow-copied into the cache. For reference-typed values the
// cache and the caller's slice alias the same underlying value; the cache does
// not mutate the slice, but callers should pass values they are done mutating.
func (c *Cache[K, V]) Restore(entries []Entry[K, V]) error {
	snapshot := make([]simplelru.Entry[K, V], len(entries))
	for i := range entries {
		snapshot[i] = simplelru.Entry[K, V]{Key: entries[i].Key, Value: entries[i].Value}
	}

	var ks []K
	var vs []V
	c.lock.Lock()
	err := c.lru.Restore(snapshot)
	if err == nil && c.onEvictedCB != nil && len(c.evictedKeys) > 0 {
		ks, vs = c.evictedKeys, c.evictedVals
		c.initEvictBuffers()
	}
	c.lock.Unlock()

	if err != nil {
		return err
	}
	if c.onEvictedCB != nil {
		for i := 0; i < len(ks); i++ {
			c.onEvictedCB(ks[i], vs[i])
		}
	}
	return nil
}
