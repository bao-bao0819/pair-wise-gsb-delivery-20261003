package ksuid

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"sync"
	"time"
)

var (
	// ErrPayloadOverflow is returned when a Generator has used every payload
	// for its current logical timestamp.
	ErrPayloadOverflow = errors.New("ksuid: payload exhausted")

	// ErrTimestampOutOfRange is returned when the clock reports a time that
	// cannot be represented by a KSUID timestamp.
	ErrTimestampOutOfRange = errors.New("ksuid: timestamp out of range")
)

// Generator produces strictly increasing KSUIDs within a single instance.
// It uses its own lock and payload buffer and does not share state with the
// package-level generators or other Generator values.
//
// When the clock advances to a new second, a fresh random payload is read.
// When the clock stays on the same second or moves backward, the generator
// keeps its logical second and increments the payload. If the random source
// fails, the generator state is unchanged.
type Generator struct {
	now  func() time.Time
	rand io.Reader

	mu          sync.Mutex
	initialized bool
	timestamp   uint32
	payload     [payloadLengthInBytes]byte
}

// NewGenerator constructs a Generator using the supplied clock and random
// source. Nil arguments use time.Now and crypto/rand.Reader respectively.
func NewGenerator(now func() time.Time, r io.Reader) *Generator {
	if now == nil {
		now = time.Now
	}
	if r == nil {
		r = rand.Reader
	}
	return &Generator{
		now:  now,
		rand: r,
	}
}

// Next returns the next strictly increasing KSUID. Concurrent calls are
// linearized by the generator's mutex.
func (g *Generator) Next() (KSUID, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	current, err := g.timestampFromClock()
	if err != nil {
		return Nil, err
	}

	if !g.initialized || current > g.timestamp {
		var payload [payloadLengthInBytes]byte
		if _, err := io.ReadFull(g.rand, payload[:]); err != nil {
			return Nil, err
		}

		g.timestamp = current
		g.payload = payload
		g.initialized = true
	} else {
		next := incr128(makeUint128FromPayload(g.payload[:]))
		if next == (uint128{}) {
			return Nil, ErrPayloadOverflow
		}
		g.payload = next.bytes()
	}

	var id KSUID
	binary.BigEndian.PutUint32(id[:timestampLengthInBytes], g.timestamp)
	copy(id[timestampLengthInBytes:], g.payload[:])
	return id, nil
}

func (g *Generator) timestampFromClock() (uint32, error) {
	sec := g.now().Unix()
	if sec < epochStamp || sec > epochStamp+int64(math.MaxUint32) {
		return 0, ErrTimestampOutOfRange
	}
	return uint32(sec - epochStamp), nil
}
