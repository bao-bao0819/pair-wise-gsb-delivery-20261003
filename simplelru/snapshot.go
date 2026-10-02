// Copyright IBM Corp. 2014, 2026
// SPDX-License-Identifier: MPL-2.0

package simplelru

import (
	"errors"
	"fmt"

	"github.com/hashicorp/golang-lru/v2/internal"
)

// Entry is a key/value pair as held by an LRU. Snapshot returns a slice of
// Entry values ordered from oldest to newest; Restore consumes the same
// representation to rebuild a cache.
type Entry[K comparable, V any] struct {
	Key   K
	Value V
}

// ErrDuplicateKey is returned by Restore when the supplied entries contain the
// same key more than once. The key is wrapped, so errors.Is reports
// ErrDuplicateKey for any duplicate.
var ErrDuplicateKey = errors.New("snapshot contains duplicate key")

// Snapshot returns the cache contents as an ordered list of entries, from
// oldest to newest.
//
// Snapshot does not modify the cache in any way: the "recently used"-ness of
// every entry is left untouched, unlike reading the values one by one with
// Get. The returned slice is allocated by the caller-facing method and is
// independent from the cache's internal list; the cache keeps no reference to
// it.
//
// Values are copied as-is (a shallow copy). If V is a pointer, slice, map, or
// other reference type, the returned Entry shares the underlying value with
// the cache. Mutating such a value through the snapshot mutates the value
// visible to the cache as well; deep copying is the caller's responsibility if
// isolation is required.
func (c *LRU[K, V]) Snapshot() []Entry[K, V] {
	entries := make([]Entry[K, V], 0, c.Len())
	for ent := c.evictList.Back(); ent != nil; ent = ent.PrevEntry() {
		entries = append(entries, Entry[K, V]{Key: ent.Key, Value: ent.Value})
	}
	return entries
}

// Restore atomically replaces the cache contents with the supplied entries.
//
// entries must be ordered from oldest to newest; the first element becomes the
// oldest entry and the last element the most recently used one. Passing an
// empty slice (or nil) clears the cache.
//
// Restore validates the entries before touching the cache. If the same key
// occurs more than once, ErrDuplicateKey (wrapping the offending key) is
// returned and the cache is left completely unchanged. The validation runs
// before any entry is removed, so on failure the old cache, including its
// ordering and contents, stays intact.
//
// If entries contains more items than the cache capacity, the oldest surplus
// entries are dropped and the newest Cap entries are kept, mirroring the
// capacity limit of Add. Dropped entries are never added to the cache and do
// not trigger the eviction callback.
//
// Every entry removed from the replaced (old) cache is reported through the
// eviction callback, in the same way as Purge. As with Purge, this LRU is not
// safe for concurrent use and the callback is invoked inline; the thread-safe
// wrappers invoke it outside of their locks.
//
// The values in entries are shallow-copied into the cache. When a value is a
// reference type, the cache and the caller's slice alias the same underlying
// value. The cache never modifies the entries slice itself, but callers must
// not rely on exclusive ownership of mutable values: either pass deeply owned
// values or do not mutate them after Restore.
func (c *LRU[K, V]) Restore(entries []Entry[K, V]) error {
	if err := validateEntries(entries); err != nil {
		return err
	}

	// Discard the oldest surplus entries before building the replacement so
	// that dropped entries are never added and never reach onEvict.
	if overflow := len(entries) - c.size; overflow > 0 {
		entries = entries[overflow:]
	}

	// Rebuild from scratch into a fresh structure. The existing cache is only
	// mutated after every entry has been successfully inserted, so a panic
	// during construction cannot leave the cache partially restored.
	evictList := internal.NewList[K, V]()
	items := make(map[K]*internal.Entry[K, V], len(entries))
	for i := range entries {
		ent := evictList.PushFront(entries[i].Key, entries[i].Value)
		items[entries[i].Key] = ent
	}

	// Remove the old contents, honoring the eviction callback.
	for k, v := range c.items {
		if c.onEvict != nil {
			c.onEvict(k, v.Value)
		}
		delete(c.items, k)
	}

	c.evictList = evictList
	c.items = items
	return nil
}

// validateEntries checks structural invariants of a snapshot before it is
// applied. Keys must be unique, since an LRU maps every key to at most one
// position in the recency order.
func validateEntries[K comparable, V any](entries []Entry[K, V]) error {
	seen := make(map[K]struct{}, len(entries))
	for i := range entries {
		key := entries[i].Key
		if _, exists := seen[key]; exists {
			return fmt.Errorf("%w: %v", ErrDuplicateKey, key)
		}
		seen[key] = struct{}{}
	}
	return nil
}
