package fastjson

import (
	"sync"
)

// RetentionPolicy limits the buffer capacity kept by pooled Parsers and Arenas
// once they are returned to a pool.
//
// A policy is applied only at the lifecycle boundary, i.e. when an item is Put
// back into the pool. Buffers that exceed a limit are released instead of being
// retained, while buffers within the limit keep their capacity for re-use.
//
// The zero value leaves every limit unset and preserves the original pooling
// behavior: the whole input buffer and the whole value cache are retained.
type RetentionPolicy struct {
	// MaxInputBytes is the maximum capacity in bytes retained for the parser
	// input buffer (Parser) or the marshaling buffer (Arena).
	MaxInputBytes int

	// MaxValues is the maximum number of cached Value slots retained.
	// This bounds the capacity of the Value cache itself.
	MaxValues int

	// MaxArrayValues bounds the capacity retained by a single cached array.
	// Any individual array whose backing slice exceeds the limit is released,
	// so one huge array cannot pin a large allocation forever.
	MaxArrayValues int

	// MaxObjectItems bounds the capacity retained by a single cached object.
	// Any individual object whose entry slice exceeds the limit is released,
	// so one huge object cannot pin a large allocation forever.
	MaxObjectItems int
}

func (rp RetentionPolicy) active() bool {
	return rp.MaxInputBytes > 0 || rp.MaxValues > 0 ||
		rp.MaxArrayValues > 0 || rp.MaxObjectItems > 0
}

// ParserPool may be used for pooling Parsers for similarly typed JSONs.
type ParserPool struct {
	pool      sync.Pool
	Retention RetentionPolicy
}

// NewParserPool returns a ParserPool configured with the given retention policy.
//
// A zero policy is equivalent to a zero-value ParserPool: every buffer is
// retained unchanged.
func NewParserPool(rp RetentionPolicy) *ParserPool {
	return &ParserPool{Retention: rp}
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
	if pp.Retention.active() {
		// Enforce the retention limits at the lifecycle boundary. Values are
		// invalidated by Put, so releasing their backing storage is safe.
		p.shrink(pp.Retention)
	}
	pp.pool.Put(p)
}

// ArenaPool may be used for pooling Arenas for similarly typed JSONs.
type ArenaPool struct {
	pool      sync.Pool
	Retention RetentionPolicy
}

// NewArenaPool returns an ArenaPool configured with the given retention policy.
//
// A zero policy is equivalent to a zero-value ArenaPool: every buffer is
// retained unchanged.
func NewArenaPool(rp RetentionPolicy) *ArenaPool {
	return &ArenaPool{Retention: rp}
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
	if ap.Retention.active() {
		a.shrink(ap.Retention)
	} else {
		a.Reset()
	}
	ap.pool.Put(a)
}
