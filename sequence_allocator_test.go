package ksuid

import (
	"bytes"
	"encoding/binary"
	"math"
	"sync"
	"testing"
)

func sequenceNumberOf(id KSUID) int {
	return int(binary.BigEndian.Uint16(id[len(id)-2:]))
}

func drainRange(t *testing.T, r *SequenceRange) []KSUID {
	t.Helper()
	ids := make([]KSUID, 0, r.Remaining())
	for {
		id, err := r.Next()
		if err == ErrRangeExhausted {
			return ids
		}
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
}

func TestSequenceAllocatorConcurrentReserve(t *testing.T) {
	alloc := SequenceAllocator{Seed: New()}
	const workers = 64
	sizes := make([]int, workers)
	total := 0
	for i := range sizes {
		sizes[i] = 1 + (i*977)%509
		total += sizes[i]
	}
	ranges := make([]*SequenceRange, workers)
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ranges[i], errs[i] = alloc.Reserve(sizes[i])
		}(i)
	}
	wg.Wait()
	produced := make([][]KSUID, workers)
	for i := 0; i < workers; i++ {
		if errs[i] != nil {
			t.Fatalf("reserve %d failed: %v", sizes[i], errs[i])
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			produced[i] = drainRange(t, ranges[i])
		}(i)
	}
	wg.Wait()
	seen := make(map[int]struct{}, total)
	count := 0
	for i, ids := range produced {
		if len(ids) != sizes[i] {
			t.Fatalf("range %d produced %d ids, expected %d", i, len(ids), sizes[i])
		}
		for _, id := range ids {
			n := sequenceNumberOf(id)
			if _, dup := seen[n]; dup {
				t.Fatalf("duplicate sequence number %d in %s", n, id)
			}
			seen[n] = struct{}{}
			count++
		}
	}
	if count != total {
		t.Fatalf("produced %d ids, expected %d", count, total)
	}
	for n := 0; n < total; n++ {
		if _, ok := seen[n]; !ok {
			t.Fatalf("sequence number %d missing from merged output", n)
		}
	}
	if remaining := alloc.Remaining(); remaining != sequenceCapacity-total {
		t.Fatalf("allocator has %d ids left, expected %d", remaining, sequenceCapacity-total)
	}
}

func TestSequenceAllocatorLastItem(t *testing.T) {
	alloc := SequenceAllocator{Seed: New()}
	first, err := alloc.Reserve(sequenceCapacity - 1)
	if err != nil {
		t.Fatal(err)
	}
	last, err := alloc.Reserve(1)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(drainRange(t, first)); got != sequenceCapacity-1 {
		t.Fatalf("first range produced %d ids, expected %d", got, sequenceCapacity-1)
	}
	id, err := last.Next()
	if err != nil {
		t.Fatal(err)
	}
	if n := sequenceNumberOf(id); n != math.MaxUint16 {
		t.Fatalf("last id has sequence number %d, expected %d", n, math.MaxUint16)
	}
	if _, err := last.Next(); err != ErrRangeExhausted {
		t.Fatalf("expected ErrRangeExhausted after last id, got %v", err)
	}
	if _, err := alloc.Reserve(1); err != ErrReserveExhausted {
		t.Fatalf("expected ErrReserveExhausted on full allocator, got %v", err)
	}
}

func TestSequenceAllocatorWholePoolAtOnce(t *testing.T) {
	alloc := SequenceAllocator{Seed: New()}
	r, err := alloc.Reserve(sequenceCapacity)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Remaining(); got != sequenceCapacity {
		t.Fatalf("range has %d ids remaining, expected %d", got, sequenceCapacity)
	}
	min, max := r.Bounds()
	if sequenceNumberOf(min) != 0 || sequenceNumberOf(max) != math.MaxUint16 {
		t.Fatalf("whole-pool bounds are [%d, %d], expected [0, %d]",
			sequenceNumberOf(min), sequenceNumberOf(max), math.MaxUint16)
	}
	if got := alloc.Remaining(); got != 0 {
		t.Fatalf("allocator has %d ids left, expected 0", got)
	}
	if _, err := alloc.Reserve(1); err != ErrReserveExhausted {
		t.Fatalf("expected ErrReserveExhausted, got %v", err)
	}
}

func TestSequenceAllocatorFailedReserveKeepsCursor(t *testing.T) {
	alloc := SequenceAllocator{Seed: New()}
	if _, err := alloc.Reserve(sequenceCapacity - 10); err != nil {
		t.Fatal(err)
	}
	if _, err := alloc.Reserve(11); err != ErrReserveExhausted {
		t.Fatalf("expected ErrReserveExhausted, got %v", err)
	}
	if got := alloc.Remaining(); got != 10 {
		t.Fatalf("failed reservation moved the cursor: %d ids left, expected 10", got)
	}
	r, err := alloc.Reserve(10)
	if err != nil {
		t.Fatal(err)
	}
	min, max := r.Bounds()
	if sequenceNumberOf(min) != sequenceCapacity-10 || sequenceNumberOf(max) != math.MaxUint16 {
		t.Fatalf("post-failure range bounds are [%d, %d], expected [%d, %d]",
			sequenceNumberOf(min), sequenceNumberOf(max), sequenceCapacity-10, math.MaxUint16)
	}
}

func TestSequenceAllocatorReserveValidation(t *testing.T) {
	alloc := SequenceAllocator{Seed: New()}
	for _, n := range []int{0, -1, math.MinInt32} {
		if _, err := alloc.Reserve(n); err != ErrReserveZero {
			t.Fatalf("Reserve(%d): expected ErrReserveZero, got %v", n, err)
		}
	}
	for _, n := range []int{sequenceCapacity + 1, math.MaxInt32} {
		if _, err := alloc.Reserve(n); err != ErrReserveTooLarge {
			t.Fatalf("Reserve(%d): expected ErrReserveTooLarge, got %v", n, err)
		}
	}
	if got := alloc.Remaining(); got != sequenceCapacity {
		t.Fatalf("invalid reservations consumed ids: %d left, expected %d", got, sequenceCapacity)
	}
}

func TestSequenceRangeBoundsAndRemainingAreFixed(t *testing.T) {
	seed := New()
	alloc := SequenceAllocator{Seed: seed}
	r, err := alloc.Reserve(100)
	if err != nil {
		t.Fatal(err)
	}
	min, max := r.Bounds()
	if sequenceNumberOf(min) != 0 || sequenceNumberOf(max) != 99 {
		t.Fatalf("bounds are [%d, %d], expected [0, 99]",
			sequenceNumberOf(min), sequenceNumberOf(max))
	}
	if _, err := alloc.Reserve(1000); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 40; i++ {
		if _, err := r.Next(); err != nil {
			t.Fatal(err)
		}
	}
	if got := r.Remaining(); got != 60 {
		t.Fatalf("range has %d ids remaining, expected 60", got)
	}
	if min2, max2 := r.Bounds(); min2 != min || max2 != max {
		t.Fatal("range bounds changed after further reservations and consumption")
	}
	id, err := r.Next()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(id[:18], seed[:18]) {
		t.Fatalf("id %s does not share the seed's leading 18 bytes", id)
	}
	if n := sequenceNumberOf(id); n != 40 {
		t.Fatalf("id has sequence number %d, expected 40", n)
	}
}

func TestSequenceRangeExhaustion(t *testing.T) {
	alloc := SequenceAllocator{Seed: New()}
	r, err := alloc.Reserve(3)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if got := r.Remaining(); got != 3-i {
			t.Fatalf("range has %d ids remaining, expected %d", got, 3-i)
		}
		id, err := r.Next()
		if err != nil {
			t.Fatal(err)
		}
		if n := sequenceNumberOf(id); n != i {
			t.Fatalf("id has sequence number %d, expected %d", n, i)
		}
	}
	if got := r.Remaining(); got != 0 {
		t.Fatalf("exhausted range has %d ids remaining, expected 0", got)
	}
	if _, err := r.Next(); err != ErrRangeExhausted {
		t.Fatalf("expected ErrRangeExhausted, got %v", err)
	}
	if min, max := r.Bounds(); sequenceNumberOf(min) != 0 || sequenceNumberOf(max) != 2 {
		t.Fatal("bounds changed after exhaustion")
	}
}
