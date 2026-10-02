// Copyright IBM Corp. 2014, 2025
// SPDX-License-Identifier: MPL-2.0

package lru

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/hashicorp/golang-lru/v2/simplelru"
)

func TestCacheSnapshotRestore_RoundTrip(t *testing.T) {
	src, err := New[int, int](4)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	for i := 1; i <= 4; i++ {
		src.Add(i, i*10)
	}
	// Reorder so snapshot order differs from insertion order.
	src.Get(2)

	s := src.Snapshot()
	// Snapshot must not change recency.
	if !reflect.DeepEqual(src.Keys(), []int{1, 3, 4, 2}) {
		t.Fatalf("snapshot changed recency: %v", src.Keys())
	}

	dst, err := New[int, int](4)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if err := dst.Restore(s); err != nil {
		t.Fatalf("err: %v", err)
	}
	if !reflect.DeepEqual(dst.Keys(), src.Keys()) {
		t.Fatalf("keys mismatch: %v vs %v", dst.Keys(), src.Keys())
	}
	if !reflect.DeepEqual(dst.Values(), src.Values()) {
		t.Fatalf("values mismatch: %v vs %v", dst.Values(), src.Values())
	}
}

func TestCacheRestore_EvictCallbackOnReplace(t *testing.T) {
	var mu sync.Mutex
	var evicted []int
	c, err := NewWithEvict[int, int](3, func(k, v int) {
		mu.Lock()
		evicted = append(evicted, k)
		mu.Unlock()
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	c.Add(1, 10)
	c.Add(2, 20)

	s := simplelru.Snapshot[int, int]{Entries: []simplelru.Entry[int, int]{
		{Key: 8, Value: 80},
		{Key: 9, Value: 90},
	}}
	if err := c.Restore(s); err != nil {
		t.Fatalf("err: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if want := []int{1, 2}; !reflect.DeepEqual(evicted, want) {
		t.Fatalf("old entries not reported as evicted: %v", evicted)
	}
	if want := []int{8, 9}; !reflect.DeepEqual(c.Keys(), want) {
		t.Fatalf("bad contents after restore: %v", c.Keys())
	}
}

func TestCacheRestore_DuplicateKeyKeepsOldCache(t *testing.T) {
	c, err := New[int, int](3)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	c.Add(1, 10)
	c.Add(2, 20)
	before := c.Keys()

	s := simplelru.Snapshot[int, int]{Entries: []simplelru.Entry[int, int]{
		{Key: 5, Value: 50},
		{Key: 5, Value: 55},
	}}
	if err := c.Restore(s); !errors.Is(err, simplelru.ErrDuplicateKey) {
		t.Fatalf("expected ErrDuplicateKey, got: %v", err)
	}
	if !reflect.DeepEqual(c.Keys(), before) {
		t.Fatalf("cache modified on failed restore: %v", c.Keys())
	}
}

func TestCacheSnapshot_ConsistentUnderConcurrency(t *testing.T) {
	c, err := New[int, int](128)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	for i := 0; i < 128; i++ {
		c.Add(i, i)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				c.Add(i%128, i)
				c.Get((i + g) % 128)
			}
		}(g)
	}

	// Every snapshot must be internally consistent: keys and values come
	// from the same point in time, so each key appears exactly once.
	for i := 0; i < 100; i++ {
		s := c.Snapshot()
		seen := make(map[int]int, len(s.Entries))
		for _, e := range s.Entries {
			if _, dup := seen[e.Key]; dup {
				close(stop)
				t.Fatalf("inconsistent snapshot: duplicate key %d", e.Key)
			}
			seen[e.Key] = e.Value
		}
	}
	close(stop)
	wg.Wait()
}
