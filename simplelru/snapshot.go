// Copyright IBM Corp. 2014, 2025
// SPDX-License-Identifier: MPL-2.0

package simplelru

import (
	"errors"
	"fmt"

	"github.com/hashicorp/golang-lru/v2/internal"
)

// ErrDuplicateKey is returned by Restore when a snapshot contains the same
// key more than once. The cache is left unchanged when this error is
// returned.
var ErrDuplicateKey = errors.New("simplelru: snapshot contains duplicate key")

// Entry is a single key/value pair captured in a Snapshot.
type Entry[K comparable, V any] struct {
	Key   K
	Value V
}

// Snapshot is an ordered, self-contained copy of a cache's contents.
//
// Entries are ordered from oldest to newest, matching the cache's eviction
// order: Entries[0] is the entry that would be evicted first. A Snapshot is
// plain data owned by the caller; the library does not bind it to any file
// or serialization format, so callers may persist or transmit it however
// they choose.
//
// Values are copied by assignment only. If V is a reference type (pointer,
// map, slice, etc.) the snapshot shares the underlying data with the cache.
// While a value is reachable from both the snapshot and the cache, mutating
// it through one is visible through the other, so callers must not mutate
// shared values concurrently with cache access. Passing a Snapshot to
// Restore hands its values to the cache: afterwards the caller must treat
// them as cache-owned and must not mutate them without external
// synchronization.
type Snapshot[K comparable, V any] struct {
	// Entries holds the cache contents from oldest to newest.
	Entries []Entry[K, V]
}

// Snapshot returns the cache contents as an ordered Snapshot, from oldest
// to newest, without updating the recent-ness of any entry.
//
// The returned Snapshot is owned by the caller; see the Snapshot docs for
// the ownership rules applying to mutable values.
func (c *LRU[K, V]) Snapshot() Snapshot[K, V] {
	entries := make([]Entry[K, V], 0, c.evictList.Length())
	for ent := c.evictList.Back(); ent != nil; ent = ent.PrevEntry() {
		entries = append(entries, Entry[K, V]{Key: ent.Key, Value: ent.Value})
	}
	return Snapshot[K, V]{Entries: entries}
}

// Restore atomically replaces the entire contents of the cache with the
// given snapshot, preserving the snapshot's ordering: s.Entries is
// interpreted as oldest to newest, so after Restore the first entry is the
// next eviction candidate.
//
// The snapshot is validated before anything is modified: if any key appears
// more than once, Restore returns an error wrapping ErrDuplicateKey and the
// cache is left completely unchanged.
//
// If the snapshot holds more entries than the cache's capacity, the excess
// is discarded from the oldest end of the snapshot and only the newest
// Cap() entries are restored. Entries discarded this way never enter the
// cache and do not trigger the eviction callback.
//
// Every entry of the previous cache contents is reported to the eviction
// callback (if any), from oldest to newest, exactly as if it had been
// evicted. Restoring an empty snapshot therefore empties the cache, similar
// to Purge.
//
// After a successful Restore the cache owns the snapshot's values; see the
// Snapshot docs for the ownership rules applying to mutable values.
func (c *LRU[K, V]) Restore(s Snapshot[K, V]) error {
	entries := s.Entries

	// Validate the full snapshot before touching the cache so that a
	// failure leaves the old contents intact.
	seen := make(map[K]struct{}, len(entries))
	for _, e := range entries {
		if _, ok := seen[e.Key]; ok {
			return fmt.Errorf("%w: %v", ErrDuplicateKey, e.Key)
		}
		seen[e.Key] = struct{}{}
	}

	// Capacity convention: when the snapshot does not fit, keep the newest
	// entries and discard the oldest excess.
	if len(entries) > c.size {
		entries = entries[len(entries)-c.size:]
	}

	// Build the replacement structures fully before swapping them in.
	newList := internal.NewList[K, V]()
	newItems := make(map[K]*internal.Entry[K, V], len(entries))
	for _, e := range entries {
		newItems[e.Key] = newList.PushFront(e.Key, e.Value)
	}

	// Report the replaced entries as evictions, oldest first.
	if c.onEvict != nil {
		for ent := c.evictList.Back(); ent != nil; ent = ent.PrevEntry() {
			c.onEvict(ent.Key, ent.Value)
		}
	}

	c.evictList = newList
	c.items = newItems
	return nil
}
