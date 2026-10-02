// Copyright 2026 The Inet.Af AUTHORS. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package netipx

import (
	"math/big"
	"net/netip"
	"sort"
)

// IPSetIndex is a reusable ordinal index over an immutable IPSet.
//
// It assigns every IP in the set a dense ordinal: IPv4 addresses sort
// before IPv6 addresses, each in ascending numerical order, and gaps
// between ranges do not occupy ordinals. IPv4-mapped IPv6 addresses
// keep the address family they have in the set (they sort as IPv6).
//
// The index is built once from the set's normalized ranges, without
// expanding individual addresses, so building it is proportional to
// the number of ranges, not the number of addresses.
//
// An IPSetIndex is a snapshot of the set at build time. It is safe
// for concurrent use, and read-only queries never modify the index or
// the set it was built from.
type IPSetIndex struct {
	// rr is a copy of the set's normalized ranges.
	rr []IPRange

	// starts[i] is the ordinal of rr[i].From().
	starts []*big.Int

	// count is the total number of addresses in the set.
	count *big.Int
}

// Index returns an IPSetIndex over s.
//
// The returned index is a snapshot: later changes to the builder or
// set used to construct it do not affect the index.
func (s *IPSet) Index() *IPSetIndex {
	idx := &IPSetIndex{
		rr:     append([]IPRange(nil), s.rr...),
		starts: make([]*big.Int, len(s.rr)),
		count:  new(big.Int),
	}
	for i, r := range s.rr {
		idx.starts[i] = new(big.Int).Set(idx.count)
		idx.count.Add(idx.count, rangeSize(r))
	}
	return idx
}

// Count returns the exact number of addresses in the indexed set.
// It can represent values of 2^128 and beyond.
//
// The returned value is a copy; mutating it does not affect the index.
func (x *IPSetIndex) Count() *big.Int {
	return new(big.Int).Set(x.count)
}

// At returns the address at ordinal i in the indexed set.
//
// If i is nil, negative, or greater than or equal to Count, At
// returns the zero Addr and ok=false. At does not modify i.
func (x *IPSetIndex) At(i *big.Int) (ip netip.Addr, ok bool) {
	if i == nil || i.Sign() < 0 || i.Cmp(x.count) >= 0 {
		return netip.Addr{}, false
	}
	// Find the last range whose start ordinal is <= i.
	j := sort.Search(len(x.starts), func(j int) bool {
		return x.starts[j].Cmp(i) > 0
	}) - 1
	off := new(big.Int).Sub(i, x.starts[j])
	v := addrBig(x.rr[j].from)
	v.Add(v, off)
	return addrFromBig(v, x.rr[j].from.Is4()), true
}

// IndexOf returns the ordinal of ip in the indexed set.
//
// If ip is invalid, has an IPv6 zone, or is not in the set, IndexOf
// returns ok=false. The returned value is a fresh copy; mutating it
// does not affect the index.
func (x *IPSetIndex) IndexOf(ip netip.Addr) (i *big.Int, ok bool) {
	if !ip.IsValid() || ip.Zone() != "" {
		return nil, false
	}
	j := sort.Search(len(x.rr), func(j int) bool {
		return ip.Less(x.rr[j].from)
	})
	if j == 0 {
		return nil, false
	}
	j--
	r := x.rr[j]
	if r.to.Less(ip) {
		return nil, false
	}
	off := addrBig(ip)
	off.Sub(off, addrBig(r.from))
	return off.Add(off, x.starts[j]), true
}

// rangeSize returns the number of addresses in r.
func rangeSize(r IPRange) *big.Int {
	size := addrBig(r.to)
	size.Sub(size, addrBig(r.from))
	return size.Add(size, big.NewInt(1))
}

// addrBig returns the numerical value of ip as a big.Int.
func addrBig(ip netip.Addr) *big.Int {
	a := ip.As16()
	return new(big.Int).SetBytes(a[:])
}

// addrFromBig returns the address with numerical value v, as an IPv4
// address if is4 and an IPv6 address otherwise. v must fit in the
// address family.
func addrFromBig(v *big.Int, is4 bool) netip.Addr {
	var a [16]byte
	v.FillBytes(a[:])
	if is4 {
		return netip.AddrFrom4([4]byte{a[12], a[13], a[14], a[15]})
	}
	return netip.AddrFrom16(a)
}
