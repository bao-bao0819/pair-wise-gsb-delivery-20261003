// Copyright IBM Corp. 2014, 2025
// SPDX-License-Identifier: MPL-2.0

package simplelru

import (
	"errors"
	"reflect"
	"testing"
)

func TestLRUSnapshot_OrderAndNoRecencyChange(t *testing.T) {
	l, err := NewLRU[int, int](3, nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	for i := 1; i <= 3; i++ {
		l.Add(i, i*10)
	}
	// Make 1 the most recently used.
	if _, ok := l.Get(1); !ok {
		t.Fatal("expected key 1")
	}

	s := l.Snapshot()
	wantKeys := []int{2, 3, 1}
	wantVals := []int{20, 30, 10}
	if len(s.Entries) != len(wantKeys) {
		t.Fatalf("bad snapshot len: %d", len(s.Entries))
	}
	for i, e := range s.Entries {
		if e.Key != wantKeys[i] || e.Value != wantVals[i] {
			t.Fatalf("bad entry %d: (%v, %v)", i, e.Key, e.Value)
		}
	}

	// Taking a snapshot must not disturb recency: key 2 is still oldest.
	if evicted := l.Add(4, 40); !evicted {
		t.Fatal("expected an eviction")
	}
	if l.Contains(2) {
		t.Fatal("snapshot updated recency; key 2 should have been evicted")
	}
}

func TestLRURestore_RoundTrip(t *testing.T) {
	src, err := NewLRU[int, int](4, nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	for i := 1; i <= 4; i++ {
		src.Add(i, i*10)
	}
	// Reorder so the snapshot order is not insertion order.
	src.Get(2)
	s := src.Snapshot()

	dst, err := NewLRU[int, int](4, nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	dst.Add(99, 99)
	if err := dst.Restore(s); err != nil {
		t.Fatalf("err: %v", err)
	}

	if !reflect.DeepEqual(dst.Keys(), src.Keys()) {
		t.Fatalf("keys mismatch: %v vs %v", dst.Keys(), src.Keys())
	}
	if !reflect.DeepEqual(dst.Values(), src.Values()) {
		t.Fatalf("values mismatch: %v vs %v", dst.Values(), src.Values())
	}
	// Recency must be preserved: oldest snapshot entry evicts first.
	dst.Add(100, 100)
	if dst.Contains(1) {
		t.Fatal("oldest snapshot entry should have been evicted first")
	}
}

func TestLRURestore_DuplicateKeyKeepsOldCache(t *testing.T) {
	var evicted []int
	l, err := NewLRU[int, int](3, func(k, v int) { evicted = append(evicted, k) })
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	l.Add(1, 10)
	l.Add(2, 20)
	before := l.Keys()

	s := Snapshot[int, int]{Entries: []Entry[int, int]{
		{Key: 5, Value: 50},
		{Key: 6, Value: 60},
		{Key: 5, Value: 55},
	}}
	err = l.Restore(s)
	if !errors.Is(err, ErrDuplicateKey) {
		t.Fatalf("expected ErrDuplicateKey, got: %v", err)
	}
	if !reflect.DeepEqual(l.Keys(), before) {
		t.Fatalf("cache modified on failed restore: %v", l.Keys())
	}
	if len(evicted) != 0 {
		t.Fatalf("evict callback fired on failed restore: %v", evicted)
	}
}

func TestLRURestore_CapacityDropsOldest(t *testing.T) {
	var evicted []int
	l, err := NewLRU[int, int](2, func(k, v int) { evicted = append(evicted, k) })
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	s := Snapshot[int, int]{Entries: []Entry[int, int]{
		{Key: 1, Value: 10},
		{Key: 2, Value: 20},
		{Key: 3, Value: 30},
		{Key: 4, Value: 40},
	}}
	if err := l.Restore(s); err != nil {
		t.Fatalf("err: %v", err)
	}
	if want := []int{3, 4}; !reflect.DeepEqual(l.Keys(), want) {
		t.Fatalf("expected newest entries kept, got: %v", l.Keys())
	}
	// Dropped snapshot entries never entered the cache: no callbacks.
	if len(evicted) != 0 {
		t.Fatalf("unexpected evict callbacks: %v", evicted)
	}
}

func TestLRURestore_EvictsOldEntries(t *testing.T) {
	var evicted []int
	l, err := NewLRU[int, int](4, func(k, v int) { evicted = append(evicted, k) })
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	l.Add(1, 10)
	l.Add(2, 20)
	l.Add(3, 30)

	s := Snapshot[int, int]{Entries: []Entry[int, int]{
		{Key: 7, Value: 70},
		{Key: 8, Value: 80},
	}}
	if err := l.Restore(s); err != nil {
		t.Fatalf("err: %v", err)
	}
	if want := []int{1, 2, 3}; !reflect.DeepEqual(evicted, want) {
		t.Fatalf("old entries not evicted oldest-first: %v", evicted)
	}
	if want := []int{7, 8}; !reflect.DeepEqual(l.Keys(), want) {
		t.Fatalf("bad contents after restore: %v", l.Keys())
	}
}

func TestLRURestore_EmptySnapshotPurges(t *testing.T) {
	var evicted []int
	l, err := NewLRU[int, int](2, func(k, v int) { evicted = append(evicted, k) })
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	l.Add(1, 10)
	l.Add(2, 20)

	if err := l.Restore(Snapshot[int, int]{}); err != nil {
		t.Fatalf("err: %v", err)
	}
	if l.Len() != 0 {
		t.Fatalf("expected empty cache, got len %d", l.Len())
	}
	if want := []int{1, 2}; !reflect.DeepEqual(evicted, want) {
		t.Fatalf("expected all old entries evicted, got: %v", evicted)
	}
}
