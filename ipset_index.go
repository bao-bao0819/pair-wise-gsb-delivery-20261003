package netipx

import (
	"math/big"
	"math/bits"
	"net/netip"
	"sort"
)

// An IPSetIndex is a reusable ordinal index over an immutable IPSet.
//
// Addresses are numbered from zero in a single global order: all IPv4
// addresses come first, in ascending numeric order, followed by all
// IPv6 addresses, also in ascending numeric order. Gaps between the
// ranges of the set do not consume an ordinal, so the ordinal of an
// address is its rank among the members of the set, not its numeric
// IP value.
//
// The index is built once from the normalized ranges of the IPSet and
// never expands the address space: it stores a running member count per
// range instead of enumerating addresses. An IPSetIndex is safe for
// concurrent use by multiple goroutines, and building and querying it
// does not modify the IPSet it was built from.
//
// The zero IPSetIndex is a valid index over an empty set.
type IPSetIndex struct {
	// s is the set being indexed; it is never mutated.
	s *IPSet

	// cum[i] is the number of set members in the first i ranges,
	// i.e. the ordinal of rr[i].from within the set. The length is
	// len(s.rr)+1, so cum[len(s.rr)] is the total count.
	//
	// Counts are arbitrary precision because a set containing all of
	// IPv6 space alone holds 2^128 members, and a dual-family set more.
	cum []big.Int
}

// Index returns a reusable ordinal index over s.
//
// Building the index takes time proportional to the number of ranges
// in s. It does not modify s, and the returned index may be used
// concurrently by multiple goroutines.
func (s *IPSet) Index() *IPSetIndex {
	if s == nil {
		return &IPSetIndex{}
	}
	ix := &IPSetIndex{
		s:   s,
		cum: make([]big.Int, len(s.rr)+1),
	}
	for i, r := range s.rr {
		ix.cum[i+1].Add(&ix.cum[i], rangeSize(r))
	}
	return ix
}

// rangeSize returns the inclusive number of addresses in r as an
// exact integer. It computes to-from+1 on the normalized range rather
// than enumerating addresses, so the result may be as large as 2^128
// for a full IPv6 range.
func rangeSize(r IPRange) *big.Int {
	from := u128From16(r.from.As16())
	to := u128From16(r.to.As16())
	// size = to - from + 1. The +1 is done in arbitrary precision to
	// retain the value 2^128 when from==0 and to==max.
	lo, borrow := bits.Sub64(to.lo, from.lo, 0)
	hi, _ := bits.Sub64(to.hi, from.hi, borrow)
	n := new(big.Int).SetUint64(hi)
	n.Lsh(n, 64)
	n.Or(n, new(big.Int).SetUint64(lo))
	return n.Add(n, big.NewInt(1))
}

// Count returns the exact number of addresses in the set.
//
// The returned *big.Int is a fresh value that the caller may modify;
// it never aliases the index's internal state.
func (ix *IPSetIndex) Count() *big.Int {
	if ix == nil || len(ix.cum) == 0 {
		return new(big.Int)
	}
	return new(big.Int).Set(&ix.cum[len(ix.cum)-1])
}

// At returns the address at the given zero-based ordinal in the set.
//
// Addresses are ordered with all IPv4 addresses first, then all IPv6
// addresses, each group in ascending numeric order. If ordinal is
// negative, or greater than or equal to Count, At returns the zero
// Addr, for which [net/netip.Addr.IsValid] is false.
func (ix *IPSetIndex) At(ordinal *big.Int) netip.Addr {
	if ix == nil || len(ix.cum) == 0 || ordinal == nil || ordinal.Sign() < 0 {
		return netip.Addr{}
	}
	total := &ix.cum[len(ix.cum)-1]
	if ordinal.Cmp(total) >= 0 {
		return netip.Addr{}
	}

	// Find the first range whose exclusive end count is strictly
	// greater than ordinal; that range contains the requested member.
	n := len(ix.s.rr)
	i := sort.Search(n, func(i int) bool {
		return ordinal.Cmp(&ix.cum[i+1]) < 0
	})
	r := ix.s.rr[i]

	// The offset within the range is guaranteed to fit in 128 bits,
	// as is any range in a single address family.
	off := bigSubToU128(ordinal, &ix.cum[i])

	from := u128From16(r.from.As16())
	lo, carry := bits.Add64(from.lo, off.lo, 0)
	hi, _ := bits.Add64(from.hi, off.hi, carry)
	addr := uint128{hi: hi, lo: lo}
	if r.from.Is4() {
		return addr.IP4()
	}
	return addr.IP6()
}

// IndexOf returns the zero-based ordinal of addr within the set.
//
// The ordering is the same as for At: IPv4 addresses first, then IPv6
// addresses, each group in ascending numeric order. If addr is invalid,
// carries an IPv6 zone, or is not a member of the set, ok is false and
// the returned *big.Int is nil.
//
// Address families follow the set's existing rules: addr must belong to
// the same family as a range to match. An IPv4-mapped IPv6 address is
// treated as IPv6 and is not implicitly unmapped to IPv4, so it only
// matches a set that explicitly contains that IPv6 address.
//
// On success the returned *big.Int is a fresh value that the caller may
// modify; it never aliases the index's internal state.
func (ix *IPSetIndex) IndexOf(addr netip.Addr) (ordinal *big.Int, ok bool) {
	if ix == nil || ix.s == nil || !addr.IsValid() || addr.Zone() != "" {
		return nil, false
	}
	// Locate the last range starting at or before addr, mirroring
	// IPSet.Contains. A normalized set is sorted with all IPv4 ranges
	// before IPv6 ranges, so cross-family comparisons fall out
	// naturally and v4-mapped v6 addresses compare as IPv6.
	i := sort.Search(len(ix.s.rr), func(i int) bool {
		return addr.Less(ix.s.rr[i].from)
	})
	if i == 0 {
		return nil, false
	}
	i--
	r := ix.s.rr[i]
	if !r.contains(addr) {
		return nil, false
	}
	from := u128From16(r.from.As16())
	cur := u128From16(addr.As16())
	lo, borrow := bits.Sub64(cur.lo, from.lo, 0)
	hi, _ := bits.Sub64(cur.hi, from.hi, borrow)
	off := new(big.Int).SetUint64(hi)
	off.Lsh(off, 64)
	off.Or(off, new(big.Int).SetUint64(lo))
	return off.Add(off, &ix.cum[i]), true
}

// bigSubToU128 returns x-y as a uint128. Both values must be
// non-negative, x >= y, and the result must fit in 128 bits; that
// holds for an ordinal minus the base count of its containing range.
// It performs the subtraction over fixed-size machine words so the At
// hot path allocates nothing and stays safe for concurrent use.
func bigSubToU128(x, y *big.Int) uint128 {
	xw, yw := x.Bits(), y.Bits()
	word := func(b []big.Word, i int) uint64 {
		if i < len(b) {
			return uint64(b[i])
		}
		return 0
	}
	var u uint128
	if bits.UintSize == 32 {
		d0, bo := bits.Sub64(word(xw, 0), word(yw, 0), 0)
		d1, bo := bits.Sub64(word(xw, 1), word(yw, 1), bo)
		d2, bo := bits.Sub64(word(xw, 2), word(yw, 2), bo)
		d3, _ := bits.Sub64(word(xw, 3), word(yw, 3), bo)
		u.lo = d0 | d1<<32
		u.hi = d2 | d3<<32
	} else {
		d0, bo := bits.Sub64(word(xw, 0), word(yw, 0), 0)
		d1, _ := bits.Sub64(word(xw, 1), word(yw, 1), bo)
		u.lo, u.hi = d0, d1
	}
	return u
}
