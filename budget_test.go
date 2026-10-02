package backoff

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// countingBudget is a Budget test double with exact call accounting.
type countingBudget struct {
	mu        sync.Mutex
	allowance int
	acquires  int
	releases  int
}

func (b *countingBudget) Acquire() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.acquires++
	if b.allowance == 0 {
		return false
	}
	b.allowance--
	return true
}

func (b *countingBudget) Release() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.releases++
	b.allowance++
}

func (b *countingBudget) counts() (acquires, releases, allowance int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.acquires, b.releases, b.allowance
}

// manualTimer is a timer the test drives by hand: Start reports that a wait
// has begun and blocks until the test observes it, and the wait ends when
// the test sends on fire.
type manualTimer struct {
	started chan struct{}
	fire    chan time.Time
}

func newManualTimer() *manualTimer {
	return &manualTimer{started: make(chan struct{}), fire: make(chan time.Time)}
}

func (t *manualTimer) Start(time.Duration) { t.started <- struct{}{} }
func (t *manualTimer) Stop()               {}
func (t *manualTimer) C() <-chan time.Time { return t.fire }

func TestRetryBudgetSharedByConcurrentLoops(t *testing.T) {
	// Two concurrent Retry loops share a budget of two allowances. Loop A
	// succeeds on its second attempt; loop B is cancelled while waiting.
	budget := &countingBudget{allowance: 2}
	opErr := errors.New("boom")
	stopErr := errors.New("stop")

	timerA := newManualTimer()
	ctxB, cancelB := context.WithCancelCause(context.Background())
	defer cancelB(context.Canceled)
	timerB := newManualTimer()

	type outcome struct {
		res   int
		err   error
		calls int
	}
	resultA := make(chan outcome, 1)
	resultB := make(chan outcome, 1)

	go func() {
		calls := 0
		res, err := Retry(context.Background(), func() (int, error) {
			calls++
			if calls == 1 {
				return 0, opErr
			}
			return 42, nil
		}, WithBackOff(&ZeroBackOff{}), WithMaxElapsedTime(0), withTimer(timerA), WithBudget(budget))
		resultA <- outcome{res, err, calls}
	}()

	go func() {
		calls := 0
		res, err := Retry(ctxB, func() (int, error) {
			calls++
			return 0, opErr
		}, WithBackOff(&ZeroBackOff{}), WithMaxElapsedTime(0), withTimer(timerB), WithBudget(budget))
		resultB <- outcome{res, err, calls}
	}()

	// Both loops run their free first attempt, reserve one allowance each,
	// and block in the backoff wait.
	<-timerA.started
	<-timerB.started

	// Loop A's wait completes: its reservation is consumed by the second
	// attempt, which succeeds. A consumed reservation is never returned.
	timerA.fire <- time.Now()
	if got := <-resultA; got.err != nil || got.res != 42 || got.calls != 2 {
		t.Errorf("loop A = %+v, want res 42, no error, 2 calls", got)
	}

	// Loop B is cancelled while waiting: its unused reservation is returned.
	cancelB(stopErr)
	gotB := <-resultB
	if !errors.Is(gotB.err, stopErr) {
		t.Errorf("loop B Cause = %v, want %v", gotB.err, stopErr)
	}
	if !errors.Is(gotB.err, opErr) {
		t.Errorf("loop B error = %v, want last operation error preserved", gotB.err)
	}
	if gotB.calls != 1 {
		t.Errorf("loop B calls = %d, want 1 (cancelled before the retry ran)", gotB.calls)
	}

	// Exact accounting: two reservations, one consumed by A's successful
	// retry, one returned by B — no dangling reservation.
	acquires, releases, allowance := budget.counts()
	if acquires != 2 || releases != 1 || allowance != 1 {
		t.Errorf("budget acquires=%d releases=%d allowance=%d, want 2/1/1", acquires, releases, allowance)
	}
}

func TestRetryBudgetExhausted(t *testing.T) {
	// Two loops share a budget with a single allowance. Loop A holds it
	// while waiting; loop B finds the budget spent and stops without waiting.
	budget := &countingBudget{allowance: 1}
	opErr := errors.New("boom")

	ctxA, cancelA := context.WithCancel(context.Background())
	defer cancelA()
	timerA := newManualTimer()
	resultA := make(chan error, 1)
	go func() {
		_, err := Retry(ctxA, func() (int, error) {
			return 0, opErr
		}, WithBackOff(&ZeroBackOff{}), WithMaxElapsedTime(0), withTimer(timerA), WithBudget(budget))
		resultA <- err
	}()

	// Loop A reserves the only allowance and blocks in the wait.
	<-timerA.started

	// Loop B's first attempt is free, but the retry finds the budget spent.
	timerB := newManualTimer()
	callsB := 0
	_, errB := Retry(context.Background(), func() (int, error) {
		callsB++
		return 0, opErr
	}, WithBackOff(&ZeroBackOff{}), WithMaxElapsedTime(0), withTimer(timerB), WithBudget(budget))
	if !errors.Is(errB, ErrBudgetExhausted) {
		t.Errorf("loop B error = %v, want ErrBudgetExhausted", errB)
	}
	if !errors.Is(errB, opErr) {
		t.Errorf("loop B error = %v, want last operation error preserved", errB)
	}
	if callsB != 1 {
		t.Errorf("loop B calls = %d, want 1 (only the free first attempt)", callsB)
	}
	select {
	case <-timerB.started:
		t.Error("loop B waited despite the exhausted budget")
	default:
	}

	// Cancelling loop A during its wait returns its reservation, leaving no
	// dangling reservation behind.
	cancelA()
	if errA := <-resultA; !errors.Is(errA, context.Canceled) {
		t.Errorf("loop A error = %v, want context.Canceled", errA)
	}
	acquires, releases, allowance := budget.counts()
	if acquires != 2 || releases != 1 || allowance != 1 {
		t.Errorf("budget acquires=%d releases=%d allowance=%d, want 2/1/1", acquires, releases, allowance)
	}
}

func TestRetryBudgetConsumedByAttempt(t *testing.T) {
	// A reservation whose wait completed is consumed by the attempt: when
	// the retried operation then fails into WithMaxTries, nothing is
	// returned to the budget.
	budget := &countingBudget{allowance: 1}
	opErr := errors.New("boom")
	tm := newManualTimer()

	calls := 0
	done := make(chan error, 1)
	go func() {
		_, err := Retry(context.Background(), func() (int, error) {
			calls++
			return 0, opErr
		}, WithBackOff(&ZeroBackOff{}), WithMaxElapsedTime(0), WithMaxTries(2), withTimer(tm), WithBudget(budget))
		done <- err
	}()

	<-tm.started
	tm.fire <- time.Now()

	if err := <-done; !errors.Is(err, ErrExhausted) {
		t.Fatalf("error = %v, want ErrExhausted", err)
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2", calls)
	}
	// The reservation paid for the second attempt: consumed, not returned.
	acquires, releases, allowance := budget.counts()
	if acquires != 1 || releases != 0 || allowance != 0 {
		t.Errorf("budget acquires=%d releases=%d allowance=%d, want 1/0/0", acquires, releases, allowance)
	}
}

func TestRetryBudgetFirstAttemptFree(t *testing.T) {
	// A budget with no allowance at all still lets the first attempt run,
	// and a first-attempt success never touches the budget.
	budget := &countingBudget{}
	res, err := Retry(context.Background(),
		func() (int, error) { return 7, nil },
		withTimer(&testTimer{}),
		WithBudget(budget),
	)
	if err != nil || res != 7 {
		t.Fatalf("Retry = (%d, %v), want (7, nil)", res, err)
	}
	if acquires, _, _ := budget.counts(); acquires != 0 {
		t.Errorf("budget acquired %d times, want 0", acquires)
	}
}

func TestRetryBudgetNotTouchedWhenStopping(t *testing.T) {
	// Every stop check takes precedence over the budget: none of these
	// endings may reserve allowance they would never use.
	opErr := errors.New("boom")

	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []struct {
		name string
		ctx  context.Context
		op   func() (int, error)
		opts []RetryOption
		want error
	}{
		{
			name: "permanent error",
			ctx:  context.Background(),
			op:   func() (int, error) { return 0, Permanent(opErr) },
			want: ErrPermanent,
		},
		{
			name: "max tries",
			ctx:  context.Background(),
			op:   func() (int, error) { return 0, opErr },
			opts: []RetryOption{WithMaxTries(1)},
			want: ErrExhausted,
		},
		{
			name: "max elapsed time",
			ctx:  context.Background(),
			op:   func() (int, error) { return 0, opErr },
			opts: []RetryOption{WithMaxElapsedTime(time.Millisecond), WithBackOff(NewConstantBackOff(time.Second))},
			want: ErrMaxElapsedTime,
		},
		{
			name: "backoff stop",
			ctx:  context.Background(),
			op:   func() (int, error) { return 0, opErr },
			opts: []RetryOption{WithBackOff(&StopBackOff{})},
			want: ErrExhausted,
		},
		{
			name: "context cancelled",
			ctx:  cancelledCtx,
			op:   func() (int, error) { return 0, opErr },
			want: context.Canceled,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			budget := &countingBudget{allowance: 1}
			opts := append([]RetryOption{WithBudget(budget), withTimer(&testTimer{})}, tt.opts...)
			_, err := Retry(tt.ctx, tt.op, opts...)
			if !errors.Is(err, tt.want) {
				t.Errorf("error = %v, want %v", err, tt.want)
			}
			if acquires, _, _ := budget.counts(); acquires != 0 {
				t.Errorf("budget acquired %d times, want 0", acquires)
			}
		})
	}
}
