package ksuid

import (
	"errors"
	"math"
	"sync"
)

// sequenceCapacity is the total number of KSUIDs that can be produced from a
// single seed: the 2-byte sequence number space.
const sequenceCapacity = math.MaxUint16 + 1

var (
	// ErrReserveZero is returned when reserving a non-positive number of IDs.
	ErrReserveZero = errors.New("ksuid: cannot reserve a non-positive number of IDs")

	// ErrReserveTooLarge is returned when a single reservation exceeds the
	// total capacity of a sequence (65536 IDs).
	ErrReserveTooLarge = errors.New("ksuid: reservation exceeds the sequence capacity of 65536 IDs")

	// ErrReserveExhausted is returned when the sequence does not have enough
	// IDs left to satisfy a reservation. The allocator cursor is left
	// unchanged, so smaller reservations may still succeed afterwards.
	ErrReserveExhausted = errors.New("ksuid: not enough IDs left in the sequence to satisfy the reservation")

	// ErrRangeExhausted is returned by SequenceRange.Next once every ID of
	// the reserved range has been consumed.
	ErrRangeExhausted = errors.New("ksuid: the reserved range of IDs has been exhausted")
)

// SequenceAllocator is a concurrency-safe counterpart of Sequence. Instead of
// producing IDs one at a time, it hands out non-overlapping ranges of the
// 65536-ID space of a seed via Reserve. Each reserved range is an independent
// SequenceRange that a single worker can consume locally without further
// synchronization.
//
// Ranges that are reserved but only partially consumed are never reclaimed:
// the allocator cursor only moves forward, so a stale holder of a range can
// never observe IDs that were handed out to someone else.
type SequenceAllocator struct {
	// The seed is used as base for the KSUID generator, all generated KSUIDs
	// share the same leading 18 bytes of the seed (timestamp and the first
	// 14 payload bytes).
	Seed KSUID

	mu    sync.Mutex
	count uint32 // next sequence number to hand out
}

// Reserve atomically allocates n consecutive IDs and returns them as an
// independent SequenceRange.
//
// Reservations never overlap. If fewer than n IDs remain, the whole
// reservation fails with ErrReserveExhausted and the allocator cursor is not
// advanced. Reserving zero (or a negative count) fails with ErrReserveZero,
// and reserving more than the total sequence capacity fails with
// ErrReserveTooLarge.
func (a *SequenceAllocator) Reserve(n int) (*SequenceRange, error) {
	if n <= 0 {
		return nil, ErrReserveZero
	}
	if n > sequenceCapacity {
		return nil, ErrReserveTooLarge
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	if remaining := sequenceCapacity - int(a.count); n > remaining {
		return nil, ErrReserveExhausted
	}

	r := &SequenceRange{
		seed:  a.Seed,
		first: a.count,
		last:  a.count + uint32(n) - 1,
		next:  a.count,
	}
	a.count += uint32(n)
	return r, nil
}

// Remaining returns the number of IDs that have not been reserved yet.
func (a *SequenceAllocator) Remaining() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return sequenceCapacity - int(a.count)
}

// SequenceRange is a fixed, non-overlapping slice of a sequence handed out by
// SequenceAllocator.Reserve. It is consumed locally with Next and does not
// depend on the allocator's state after reservation.
//
// SequenceRange values are not safe to use concurrently from multiple
// goroutines.
type SequenceRange struct {
	seed  KSUID
	first uint32 // first sequence number of the range (inclusive)
	last  uint32 // last sequence number of the range (inclusive)
	next  uint32 // next sequence number to produce
}

// Next produces the next KSUID in the range, or ErrRangeExhausted once all
// reserved IDs have been consumed.
func (r *SequenceRange) Next() (KSUID, error) {
	if r.next > r.last {
		return Nil, ErrRangeExhausted
	}
	id := withSequenceNumber(r.seed, uint16(r.next))
	r.next++
	return id, nil
}

// Bounds returns the inclusive min and max bounds of the range. The bounds
// are fixed at reservation time and never change, regardless of how many IDs
// have been consumed or reserved elsewhere.
func (r *SequenceRange) Bounds() (min KSUID, max KSUID) {
	return withSequenceNumber(r.seed, uint16(r.first)),
		withSequenceNumber(r.seed, uint16(r.last))
}

// Remaining returns the number of IDs not yet consumed from the range.
func (r *SequenceRange) Remaining() int {
	if r.next > r.last {
		return 0
	}
	return int(r.last-r.next) + 1
}
