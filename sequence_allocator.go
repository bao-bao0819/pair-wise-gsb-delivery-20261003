package ksuid

import (
	"errors"
	"math"
	"sync"
)

const sequenceIDCount = math.MaxUint16 + 1

var (
	// ErrInvalidSequenceCount is returned when a reservation does not request a
	// positive number of IDs.
	ErrInvalidSequenceCount = errors.New("ksuid: sequence reservation count must be positive")

	// ErrSequenceCountTooLarge is returned when a reservation requests more
	// than the 65536 IDs available for a seed.
	ErrSequenceCountTooLarge = errors.New("ksuid: sequence reservation count exceeds 65536")

	// ErrInsufficientSequenceIDs is returned when the allocator does not have
	// enough unreserved IDs remaining.
	ErrInsufficientSequenceIDs = errors.New("ksuid: not enough sequence IDs remaining")

	// ErrSubSequenceExhausted is returned after every ID in a SubSequence has
	// been consumed.
	ErrSubSequenceExhausted = errors.New("ksuid: sub-sequence is exhausted")
)

// SequenceAllocator divides the 65536 KSUIDs available for one seed into
// non-overlapping SubSequence values. It is safe for concurrent use.
//
// Reserved IDs are never recycled, even when a SubSequence is discarded before
// being fully consumed.
type SequenceAllocator struct {
	mu   sync.Mutex
	seed KSUID
	next uint32
}

// NewSequenceAllocator returns a SequenceAllocator that produces IDs from seed.
func NewSequenceAllocator(seed KSUID) *SequenceAllocator {
	return &SequenceAllocator{seed: seed}
}

// Reserve allocates count consecutive IDs and returns them as an independent
// SubSequence. The entire reservation fails without advancing the allocator
// when count is zero, negative, greater than 65536, or larger than the number
// of IDs remaining.
func (a *SequenceAllocator) Reserve(count int) (*SubSequence, error) {
	if count <= 0 {
		return nil, ErrInvalidSequenceCount
	}
	if count > sequenceIDCount {
		return nil, ErrSequenceCountTooLarge
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	remaining := uint32(sequenceIDCount) - a.next
	if uint32(count) > remaining {
		return nil, ErrInsufficientSequenceIDs
	}

	start := a.next
	a.next += uint32(count)

	return &SubSequence{
		seed: a.seed,
		min:  uint16(start),
		max:  uint16(start + uint32(count) - 1),
		next: start,
		end:  start + uint32(count),
	}, nil
}

// SubSequence is an independently consumable range of consecutive KSUIDs.
// Instances are intended for use by one worker and are not safe for concurrent
// use, but their values do not depend on the allocator after reservation.
type SubSequence struct {
	seed KSUID
	min  uint16
	max  uint16
	next uint32
	end  uint32
}

// Next consumes and returns the next KSUID in the reserved range.
func (seq *SubSequence) Next() (KSUID, error) {
	if seq.next >= seq.end {
		return Nil, ErrSubSequenceExhausted
	}

	id := withSequenceNumber(seq.seed, uint16(seq.next))
	seq.next++
	return id, nil
}

// Bounds returns the fixed inclusive minimum and maximum IDs of the reserved
// range. The bounds do not change as IDs are consumed.
func (seq *SubSequence) Bounds() (min KSUID, max KSUID) {
	return withSequenceNumber(seq.seed, seq.min), withSequenceNumber(seq.seed, seq.max)
}

// Remaining returns the number of reserved IDs that have not yet been consumed.
func (seq *SubSequence) Remaining() int {
	return int(seq.end - seq.next)
}
