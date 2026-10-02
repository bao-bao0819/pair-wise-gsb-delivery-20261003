package backoff

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// controlledTimer is a test timer that never fires on its own. Start records a
// fresh, unclosed fire channel; C returns a channel that delivers when that
// fire channel is closed. The test decides exactly when a waiting retry loop
// resumes by calling fire.
type controlledTimer struct {
	mu     sync.Mutex
	notify chan struct{}      // signaled once per Start, for the test
	armed  chan chan struct{} // consumed by run to wait for fire
	fireC  chan struct{}
	c      chan time.Time // stable across waits; C() is read before Start()
}

func newControlledTimer() *controlledTimer {
	t := &controlledTimer{
		notify: make(chan struct{}, 16),
		armed:  make(chan chan struct{}, 16),
		fireC:  make(chan struct{}),
		c:      make(chan time.Time, 1),
	}
	go t.run()
	return t
}

// run forwards exactly one time per Start from that wait's fire channel onto
// the stable c, so a stale tick can never be observed by a later wait.
func (t *controlledTimer) run() {
	for fire := range t.armed {
		<-fire
		t.c <- time.Time{}
	}
}

// Start arms a fresh wait.
func (t *controlledTimer) Start(time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	fire := make(chan struct{})
	t.fireC = fire
	t.armed <- fire
	t.notify <- struct{}{}
}

func (t *controlledTimer) Stop() {}

func (t *controlledTimer) C() <-chan time.Time { return t.c }

func (t *controlledTimer) fire() {
	t.mu.Lock()
	fire := t.fireC
	t.fireC = nil
	t.mu.Unlock()
	close(fire)
}

// recordingBudget is a RetryBudget with exact books of every event so tests
// can assert precise counts and prove no reservation ends unaccounted for.
type recordingBudget struct {
	mu sync.Mutex

	tokens uint
	refill bool

	acquires  int // all Acquire calls, denied ones included
	granted   int // reservations handed out
	denied    int // Acquire calls that returned false
	consumed  int // reservations whose funded attempt actually started
	cancelled int // reservations returned while still waiting
	succeeded int // Succeed calls
}

func newRecordingBudget(tokens uint, refillOnSuccess bool) *recordingBudget {
	return &recordingBudget{tokens: tokens, refill: refillOnSuccess}
}

func (b *recordingBudget) Acquire() (Reservation, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.acquires++
	if b.tokens == 0 {
		b.denied++
		return nil, false
	}
	b.tokens--
	b.granted++
	return &trackedReservation{budget: b}, true
}

func (b *recordingBudget) Succeed() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.succeeded++
	if b.refill {
		b.tokens++
	}
}

// assertNoDangling fails unless every granted reservation was resolved
// exactly once, as either consumed or cancelled.
func (b *recordingBudget) assertNoDangling(t *testing.T) {
	t.Helper()
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.consumed+b.cancelled != b.granted {
		t.Errorf("dangling reservations: granted=%d consumed=%d cancelled=%d",
			b.granted, b.consumed, b.cancelled)
	}
}

func (b *recordingBudget) state() (acquires, granted, denied, consumed, cancelled, succeeded, tokens int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.acquires, b.granted, b.denied, b.consumed, b.cancelled, b.succeeded, int(b.tokens)
}

// trackedReservation records its own outcome. consume and Cancel are mutually
// exclusive via once, mirroring the Retry contract that a funded attempt that
// starts is spent and one cancelled in the wait is returned.
type trackedReservation struct {
	budget *recordingBudget
	once   sync.Once
}

func (r *trackedReservation) consume() {
	r.once.Do(func() {
		r.budget.mu.Lock()
		r.budget.consumed++
		r.budget.mu.Unlock()
	})
}

func (r *trackedReservation) Cancel() {
	r.once.Do(func() {
		r.budget.mu.Lock()
		r.budget.cancelled++
		r.budget.tokens++
		r.budget.mu.Unlock()
	})
}

// runBudget wraps a shared recordingBudget for one Retry loop. It remembers
// the reservation granted to this loop so the loop's operation can mark it
// consumed the moment a funded attempt starts.
type runBudget struct {
	shared *recordingBudget

	mu          sync.Mutex
	outstanding *trackedReservation
}

func (b *runBudget) Acquire() (Reservation, bool) {
	r, ok := b.shared.Acquire()
	if !ok {
		return nil, false
	}
	tr := r.(*trackedReservation)
	b.mu.Lock()
	b.outstanding = tr
	b.mu.Unlock()
	return tr, true
}

func (b *runBudget) Succeed() { b.shared.Succeed() }

// consumeOutstanding marks the reservation of a funded attempt spent when the
// operation for that attempt starts. First attempts (no reservation yet) do
// nothing.
func (b *runBudget) consumeOutstanding() {
	b.mu.Lock()
	r := b.outstanding
	b.outstanding = nil
	b.mu.Unlock()
	if r != nil {
		r.consume()
	}
}

// attempt is the scripted result of one operation call.
type attempt struct {
	err error
	res int
}

// retryHarness runs one Retry loop driven step by step by the test: every
// operation call blocks on gate until the test releases it, then returns the
// next scripted attempt. started signals each call.
type retryHarness struct {
	timer   *controlledTimer
	started chan struct{}
	gate    chan chan struct{}
	done    chan error
}

func startHarness(ctx context.Context, shared *recordingBudget, script []attempt, opts ...RetryOption) (*retryHarness, *runBudget) {
	tm := newControlledTimer()
	rb := &runBudget{shared: shared}
	h := &retryHarness{
		timer:   tm,
		started: make(chan struct{}, 8),
		gate:    make(chan chan struct{}, 1),
		done:    make(chan error, 1),
	}

	var n int
	op := func() (int, error) {
		rb.consumeOutstanding()
		h.started <- struct{}{}
		release := make(chan struct{})
		h.gate <- release
		<-release
		i := n
		n++
		a := script[i%len(script)]
		return a.res, a.err
	}

	allOpts := append([]RetryOption{
		WithBackOff(&ZeroBackOff{}),
		WithMaxElapsedTime(0),
		withTimer(tm),
		WithRetryBudget(rb),
	}, opts...)

	go func() {
		_, err := Retry(ctx, op, allOpts...)
		h.done <- err
	}()
	return h, rb
}

// startCall waits for an operation invocation to begin, then lets it finish
// and return its scripted result.
func (h *retryHarness) startCall(t *testing.T) {
	t.Helper()
	select {
	case <-h.started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for an operation attempt to start")
	}
	release := <-h.gate
	close(release)
}

// waitFor blocks until the loop enters a backoff wait, returning its release.
func (h *retryHarness) waitFor(t *testing.T) {
	t.Helper()
	select {
	case <-h.timer.notify:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the backoff wait to start")
	}
}

func (h *retryHarness) releaseWait() { h.timer.fire() }

func (h *retryHarness) await(t *testing.T) error {
	t.Helper()
	select {
	case err := <-h.done:
		return err
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Retry to finish")
		return nil
	}
}

var boom = errors.New("boom")

func alwaysFail() []attempt  { return []attempt{{err: boom}} }
func succeedOnce() []attempt { return []attempt{{res: 1}} }
