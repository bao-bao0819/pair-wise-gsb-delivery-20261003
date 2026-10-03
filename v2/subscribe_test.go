package gobreaker

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func newSubscriptionTestCB() *CircuitBreaker[bool] {
	var st Settings
	st.Name = "sub-test"
	st.MaxRequests = 1
	st.Timeout = 60 * time.Second
	st.ReadyToTrip = func(counts Counts) bool {
		return counts.ConsecutiveFailures >= 1
	}
	return NewCircuitBreaker[bool](st)
}

func recvTransition(t *testing.T, ch <-chan StateTransition) StateTransition {
	t.Helper()
	select {
	case tr := <-ch:
		return tr
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for state transition notification")
		return StateTransition{}
	}
}

func assertNoTransition(t *testing.T, ch <-chan StateTransition) {
	t.Helper()
	select {
	case tr := <-ch:
		t.Fatalf("unexpected state transition notification: %+v", tr)
	case <-time.After(100 * time.Millisecond):
	}
}

// TestSubscriptionNotificationOrder drives consecutive transitions while the
// subscriber callback is blocked, then verifies that the subscriber observes
// every transition in order with monotonically increasing sequence numbers.
// The callback queries State and Counts to prove that doing so does not
// deadlock with the internal state lock.
func TestSubscriptionNotificationOrder(t *testing.T) {
	cb := newSubscriptionTestCB()

	gate := make(chan struct{})
	delivered := make(chan StateTransition, 8)
	queried := make(chan Counts, 8)

	sub := cb.Subscribe(func(tr StateTransition) {
		_ = cb.State() // must not deadlock with the internal state lock
		queried <- cb.Counts()
		delivered <- tr
		<-gate // block until the test releases this callback
	})
	defer sub.Unsubscribe()

	// Transition 1: closed -> open. The callback starts and blocks.
	assert.NoError(t, fail(cb))

	// Transitions 2 and 3 are generated while the first callback is blocked.
	pseudoSleep(cb, 90*time.Second)
	assert.Equal(t, StateHalfOpen, cb.State()) // open -> half-open
	assert.NoError(t, succeed(cb))             // half-open -> closed

	tr1 := recvTransition(t, delivered)
	assert.Equal(t, uint64(1), tr1.Seq)
	assert.Equal(t, "sub-test", tr1.Name)
	assert.Equal(t, StateClosed, tr1.From)
	assert.Equal(t, StateOpen, tr1.To)
	assert.Equal(t, Counts{Requests: 1, TotalFailures: 1, ConsecutiveFailures: 1}, tr1.Counts)
	<-queried
	gate <- struct{}{}

	tr2 := recvTransition(t, delivered)
	assert.Equal(t, uint64(2), tr2.Seq)
	assert.Equal(t, StateOpen, tr2.From)
	assert.Equal(t, StateHalfOpen, tr2.To)
	assert.Equal(t, Counts{}, tr2.Counts)
	<-queried
	gate <- struct{}{}

	tr3 := recvTransition(t, delivered)
	assert.Equal(t, uint64(3), tr3.Seq)
	assert.Equal(t, StateHalfOpen, tr3.From)
	assert.Equal(t, StateClosed, tr3.To)
	assert.Equal(t, Counts{Requests: 1, TotalSuccesses: 1, ConsecutiveSuccesses: 1}, tr3.Counts)
	<-queried
	gate <- struct{}{}

	assertNoTransition(t, delivered)
}

// TestSubscriptionUnsubscribeStopsCallbacks verifies that after Unsubscribe
// returns, no new callback invocation is started even though further
// transitions occur.
func TestSubscriptionUnsubscribeStopsCallbacks(t *testing.T) {
	cb := newSubscriptionTestCB()

	gate := make(chan struct{})
	delivered := make(chan StateTransition, 8)

	sub := cb.Subscribe(func(tr StateTransition) {
		delivered <- tr
		<-gate
	})

	assert.NoError(t, fail(cb)) // closed -> open
	tr := recvTransition(t, delivered)
	assert.Equal(t, uint64(1), tr.Seq)

	// Unsubscribe while the first callback is still blocked, then let it
	// finish and produce more transitions.
	sub.Unsubscribe()
	close(gate)

	pseudoSleep(cb, 90*time.Second)
	assert.Equal(t, StateHalfOpen, cb.State())
	assert.NoError(t, succeed(cb))
	assert.NoError(t, fail(cb))

	assertNoTransition(t, delivered)

	// Unsubscribing twice is a no-op.
	sub.Unsubscribe()
}

// TestSubscriptionSelfUnsubscribe verifies that a subscriber can cancel its
// own subscription from within the callback without deadlocking, and that no
// further callbacks are started afterwards.
func TestSubscriptionSelfUnsubscribe(t *testing.T) {
	cb := newSubscriptionTestCB()

	delivered := make(chan StateTransition, 8)
	var sub Subscription
	sub = cb.Subscribe(func(tr StateTransition) {
		delivered <- tr
		sub.Unsubscribe()
	})
	defer sub.Unsubscribe()

	assert.NoError(t, fail(cb)) // closed -> open
	tr := recvTransition(t, delivered)
	assert.Equal(t, uint64(1), tr.Seq)

	pseudoSleep(cb, 90*time.Second)
	assert.Equal(t, StateHalfOpen, cb.State())
	assert.NoError(t, succeed(cb))

	assertNoTransition(t, delivered)
}

// TestSubscriptionCallbackPanic verifies that a panicking callback neither
// corrupts the permit counting of the CircuitBreaker nor stops subsequent
// notifications of the same subscription.
func TestSubscriptionCallbackPanic(t *testing.T) {
	cb := newSubscriptionTestCB()

	delivered := make(chan StateTransition, 8)
	var panics atomic.Int32
	sub := cb.Subscribe(func(tr StateTransition) {
		delivered <- tr
		if tr.Seq == 1 {
			panics.Add(1)
			panic("boom")
		}
	})
	defer sub.Unsubscribe()

	assert.NoError(t, fail(cb)) // closed -> open, callback panics
	tr := recvTransition(t, delivered)
	assert.Equal(t, uint64(1), tr.Seq)

	// Permit counting is unaffected by the panic.
	assert.Equal(t, StateOpen, cb.State())
	assert.Equal(t, Counts{}, cb.Counts())

	pseudoSleep(cb, 90*time.Second)
	assert.Equal(t, StateHalfOpen, cb.State()) // open -> half-open

	// With MaxRequests == 1 the single half-open permit is held by the
	// in-flight request, so a concurrent request is rejected.
	ch := succeedLater(cb, 100*time.Millisecond)
	time.Sleep(10 * time.Millisecond)
	assert.ErrorIs(t, fail(cb), ErrTooManyRequests)
	// The in-flight request holds the only half-open permit.
	assert.Equal(t, Counts{Requests: 1}, cb.Counts())
	assert.NoError(t, <-ch) // half-open -> closed

	// The transition to closed started a new generation with cleared counts.
	assert.Equal(t, Counts{}, cb.Counts())

	// The dispatcher survived the panic and keeps delivering in order.
	tr = recvTransition(t, delivered)
	assert.Equal(t, uint64(2), tr.Seq)
	assert.Equal(t, StateHalfOpen, tr.To)
	tr = recvTransition(t, delivered)
	assert.Equal(t, uint64(3), tr.Seq)
	assert.Equal(t, StateClosed, tr.To)
	assert.Equal(t, int32(1), panics.Load())
}

// TestSubscriptionOverflowDropNewest verifies that with a full queue the
// incoming transitions are discarded.
func TestSubscriptionOverflowDropNewest(t *testing.T) {
	cb := newSubscriptionTestCB()

	gate := make(chan struct{})
	delivered := make(chan StateTransition, 8)
	sub := cb.Subscribe(func(tr StateTransition) {
		delivered <- tr
		<-gate
	}, SubscribeOptions{BufferSize: 1, Overflow: DropNewest})
	defer sub.Unsubscribe()

	assert.NoError(t, fail(cb)) // seq 1: closed -> open
	tr := recvTransition(t, delivered)
	assert.Equal(t, uint64(1), tr.Seq) // callback is now blocked

	pseudoSleep(cb, 90*time.Second)
	assert.Equal(t, StateHalfOpen, cb.State()) // seq 2: queued
	assert.NoError(t, succeed(cb))             // seq 3: dropped
	assert.NoError(t, fail(cb))                // seq 4: dropped

	close(gate)

	tr = recvTransition(t, delivered)
	assert.Equal(t, uint64(2), tr.Seq)
	assert.Equal(t, StateHalfOpen, tr.To)
	assertNoTransition(t, delivered)
}

// TestSubscriptionOverflowDropOldest verifies that with a full queue the
// oldest queued transitions are evicted in favor of the most recent ones.
func TestSubscriptionOverflowDropOldest(t *testing.T) {
	cb := newSubscriptionTestCB()

	gate := make(chan struct{})
	delivered := make(chan StateTransition, 8)
	sub := cb.Subscribe(func(tr StateTransition) {
		delivered <- tr
		<-gate
	}, SubscribeOptions{BufferSize: 1, Overflow: DropOldest})
	defer sub.Unsubscribe()

	assert.NoError(t, fail(cb)) // seq 1: closed -> open
	tr := recvTransition(t, delivered)
	assert.Equal(t, uint64(1), tr.Seq) // callback is now blocked

	pseudoSleep(cb, 90*time.Second)
	assert.Equal(t, StateHalfOpen, cb.State()) // seq 2: queued
	assert.NoError(t, succeed(cb))             // seq 3: evicts seq 2
	assert.NoError(t, fail(cb))                // seq 4: evicts seq 3

	close(gate)

	tr = recvTransition(t, delivered)
	assert.Equal(t, uint64(4), tr.Seq)
	assert.Equal(t, StateClosed, tr.From)
	assert.Equal(t, StateOpen, tr.To)
	assertNoTransition(t, delivered)
}

// TestTwoStepSubscription verifies that TwoStepCircuitBreaker forwards
// subscriptions to the underlying CircuitBreaker.
func TestTwoStepSubscription(t *testing.T) {
	var st Settings
	st.Name = "two-step"
	st.ReadyToTrip = func(counts Counts) bool {
		return counts.ConsecutiveFailures >= 1
	}
	tscb := NewTwoStepCircuitBreaker[bool](st)

	delivered := make(chan StateTransition, 1)
	sub := tscb.Subscribe(func(tr StateTransition) {
		delivered <- tr
	})
	defer sub.Unsubscribe()

	done, err := tscb.Allow()
	assert.NoError(t, err)
	done(errFailed)

	tr := recvTransition(t, delivered)
	assert.Equal(t, uint64(1), tr.Seq)
	assert.Equal(t, "two-step", tr.Name)
	assert.Equal(t, StateClosed, tr.From)
	assert.Equal(t, StateOpen, tr.To)
}
