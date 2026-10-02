package ksuid

import (
	"bytes"
	"errors"
	"io"
	"math"
	"sync"
	"testing"
	"time"
)

type scriptClock struct {
	now time.Time
}

func (c *scriptClock) Time() time.Time {
	return c.now
}

type scriptedRandomCall struct {
	payload []byte
	n       int
	err     error
}

type scriptedRandom struct {
	calls []scriptedRandomCall
	n     int
}

func (r *scriptedRandom) Read(p []byte) (int, error) {
	if r.n == len(r.calls) {
		return 0, errors.New("unexpected random source read")
	}

	call := r.calls[r.n]
	r.n++

	n := call.n
	if n == 0 && call.err == nil {
		n = len(p)
	}
	if call.payload != nil {
		copy(p, call.payload)
	}
	return n, call.err
}

type fixedRandom struct {
	payload []byte
}

func (r *fixedRandom) Read(p []byte) (int, error) {
	copy(p, r.payload)
	return len(p), nil
}

func TestGeneratorAdvanceSameSecondAndRollback(t *testing.T) {
	clock := &scriptClock{now: time.Unix(epochStamp+100, 0)}
	first := testPayload(0x10, 0x01)
	second := testPayload(0x20, 0x02)
	random := &scriptedRandom{calls: []scriptedRandomCall{
		{payload: first[:]},
		{payload: second[:]},
	}}
	generator := NewGenerator(clock.Time, random)

	id1, err := generator.Next()
	if err != nil {
		t.Fatal(err)
	}
	id2, err := generator.Next()
	if err != nil {
		t.Fatal(err)
	}

	clock.now = time.Unix(epochStamp+99, 0)
	id3, err := generator.Next()
	if err != nil {
		t.Fatal(err)
	}

	clock.now = time.Unix(epochStamp+101, 0)
	id4, err := generator.Next()
	if err != nil {
		t.Fatal(err)
	}

	ids := []KSUID{id1, id2, id3, id4}
	for i := 1; i < len(ids); i++ {
		if Compare(ids[i-1], ids[i]) >= 0 {
			t.Fatalf("ID %d is not greater than the previous ID: %s <= %s", i, ids[i], ids[i-1])
		}
	}

	assertGeneratorState(t, generator, 101, makeUint128FromPayload(second[:]).bytes())

	clock.now = time.Unix(epochStamp+101, 0)
	if _, err := generator.Next(); err != nil {
		t.Fatal(err)
	}
	assertGeneratorState(t, generator, 101, incr128(makeUint128FromPayload(second[:])).bytes())

	if random.n != 2 {
		t.Fatalf("expected 2 random payload reads, got %d", random.n)
	}
}

func TestGeneratorRandomFailureDoesNotCommitState(t *testing.T) {
	clock := &scriptClock{now: time.Unix(epochStamp+100, 0)}
	entropyFailure := errors.New("entropy source unavailable")
	first := testPayload(0x10, 0x01)
	second := testPayload(0x20, 0x02)
	random := &scriptedRandom{calls: []scriptedRandomCall{
		{err: entropyFailure},
		{payload: first[:]},
		{err: entropyFailure},
		{payload: second[:]},
	}}
	generator := NewGenerator(clock.Time, random)

	if _, err := generator.Next(); err != entropyFailure {
		t.Fatalf("expected entropy failure, got %v", err)
	}
	if generator.initialized {
		t.Fatal("failed initial read initialized the generator")
	}

	id1, err := generator.Next()
	if err != nil {
		t.Fatal(err)
	}
	id2, err := generator.Next()
	if err != nil {
		t.Fatal(err)
	}

	clock.now = time.Unix(epochStamp+101, 0)
	if _, err := generator.Next(); err != entropyFailure {
		t.Fatalf("expected entropy failure after advancing time, got %v", err)
	}
	assertGeneratorState(t, generator, 100, incr128(makeUint128FromPayload(first[:])).bytes())

	id3, err := generator.Next()
	if err != nil {
		t.Fatal(err)
	}
	if Compare(id2, id3) >= 0 {
		t.Fatalf("retry did not produce a greater ID: %s <= %s", id3, id2)
	}

	if id1.Timestamp() != 100 || id3.Timestamp() != 101 {
		t.Fatalf("unexpected timestamps: %d and %d", id1.Timestamp(), id3.Timestamp())
	}
	if random.n != 4 {
		t.Fatalf("expected 4 random reads, got %d", random.n)
	}
}

func TestGeneratorPartialRandomReadDoesNotCommitState(t *testing.T) {
	clock := &scriptClock{now: time.Unix(epochStamp+100, 0)}
	payload := testPayload(0x30, 0x03)
	random := &scriptedRandom{calls: []scriptedRandomCall{
		{payload: payload[:], n: 8, err: io.EOF},
		{payload: payload[:]},
	}}
	generator := NewGenerator(clock.Time, random)

	if _, err := generator.Next(); err != io.ErrUnexpectedEOF {
		t.Fatalf("expected unexpected EOF, got %v", err)
	}
	if generator.initialized || generator.payload != [payloadLengthInBytes]byte{} {
		t.Fatalf("partial random read committed state: initialized=%v payload=%x", generator.initialized, generator.payload)
	}

	id, err := generator.Next()
	if err != nil {
		t.Fatal(err)
	}
	if id.Timestamp() != 100 || bytes.Compare(id.Payload(), payload[:]) != 0 {
		t.Fatalf("unexpected ID after retry: timestamp=%d payload=%x", id.Timestamp(), id.Payload())
	}
}

func TestGeneratorPayloadOverflow(t *testing.T) {
	clock := &scriptClock{now: time.Unix(epochStamp+100, 0)}
	maxPayload := testPayload(0xff, 0xff)
	nextPayload := testPayload(0x00, 0x01)
	random := &scriptedRandom{calls: []scriptedRandomCall{
		{payload: maxPayload[:]},
		{payload: nextPayload[:]},
	}}
	generator := NewGenerator(clock.Time, random)

	id, err := generator.Next()
	if err != nil {
		t.Fatal(err)
	}

	if _, err := generator.Next(); err != ErrPayloadOverflow {
		t.Fatalf("same second: expected payload overflow, got %v", err)
	}
	clock.now = time.Unix(epochStamp+99, 0)
	if _, err := generator.Next(); err != ErrPayloadOverflow {
		t.Fatalf("clock rollback: expected payload overflow, got %v", err)
	}
	assertGeneratorState(t, generator, 100, maxPayload)

	clock.now = time.Unix(epochStamp+101, 0)
	next, err := generator.Next()
	if err != nil {
		t.Fatal(err)
	}
	if Compare(id, next) >= 0 {
		t.Fatalf("ID after waiting for the next second is not greater: %s <= %s", next, id)
	}
	assertGeneratorState(t, generator, 101, nextPayload)
}

func TestGeneratorTimestampOutOfRange(t *testing.T) {
	clock := &scriptClock{now: time.Unix(epochStamp-1, 0)}
	first := testPayload(0x10, 0x01)
	second := testPayload(0x20, 0x02)
	random := &scriptedRandom{calls: []scriptedRandomCall{
		{payload: first[:]},
		{payload: second[:]},
	}}
	generator := NewGenerator(clock.Time, random)

	if _, err := generator.Next(); err != ErrTimestampOutOfRange {
		t.Fatalf("before epoch: expected timestamp range error, got %v", err)
	}
	if random.n != 0 || generator.initialized {
		t.Fatal("out-of-range timestamp consumed entropy or initialized the generator")
	}

	clock.now = time.Unix(epochStamp+100, 0)
	if _, err := generator.Next(); err != nil {
		t.Fatal(err)
	}

	clock.now = time.Unix(epochStamp+int64(math.MaxUint32), 0)
	maxID, err := generator.Next()
	if err != nil {
		t.Fatal(err)
	}

	clock.now = clock.now.Add(time.Second)
	if _, err := generator.Next(); err != ErrTimestampOutOfRange {
		t.Fatalf("after max timestamp: expected timestamp range error, got %v", err)
	}
	assertGeneratorState(t, generator, math.MaxUint32, second)
	if maxID.Timestamp() != math.MaxUint32 {
		t.Fatalf("expected max timestamp, got %d", maxID.Timestamp())
	}
	if random.n != 2 {
		t.Fatalf("expected 2 random reads, got %d", random.n)
	}
}

func TestGeneratorConcurrentNext(t *testing.T) {
	const count = 300

	clock := &scriptClock{now: time.Unix(epochStamp+100, 0)}
	random := &fixedRandom{payload: make([]byte, payloadLengthInBytes)}
	generator := NewGenerator(clock.Time, random)

	ids := make([]KSUID, count)
	var waitGroup sync.WaitGroup
	for i := range ids {
		waitGroup.Add(1)
		go func(i int) {
			defer waitGroup.Done()

			id, err := generator.Next()
			if err != nil {
				t.Errorf("Next: %v", err)
				return
			}
			ids[i] = id
		}(i)
	}
	waitGroup.Wait()

	seen := make(map[KSUID]struct{}, count)
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			t.Fatalf("duplicate ID generated: %s", id)
		}
		seen[id] = struct{}{}
	}

	Sort(ids[:])
	for i, id := range ids {
		expected := makeUint128(0, uint64(i))
		if id.Timestamp() != 100 || cmp128(uint128Payload(id), expected) != 0 {
			t.Fatalf("ID %d has unexpected timestamp or payload: %s", i, id)
		}
	}
}

func TestGeneratorInstancesAreIndependent(t *testing.T) {
	clock := &scriptClock{now: time.Unix(epochStamp+100, 0)}
	firstPayload := testPayload(0xa0, 0x0a)
	secondPayload := testPayload(0xb0, 0x0b)
	firstGenerator := NewGenerator(clock.Time, &fixedRandom{payload: firstPayload[:]})
	secondGenerator := NewGenerator(clock.Time, &fixedRandom{payload: secondPayload[:]})

	firstID1, err := firstGenerator.Next()
	if err != nil {
		t.Fatal(err)
	}
	secondID1, err := secondGenerator.Next()
	if err != nil {
		t.Fatal(err)
	}
	firstID2, err := firstGenerator.Next()
	if err != nil {
		t.Fatal(err)
	}
	secondID2, err := secondGenerator.Next()
	if err != nil {
		t.Fatal(err)
	}

	assertGeneratorState(t, firstGenerator, 100, incr128(makeUint128FromPayload(firstPayload[:])).bytes())
	assertGeneratorState(t, secondGenerator, 100, incr128(makeUint128FromPayload(secondPayload[:])).bytes())

	if Compare(firstID1, firstID2) >= 0 || Compare(secondID1, secondID2) >= 0 {
		t.Fatal("each generator did not increment its own payload")
	}
	if bytes.Compare(firstID1.Payload(), secondID1.Payload()) == 0 {
		t.Fatal("generators shared their payload buffer")
	}
}

func testPayload(high byte, low byte) [payloadLengthInBytes]byte {
	var payload [payloadLengthInBytes]byte
	for i := range payload {
		payload[i] = high
	}
	payload[len(payload)-1] = low
	return payload
}

func assertGeneratorState(t *testing.T, generator *Generator, timestamp uint32, payload [payloadLengthInBytes]byte) {
	t.Helper()

	generator.mu.Lock()
	defer generator.mu.Unlock()

	if !generator.initialized {
		t.Fatal("generator is not initialized")
	}
	if generator.timestamp != timestamp {
		t.Fatalf("expected logical timestamp %d, got %d", timestamp, generator.timestamp)
	}
	if generator.payload != payload {
		t.Fatalf("expected payload %x, got %x", payload, generator.payload)
	}
}
