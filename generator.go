package ksuid

import (
	crand "crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"sync"
	"time"
)

var (
	// ErrTimestampOutOfRange is returned when the clock returns a time that
	// cannot be represented by a KSUID timestamp.
	ErrTimestampOutOfRange = errors.New("ksuid: timestamp out of range")

	// ErrPayloadExhausted is returned when all payload values in the current
	// logical second have already been used.
	ErrPayloadExhausted = errors.New("ksuid: payload values exhausted")
)

// Generator produces strictly increasing KSUIDs from a single instance. It
// tolerates clock moves backwards by continuing to use the last logical
// second until the wall clock catches up.
//
// Generator is safe for concurrent use. The zero value uses time.Now and
// crypto/rand.Reader. Separate generators have independent state and locks.
type Generator struct {
	// Now returns the current wall-clock time. If nil, time.Now is used.
	Now func() time.Time

	// Rand is the source of random payload bytes. If nil, crypto/rand.Reader
	// is used.
	Rand io.Reader

	mu          sync.Mutex
	initialized bool
	timestamp   uint32
	payload     [payloadLengthInBytes]byte
}

// NewGenerator creates a Generator using now as its clock and r as its random
// source. Nil arguments select the default clock or random source.
func NewGenerator(now func() time.Time, r io.Reader) *Generator {
	return &Generator{Now: now, Rand: r}
}

// Next returns the next strictly increasing KSUID. A new random payload is
// read when the wall clock advances to a second later than the generator's
// logical second. Within that second, or while the clock remains at or moves
// behind the logical second, the payload is incremented.
//
// Next does not advance to a future second when payload values are exhausted.
// Random and timestamp errors leave the generator state unchanged.
func (g *Generator) Next() (KSUID, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	now := g.Now
	if now == nil {
		now = time.Now
	}

	timestamp, err := correctedTimestamp(now())
	if err != nil {
		return Nil, err
	}

	if !g.initialized {
		payload, err := readGeneratorPayload(g.Rand)
		if err != nil {
			return Nil, err
		}

		g.timestamp = timestamp
		g.payload = payload
		g.initialized = true
		return newKSUID(g.timestamp, g.payload), nil
	}

	if timestamp <= g.timestamp {
		payload := g.payload
		if !incrementPayload(&payload) {
			return Nil, ErrPayloadExhausted
		}

		g.payload = payload
		return newKSUID(g.timestamp, g.payload), nil
	}

	payload, err := readGeneratorPayload(g.Rand)
	if err != nil {
		return Nil, err
	}

	g.timestamp = timestamp
	g.payload = payload
	return newKSUID(g.timestamp, g.payload), nil
}

func correctedTimestamp(t time.Time) (uint32, error) {
	timestamp := t.Unix() - epochStamp
	if timestamp < 0 || timestamp > math.MaxUint32 {
		return 0, ErrTimestampOutOfRange
	}
	return uint32(timestamp), nil
}

func readGeneratorPayload(r io.Reader) ([payloadLengthInBytes]byte, error) {
	var payload [payloadLengthInBytes]byte

	if r == nil {
		r = crand.Reader
	}

	_, err := io.ReadFull(r, payload[:])
	if err != nil {
		return [payloadLengthInBytes]byte{}, err
	}

	return payload, nil
}

func incrementPayload(payload *[payloadLengthInBytes]byte) bool {
	for i := len(payload) - 1; i >= 0; i-- {
		if payload[i] != math.MaxUint8 {
			payload[i]++
			return true
		}
		payload[i] = 0
	}
	return false
}

func newKSUID(timestamp uint32, payload [payloadLengthInBytes]byte) KSUID {
	var id KSUID
	binary.BigEndian.PutUint32(id[:timestampLengthInBytes], timestamp)
	copy(id[timestampLengthInBytes:], payload[:])
	return id
}
