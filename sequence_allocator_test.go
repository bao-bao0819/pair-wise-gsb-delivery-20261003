package ksuid

import (
	"encoding/binary"
	"math"
	"sync"
	"testing"
	"time"
)

type reservedIDs struct {
	boundsMin uint16
	boundsMax uint16
	ids       []KSUID
}

func testAllocatorSeed() KSUID {
	payload := [payloadLengthInBytes]byte{
		0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07,
		0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e,
		0xa5, 0x5a,
	}

	seed, err := FromParts(time.Unix(1500000000, 0), payload[:])
	if err != nil {
		panic(err)
	}
	return seed
}

func sequenceNumberOf(id KSUID) uint16 {
	return binary.BigEndian.Uint16(id[len(id)-2:])
}

func consumeSubSequence(t *testing.T, sub *SubSequence) []KSUID {
	t.Helper()

	min, max := sub.Bounds()
	minNumber := sequenceNumberOf(min)
	maxNumber := sequenceNumberOf(max)
	expected := int(maxNumber) - int(minNumber) + 1

	if remaining := sub.Remaining(); remaining != expected {
		t.Fatalf("initial remaining = %d, want %d", remaining, expected)
	}

	ids := make([]KSUID, 0, expected)
	for i := 0; i < expected; i++ {
		id, err := sub.Next()
		if err != nil {
			t.Fatal(err)
		}
		if number := sequenceNumberOf(id); number != minNumber+uint16(i) {
			t.Fatalf("sequence number = %d, want %d", number, minNumber+uint16(i))
		}
		ids = append(ids, id)
	}

	if remaining := sub.Remaining(); remaining != 0 {
		t.Fatalf("remaining after consuming all IDs = %d, want 0", remaining)
	}
	if _, err := sub.Next(); err != ErrSubSequenceExhausted {
		t.Fatalf("error after exhausting sub-sequence = %v, want %v", err, ErrSubSequenceExhausted)
	}

	boundsMin, boundsMax := sub.Bounds()
	if boundsMin != min || boundsMax != max {
		t.Fatal("bounds changed after consuming the sub-sequence")
	}

	return ids
}

func TestSequenceAllocatorConcurrentReservations(t *testing.T) {
	seed := testAllocatorSeed()
	allocator := NewSequenceAllocator(seed)

	sizes := varyingReservationSizes(t)
	results := make(chan reservedIDs, len(sizes))
	var wg sync.WaitGroup

	for _, size := range sizes {
		size := size
		wg.Add(1)
		go func() {
			defer wg.Done()

			sub, err := allocator.Reserve(size)
			if err != nil {
				t.Errorf("Reserve(%d): %v", size, err)
				return
			}

			min, max := sub.Bounds()
			result := reservedIDs{
				boundsMin: sequenceNumberOf(min),
				boundsMax: sequenceNumberOf(max),
			}
			result.ids = consumeSubSequence(t, sub)
			results <- result
		}()
	}

	wg.Wait()
	close(results)

	seen := make(map[uint16]bool, math.MaxUint16+1)
	for result := range results {
		if int(result.boundsMax)-int(result.boundsMin)+1 != len(result.ids) {
			t.Fatal("reserved bounds do not match consumed IDs")
		}

		for i, id := range result.ids {
			number := sequenceNumberOf(id)
			if number != result.boundsMin+uint16(i) {
				t.Fatalf("sequence number = %d, want %d", number, result.boundsMin+uint16(i))
			}
			if seen[number] {
				t.Fatalf("sequence number %d was reserved twice", number)
			}
			seen[number] = true

			if string(id[:len(id)-2]) != string(seed[:len(seed)-2]) {
				t.Fatalf("id %s does not preserve the seed timestamp and first 14 payload bytes", id)
			}
		}
	}

	if len(seen) != math.MaxUint16+1 {
		t.Fatalf("reserved %d unique IDs, want %d", len(seen), math.MaxUint16+1)
	}
	for number := 0; number <= math.MaxUint16; number++ {
		if !seen[uint16(number)] {
			t.Fatalf("sequence number %d was not reserved", number)
		}
	}
}

func varyingReservationSizes(t *testing.T) []int {
	t.Helper()

	patterns := []int{1, 2, 3, 5, 8, 13, 17, 64, 257, 1023}
	sizes := make([]int, 0, 256)
	total := 0

	for i := 0; total < math.MaxUint16+1; i++ {
		size := patterns[i%len(patterns)]
		if remaining := math.MaxUint16 + 1 - total; size > remaining {
			size = remaining
		}
		sizes = append(sizes, size)
		total += size
	}

	if total != math.MaxUint16+1 {
		t.Fatalf("test reservations total %d, want %d", total, math.MaxUint16+1)
	}
	return sizes
}

func TestSequenceAllocatorReserveWholePool(t *testing.T) {
	seed := testAllocatorSeed()
	allocator := NewSequenceAllocator(seed)

	sub, err := allocator.Reserve(math.MaxUint16 + 1)
	if err != nil {
		t.Fatal(err)
	}

	min, max := sub.Bounds()
	if sequenceNumberOf(min) != 0 || sequenceNumberOf(max) != math.MaxUint16 {
		t.Fatal("whole-pool reservation has incorrect bounds")
	}
	if remaining := sub.Remaining(); remaining != math.MaxUint16+1 {
		t.Fatalf("remaining = %d, want %d", remaining, math.MaxUint16+1)
	}

	ids := consumeSubSequence(t, sub)
	if len(ids) != math.MaxUint16+1 {
		t.Fatalf("consumed %d IDs, want %d", len(ids), math.MaxUint16+1)
	}
	if sequenceNumberOf(ids[0]) != 0 || sequenceNumberOf(ids[len(ids)-1]) != math.MaxUint16 {
		t.Fatal("whole-pool reservation does not include both end IDs")
	}

	if _, err := allocator.Reserve(1); err != ErrInsufficientSequenceIDs {
		t.Fatalf("error after reserving whole pool = %v, want %v", err, ErrInsufficientSequenceIDs)
	}
}

func TestSequenceAllocatorLastIDAfterFailedReservation(t *testing.T) {
	allocator := NewSequenceAllocator(testAllocatorSeed())

	first, err := allocator.Reserve(math.MaxUint16)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := allocator.Reserve(2); err != ErrInsufficientSequenceIDs {
		t.Fatalf("error = %v, want %v", err, ErrInsufficientSequenceIDs)
	}

	last, err := allocator.Reserve(1)
	if err != nil {
		t.Fatal("failed reservation advanced the allocator")
	}

	min, max := last.Bounds()
	if min != max {
		t.Fatal("last ID reservation has different min and max bounds")
	}
	if number := sequenceNumberOf(min); number != math.MaxUint16 {
		t.Fatalf("last sequence number = %d, want %d", number, math.MaxUint16)
	}

	firstIDs := consumeSubSequence(t, first)
	lastID := consumeSubSequence(t, last)[0]
	if number := sequenceNumberOf(lastID); number != math.MaxUint16 {
		t.Fatalf("last consumed number = %d, want %d", number, math.MaxUint16)
	}
	if sequenceNumberOf(firstIDs[len(firstIDs)-1]) != math.MaxUint16-1 {
		t.Fatal("first reservation includes the final sequence number")
	}
}

func TestSequenceAllocatorInvalidReservationCounts(t *testing.T) {
	allocator := NewSequenceAllocator(testAllocatorSeed())

	for _, count := range []int{0, -1} {
		if _, err := allocator.Reserve(count); err != ErrInvalidSequenceCount {
			t.Fatalf("Reserve(%d) error = %v, want %v", count, err, ErrInvalidSequenceCount)
		}
	}

	if _, err := allocator.Reserve(math.MaxUint16 + 2); err != ErrSequenceCountTooLarge {
		t.Fatalf("error = %v, want %v", err, ErrSequenceCountTooLarge)
	}

	sub, err := allocator.Reserve(1)
	if err != nil {
		t.Fatal(err)
	}
	if remaining := sub.Remaining(); remaining != 1 {
		t.Fatalf("remaining after invalid reservations = %d, want 1", remaining)
	}
}

func TestSequenceAllocatorDoesNotRecycleUnusedTail(t *testing.T) {
	allocator := NewSequenceAllocator(testAllocatorSeed())

	first, err := allocator.Reserve(3)
	if err != nil {
		t.Fatal(err)
	}
	second, err := allocator.Reserve(2)
	if err != nil {
		t.Fatal(err)
	}

	id, err := first.Next()
	if err != nil {
		t.Fatal(err)
	}
	if number := sequenceNumberOf(id); number != 0 {
		t.Fatalf("first ID number = %d, want 0", number)
	}

	later, err := allocator.Reserve(2)
	if err != nil {
		t.Fatal(err)
	}

	min, max := later.Bounds()
	if sequenceNumberOf(min) != 5 || sequenceNumberOf(max) != 6 {
		t.Fatalf("later reservation bounds = [%d,%d], want [5,6]", sequenceNumberOf(min), sequenceNumberOf(max))
	}

	secondMin, secondMax := second.Bounds()
	laterMin, laterMax := later.Bounds()
	if Compare(secondMax, laterMin) >= 0 || Compare(laterMax, secondMin) <= 0 {
		t.Fatal("later reservation overlaps an existing reservation")
	}

	secondID, err := second.Next()
	if err != nil {
		t.Fatal(err)
	}
	if number := sequenceNumberOf(secondID); number != 3 {
		t.Fatalf("second reservation first number = %d, want 3", number)
	}
}

func TestSubSequenceBoundsAreIndependentOfAllocator(t *testing.T) {
	allocator := NewSequenceAllocator(testAllocatorSeed())

	sub, err := allocator.Reserve(2)
	if err != nil {
		t.Fatal(err)
	}
	min, max := sub.Bounds()

	for _, count := range []int{4, 8} {
		if _, err := allocator.Reserve(count); err != nil {
			t.Fatal(err)
		}
	}

	newMin, newMax := sub.Bounds()
	if newMin != min || newMax != max {
		t.Fatal("sub-sequence bounds changed after later reservations")
	}
	if remaining := sub.Remaining(); remaining != 2 {
		t.Fatalf("remaining after later reservations = %d, want 2", remaining)
	}

	first, err := sub.Next()
	if err != nil {
		t.Fatal(err)
	}
	if first != min {
		t.Fatal("first ID does not equal the fixed minimum bound")
	}

	second, err := sub.Next()
	if err != nil {
		t.Fatal(err)
	}
	if second != max {
		t.Fatal("second ID does not equal the fixed maximum bound")
	}
}
