package fastjson

import (
	"sync"
)

// RetentionPolicy limits how much capacity is retained by objects
// returned to ParserPool or ArenaPool.
//
// The policy is applied only when an object is put back into the pool,
// i.e. at a lifecycle boundary, so it never invalidates Values that
// are still in use.
//
// The zero policy retains all the capacity, preserving the original
// pool behavior.
type RetentionPolicy struct {
	// MaxInputBufferSize limits the capacity of the internal byte
	// buffer holding the parsed input in Parser or the marshaled
	// data in Arena. Bigger buffers are released on Put.
	MaxInputBufferSize int

	// MaxCachedValues limits the capacity of the Values cache
	// inside Parser or Arena. Bigger caches are released on Put.
	MaxCachedValues int

	// MaxContainerValues limits the capacity of a single array or
	// object stored inside a cached Value. Bigger containers are
	// released on Put, so a single huge array or object cannot
	// pin memory in the cache.
	MaxContainerValues int
}

func (rp *RetentionPolicy) shrinkBuffer(b []byte) []byte {
	if rp.MaxInputBufferSize > 0 && cap(b) > rp.MaxInputBufferSize {
		return nil
	}
	return b
}

func (rp *RetentionPolicy) shrinkCache(c *cache) {
	if rp.MaxCachedValues > 0 && cap(c.vs) > rp.MaxCachedValues {
		// The whole cache is too big. Drop it together with
		// all the containers stored in its Values.
		c.vs = nil
		return
	}
	if rp.MaxContainerValues > 0 {
		for i := range c.vs {
			v := &c.vs[i]
			if cap(v.a) > rp.MaxContainerValues {
				v.a = nil
			}
			if cap(v.o.kvs) > rp.MaxContainerValues {
				v.o.kvs = nil
			}
		}
	}
}

// ParserPool may be used for pooling Parsers for similarly typed JSONs.
type ParserPool struct {
	// Retention optionally limits the capacity retained by Parsers
	// returned to the pool. The zero value retains everything.
	Retention RetentionPolicy

	pool sync.Pool
}

// Get returns a Parser from pp.
//
// The Parser must be Put to pp after use.
func (pp *ParserPool) Get() *Parser {
	v := pp.pool.Get()
	if v == nil {
		return &Parser{}
	}
	return v.(*Parser)
}

// Put returns p to pp.
//
// p and objects recursively returned from p cannot be used after p
// is put into pp.
func (pp *ParserPool) Put(p *Parser) {
	p.b = pp.Retention.shrinkBuffer(p.b)
	pp.Retention.shrinkCache(&p.c)
	pp.pool.Put(p)
}

// ArenaPool may be used for pooling Arenas for similarly typed JSONs.
type ArenaPool struct {
	// Retention optionally limits the capacity retained by Arenas
	// returned to the pool. The zero value retains everything.
	Retention RetentionPolicy

	pool sync.Pool
}

// Get returns an Arena from ap.
//
// The Arena must be Put to ap after use.
func (ap *ArenaPool) Get() *Arena {
	v := ap.pool.Get()
	if v == nil {
		return &Arena{}
	}
	return v.(*Arena)
}

// Put returns a to ap.
//
// a and objects created by a cannot be used after a is put into ap.
func (ap *ArenaPool) Put(a *Arena) {
	a.Reset()
	a.b = ap.Retention.shrinkBuffer(a.b)
	ap.Retention.shrinkCache(&a.c)
	ap.pool.Put(a)
}
