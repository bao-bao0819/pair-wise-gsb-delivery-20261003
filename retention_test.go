package fastjson

import (
	"strings"
	"testing"
)

// These tests assert on observable slice capacities inside the package. They do
// not rely on process RSS or on forcing a GC: a released buffer is observable
// as a nil/zero-capacity slice, while a retained buffer keeps its capacity.

func bigJSON(n int) string {
	var b strings.Builder
	b.Grow(2*n + 8)
	b.WriteByte('[')
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('1')
	}
	b.WriteByte(']')
	return b.String()
}

// After a large input the input buffer and value cache are oversized. Once the
// Parser is returned to a bounded pool, repeated small inputs must reuse small
// buffers instead of permanently retaining the big allocation.
func TestParserPoolRetentionBigInputThenSmallInputs(t *testing.T) {
	const (
		inputLimit = 128
		valueLimit = 16
	)
	pp := NewParserPool(RetentionPolicy{
		MaxInputBytes:  inputLimit,
		MaxValues:      valueLimit,
		MaxArrayValues: valueLimit,
	})

	p := pp.Get()
	if _, err := p.Parse(bigJSON(10000)); err != nil {
		t.Fatalf("cannot parse big json: %s", err)
	}
	if cap(p.b) <= inputLimit || cap(p.c.vs) <= valueLimit {
		t.Fatalf("precondition: expected buffers to grow big; got cap(b)=%d cap(vs)=%d",
			cap(p.b), cap(p.c.vs))
	}

	pp.Put(p)
	if cap(p.b) != 0 {
		t.Fatalf("big input buffer must be released on Put; got cap(b)=%d", cap(p.b))
	}
	if cap(p.c.vs) != 0 {
		t.Fatalf("big value cache must be released on Put; got cap(vs)=%d", cap(p.c.vs))
	}

	// Many subsequent small requests must never recreate or retain big buffers.
	for range 50 {
		p = pp.Get()
		if _, err := p.Parse(`{"k":[1,2,3],"s":"abc"}`); err != nil {
			t.Fatalf("cannot parse small json: %s", err)
		}
		if cap(p.b) > inputLimit {
			t.Fatalf("small input retained oversized input buffer: cap(b)=%d", cap(p.b))
		}
		if cap(p.c.vs) > valueLimit {
			t.Fatalf("small input retained oversized value cache: cap(vs)=%d", cap(p.c.vs))
		}
		// Values handed out before Put remain valid for the request lifetime.
		if v := p.c.vs; len(v) == 0 || v[0].t != TypeObject {
			t.Fatalf("unexpected parsed root value")
		}
		pp.Put(p)
	}
}

// Small buffers within the limits keep their capacity and stay reusable across
// parse cycles; the policy only releases buffers that exceed a limit.
func TestParserShrinkKeepsSmallBuffersReusable(t *testing.T) {
	rp := RetentionPolicy{
		MaxInputBytes:  4096,
		MaxValues:      128,
		MaxArrayValues: 64,
		MaxObjectItems: 64,
	}

	var p Parser
	if _, err := p.Parse(`{"a":[1,2,3],"b":{"c":"d"}}`); err != nil {
		t.Fatalf("cannot parse: %s", err)
	}

	inputCap := cap(p.b)
	valueCap := cap(p.c.vs)

	p.shrink(rp)
	if cap(p.b) != inputCap {
		t.Fatalf("small input buffer must be retained; got %d want %d", cap(p.b), inputCap)
	}
	if cap(p.c.vs) != valueCap {
		t.Fatalf("small value cache must be retained; got %d want %d", cap(p.c.vs), valueCap)
	}

	// The retained backing storage must still serve subsequent parses correctly.
	for range 10 {
		v, err := p.Parse(`[true,false,null]`)
		if err != nil {
			t.Fatalf("cannot reparse after shrink: %s", err)
		}
		arr, arrErr := v.Array()
		if arrErr != nil || v.Type() != TypeArray || arr[0].Type() != TypeTrue {
			t.Fatalf("unexpected value after reuse: %s", v)
		}
		if cap(p.b) != inputCap || cap(p.c.vs) != valueCap {
			t.Fatalf("reuse must not change retained capacity: cap(b)=%d cap(vs)=%d",
				cap(p.b), cap(p.c.vs))
		}
		p.shrink(rp)
	}
}

// Per-value limits release the backing storage of a single huge array/object,
// while values within the limits keep their capacity. Values are constructed
// directly so their slice headers cannot alias neighboring cache slots (the
// dense Value packing in a cache can overlap grown headers, which is exactly
// why cache.shrink iterates only the live slots).
func TestValueShrinkReleasesSingleHugeContainers(t *testing.T) {
	rp := RetentionPolicy{
		MaxArrayValues: 64,
		MaxObjectItems: 64,
	}

	bigArray := &Value{t: TypeArray, a: make([]*Value, 500)}
	for i := range bigArray.a {
		bigArray.a[i] = valueTrue
	}
	bigObject := &Value{t: TypeObject}
	bigObject.o.kvs = make([]kv, 500)
	for i := range bigObject.o.kvs {
		bigObject.o.kvs[i] = kv{k: "k", v: valueTrue}
	}
	bigObject.o.keysUnescaped = true

	smallArray := &Value{t: TypeArray, a: make([]*Value, 2, 8)}
	smallObject := &Value{t: TypeObject}
	smallObject.o.kvs = make([]kv, 1, 8)

	smallArray.shrink(rp)
	smallObject.shrink(rp)
	if c := cap(smallArray.a); c != 8 {
		t.Fatalf("small array capacity must be retained; got %d", c)
	}
	if c := cap(smallObject.o.kvs); c != 8 {
		t.Fatalf("small object capacity must be retained; got %d", c)
	}

	bigArray.shrink(rp)
	bigObject.shrink(rp)
	if c := cap(bigArray.a); c != 0 {
		t.Fatalf("huge array backing storage must be released; got cap=%d", c)
	}
	if c := cap(bigObject.o.kvs); c != 0 {
		t.Fatalf("huge object backing storage must be released; got cap=%d", c)
	}
	if bigObject.o.keysUnescaped {
		t.Fatalf("released object must reset keysUnescaped")
	}

	// Released containers can be reused again without issues.
	bigArray.t = TypeArray
	bigArray.SetArrayItem(0, valueNull)
	if len(bigArray.a) != 1 || bigArray.a[0].t != TypeNull {
		t.Fatalf("released array cannot be reused")
	}
}

// A cache populated with independent, within-limit Values keeps its backing
// storage on shrink; the value-level loop must not discard small containers.
func TestCacheShrinkRetainsSmallContainers(t *testing.T) {
	rp := RetentionPolicy{
		MaxValues:      128,
		MaxArrayValues: 64,
		MaxObjectItems: 64,
	}
	var c cache
	for i := 0; i < 5; i++ {
		v := c.getValue()
		v.t = TypeArray
		v.a = make([]*Value, i+1)
	}
	capVs := cap(c.vs)
	c.shrink(rp)
	if c := cap(c.vs); c != capVs {
		t.Fatalf("small cache must be retained; got %d want %d", c, capVs)
	}
	if len(c.vs) != 0 {
		t.Fatalf("cache must be empty after shrink")
	}
}

// ArenaPool applies the same policy: big marshaling buffer and value cache are
// released on Put, while small arenas keep and reuse their capacity.
func TestArenaPoolRetention(t *testing.T) {
	const (
		inputLimit = 128
		valueLimit = 16
	)
	ap := NewArenaPool(RetentionPolicy{
		MaxInputBytes:  inputLimit,
		MaxValues:      valueLimit,
		MaxArrayValues: valueLimit,
		MaxObjectItems: valueLimit,
	})

	a := ap.Get()
	// Make the marshaling buffer big via many NewString calls.
	a.NewString(strings.Repeat("x", 10000))
	arr := a.NewArray()
	for i := 0; i < 10000; i++ {
		arr.SetArrayItem(i, a.NewNumberInt(i))
	}
	if cap(a.b) <= inputLimit || cap(a.c.vs) <= valueLimit {
		t.Fatalf("precondition: expected arena buffers to grow; got cap(b)=%d cap(vs)=%d",
			cap(a.b), cap(a.c.vs))
	}

	ap.Put(a)
	if cap(a.b) != 0 {
		t.Fatalf("big arena buffer must be released; got cap(b)=%d", cap(a.b))
	}
	if cap(a.c.vs) != 0 {
		t.Fatalf("big arena value cache must be released; got cap=%d", cap(a.c.vs))
	}

	// Subsequent small workloads stay small and keep reusing retained capacity.
	for range 20 {
		a = ap.Get()
		o := a.NewObject()
		o.Set("k", a.NewString("v"))
		// Append the marshaled value into the arena's own buffer to exercise
		// small, within-limit reuse of a.b.
		a.b = o.MarshalTo(a.b[:0])
		if cap(a.b) > inputLimit {
			t.Fatalf("small arena retained oversized buffer: cap(b)=%d", cap(a.b))
		}
		if cap(a.c.vs) > valueLimit {
			t.Fatalf("small arena retained oversized value cache: cap(vs)=%d", cap(a.c.vs))
		}
		ap.Put(a)
	}
}

// With no policy configured, both pools keep the original semantics: Reset is
// performed for Arenas but every buffer retains its full capacity.
func TestPoolsZeroPolicyRetainEverything(t *testing.T) {
	var pp ParserPool
	p := pp.Get()
	if _, err := p.Parse(bigJSON(10000)); err != nil {
		t.Fatalf("cannot parse big json: %s", err)
	}
	inputCap, valueCap := cap(p.b), cap(p.c.vs)
	pp.Put(p)
	if cap(p.b) != inputCap || cap(p.c.vs) != valueCap {
		t.Fatalf("zero policy must retain capacity: cap(b)=%d->%d cap(vs)=%d->%d",
			inputCap, cap(p.b), valueCap, cap(p.c.vs))
	}

	var ap ArenaPool
	a := ap.Get()
	aa := a.NewArray()
	for i := 0; i < 1000; i++ {
		aa.SetArrayItem(i, a.NewNumberInt(i))
	}
	valueCap = cap(a.c.vs)
	ap.Put(a)
	if len(a.c.vs) != 0 {
		t.Fatalf("zero policy must still reset the arena")
	}
	if cap(a.c.vs) != valueCap {
		t.Fatalf("zero policy must retain arena value capacity: got %d want %d",
			cap(a.c.vs), valueCap)
	}
}

// The policy runs only on Put. Values produced during a request stay valid and
// usable until that request returns the Parser to the pool.
func TestShrinkDoesNotInvalidateValuesBeforePut(t *testing.T) {
	var pp ParserPool
	p := pp.Get()
	v, err := p.Parse(`{"a":[1,2,3]}`)
	if err != nil {
		t.Fatalf("cannot parse: %s", err)
	}
	// Before Put the returned value is fully usable.
	arr, err := v.Get("a").Array()
	if err != nil || arr[2].GetInt64() != 3 {
		t.Fatalf("unexpected value before Put: %s", v)
	}
	pp.Put(p) // only here does the value become invalid, by documented contract
}
