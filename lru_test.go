// Copyright IBM Corp. 2014, 2025
// SPDX-License-Identifier: MPL-2.0

package lru

import (
	"reflect"
	"testing"
)

func BenchmarkLRU_Rand(b *testing.B) {
	l, err := New[int64, int64](8192)
	if err != nil {
		b.Fatalf("err: %v", err)
	}

	trace := make([]int64, b.N*2)
	for i := 0; i < b.N*2; i++ {
		trace[i] = getRand(b) % 32768
	}

	b.ResetTimer()

	var hit, miss int
	for i := 0; i < 2*b.N; i++ {
		if i%2 == 0 {
			l.Add(trace[i], trace[i])
		} else {
			if _, ok := l.Get(trace[i]); ok {
				hit++
			} else {
				miss++
			}
		}
	}
	b.Logf("hit: %d miss: %d ratio: %f", hit, miss, float64(hit)/float64(hit+miss))
}

func BenchmarkLRU_Freq(b *testing.B) {
	l, err := New[int64, int64](8192)
	if err != nil {
		b.Fatalf("err: %v", err)
	}

	trace := make([]int64, b.N*2)
	for i := 0; i < b.N*2; i++ {
		if i%2 == 0 {
			trace[i] = getRand(b) % 16384
		} else {
			trace[i] = getRand(b) % 32768
		}
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		l.Add(trace[i], trace[i])
	}
	var hit, miss int
	for i := 0; i < b.N; i++ {
		if _, ok := l.Get(trace[i]); ok {
			hit++
		} else {
			miss++
		}
	}
	b.Logf("hit: %d miss: %d ratio: %f", hit, miss, float64(hit)/float64(hit+miss))
}

func TestLRU(t *testing.T) {
	evictCounter := 0
	onEvicted := func(k int, v int) {
		if k != v {
			t.Fatalf("Evict values not equal (%v!=%v)", k, v)
		}
		evictCounter++
	}
	l, err := NewWithEvict(128, onEvicted)
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	for i := 0; i < 256; i++ {
		l.Add(i, i)
	}
	if l.Len() != 128 {
		t.Fatalf("bad len: %v", l.Len())
	}
	if l.Cap() != 128 {
		t.Fatalf("expect %d, but %d", 128, l.Cap())
	}

	if evictCounter != 128 {
		t.Fatalf("bad evict count: %v", evictCounter)
	}

	for i, k := range l.Keys() {
		if v, ok := l.Get(k); !ok || v != k || v != i+128 {
			t.Fatalf("bad key: %v", k)
		}
	}
	for i, v := range l.Values() {
		if v != i+128 {
			t.Fatalf("bad value: %v", v)
		}
	}
	for i := 0; i < 128; i++ {
		if _, ok := l.Get(i); ok {
			t.Fatalf("should be evicted")
		}
	}
	for i := 128; i < 256; i++ {
		if _, ok := l.Get(i); !ok {
			t.Fatalf("should not be evicted")
		}
	}
	for i := 128; i < 192; i++ {
		l.Remove(i)
		if _, ok := l.Get(i); ok {
			t.Fatalf("should be deleted")
		}
	}

	l.Get(192) // expect 192 to be last key in l.Keys()

	for i, k := range l.Keys() {
		if (i < 63 && k != i+193) || (i == 63 && k != 192) {
			t.Fatalf("out of order key: %v", k)
		}
	}

	l.Purge()
	if l.Len() != 0 {
		t.Fatalf("bad len: %v", l.Len())
	}
	if _, ok := l.Get(200); ok {
		t.Fatalf("should contain nothing")
	}
}

// test that Add returns true/false if an eviction occurred
func TestLRUAdd(t *testing.T) {
	evictCounter := 0
	onEvicted := func(k int, v int) {
		evictCounter++
	}

	l, err := NewWithEvict(1, onEvicted)
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	if l.Add(1, 1) == true || evictCounter != 0 {
		t.Errorf("should not have an eviction")
	}
	if l.Add(2, 2) == false || evictCounter != 1 {
		t.Errorf("should have an eviction")
	}
}

// test that Contains doesn't update recent-ness
func TestLRUContains(t *testing.T) {
	l, err := New[int, int](2)
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	l.Add(1, 1)
	l.Add(2, 2)
	if !l.Contains(1) {
		t.Errorf("1 should be contained")
	}

	l.Add(3, 3)
	if l.Contains(1) {
		t.Errorf("Contains should not have updated recent-ness of 1")
	}
}

// test that ContainsOrAdd doesn't update recent-ness
func TestLRUContainsOrAdd(t *testing.T) {
	l, err := New[int, int](2)
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	l.Add(1, 1)
	l.Add(2, 2)
	contains, evict := l.ContainsOrAdd(1, 1)
	if !contains {
		t.Errorf("1 should be contained")
	}
	if evict {
		t.Errorf("nothing should be evicted here")
	}

	l.Add(3, 3)
	contains, evict = l.ContainsOrAdd(1, 1)
	if contains {
		t.Errorf("1 should not have been contained")
	}
	if !evict {
		t.Errorf("an eviction should have occurred")
	}
	if !l.Contains(1) {
		t.Errorf("now 1 should be contained")
	}
}

// test that PeekOrAdd doesn't update recent-ness
func TestLRUPeekOrAdd(t *testing.T) {
	l, err := New[int, int](2)
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	l.Add(1, 1)
	l.Add(2, 2)
	previous, contains, evict := l.PeekOrAdd(1, 1)
	if !contains {
		t.Errorf("1 should be contained")
	}
	if evict {
		t.Errorf("nothing should be evicted here")
	}
	if previous != 1 {
		t.Errorf("previous is not equal to 1")
	}

	l.Add(3, 3)
	contains, evict = l.ContainsOrAdd(1, 1)
	if contains {
		t.Errorf("1 should not have been contained")
	}
	if !evict {
		t.Errorf("an eviction should have occurred")
	}
	if !l.Contains(1) {
		t.Errorf("now 1 should be contained")
	}
}

// test that Peek doesn't update recent-ness
func TestLRUPeek(t *testing.T) {
	l, err := New[int, int](2)
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	l.Add(1, 1)
	l.Add(2, 2)
	if v, ok := l.Peek(1); !ok || v != 1 {
		t.Errorf("1 should be set to 1: %v, %v", v, ok)
	}

	l.Add(3, 3)
	if l.Contains(1) {
		t.Errorf("should not have updated recent-ness of 1")
	}
}

// test that Resize can upsize and downsize
func TestLRUResize(t *testing.T) {
	onEvictCounter := 0
	onEvicted := func(k int, v int) {
		onEvictCounter++
	}
	l, err := NewWithEvict(2, onEvicted)
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	// Downsize
	l.Add(1, 1)
	l.Add(2, 2)
	evicted := l.Resize(1)
	if evicted != 1 {
		t.Errorf("1 element should have been evicted: %v", evicted)
	}
	if onEvictCounter != 1 {
		t.Errorf("onEvicted should have been called 1 time: %v", onEvictCounter)
	}

	l.Add(3, 3)
	if l.Contains(1) {
		t.Errorf("Element 1 should have been evicted")
	}

	// Upsize
	evicted = l.Resize(2)
	if evicted != 0 {
		t.Errorf("0 elements should have been evicted: %v", evicted)
	}

	l.Add(4, 4)
	if !l.Contains(3) || !l.Contains(4) {
		t.Errorf("Cache should have contained 2 elements")
	}
}

func (c *Cache[K, V]) wantKeys(t *testing.T, want []K) {
	t.Helper()
	got := c.Keys()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("wrong keys got: %v, want: %v ", got, want)
	}
}

// TestCache_GetMultiple verifies positional results, including duplicate keys,
// missing keys and stored zero values.
func TestCache_GetMultiple(t *testing.T) {
	l, err := New[int, int](4)
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	// Store an explicitly zero value; it must not be reported as a miss.
	l.Add(10, 0)
	l.Add(20, 200)
	l.Add(30, 300)

	keys := []int{30, 99, 10, 30, 40, 20}
	results := l.GetMultiple(keys, false)
	if len(results) != len(keys) {
		t.Fatalf("expected %d results, got %d", len(keys), len(results))
	}

	want := []GetResult[int, int]{
		{Key: 30, Value: 300, OK: true},
		{Key: 99, Value: 0, OK: false},
		{Key: 10, Value: 0, OK: true}, // zero value, but present
		{Key: 30, Value: 300, OK: true},
		{Key: 40, Value: 0, OK: false},
		{Key: 20, Value: 200, OK: true},
	}
	if !reflect.DeepEqual(results, want) {
		t.Errorf("GetMultiple results got: %+v, want: %+v", results, want)
	}

	if results[0].Key != 30 || results[2].Key != 10 {
		t.Errorf("result keys should mirror input order")
	}
	if results[2].Value != 0 || !results[2].OK {
		t.Errorf("stored zero value must count as a hit")
	}
	if results[1].OK || results[4].OK {
		t.Errorf("absent keys must be reported as misses")
	}

	// Empty input returns an empty (non-nil), positionally aligned slice.
	if got := l.GetMultiple(nil, false); len(got) != 0 {
		t.Errorf("expected no results for empty input, got %v", got)
	}
}

// TestCache_GetMultipleReadOnly verifies that a read-only batch never changes
// recency, so eviction order is unaffected.
func TestCache_GetMultipleReadOnly(t *testing.T) {
	var evictedKeys []int
	l, err := NewWithEvict[int, int](3, func(key, _ int) {
		evictedKeys = append(evictedKeys, key)
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	l.Add(1, 100)
	l.Add(2, 200)
	l.Add(3, 300)
	l.wantKeys(t, []int{1, 2, 3})

	// Duplicates and misses included; read-only mode changes nothing.
	l.GetMultiple([]int{9, 3, 1, 3, 2, 8}, false)
	l.wantKeys(t, []int{1, 2, 3})

	l.Add(4, 400)
	l.wantKeys(t, []int{2, 3, 4})
	if !reflect.DeepEqual(evictedKeys, []int{1}) {
		t.Errorf("read-only batch changed eviction order, evicted: %v", evictedKeys)
	}
}

// TestCache_GetMultipleTouch verifies touch semantics: present keys are
// touched in first-appearance order, each only once, and misses are ignored.
func TestCache_GetMultipleTouch(t *testing.T) {
	var evictedKeys []int
	l, err := NewWithEvict[int, int](3, func(key, _ int) {
		evictedKeys = append(evictedKeys, key)
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	l.Add(1, 100)
	l.Add(2, 200)
	l.Add(3, 300) // LRU order: [1, 2, 3]

	// First-appearance order is 3, 1, 2 (9 is a miss, 3/1 are repeats).
	l.GetMultiple([]int{3, 9, 1, 3, 2, 1}, true)
	l.wantKeys(t, []int{3, 1, 2})

	// Inserting until capacity verifies the exact eviction sequence.
	l.Add(4, 400) // evicts 3
	l.wantKeys(t, []int{1, 2, 4})
	l.Add(5, 500) // evicts 1
	l.wantKeys(t, []int{2, 4, 5})
	l.Add(6, 600) // evicts 2
	l.wantKeys(t, []int{4, 5, 6})

	if !reflect.DeepEqual(evictedKeys, []int{3, 1, 2}) {
		t.Errorf("unexpected eviction order: %v", evictedKeys)
	}
}

// TestCache_GetMultipleTouchZeroValue verifies a zero-valued hit is touched
// like any other hit and survives eviction.
func TestCache_GetMultipleTouchZeroValue(t *testing.T) {
	l, err := New[int, int](2)
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	l.Add(1, 0)
	l.Add(2, 200)

	results := l.GetMultiple([]int{1, 7, 1}, true)
	if !results[0].OK || results[0].Value != 0 {
		t.Fatalf("zero value must be a hit: %+v", results)
	}

	l.Add(3, 300)
	if _, ok := l.Get(1); !ok {
		t.Errorf("touched zero-valued key should not be evicted")
	}
	if _, ok := l.Get(2); ok {
		t.Errorf("untouched key should have been evicted")
	}
}

// TestCache_GetMultipleConcurrent checks the batch runs as one linearizable
// operation against concurrent Add/Remove/Resize.
func TestCache_GetMultipleConcurrent(t *testing.T) {
	l, err := New[int, int](16)
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		keys := []int{1, 2, 3, 2, 100}
		for i := 0; i < 500; i++ {
			results := l.GetMultiple(keys, i%2 == 0)
			for j, r := range results {
				if r.Key != keys[j] {
					t.Errorf("result key misaligned: got %d want %d", r.Key, keys[j])
					return
				}
			}
		}
	}()

	for i := 0; i < 500; i++ {
		l.Add(i%32, i)
		if i%10 == 0 {
			l.Remove(3)
		}
		if i%50 == 0 {
			l.Resize(8 + i%16)
		}
	}
	<-done
}

func TestCache_EvictionSameKey(t *testing.T) {
	t.Run("Add", func(t *testing.T) {
		var evictedKeys []int

		cache, _ := NewWithEvict(
			2,
			func(key int, _ struct{}) {
				evictedKeys = append(evictedKeys, key)
			})

		if evicted := cache.Add(1, struct{}{}); evicted {
			t.Error("First 1: got unexpected eviction")
		}
		cache.wantKeys(t, []int{1})

		if evicted := cache.Add(2, struct{}{}); evicted {
			t.Error("2: got unexpected eviction")
		}
		cache.wantKeys(t, []int{1, 2})

		if evicted := cache.Add(1, struct{}{}); evicted {
			t.Error("Second 1: got unexpected eviction")
		}
		cache.wantKeys(t, []int{2, 1})

		if evicted := cache.Add(3, struct{}{}); !evicted {
			t.Error("3: did not get expected eviction")
		}
		cache.wantKeys(t, []int{1, 3})

		want := []int{2}
		if !reflect.DeepEqual(evictedKeys, want) {
			t.Errorf("evictedKeys got: %v want: %v", evictedKeys, want)
		}
	})

	t.Run("ContainsOrAdd", func(t *testing.T) {
		var evictedKeys []int

		cache, _ := NewWithEvict(
			2,
			func(key int, _ struct{}) {
				evictedKeys = append(evictedKeys, key)
			})

		contained, evicted := cache.ContainsOrAdd(1, struct{}{})
		if contained {
			t.Error("First 1: got unexpected contained")
		}
		if evicted {
			t.Error("First 1: got unexpected eviction")
		}
		cache.wantKeys(t, []int{1})

		contained, evicted = cache.ContainsOrAdd(2, struct{}{})
		if contained {
			t.Error("2: got unexpected contained")
		}
		if evicted {
			t.Error("2: got unexpected eviction")
		}
		cache.wantKeys(t, []int{1, 2})

		contained, evicted = cache.ContainsOrAdd(1, struct{}{})
		if !contained {
			t.Error("Second 1: did not get expected contained")
		}
		if evicted {
			t.Error("Second 1: got unexpected eviction")
		}
		cache.wantKeys(t, []int{1, 2})

		contained, evicted = cache.ContainsOrAdd(3, struct{}{})
		if contained {
			t.Error("3: got unexpected contained")
		}
		if !evicted {
			t.Error("3: did not get expected eviction")
		}
		cache.wantKeys(t, []int{2, 3})

		want := []int{1}
		if !reflect.DeepEqual(evictedKeys, want) {
			t.Errorf("evictedKeys got: %v want: %v", evictedKeys, want)
		}
	})

	t.Run("PeekOrAdd", func(t *testing.T) {
		var evictedKeys []int

		cache, _ := NewWithEvict(
			2,
			func(key int, _ struct{}) {
				evictedKeys = append(evictedKeys, key)
			})

		_, contained, evicted := cache.PeekOrAdd(1, struct{}{})
		if contained {
			t.Error("First 1: got unexpected contained")
		}
		if evicted {
			t.Error("First 1: got unexpected eviction")
		}
		cache.wantKeys(t, []int{1})

		_, contained, evicted = cache.PeekOrAdd(2, struct{}{})
		if contained {
			t.Error("2: got unexpected contained")
		}
		if evicted {
			t.Error("2: got unexpected eviction")
		}
		cache.wantKeys(t, []int{1, 2})

		_, contained, evicted = cache.PeekOrAdd(1, struct{}{})
		if !contained {
			t.Error("Second 1: did not get expected contained")
		}
		if evicted {
			t.Error("Second 1: got unexpected eviction")
		}
		cache.wantKeys(t, []int{1, 2})

		_, contained, evicted = cache.PeekOrAdd(3, struct{}{})
		if contained {
			t.Error("3: got unexpected contained")
		}
		if !evicted {
			t.Error("3: did not get expected eviction")
		}
		cache.wantKeys(t, []int{2, 3})

		want := []int{1}
		if !reflect.DeepEqual(evictedKeys, want) {
			t.Errorf("evictedKeys got: %v want: %v", evictedKeys, want)
		}
	})
}
