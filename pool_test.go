package fastjson

import (
	"strconv"
	"strings"
	"testing"
)

func makeBigArrayJSON(n int) string {
	var sb strings.Builder
	sb.WriteByte('[')
	for i := 0; i < n; i++ {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(`{"key":"some-raw-string-value","n":12345}`)
	}
	sb.WriteByte(']')
	return sb.String()
}

func TestParserPoolRetention(t *testing.T) {
	var pp ParserPool
	pp.Retention = RetentionPolicy{
		MaxInputBufferSize: 1024,
		MaxCachedValues:    64,
		MaxContainerValues: 16,
	}

	// Parse a big input, so the Parser grows a big input buffer,
	// a big Values cache and big per-Value containers.
	p := pp.Get()
	v, err := p.Parse(makeBigArrayJSON(1000))
	if err != nil {
		t.Fatalf("cannot parse big input: %s", err)
	}
	if n := len(v.GetArray()); n != 1000 {
		t.Fatalf("unexpected array size; got %d; want 1000", n)
	}
	pp.Put(p)

	// The capacity exceeding the policy limits must be released on Put.
	p = pp.Get()
	if n := cap(p.b); n > 1024 {
		t.Fatalf("input buffer capacity is retained: %d; want <= 1024", n)
	}
	if n := cap(p.c.vs); n > 64 {
		t.Fatalf("values cache capacity is retained: %d; want <= 64", n)
	}

	// Many subsequent small inputs must keep the capacities bounded
	// and must be served from the re-usable cache.
	for i := 0; i < 100; i++ {
		v, err := p.Parse(`{"a":[1,2,3],"b":"x"}`)
		if err != nil {
			t.Fatalf("cannot parse small input: %s", err)
		}
		if n := v.GetInt("a", "2"); n != 3 {
			t.Fatalf("unexpected value; got %d; want 3", n)
		}
		pp.Put(p)
		p = pp.Get()
		if n := cap(p.b); n > 1024 {
			t.Fatalf("input buffer capacity grew after small inputs: %d; want <= 1024", n)
		}
		if n := cap(p.c.vs); n > 64 {
			t.Fatalf("values cache capacity grew after small inputs: %d; want <= 64", n)
		}
	}

	// Small Values must still be re-used: the root Value pointer
	// must be stable across subsequent parses.
	v1, err := p.Parse(`{"x":1}`)
	if err != nil {
		t.Fatalf("cannot parse: %s", err)
	}
	v2, err := p.Parse(`{"y":2}`)
	if err != nil {
		t.Fatalf("cannot parse: %s", err)
	}
	if v1 != v2 {
		t.Fatalf("expected the root Value to be re-used across parses")
	}
	pp.Put(p)
}

func TestParserPoolRetentionSingleBigContainer(t *testing.T) {
	var pp ParserPool
	pp.Retention = RetentionPolicy{
		// Keep the Values cache itself, but trim big containers
		// stored in the cached Values.
		MaxCachedValues:    4096,
		MaxContainerValues: 16,
	}

	p := pp.Get()
	v, err := p.Parse(makeBigArrayJSON(1000))
	if err != nil {
		t.Fatalf("cannot parse big input: %s", err)
	}
	if n := len(v.GetArray()); n != 1000 {
		t.Fatalf("unexpected array size; got %d; want 1000", n)
	}
	pp.Put(p)

	p = pp.Get()
	if n := cap(p.c.vs); n == 0 {
		t.Fatalf("values cache must be retained under the policy limit")
	}
	for i := range p.c.vs {
		v := &p.c.vs[i]
		if n := cap(v.a); n > 16 {
			t.Fatalf("array capacity is retained in the cached value %d: %d; want <= 16", i, n)
		}
		if n := cap(v.o.kvs); n > 16 {
			t.Fatalf("object capacity is retained in the cached value %d: %d; want <= 16", i, n)
		}
	}

	// The trimmed cache must remain usable.
	v, err = p.Parse(`[1,2,3]`)
	if err != nil {
		t.Fatalf("cannot parse: %s", err)
	}
	if n := len(v.GetArray()); n != 3 {
		t.Fatalf("unexpected array size; got %d; want 3", n)
	}
	pp.Put(p)
}

func TestParserPoolDefaultRetention(t *testing.T) {
	// The zero RetentionPolicy must preserve the original behavior:
	// all the capacity is retained.
	var pp ParserPool

	p := pp.Get()
	if _, err := p.Parse(makeBigArrayJSON(1000)); err != nil {
		t.Fatalf("cannot parse big input: %s", err)
	}
	bufCap := cap(p.b)
	cacheCap := cap(p.c.vs)
	if bufCap == 0 || cacheCap == 0 {
		t.Fatalf("expected non-zero capacities after parsing big input")
	}
	pp.Put(p)

	p = pp.Get()
	if n := cap(p.b); n != bufCap {
		t.Fatalf("input buffer capacity must be retained; got %d; want %d", n, bufCap)
	}
	if n := cap(p.c.vs); n != cacheCap {
		t.Fatalf("values cache capacity must be retained; got %d; want %d", n, cacheCap)
	}
	pp.Put(p)
}

func TestArenaPoolRetention(t *testing.T) {
	var ap ArenaPool
	ap.Retention = RetentionPolicy{
		MaxInputBufferSize: 128,
		MaxCachedValues:    32,
		MaxContainerValues: 8,
	}

	// Build a big document, so the Arena grows a big buffer,
	// a big Values cache and a big container.
	a := ap.Get()
	a.NewString(strings.Repeat("x", 4096))
	ary := a.NewArray()
	for i := 0; i < 1000; i++ {
		ary.SetArrayItem(i, a.NewNumberInt(i))
	}
	ap.Put(a)

	// The capacity exceeding the policy limits must be released on Put.
	a = ap.Get()
	if n := cap(a.b); n > 128 {
		t.Fatalf("buffer capacity is retained: %d; want <= 128", n)
	}
	if n := cap(a.c.vs); n > 32 {
		t.Fatalf("values cache capacity is retained: %d; want <= 32", n)
	}

	// Small Values must still be re-usable after the shrink.
	for i := 0; i < 100; i++ {
		o := a.NewObject()
		o.Set("n", a.NewNumberInt(i))
		if s := o.String(); s != `{"n":`+strconv.Itoa(i)+`}` {
			t.Fatalf("unexpected object: %q", s)
		}
		ap.Put(a)
		a = ap.Get()
		if n := cap(a.b); n > 128 {
			t.Fatalf("buffer capacity grew after small values: %d; want <= 128", n)
		}
		if n := cap(a.c.vs); n > 32 {
			t.Fatalf("values cache capacity grew after small values: %d; want <= 32", n)
		}
	}
	ap.Put(a)
}

func TestArenaPoolDefaultRetention(t *testing.T) {
	// The zero RetentionPolicy must preserve the original behavior:
	// all the capacity is retained.
	var ap ArenaPool

	a := ap.Get()
	ary := a.NewArray()
	for i := 0; i < 1000; i++ {
		ary.SetArrayItem(i, a.NewNumberInt(i))
	}
	cacheCap := cap(a.c.vs)
	if cacheCap == 0 {
		t.Fatalf("expected non-zero cache capacity")
	}
	ap.Put(a)

	a = ap.Get()
	if n := cap(a.c.vs); n != cacheCap {
		t.Fatalf("values cache capacity must be retained; got %d; want %d", n, cacheCap)
	}
	ap.Put(a)
}
