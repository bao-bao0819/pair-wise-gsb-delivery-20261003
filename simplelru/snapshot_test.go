// Copyright IBM Corp. 2014, 2026
// SPDX-License-Identifier: MPL-2.0

package simplelru

import (
	"errors"
	"reflect"
	"testing"
)

func TestLRU_SnapshotOrderAndNoTouch(t *testing.T) {
	l, err := NewLRU[int, int](5, nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	for i := 1; i <= 5; i++ {
		l.Add(i, i*10)
	}

	entries := l.Snapshot()
	expected := []Entry[int, int]{
		{1, 10}, {2, 20}, {3, 30}, {4, 40}, {5, 50},
	}
	if !reflect.DeepEqual(entries, expected) {
		t.Fatalf("snapshot order mismatch: got %v want %v", entries, expected)
	}

	// Snapshot must not change recent-ness: adding 6 evicts key 1 (oldest),
	// whereas touching key 1 via Get would have evicted key 2 instead.
	l.Add(6, 60)
	if _, ok := l.Get(1); ok {
		t.Fatalf("key 1 should have remained the oldest and been evicted")
	}
	if _, ok := l.Peek(2); !ok {
		t.Fatalf("key 2 should still be present after non-touching snapshot")
	}

	// Taking another snapshot still reports oldest to newest.
	got := l.Snapshot()
	want := []Entry[int, int]{
		{2, 20}, {3, 30}, {4, 40}, {5, 50}, {6, 60},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("second snapshot mismatch: got %v want %v", got, want)
	}
}

func TestLRU_SnapshotEmpty(t *testing.T) {
	l, _ := NewLRU[int, int](4, nil)
	entries := l.Snapshot()
	if len(entries) != 0 {
		t.Fatalf("expected empty snapshot, got %v", entries)
	}
}

func TestLRU_SnapshotIndependentSlice(t *testing.T) {
	l, _ := NewLRU[int, int](4, nil)
	l.Add(1, 10)

	entries := l.Snapshot()
	entries[0] = Entry[int, int]{999, 999}

	if v, ok := l.Peek(1); !ok || v != 10 {
		t.Fatalf("mutating snapshot changed the cache: got %v ok %v", v, ok)
	}
}

func TestLRU_RestoreOrder(t *testing.T) {
	l, _ := NewLRU[int, int](5, nil)
	entries := []Entry[int, int]{
		{1, 10}, {2, 20}, {3, 30}, {4, 40}, {5, 50},
	}
	if err := l.Restore(entries); err != nil {
		t.Fatalf("err: %v", err)
	}

	if !reflect.DeepEqual(l.Keys(), []int{1, 2, 3, 4, 5}) {
		t.Fatalf("restored keys wrong: %v", l.Keys())
	}
	if k, v, ok := l.GetOldest(); !ok || k != 1 || v != 10 {
		t.Fatalf("oldest entry wrong: %v %v %v", k, v, ok)
	}

	// Most recent is key 5, so adding one evicts the oldest key 1.
	l.Add(6, 60)
	if _, ok := l.Peek(1); ok {
		t.Fatalf("key 1 should be evicted as the restored oldest")
	}
	if _, ok := l.Peek(5); !ok {
		t.Fatalf("key 5 should be the most recently used restored entry")
	}
}

func TestLRU_RestoreDuplicateKeyLeavesCacheIntact(t *testing.T) {
	l, _ := NewLRU[int, int](128, nil)
	for i := 1; i <= 3; i++ {
		l.Add(i, i)
	}

	err := l.Restore([]Entry[int, int]{
		{10, 100}, {11, 110}, {10, 1000},
	})
	if !errors.Is(err, ErrDuplicateKey) {
		t.Fatalf("expected ErrDuplicateKey, got %v", err)
	}

	if l.Len() != 3 {
		t.Fatalf("cache length changed on failure: got %d", l.Len())
	}
	want := []int{1, 2, 3}
	if !reflect.DeepEqual(l.Keys(), want) {
		t.Fatalf("cache order changed on failure: got %v", l.Keys())
	}
	if v, ok := l.Peek(2); !ok || v != 2 {
		t.Fatalf("cache contents changed on failure")
	}
}

func TestLRU_RestoreTrimsOldest(t *testing.T) {
	evicted := []Entry[int, int]{}
	l, _ := NewLRU[int, int](3, func(k, v int) {
		evicted = append(evicted, Entry[int, int]{k, v})
	})

	err := l.Restore([]Entry[int, int]{
		{1, 10}, {2, 20}, {3, 30}, {4, 40}, {5, 50},
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	if l.Len() != 3 {
		t.Fatalf("expected len 3, got %d", l.Len())
	}
	if !reflect.DeepEqual(l.Keys(), []int{3, 4, 5}) {
		t.Fatalf("oldest entries should be dropped: got %v", l.Keys())
	}
	if k, _, ok := l.GetOldest(); !ok || k != 3 {
		t.Fatalf("oldest kept entry should be key 3")
	}
	// Dropped snapshot entries never made it into the cache and are not evictions.
	if len(evicted) != 0 {
		t.Fatalf("dropped entries must not fire onEvict, got %v", evicted)
	}
}

func TestLRU_RestoreReplacementFiresEvict(t *testing.T) {
	evicted := map[int]int{}
	l, _ := NewLRU[int, int](128, func(k, v int) {
		evicted[k] = v
	})
	for i := 1; i <= 3; i++ {
		l.Add(i, i*10)
	}

	if err := l.Restore([]Entry[int, int]{{7, 70}, {8, 80}}); err != nil {
		t.Fatalf("err: %v", err)
	}

	if !reflect.DeepEqual(evicted, map[int]int{1: 10, 2: 20, 3: 30}) {
		t.Fatalf("old entries should be evicted: %v", evicted)
	}
	if !reflect.DeepEqual(l.Keys(), []int{7, 8}) {
		t.Fatalf("cache should hold restored entries: %v", l.Keys())
	}

	// A failed restore must not evict anything.
	delete(evicted, 1)
	delete(evicted, 2)
	delete(evicted, 3)
	err := l.Restore([]Entry[int, int]{{9, 90}, {9, 91}})
	if !errors.Is(err, ErrDuplicateKey) {
		t.Fatalf("expected ErrDuplicateKey, got %v", err)
	}
	if len(evicted) != 0 {
		t.Fatalf("failed restore must not fire onEvict, got %v", evicted)
	}
	if !reflect.DeepEqual(l.Keys(), []int{7, 8}) {
		t.Fatalf("failed restore mutated cache: %v", l.Keys())
	}
}

func TestLRU_RestoreEmptyClears(t *testing.T) {
	evicted := 0
	l, _ := NewLRU[int, int](4, func(k, v int) { evicted++ })
	l.Add(1, 1)
	l.Add(2, 2)

	if err := l.Restore(nil); err != nil {
		t.Fatalf("err: %v", err)
	}
	if l.Len() != 0 {
		t.Fatalf("expected empty cache, len %d", l.Len())
	}
	if evicted != 2 {
		t.Fatalf("expected 2 eviction callbacks, got %d", evicted)
	}
}

func TestLRU_RestoreTrimAndEvictOldCache(t *testing.T) {
	evicted := map[int]bool{}
	l, _ := NewLRU[int, int](2, func(k, v int) { evicted[k] = true })
	l.Add(100, 0)
	l.Add(101, 0)

	err := l.Restore([]Entry[int, int]{
		{1, 1}, {2, 2}, {3, 3}, {4, 4},
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	// Old cache entries are evicted; snapshot keys 1 and 2 are dropped as the
	// oldest surplus and never appear in the callback; keys 3 and 4 are kept.
	if !reflect.DeepEqual(evicted, map[int]bool{100: true, 101: true}) {
		t.Fatalf("unexpected evictions: %v", evicted)
	}
	if !reflect.DeepEqual(l.Keys(), []int{3, 4}) {
		t.Fatalf("unexpected keys after trim+replace: %v", l.Keys())
	}
	if k, _, ok := l.GetOldest(); !ok || k != 3 {
		t.Fatalf("oldest surviving entry should be 3")
	}
}

func TestLRU_SnapshotRestoreRoundTrip(t *testing.T) {
	l, _ := NewLRU[int, int](128, nil)
	l.Add(1, 1)
	l.Add(2, 2)
	l.Get(1) // make order: 2 (oldest), 1 (newest)

	snapshot := l.Snapshot()

	other, _ := NewLRU[int, int](128, nil)
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
