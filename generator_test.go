package ksuid

import (
	"bytes"
	"errors"
	"io"
	"math"
	"sort"
	"sync"
	"testing"
	"time"
)

type scriptedClock struct {
	times []time.Time
	index int
}

func (c *scriptedClock) next() time.Time {
	t := c.times[c.index]
	if c.index < len(c.times)-1 {
		c.index++
	}
	return t
}

type fixedRand struct {
	payloads [][payloadLengthInBytes]byte
	index    int
}

func (r *fixedRand) Read(p []byte) (int, error) {
	payload := r.payloads[r.index]
	if r.index < len(r.payloads)-1 {
		r.index++
	}
	n := copy(p, payload[:])
	return n, nil
}

type scriptedRand struct {
	results [][]byte
	errors  []error
	index   int
	calls   int
}

func (r *scriptedRand) Read(p []byte) (int, error) {
	r.calls++
	result := []byte(nil)
	err := io.EOF

	if r.index < len(r.results) {
		result = r.results[r.index]
	}
	if r.index < len(r.errors) {
		err = r.errors[r.index]
	}
	if r.index < len(r.results)-1 || r.index < len(r.errors)-1 {
		r.index++
	}

	return copy(p, result), err
}

func TestGeneratorAdvancesSameSecondAndRollsBack(t *testing.T) {
	base := correctedUTCTimestampToTime(1)
	clock := &scriptedClock{times: []time.Time{
		base,
		base,
		base.Add(time.Second),
		base.Add(2 * time.Second),
		base.Add(time.Second),
		base,
	}}
	random := &fixedRand{payloads: [][payloadLengthInBytes]byte{
		payloadFromBytes(0x00, 0x10),
		payloadFromBytes(0x01, 0x00),
		payloadFromBytes(0x02, 0xff),
	}}

	generator := NewGenerator(clock.next, random)
	ids := make([]KSUID, 0, 6)

	for i := 0; i < 6; i++ {
		id, err := generator.Next()
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}

	expected := []KSUID{
		idFromParts(1, payloadFromBytes(0x00, 0x10)),
		idFromParts(1, payloadFromBytes(0x00, 0x11)),
		idFromParts(2, payloadFromBytes(0x01, 0x00)),
		idFromParts(3, payloadFromBytes(0x02, 0xff)),
		idFromParts(3, payloadFromBytes(0x03, 0x00)),
		idFromParts(3, payloadFromBytes(0x03, 0x01)),
	}

	for i := range expected {
		if ids[i] != expected[i] {
			t.Fatalf("id %d = % x, want % x", i, ids[i][:], expected[i][:])
		}
	}

	for i := 1; i < len(ids); i++ {
		if Compare(ids[i-1], ids[i]) >= 0 {
			t.Fatalf("ids are not strictly increasing at %d: %s >= %s", i, ids[i-1], ids[i])
		}
	}

	if random.index != 2 {
		t.Fatalf("read %d fresh payloads, want 3", random.index+1)
	}
}

func TestGeneratorEntropyFailureDoesNotCommitState(t *testing.T) {
	base := correctedUTCTimestampToTime(10)
	failure := errors.New("entropy unavailable")

	t.Run("first call", func(t *testing.T) {
		random := &scriptedRand{
			results: [][]byte{
				{},
				make([]byte, payloadLengthInBytes/2),
				payloadBytes(0x00, 0x42),
			},
			errors: []error{failure, io.ErrUnexpectedEOF, nil},
		}
		clock := &scriptedClock{times: []time.Time{base, base, base}}
		generator := NewGenerator(clock.next, random)

		if _, err := generator.Next(); !errors.Is(err, failure) {
			t.Fatalf("error = %v, want %v", err, failure)
		}

		if _, err := generator.Next(); !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatalf("error = %v, want %v", err, io.ErrUnexpectedEOF)
		}

		id, err := generator.Next()
		if err != nil {
			t.Fatal(err)
		}

		want := idFromParts(10, payloadFromBytes(0x00, 0x42))
		if id != want {
			t.Fatalf("id = % x, want % x", id[:], want[:])
		}
		if random.calls != 3 {
			t.Fatalf("random was called %d times, want 3", random.calls)
		}
	})

	t.Run("new second", func(t *testing.T) {
		random := &scriptedRand{
			results: [][]byte{
				payloadBytes(0x00, 0x42),
				{},
				payloadBytes(0x00, 0x43),
				payloadBytes(0x00, 0x99),
			},
			errors: []error{nil, failure, nil, nil},
		}
		clock := &scriptedClock{times: []time.Time{
			base,
			base.Add(time.Second),
			base.Add(time.Second),
			base.Add(2 * time.Second),
		}}
		generator := NewGenerator(clock.next, random)

		id, err := generator.Next()
		if err != nil {
			t.Fatal(err)
		}

		if _, err := generator.Next(); !errors.Is(err, failure) {
			t.Fatalf("error = %v, want %v", err, failure)
		}

		retry, err := generator.Next()
		if err != nil {
			t.Fatal(err)
		}

		want := idFromParts(11, payloadFromBytes(0x00, 0x43))
		if retry != want {
			t.Fatalf("retry id = % x, want % x", retry[:], want[:])
		}
		if Compare(retry, id) <= 0 {
			t.Fatalf("retry id %s did not follow %s", retry, id)
		}

		advanced, err := generator.Next()
		if err != nil {
			t.Fatal(err)
		}
		want = idFromParts(12, payloadFromBytes(0x00, 0x99))
		if advanced != want {
			t.Fatalf("advanced id = % x, want % x", advanced[:], want[:])
		}
	})
}

func TestGeneratorMaxPayload(t *testing.T) {
	base := correctedUTCTimestampToTime(20)
	maxPayload := [payloadLengthInBytes]byte{}
	for i := range maxPayload {
		maxPayload[i] = math.MaxUint8
	}

	t.Run("same second is exhausted", func(t *testing.T) {
		clock := &scriptedClock{times: []time.Time{base, base, base.Add(time.Second)}}
		random := &fixedRand{payloads: [][payloadLengthInBytes]byte{maxPayload}}
		generator := NewGenerator(clock.next, random)

		id, err := generator.Next()
		if err != nil {
			t.Fatal(err)
		}
		if id != idFromParts(20, maxPayload) {
			t.Fatalf("id = % x", id[:])
		}

		if _, err := generator.Next(); !errors.Is(err, ErrPayloadExhausted) {
			t.Fatalf("error = %v, want %v", err, ErrPayloadExhausted)
		}

		advanced, err := generator.Next()
		if err != nil {
			t.Fatal(err)
		}
		want := idFromParts(21, maxPayload)
		if advanced != want {
			t.Fatalf("id = % x, want % x", advanced[:], want[:])
		}
	})

	t.Run("rollback is exhausted", func(t *testing.T) {
		clock := &scriptedClock{times: []time.Time{base, base.Add(-time.Second)}}
		random := &fixedRand{payloads: [][payloadLengthInBytes]byte{maxPayload}}
		generator := NewGenerator(clock.next, random)

		if _, err := generator.Next(); err != nil {
			t.Fatal(err)
		}
		if _, err := generator.Next(); !errors.Is(err, ErrPayloadExhausted) {
			t.Fatalf("error = %v, want %v", err, ErrPayloadExhausted)
		}
	})
}

func TestGeneratorTimestampOutOfRange(t *testing.T) {
	beforeEpoch := time.Unix(epochStamp-1, 0)
	afterMax := correctedUTCTimestampToTime(math.MaxUint32).Add(time.Second)

	t.Run("before epoch on first call", func(t *testing.T) {
		clock := &scriptedClock{times: []time.Time{beforeEpoch, correctedUTCTimestampToTime(1)}}
		random := &fixedRand{payloads: [][payloadLengthInBytes]byte{payloadFromBytes(0x01)}}
		generator := NewGenerator(clock.next, random)

		if _, err := generator.Next(); !errors.Is(err, ErrTimestampOutOfRange) {
			t.Fatalf("error = %v, want %v", err, ErrTimestampOutOfRange)
		}

		id, err := generator.Next()
		if err != nil {
			t.Fatal(err)
		}
		want := idFromParts(1, payloadFromBytes(0x01))
		if id != want {
			t.Fatalf("id = % x, want % x", id[:], want[:])
		}
	})

	t.Run("after maximum", func(t *testing.T) {
		clock := &scriptedClock{times: []time.Time{afterMax, beforeEpoch}}
		generator := NewGenerator(clock.next, &fixedRand{})

		if _, err := generator.Next(); !errors.Is(err, ErrTimestampOutOfRange) {
			t.Fatalf("error = %v, want %v", err, ErrTimestampOutOfRange)
		}
		if _, err := generator.Next(); !errors.Is(err, ErrTimestampOutOfRange) {
			t.Fatalf("error = %v, want %v", err, ErrTimestampOutOfRange)
		}
	})
}

func TestGeneratorConcurrentLinearization(t *testing.T) {
	base := correctedUTCTimestampToTime(30)
	clock := &scriptedClock{times: []time.Time{base}}
	generator := NewGenerator(clock.next, bytes.NewReader(bytes.Repeat([]byte{0x00}, payloadLengthInBytes*128)))

	const count = 128
	ids := make([]KSUID, count)
	var wg sync.WaitGroup

	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id, err := generator.Next()
			if err != nil {
				t.Errorf("Next: %v", err)
				return
			}
			ids[i] = id
		}(i)
	}
	wg.Wait()

	sort.Slice(ids, func(i, j int) bool {
		return Compare(ids[i], ids[j]) < 0
	})

	for i := 1; i < len(ids); i++ {
		if Compare(ids[i-1], ids[i]) >= 0 {
			t.Fatalf("duplicate or decreasing id at %d", i)
		}
	}
}

func TestGeneratorsDoNotShareLocksOrState(t *testing.T) {
	base := correctedUTCTimestampToTime(40)
	entered := make(chan struct{})
	release := make(chan struct{})
	blockingRand := blockingReader{entered: entered, release: release}
	first := NewGenerator(func() time.Time { return base }, blockingRand)
	second := NewGenerator(func() time.Time { return base.Add(time.Second) },
		&fixedRand{payloads: [][payloadLengthInBytes]byte{payloadFromBytes(0x02)}})

	done := make(chan KSUID, 1)
	go func() {
		id, err := first.Next()
		if err != nil {
			t.Errorf("first Next: %v", err)
		}
		done <- id
	}()

	<-entered
	id, err := second.Next()
	if err != nil {
		t.Fatal(err)
	}

	want := idFromParts(41, payloadFromBytes(0x02))
	if id != want {
		t.Fatalf("second generator id = % x, want % x", id[:], want[:])
	}

	close(release)
	<-done
}

type blockingReader struct {
	entered chan<- struct{}
	release <-chan struct{}
}

func (r blockingReader) Read(p []byte) (int, error) {
	close(r.entered)
	<-r.release
	for i := range p {
		p[i] = 0x01
	}
	return len(p), nil
}

func payloadFromBytes(values ...byte) [payloadLengthInBytes]byte {
	var payload [payloadLengthInBytes]byte
	copy(payload[len(payload)-len(values):], values)
	return payload
}

func payloadBytes(values ...byte) []byte {
	payload := payloadFromBytes(values...)
	return payload[:]
}

func idFromParts(timestamp uint32, payload [payloadLengthInBytes]byte) KSUID {
	return newKSUID(timestamp, payload)
}
