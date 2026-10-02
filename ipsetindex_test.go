// Copyright 2026 The Inet.Af AUTHORS. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package netipx

import (
	"math/big"
	"net/netip"
	"sync"
	"testing"
)

// enumerateSet returns every IP in s, in index order.
func enumerateSet(s *IPSet) []netip.Addr {
	var out []netip.Addr
	for _, r := range s.Ranges() {
		for ip := r.From(); ; ip = ip.Next() {
			out = append(out, ip)
			if ip == r.To() {
				break
			}
		}
	}
	return out
}

func TestIPSetIndexAgainstEnumeration(t *testing.T) {
	s := mustIPSet(
		"+10.0.0.1-10.0.0.5",
		"+10.0.0.9-10.0.0.10",
		"+192.168.0.0-192.168.0.2",
		"+2001:db8::1-2001:db8::3",
	)
	want := enumerateSet(s)
	idx := s.Index()

	if got := idx.Count(); got.Cmp(big.NewInt(int64(len(want)))) != 0 {
		t.Fatalf("Count() = %v, want %v", got, len(want))
	}
	for i, ip := range want {
		got, ok := idx.At(big.NewInt(int64(i)))
		if !ok || got != ip {
			t.Errorf("At(%d) = %v, %v; want %v", i, got, ok, ip)
		}
		j, ok := idx.IndexOf(ip)
		if !ok || j.Cmp(big.NewInt(int64(i))) != 0 {
			t.Errorf("IndexOf(%v) = %v, %v; want %d", ip, j, ok, i)
		}
	}
	// Holes do not occupy ordinals: 10.0.0.6-10.0.0.8 are absent.
	if _, ok := idx.IndexOf(mustIP("10.0.0.6")); ok {
		t.Error("IndexOf(10.0.0.6) = ok, want not present")
	}
	if got, _ := idx.At(big.NewInt(5)); got != mustIP("10.0.0.9") {
		t.Errorf("At(5) = %v, want 10.0.0.9 (hole skipped)", got)
	}
}

func TestIPSetIndexIPv6All(t *testing.T) {
	var b IPSetBuilder
	b.AddPrefix(netip.PrefixFrom(netip.IPv6Unspecified(), 0))
	s := buildIPSet(&b)
	idx := s.Index()

	// 2^128
	full := new(big.Int).Lsh(big.NewInt(1), 128)
	if got := idx.Count(); got.Cmp(full) != 0 {
		t.Fatalf("Count() = %v, want 2^128", got)
	}
	last := mustIP("ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff")
	tests := []struct {
		i    *big.Int
		want netip.Addr
	}{
		{big.NewInt(0), netip.IPv6Unspecified()},
		{big.NewInt(1), mustIP("::1")},
		// 2^64 crosses the 64-bit carry boundary.
		{new(big.Int).Lsh(big.NewInt(1), 64), mustIP("0:0:0:1::")},
		{new(big.Int).Sub(full, big.NewInt(1)), last},
	}
	for _, tt := range tests {
		got, ok := idx.At(tt.i)
		if !ok || got != tt.want {
			t.Errorf("At(%v) = %v, %v; want %v", tt.i, got, ok, tt.want)
		}
		j, ok := idx.IndexOf(tt.want)
		if !ok || j.Cmp(tt.i) != 0 {
			t.Errorf("IndexOf(%v) = %v, %v; want %v", tt.want, j, ok, tt.i)
		}
	}
	if _, ok := idx.At(full); ok {
		t.Error("At(2^128) = ok, want out of range")
	}
}

func TestIPSetIndexCarry64(t *testing.T) {
	// A small IPv6 range straddling the low 64-bit boundary.
	s := mustIPSet("+2001:db8::ffff:fffe-2001:db8::1:0:1")
	idx := s.Index()
	if got := idx.Count(); got.Cmp(big.NewInt(4)) != 0 {
		t.Fatalf("Count() = %v, want 4", got)
	}
	want := []netip.Addr{
		mustIP("2001:db8::ffff:fffe"),
		mustIP("2001:db8::ffff:ffff"),
		mustIP("2001:db8::1:0:0"),
		mustIP("2001:db8::1:0:1"),
	}
	for i, ip := range want {
		got, ok := idx.At(big.NewInt(int64(i)))
		if !ok || got != ip {
			t.Errorf("At(%d) = %v, %v; want %v", i, got, ok, ip)
		}
	}

	// Full IPv4 space crosses the low 32 bits of its 128-bit form.
	s4 := mustIPSet("+0.0.0.0-255.255.255.255")
	idx4 := s4.Index()
	if got := idx4.Count(); got.Cmp(new(big.Int).Lsh(big.NewInt(1), 32)) != 0 {
		t.Fatalf("Count() = %v, want 2^32", got)
	}
	if got, _ := idx4.At(big.NewInt(1<<32 - 1)); got != mustIP("255.255.255.255") {
		t.Errorf("At(2^32-1) = %v, want 255.255.255.255", got)
	}
}

func TestIPSetIndexMixedFamilies(t *testing.T) {
	s := mustIPSet(
		"+2001:db8::ff-2001:db8::100",
		"+10.0.0.0-10.0.0.2",
	)
	idx := s.Index()
	if got := idx.Count(); got.Cmp(big.NewInt(5)) != 0 {
		t.Fatalf("Count() = %v, want 5", got)
	}
	// IPv4 sorts before IPv6 regardless of insertion order.
	want := []netip.Addr{
		mustIP("10.0.0.0"),
		mustIP("10.0.0.1"),
		mustIP("10.0.0.2"),
		mustIP("2001:db8::ff"),
		mustIP("2001:db8::100"),
	}
	for i, ip := range want {
		got, ok := idx.At(big.NewInt(int64(i)))
		if !ok || got != ip {
			t.Errorf("At(%d) = %v, %v; want %v", i, got, ok, ip)
		}
	}
	// The last address is the top of the IPv6 range.
	last := new(big.Int).Sub(idx.Count(), big.NewInt(1))
	if got, ok := idx.At(last); !ok || got != mustIP("2001:db8::100") {
		t.Errorf("At(Count-1) = %v, %v; want 2001:db8::100", got, ok)
	}
	if j, ok := idx.IndexOf(mustIP("2001:db8::100")); !ok || j.Cmp(last) != 0 {
		t.Errorf("IndexOf(2001:db8::100) = %v, %v; want %v", j, ok, last)
	}
}

func TestIPSetIndex4In6(t *testing.T) {
	// IPv4-mapped IPv6 addresses keep their IPv6 family in the set
	// and are not deduplicated against the same IPv4 address.
	var b IPSetBuilder
	b.Add(mustIP("10.0.0.1"))
	b.Add(netip.MustParseAddr("::ffff:10.0.0.1"))
	s := buildIPSet(&b)
	idx := s.Index()

	if got := idx.Count(); got.Cmp(big.NewInt(2)) != 0 {
		t.Fatalf("Count() = %v, want 2", got)
	}
	if got, _ := idx.At(big.NewInt(0)); got != mustIP("10.0.0.1") {
		t.Errorf("At(0) = %v, want 10.0.0.1", got)
	}
	if got, _ := idx.At(big.NewInt(1)); got != netip.MustParseAddr("::ffff:10.0.0.1") {
		t.Errorf("At(1) = %v, want ::ffff:10.0.0.1", got)
	}
	if j, ok := idx.IndexOf(netip.MustParseAddr("::ffff:10.0.0.1")); !ok || j.Cmp(big.NewInt(1)) != 0 {
		t.Errorf("IndexOf(::ffff:10.0.0.1) = %v, %v; want 1", j, ok)
	}
}

func TestIPSetIndexInvalidInputs(t *testing.T) {
	s := mustIPSet("+10.0.0.0-10.0.0.1")
	idx := s.Index()

	if _, ok := idx.At(nil); ok {
		t.Error("At(nil) = ok, want false")
	}
	if _, ok := idx.At(big.NewInt(-1)); ok {
		t.Error("At(-1) = ok, want false")
	}
	if _, ok := idx.At(big.NewInt(2)); ok {
		t.Error("At(2) = ok, want false (out of range)")
	}
	if _, ok := idx.IndexOf(netip.Addr{}); ok {
		t.Error("IndexOf(invalid) = ok, want false")
	}
	zoned := netip.MustParseAddr("fe80::1%eth0")
	if _, ok := idx.IndexOf(zoned); ok {
		t.Error("IndexOf(zoned) = ok, want false")
	}
	if _, ok := idx.IndexOf(mustIP("10.0.0.2")); ok {
		t.Error("IndexOf(10.0.0.2) = ok, want false (not in set)")
	}

	// Empty set.
	var eb IPSetBuilder
	eset, _ := eb.IPSet()
	eidx := eset.Index()
	if got := eidx.Count(); got.Sign() != 0 {
		t.Errorf("empty Count() = %v, want 0", got)
	}
	if _, ok := eidx.At(big.NewInt(0)); ok {
		t.Error("empty At(0) = ok, want false")
	}
}

func TestIPSetIndexDoesNotMutate(t *testing.T) {
	s := mustIPSet("+10.0.0.0-10.0.0.9", "+2001:db8::-2001:db8::3")
	before := s.Ranges()
	idx := s.Index()

	// Concurrent read-only queries.
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				n := big.NewInt(int64(i % 14))
				ip, ok := idx.At(n)
				if !ok {
					t.Errorf("At(%v) failed", n)
					return
				}
				if _, ok := idx.IndexOf(ip); !ok {
					t.Errorf("IndexOf(%v) failed", ip)
					return
				}
				_ = idx.Count()
			}
		}(g)
	}
	wg.Wait()

	// The set and index inputs are unchanged.
	after := s.Ranges()
	if len(before) != len(after) {
		t.Fatalf("set ranges changed: %v -> %v", before, after)
	}
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("set range %d changed: %v -> %v", i, before[i], after[i])
		}
	}

	// Values returned to callers don't alias index internals.
	c := idx.Count()
	c.Add(c, big.NewInt(1))
	if idx.Count().Cmp(big.NewInt(14)) != 0 {
		t.Error("mutating returned Count affected the index")
	}
	j, _ := idx.IndexOf(mustIP("10.0.0.0"))
	j.Add(j, big.NewInt(1))
	j2, _ := idx.IndexOf(mustIP("10.0.0.0"))
	if j2.Cmp(big.NewInt(0)) != 0 {
		t.Error("mutating returned IndexOf value affected the index")
	}
	// At does not mutate its input.
	in := big.NewInt(3)
	idx.At(in)
	if in.Cmp(big.NewInt(3)) != 0 {
		t.Error("At mutated its input")
	}
}

// benchmarkIndexSet builds a set of n disjoint ranges without
// expanding the address space.
func benchmarkIndexSet(n int) *IPSet {
	var b IPSetBuilder
	for i := 0; i < n; i++ {
		ip := netip.AddrFrom4([4]byte{byte(i >> 16), byte(i >> 8), byte(i), 0})
		b.AddRange(IPRangeFrom(ip, ip.Next()))
	}
	for i := 0; i < n; i++ {
		ip := netip.AddrFrom16([16]byte{0x20, 0x01, 0x0d, 0xb8, byte(i >> 8), byte(i)})
		b.AddRange(IPRangeFrom(ip, ip.Next()))
	}
	return buildIPSet(&b)
}

func BenchmarkIPSetIndexAt(b *testing.B) {
	for _, n := range []int{10, 100, 1000} {
		idx := benchmarkIndexSet(n).Index()
		mid := new(big.Int).Rsh(idx.Count(), 1)
		b.Run(big.NewInt(int64(n)).String(), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if _, ok := idx.At(mid); !ok {
					b.Fatal("At failed")
				}
			}
		})
	}
}

func BenchmarkIPSetIndexOf(b *testing.B) {
	for _, n := range []int{10, 100, 1000} {
		s := benchmarkIndexSet(n)
		idx := s.Index()
		mid := new(big.Int).Rsh(idx.Count(), 1)
		ip, ok := idx.At(mid)
		if !ok {
			b.Fatal("At failed")
		}
		b.Run(big.NewInt(int64(n)).String(), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if _, ok := idx.IndexOf(ip); !ok {
					b.Fatal("IndexOf failed")
				}
			}
		})
	}
}
