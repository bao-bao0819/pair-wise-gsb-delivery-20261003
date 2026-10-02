package netipx

import (
	"math/big"
	"net/netip"
	"reflect"
	"strconv"
	"sync"
	"testing"
)

// enumerateSet returns every member of s in the index's canonical
// order: IPv4 addresses first, then IPv6 addresses, each group in
// ascending numeric order. It is only suitable for the small sets used
// in these tests.
func enumerateSet(t *testing.T, s *IPSet) []netip.Addr {
	t.Helper()
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

func TestIPSetIndexEnumeration(t *testing.T) {
	tests := []struct {
		name    string
		build   func(*IPSetBuilder)
		missing []string
	}{
		{name: "empty", build: func(*IPSetBuilder) {}},
		{
			name:  "v4_singleton",
			build: func(b *IPSetBuilder) { b.Add(mustIP("1.2.3.4")) },
			missing: []string{
				"1.2.3.3", "1.2.3.5", "0.0.0.0", "255.255.255.255",
			},
		},
		{
			name: "v4_gaps",
			build: func(b *IPSetBuilder) {
				b.AddRange(MustParseIPRange("10.0.0.0-10.0.0.2"))
				b.AddRange(MustParseIPRange("10.0.0.5-10.0.0.7"))
			},
			missing: []string{"10.0.0.3", "10.0.0.4"},
		},
		{
			name: "v4_removed_middle",
			build: func(b *IPSetBuilder) {
				b.AddPrefix(mustIPPrefix("10.0.0.0/30")) // 10.0.0.0-10.0.0.3
				b.Remove(mustIP("10.0.0.1"))
			},
			missing: []string{"10.0.0.1", "9.255.255.255", "10.0.0.4"},
		},
		{
			name: "v4_cross_256",
			build: func(b *IPSetBuilder) {
				b.AddRange(MustParseIPRange("10.0.0.254-10.0.1.2"))
			},
			missing: []string{"10.0.0.253", "10.0.1.3"},
		},
		{
			name:    "v6_singleton",
			build:   func(b *IPSetBuilder) { b.Add(mustIP("2001:db8::1")) },
			missing: []string{"2001:db8::", "2001:db8::2"},
		},
		{
			name: "v6_gaps",
			build: func(b *IPSetBuilder) {
				b.AddRange(MustParseIPRange("2001:db8::-2001:db8::2"))
				b.AddRange(MustParseIPRange("2001:db8::5-2001:db8::7"))
			},
			missing: []string{"2001:db8::3", "2001:db8::4"},
		},
		{
			name: "v6_removed_middle",
			build: func(b *IPSetBuilder) {
				b.AddPrefix(mustIPPrefix("2001:db8::/126")) // ...::-...::3
				b.Remove(mustIP("2001:db8::1"))
			},
			missing: []string{"2001:db8::1"},
		},
		{
			name: "mixed",
			build: func(b *IPSetBuilder) {
				b.AddRange(MustParseIPRange("1.2.3.4-1.2.3.5"))
				b.AddRange(MustParseIPRange("2001:db8::-2001:db8::2"))
			},
			missing: []string{"1.2.3.3", "1.2.3.6", "2001:db8::3"},
		},
		{
			name: "mixed_with_holes",
			build: func(b *IPSetBuilder) {
				b.AddPrefix(mustIPPrefix("10.0.0.0/30"))
				b.Remove(mustIP("10.0.0.1"))
				b.AddPrefix(mustIPPrefix("2001:db8::/126"))
				b.Remove(mustIP("2001:db8::1"))
			},
			missing: []string{"10.0.0.1", "2001:db8::1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var b IPSetBuilder
			tt.build(&b)
			s := buildIPSet(&b)
			before := s.Ranges()
			ix := s.Index()

			members := enumerateSet(t, s)

			if got, want := ix.Count(), big.NewInt(int64(len(members))); got.Cmp(want) != 0 {
				t.Fatalf("Count = %s, want %d", got, want)
			}

			// Every enumerated member round-trips through At and
			// IndexOf at its position, and IndexOf agrees with
			// Contains.
			for i, ip := range members {
				ordinal := big.NewInt(int64(i))
				if got := ix.At(ordinal); got != ip {
					t.Fatalf("At(%d) = %v, want %v", i, got, ip)
				}
				got, ok := ix.IndexOf(ip)
				if !ok {
					t.Fatalf("IndexOf(%v) = !ok, want %d", ip, i)
				}
				if got.Cmp(ordinal) != 0 {
					t.Fatalf("IndexOf(%v) = %s, want %d", ip, got, i)
				}
				if !s.Contains(ip) {
					t.Fatalf("Contains(%v) = false", ip)
				}
			}

			// Gaps and out-of-set addresses have no ordinal.
			for _, m := range tt.missing {
				ip := mustIP(m)
				if s.Contains(ip) {
					t.Errorf("test setup error: Contains(%v) = true", ip)
				}
				if got, ok := ix.IndexOf(ip); ok {
					t.Errorf("IndexOf(%v) = %s, ok=true; want !ok", ip, got)
				}
			}

			// Building and reading the index must not alter the set.
			if got := s.Ranges(); !reflect.DeepEqual(got, before) {
				t.Errorf("set ranges changed after indexing: before %v, after %v", before, got)
			}
		})
	}
}

func TestIPSetIndexExtremes(t *testing.T) {
	var v4b IPSetBuilder
	v4b.AddPrefix(mustIPPrefix("0.0.0.0/0"))
	v4All := buildIPSet(&v4b)
	v6All := func() *IPSet {
		var b IPSetBuilder
		b.AddPrefix(mustIPPrefix("::/0"))
		return buildIPSet(&b)
	}()
	both := func() *IPSet {
		var b IPSetBuilder
		b.AddPrefix(mustIPPrefix("0.0.0.0/0"))
		b.AddPrefix(mustIPPrefix("::/0"))
		return buildIPSet(&b)
	}()

	two32 := new(big.Int).Lsh(big.NewInt(1), 32)       // 2^32
	two128 := new(big.Int).Lsh(big.NewInt(1), 128)     // 2^128
	v4Last := big.NewInt(0).Sub(two32, big.NewInt(1))  // 2^32-1
	v6Last := big.NewInt(0).Sub(two128, big.NewInt(1)) // 2^128-1
	bothLast := big.NewInt(0).Sub(new(big.Int).Add(two32, two128), big.NewInt(1))

	if got := v4All.Index().Count(); got.Cmp(two32) != 0 {
		t.Errorf("v4 /0 Count = %s, want 2^32", got)
	}
	if got := v6All.Index().Count(); got.Cmp(two128) != 0 {
		t.Errorf("v6 /0 Count = %s, want 2^128", got)
	}
	if got := both.Index().Count(); got.Cmp(new(big.Int).Add(two32, two128)) != 0 {
		t.Errorf("dual /0 Count = %s, want 2^32+2^128", got)
	}

	// Last address of each universe.
	if got, want := v4All.Index().At(v4Last), mustIP("255.255.255.255"); got != want {
		t.Errorf("v4 At(2^32-1) = %v, want %v", got, want)
	}
	if got, want := v6All.Index().At(v6Last), mustIP("ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff"); got != want {
		t.Errorf("v6 At(2^128-1) = %v, want %v", got, want)
	}
	if got, want := both.Index().At(v4Last), mustIP("255.255.255.255"); got != want {
		t.Errorf("dual At(2^32-1) = %v, want %v", got, want)
	}
	if got, want := both.Index().At(two32), mustIP("::"); got != want {
		t.Errorf("dual At(2^32) = %v, want %v", got, want)
	}
	if got, want := both.Index().At(bothLast), mustIP("ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff"); got != want {
		t.Errorf("dual At(2^32+2^128-1) = %v, want %v", got, want)
	}

	// IndexOf on the very last addresses.
	if got, ok := v6All.Index().IndexOf(mustIP("ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff")); !ok || got.Cmp(v6Last) != 0 {
		t.Errorf("v6 IndexOf(last) = %s,%v; want %s", got, ok, v6Last)
	}
	if got, ok := both.Index().IndexOf(mustIP("::")); !ok || got.Cmp(two32) != 0 {
		t.Errorf("dual IndexOf(::) = %s,%v; want %s", got, ok, two32)
	}
	if got, ok := both.Index().IndexOf(mustIP("255.255.255.255")); !ok || got.Cmp(v4Last) != 0 {
		t.Errorf("dual IndexOf(v4last) = %s,%v; want %s", got, ok, v4Last)
	}
}

func TestIPSetIndex64BitCarry(t *testing.T) {
	// Four addresses straddling the low/high 64-bit word boundary of
	// the 128-bit numeric space. The low 64-bit word starts two below
	// its maximum, so enumerating the range first overflows lo64 into
	// hi64 and then increments the high word again.
	loMax := [8]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
	loTwoBeforeMax := [8]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xfe}
	from := addr16([8]byte{}, loTwoBeforeMax)
	to := addr16(
		[8]byte{0, 0, 0, 0, 0, 0, 0, 1},
		[8]byte{0, 0, 0, 0, 0, 0, 0, 1},
	)
	var b2 IPSetBuilder
	b2.AddRange(IPRangeFrom(from, to))
	s := buildIPSet(&b2)
	ix := s.Index()
	if got, want := ix.Count(), big.NewInt(4); got.Cmp(want) != 0 {
		t.Fatalf("Count = %s, want 4", got)
	}
	want := []netip.Addr{
		addr16([8]byte{}, loTwoBeforeMax),
		addr16([8]byte{}, loMax),
		addr16([8]byte{0, 0, 0, 0, 0, 0, 0, 1}, [8]byte{}),
		to,
	}
	for i, w := range want {
		if got := ix.At(big.NewInt(int64(i))); got != w {
			t.Errorf("At(%d) = %v, want %v", i, got, w)
		}
		if got, ok := ix.IndexOf(w); !ok || got.Int64() != int64(i) {
			t.Errorf("IndexOf(%v) = %s,%v; want %d", w, got, ok, i)
		}
	}
}

func addr16(hi, lo [8]byte) netip.Addr {
	var a [16]byte
	copy(a[:8], hi[:])
	copy(a[8:], lo[:])
	return netip.AddrFrom16(a)
}

func TestIPSetIndexInvalid(t *testing.T) {
	var b IPSetBuilder
	b.Add(mustIP("1.2.3.4"))
	b.Add(mustIP("2001:db8::1"))
	ix := buildIPSet(&b).Index()

	// Negative ordinal.
	if got := ix.At(big.NewInt(-1)); got.IsValid() {
		t.Errorf("At(-1) = %v, want invalid", got)
	}
	// Ordinal equal to Count, and beyond.
	if got := ix.At(big.NewInt(2)); got.IsValid() {
		t.Errorf("At(Count) = %v, want invalid", got)
	}
	if got := ix.At(new(big.Int).Lsh(big.NewInt(1), 200)); got.IsValid() {
		t.Errorf("At(2^200) = %v, want invalid", got)
	}
	// Nil ordinal.
	if got := ix.At(nil); got.IsValid() {
		t.Errorf("At(nil) = %v, want invalid", got)
	}

	// Invalid and zoned addresses have no ordinal.
	if _, ok := ix.IndexOf(netip.Addr{}); ok {
		t.Errorf("IndexOf(invalid) = ok, want !ok")
	}
	if _, ok := ix.IndexOf(mustIP("2001:db8::1%eth0")); ok {
		t.Errorf("IndexOf(zoned) = ok, want !ok")
	}

	// The zero index and a nil set behave as empty.
	var zero IPSetIndex
	if got := zero.Count(); got.Sign() != 0 {
		t.Errorf("zero Count = %s, want 0", got)
	}
	if got := zero.At(big.NewInt(0)); got.IsValid() {
		t.Errorf("zero At(0) = %v, want invalid", got)
	}
	if _, ok := zero.IndexOf(mustIP("1.2.3.4")); ok {
		t.Errorf("zero IndexOf = ok, want !ok")
	}
	nilIx := (*IPSet)(nil).Index()
	if got := nilIx.Count(); got.Sign() != 0 {
		t.Errorf("nil-set Count = %s, want 0", got)
	}
	if got := nilIx.At(big.NewInt(0)); got.IsValid() {
		t.Errorf("nil-set At(0) = %v, want invalid", got)
	}
}

func TestIPSetIndexMappedV6(t *testing.T) {
	v4 := mustIP("1.2.3.4")
	mapped := netip.AddrFrom16(v4.As16()) // ::ffff:1.2.3.4, kept as IPv6
	if mapped.Is4() || !mapped.Is4In6() {
		t.Fatalf("test setup error: %v is not v4-mapped v6", mapped)
	}

	// A v4-only set does not report the mapped v6 form.
	var vb IPSetBuilder
	vb.Add(v4)
	v4Set := buildIPSet(&vb)
	if v4Set.Contains(mapped) {
		t.Errorf("Contains(mapped) = true for v4 set")
	}
	if _, ok := v4Set.Index().IndexOf(mapped); ok {
		t.Errorf("IndexOf(mapped) = ok for v4 set; want !ok")
	}

	// A set that explicitly contains the mapped v6 address indexes it
	// as an IPv6 member and never deduplicates it into the v4 member.
	var mb IPSetBuilder
	mb.Add(v4)
	mb.Add(mapped)
	mixed := buildIPSet(&mb)
	ix := mixed.Index()
	if got, want := ix.Count(), big.NewInt(2); got.Cmp(want) != 0 {
		t.Fatalf("Count = %s, want 2", got)
	}
	if got := ix.At(big.NewInt(0)); got != v4 {
		t.Errorf("At(0) = %v, want %v", got, v4)
	}
	if got := ix.At(big.NewInt(1)); got != mapped {
		t.Errorf("At(1) = %v, want %v", got, mapped)
	}
	if got, ok := ix.IndexOf(mapped); !ok || got.Cmp(big.NewInt(1)) != 0 {
		t.Errorf("IndexOf(mapped) = %s,%v; want 1", got, ok)
	}
	if got, ok := ix.IndexOf(v4); !ok || got.Cmp(big.NewInt(0)) != 0 {
		t.Errorf("IndexOf(v4) = %s,%v; want 0", got, ok)
	}
}

func TestIPSetIndexNoAliasing(t *testing.T) {
	var b IPSetBuilder
	b.AddRange(MustParseIPRange("1.2.3.4-1.2.3.5"))
	b.AddRange(MustParseIPRange("2001:db8::-2001:db8::1"))
	ix := buildIPSet(&b).Index()

	c1 := ix.Count()
	c1.SetInt64(999)
	if got := ix.Count(); got.Cmp(big.NewInt(4)) != 0 {
		t.Errorf("Count state mutated via returned pointer: got %s, want 4", got)
	}

	ordinal, ok := ix.IndexOf(mustIP("2001:db8::1"))
	if !ok {
		t.Fatal("IndexOf missing")
	}
	ordinal.SetInt64(0)
	if got, ok := ix.IndexOf(mustIP("2001:db8::1")); !ok || got.Cmp(big.NewInt(3)) != 0 {
		t.Errorf("IndexOf state mutated via returned pointer: got %s,%v, want 3", got, ok)
	}
}

func TestIPSetIndexConcurrent(t *testing.T) {
	var b IPSetBuilder
	for i := 0; i < 64; i += 2 {
		a := netip.AddrFrom4([4]byte{10, 0, 0, byte(i)})
		b.AddRange(IPRangeFrom(a, netip.AddrFrom4([4]byte{10, 0, 0, byte(i + 1)})))
	}
	s := buildIPSet(&b)
	before := s.Ranges()
	ix := s.Index()

	const goroutines = 16
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				n := big.NewInt(int64(j % 64))
				ip := ix.At(n)
				if !ip.IsValid() {
					t.Errorf("At(%d) invalid", j%64)
					return
				}
				if got, ok := ix.IndexOf(ip); !ok || got.Cmp(n) != 0 {
					t.Errorf("IndexOf(%v) = %s,%v; want %d", ip, got, ok, n)
					return
				}
				if ix.Count().Cmp(big.NewInt(64)) != 0 {
					t.Errorf("Count != 64 under concurrency")
					return
				}
			}
		}()
	}
	wg.Wait()

	if got := s.Ranges(); !reflect.DeepEqual(got, before) {
		t.Errorf("set ranges changed after concurrent reads: before %v, after %v", before, got)
	}
}

// sparseV6Set builds n non-adjacent singleton IPv6 ranges without
// touching the address space they span.
func sparseV6Set(n int) *IPSet {
	var b IPSetBuilder
	for i := 0; i < n; i++ {
		ip := netip.AddrFrom16([16]byte{
			byte(i >> 24), byte(i >> 16), byte(i >> 8), byte(i),
			0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
		})
		b.Add(ip)
	}
	return buildIPSet(&b)
}

func BenchmarkIPSetIndex(b *testing.B) {
	for _, n := range []int{1, 8, 64, 512, 4096} {
		s := sparseV6Set(n)
		b.Run("ranges="+strconv.Itoa(n), func(b *testing.B) {
			b.ReportAllocs()
			var sink *IPSetIndex
			for i := 0; i < b.N; i++ {
				sink = s.Index()
			}
			_ = sink
		})
	}

	// A single ::/0 range spans the entire 2^128 space but costs the
	// same to index as one range.
	var fullB IPSetBuilder
	fullB.AddPrefix(mustIPPrefix("::/0"))
	full := buildIPSet(&fullB)
	b.Run("ranges=1/v6slash0", func(b *testing.B) {
		b.ReportAllocs()
		var sink *IPSetIndex
		for i := 0; i < b.N; i++ {
			sink = full.Index()
		}
		_ = sink
	})
}

func BenchmarkIPSetIndexAt(b *testing.B) {
	for _, n := range []int{8, 64, 4096} {
		s := sparseV6Set(n)
		ix := s.Index()
		ordinal := big.NewInt(int64(n - 1)) // last range: bisects worst case
		b.Run("ranges="+strconv.Itoa(n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				sinkAddr = ix.At(ordinal)
			}
		})
	}

	// Full IPv6 universe: index math over 2^128 without enumeration.
	var fullB IPSetBuilder
	fullB.AddPrefix(mustIPPrefix("::/0"))
	full := buildIPSet(&fullB).Index()
	b.Run("v6slash0", func(b *testing.B) {
		b.ReportAllocs()
		ordinal := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 128), big.NewInt(1))
		for i := 0; i < b.N; i++ {
			sinkAddr = full.At(ordinal)
		}
	})
}

func BenchmarkIPSetIndexIndexOf(b *testing.B) {
	for _, n := range []int{8, 64, 4096} {
		s := sparseV6Set(n)
		ix := s.Index()
		ip := s.Ranges()[n-1].To()
		b.Run("ranges="+strconv.Itoa(n), func(b *testing.B) {
			b.ReportAllocs()
			var sink *big.Int
			for i := 0; i < b.N; i++ {
				sink, _ = ix.IndexOf(ip)
			}
			_ = sink
		})
	}
}

var sinkIndexCount *big.Int

func BenchmarkIPSetIndexCount(b *testing.B) {
	var sb IPSetBuilder
	sb.AddPrefix(mustIPPrefix("0.0.0.0/0"))
	sb.AddPrefix(mustIPPrefix("::/0"))
	ix := buildIPSet(&sb).Index()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sinkIndexCount = ix.Count()
	}
}
