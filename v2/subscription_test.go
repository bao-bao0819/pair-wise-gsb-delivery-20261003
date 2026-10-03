package gobreaker

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const subTestTimeout = 20 * time.Millisecond

func newSubTestBreaker() *CircuitBreaker[bool] {
	return NewCircuitBreaker[bool](Settings{
		Name:        "sub-test",
		MaxRequests: 1,
		Timeout:     subTestTimeout,
		ReadyToTrip: func(counts Counts) bool {
			return counts.ConsecutiveFailures >= 1
		},
	})
}

// waitForEvents polls until cond is satisfied or the deadline passes.
func waitForEvents(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", msg)
}

// tripOpen fails once, tripping the breaker closed -> open.
func tripOpen(t *testing.T, cb *CircuitBreaker[bool]) {
	t.Helper()
	require.NoError(t, fail(cb))
}

// recoverClosed waits out the open-state timeout and runs one successful
// request, moving the breaker open -> half-open -> closed.
func recoverClosed(t *testing.T, cb *CircuitBreaker[bool]) {
	t.Helper()
	time.Sleep(2 * subTestTimeout)
	require.NoError(t, succeed(cb))
}

func TestSubscriptionDeliversEventsInOrder(t *testing.T) {
	cb := newSubTestBreaker()

	started := make(chan struct{}, 1)
	release := make(chan struct{})

	var mu sync.Mutex
	var events []StateChangeEvent

	sub := cb.Subscribe(func(ev StateChangeEvent) {
		// Querying the breaker from inside the callback must not
		// deadlock with the internal state lock.
		_ = cb.State()
		_ = cb.Counts()

		mu.Lock()
		events = append(events, ev)
		mu.Unlock()

		select {
		case started <- struct{}{}:
		default:
		}
		<-release // block until the test releases the callback
	}, 8)
	defer sub.Unsubscribe()

	// First transition: closed -> open. The callback starts and blocks.
	tripOpen(t, cb)
	<-started

	// Trigger consecutive transitions while the callback is blocked:
	// open -> half-open -> closed, then closed -> open again.
	recoverClosed(t, cb)
	tripOpen(t, cb)

	close(release)
	waitForEvents(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(events) == 4
	}, "4 events to be delivered")

	wantStates := []State{StateOpen, StateHalfOpen, StateClosed, StateOpen}
	for i, ev := range events {
		assert.Equal(t, uint64(i+1), ev.Seq, "events must carry monotonic sequence numbers")
		assert.Equal(t, wantStates[i], ev.To)
		assert.Equal(t, "sub-test", ev.Name)
		assert.False(t, ev.Timestamp.IsZero())
	}
	assert.Equal(t, StateClosed, events[0].From)
	assert.Equal(t, uint64(0), sub.Dropped())

	// The event that tripped the breaker carries the counts that caused it.
	assert.Equal(t, uint32(1), events[0].Counts.TotalFailures)
}

func TestUnsubscribeStopsNewCallbacks(t *testing.T) {
	cb := newSubTestBreaker()

	started := make(chan struct{}, 1)
	release := make(chan struct{})
	var calls atomic.Int32

	sub := cb.Subscribe(func(ev StateChangeEvent) {
		calls.Add(1)
		select {
		case started <- struct{}{}:
		default:
		}
		<-release
	}, 8)

	tripOpen(t, cb)
	<-started // first callback is running and blocked

	sub.Unsubscribe()
	sub.Unsubscribe() // idempotent
	close(release)    // let the in-flight callback finish

	// Further transitions must not start new callback invocations.
	recoverClosed(t, cb)
	tripOpen(t, cb)
	time.Sleep(5 * subTestTimeout)

	assert.Equal(t, int32(1), calls.Load())
}

func TestUnsubscribeFromOwnCallback(t *testing.T) {
	cb := newSubTestBreaker()

	var calls atomic.Int32
	var sub Subscription
	sub = cb.Subscribe(func(ev StateChangeEvent) {
		calls.Add(1)
		sub.Unsubscribe() // must not deadlock
	}, 8)

	tripOpen(t, cb)
	waitForEvents(t, func() bool {
		return calls.Load() == 1
	}, "first callback to run")

	recoverClosed(t, cb)
	tripOpen(t, cb)
	time.Sleep(5 * subTestTimeout)

	assert.Equal(t, int32(1), calls.Load())
}

func TestCallbackPanicDoesNotCorruptBreaker(t *testing.T) {
	cb := newSubTestBreaker()

	var calls atomic.Int32
	firstEvent := make(chan StateChangeEvent, 1)
	sub := cb.Subscribe(func(ev StateChangeEvent) {
		if calls.Add(1) == 1 {
			firstEvent <- ev
		}
		panic("boom")
	}, 8)
	defer sub.Unsubscribe()

	// closed -> open, callback panics.
	tripOpen(t, cb)

	// The transition and its snapshot must be intact despite the panic.
	ev := <-firstEvent
	assert.Equal(t, StateClosed, ev.From)
	assert.Equal(t, StateOpen, ev.To)
	assert.Equal(t, uint32(1), ev.Counts.Requests)
	assert.Equal(t, uint32(1), ev.Counts.TotalFailures)

	// The breaker itself is unaffected: it is open and rejects requests.
	assert.Equal(t, StateOpen, cb.State())
	_, err := cb.Execute(func() (bool, error) { return true, nil })
	assert.ErrorIs(t, err, ErrOpenState)

	// The half-open permit accounting is intact: with MaxRequests == 1 a
	// second concurrent request is rejected while one is in flight.
	time.Sleep(2 * subTestTimeout)
	done := succeedLater(cb, 50*time.Millisecond)
	time.Sleep(10 * time.Millisecond) // let the first request take the permit
	_, err = cb.Execute(func() (bool, error) { return true, nil })
	assert.ErrorIs(t, err, ErrTooManyRequests)
	require.NoError(t, <-done)
	assert.Equal(t, StateClosed, cb.State())

	// The subscription goroutine survived the panics and keeps delivering.
	tripOpen(t, cb)
	waitForEvents(t, func() bool {
		return calls.Load() >= 4
	}, "subscription to keep delivering after panics")
}

func TestSlowSubscriberDropsOldest(t *testing.T) {
	cb := newSubTestBreaker()

	started := make(chan struct{}, 1)
	release := make(chan struct{})

	var mu sync.Mutex
	var events []StateChangeEvent

	sub := cb.Subscribe(func(ev StateChangeEvent) {
		mu.Lock()
		events = append(events, ev)
		mu.Unlock()
		select {
		case started <- struct{}{}:
		default:
		}
		<-release
	}, 1) // buffer of 1: everything else overflows
	defer sub.Unsubscribe()

	// First transition is picked up by the callback and blocks it.
	tripOpen(t, cb)
	<-started

	// Generate 4 more cycles (3 transitions each) while the callback is
	// blocked. With buffer 1 only the newest event survives each overflow.
	const cycles = 4
	for range cycles {
		recoverClosed(t, cb)
		tripOpen(t, cb)
	}

	close(release)
	const totalEvents = 1 + 3*cycles
	waitForEvents(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(events) == 2
	}, "buffered events to drain")

	// Exactly the first and the newest events were delivered, in order.
	assert.Equal(t, uint64(1), events[0].Seq)
	assert.Equal(t, uint64(totalEvents), events[1].Seq)
	assert.Equal(t, StateOpen, events[1].To)
	assert.Equal(t, uint64(totalEvents-2), sub.Dropped())
}
