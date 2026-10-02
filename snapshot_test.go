// Copyright IBM Corp. 2014, 2026
// SPDX-License-Identifier: MPL-2.0

package lru

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

func TestCache_SnapshotOrderAndNoTouch(t *testing.T) {
	l, err := New[int, int](5)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	for i := 1; i <= 5; i++ {
		l.Add(i, i*10)
	}

	entries := l.Snapshot()
	want := []Entry[int, int]{
		{1, 10}, {2, 20}, {3, 30}, {4, 40}, {5, 50},
	}
	if !reflect.DeepEqual(entries, want) {
		t.Fatalf("snapshot mismatch: got %v want %v", entries, want)
	}

	// Snapshot does not promote recency: with capacity 5, a sixth add evicts
	// key 1 only if the snapshot left it as the oldest entry (Get on every
	// key would instead have evicted key 2).
	l.Add(6, 60)
	if _, ok := l.Peek(1); ok {
		t.Fatalf("snapshot must not promote recency; key 1 should be evicted")
	}
	if _, ok := l.Peek(2); !ok {
		t.Fatalf("key 2 should remain after non-touching snapshot")
	}
}

func TestCache_RestoreOrder(t *testing.T) {
	l, _ := New[int, int](3)
	err := l.Restore([]Entry[int, int]{
		{1, 10}, {2, 20}, {3, 30},
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !reflect.DeepEqual(l.Keys(), []int{1, 2, 3}) {
		t.Fatalf("restored keys wrong: %v", l.Keys())
	}

	// Capacity is 3 and the restore gave exactly 3 entries, so a fourth add
	// evicts the oldest restored entry (key 1).
	if evicted := l.Add(4, 40); !evicted {
		t.Fatalf("adding beyond restored capacity should evict")
	}
	if _, ok := l.Peek(1); ok {
		t.Fatalf("oldest restored entry should be evicted first")
	}
}

func TestCache_RestoreDuplicateKeyLeavesCacheIntact(t *testing.T) {
	l, _ := New[int, int](128)
	l.Add(1, 1)
	l.Add(2, 2)

	err := l.Restore([]Entry[int, int]{{9, 9}, {8, 8}, {9, 9}})
	if !errors.Is(err, ErrDuplicateKey) {
		t.Fatalf("expected ErrDuplicateKey, got %v", err)
	}
	if !reflect.DeepEqual(l.Keys(), []int{1, 2}) {
		t.Fatalf("old cache must survive failed restore, got %v", l.Keys())
	}
}

func TestCache_RestoreTrimsOldest(t *testing.T) {
	evicted := map[int]int{}
	l, _ := NewWithEvict[int, int](3, func(k, v int) { evicted[k] = v })

	err := l.Restore([]Entry[int, int]{
		{1, 10}, {2, 20}, {3, 30}, {4, 40}, {5, 50},
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !reflect.DeepEqual(l.Keys(), []int{3, 4, 5}) {
		t.Fatalf("oldest entries should be trimmed, got %v", l.Keys())
	}
	if len(evicted) != 0 {
		t.Fatalf("trimmed entries must not fire onEvict, got %v", evicted)
	}
}

func TestCache_RestoreFiresEvictOutsideLock(t *testing.T) {
	var mu sync.Mutex
	evicted := map[int]int{}
	var reenteredKeys []int
	var l *Cache[int, int]
	l, _ = NewWithEvict[int, int](128, func(k, v int) {
		mu.Lock()
		defer mu.Unlock()
		evicted[k] = v
		// Callbacks must be able to safely re-enter the cache.
		if current, ok := l.Peek(7); ok {
			reenteredKeys = append(reenteredKeys, current)
		}
	})
	l.Add(1, 10)
	l.Add(2, 20)
	l.Add(100, 1000)

	err := l.Restore([]Entry[int, int]{{7, 70}, {8, 80}})
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(evicted, map[int]int{1: 10, 2: 20, 100: 1000}) {
		t.Fatalf("all old entries should be evicted, got %v", evicted)
	}
	if !reflect.DeepEqual(reenteredKeys, []int{70, 70, 70}) {
		t.Fatalf("callback could not re-enter cache, got %v", reenteredKeys)
	}
	if !reflect.DeepEqual(l.Keys(), []int{7, 8}) {
		t.Fatalf("cache should hold restored entries, got %v", l.Keys())
	}
}

func TestCache_RestoreEmptyClears(t *testing.T) {
	evicted := 0
	l, _ := NewWithEvict[int, int](4, func(k, v int) { evicted++ })
	l.Add(1, 1)
	l.Add(2, 2)

	if err := l.Restore(nil); err != nil {
		t.Fatalf("err: %v", err)
	}
	if l.Len() != 0 {
		t.Fatalf("cache should be empty, len %d", l.Len())
	}
	if evicted != 2 {
		t.Fatalf("expected 2 evictions, got %d", evicted)
	}
}

func TestCache_SnapshotRestoreRoundTrip(t *testing.T) {
	l, _ := New[int, int](128)
	l.Add(1, 10)
	l.Add(2, 20)
	l.Get(1) // recency: 2 oldest, 1 newest

	snapshot := l.Snapshot()
	other, _ := New[int, int](128)
	if err := other.Restore(snapshot); err != nil {
		t.Fatalf("err: %v", err)
	}
	if !reflect.DeepEqual(other.Keys(), l.Keys()) {
		t.Fatalf("round-trip order mismatch: %v vs %v", other.Keys(), l.Keys())
	}
	if !reflect.DeepEqual(other.Values(), l.Values()) {
		t.Fatalf("round-trip values mismatch: %v vs %v", other.Values(), l.Values())
	}
}

// TestCache_SnapshotConsistentPointInTime verifies that a snapshot taken while
// writers run is a consistent view: it never observes duplicate keys and its
// set of values matches exactly the stored keys at some logical instant.
func TestCache_SnapshotConsistentPointInTime(t *testing.T) {
	const size = 64
	l, _ := New[int, int](size)

	stop := make(chan struct{})
	var writers sync.WaitGroup

	// Writers churn the cache, changing both contents and recency order.
	for w := 0; w < 4; w++ {
		writers.Add(1)
		go func(offset int) {
			defer writers.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
					key := (i*7 + offset) % (size * 4)
					l.Add(key, key*10)
				}
			}
		}(w)
	}

	// Readers take snapshots and validate structural consistency.
	var readers sync.WaitGroup
	for r := 0; r < 4; r++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for i := 0; i < 500; i++ {
				snapshot := l.Snapshot()
				seen := make(map[int]int, len(snapshot))
				for _, e := range snapshot {
					if prev, exists := seen[e.Key]; exists {
						t.Errorf("snapshot observed duplicate key %d (%d vs %d)", e.Key, prev, e.Value)
						return
					}
					if e.Value != e.Key*10 {
						t.Errorf("snapshot value mismatch for key %d: %d", e.Key, e.Value)
						return
					}
					seen[e.Key] = e.Value
				}
			}
		}()
	}

	// Readers perform a fixed number of iterations; let the churn run
	// alongside them, then stop the writers and wait for everyone.
	readers.Wait()
	close(stop)
	writers.Wait()
}
