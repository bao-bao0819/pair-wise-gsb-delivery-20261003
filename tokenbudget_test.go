package backoff

import (
	"sync"
	"testing"
)

func TestTokenRetryBudgetCapsRetries(t *testing.T) {
	b := NewTokenRetryBudget(2)

	r1, ok := b.Acquire()
	if !ok {
		t.Fatal("first acquire failed")
	}
	r2, ok := b.Acquire()
	if !ok {
		t.Fatal("second acquire failed")
	}
	if _, ok := b.Acquire(); ok {
		t.Fatal("third acquire succeeded with only 2 tokens")
	}

	// One cancelled wait returns its token; another acquire succeeds.
	r1.Cancel()
	r3, ok := b.Acquire()
	if !ok {
		t.Fatal("acquire after cancel failed")
	}

	// r2 and r3 funded attempts that started: they are spent and not
	// cancelled, so the budget stays empty.
	if _, ok := b.Acquire(); ok {
		t.Fatal("budget should be empty")
	}
	_ = r2
	_ = r3
}

func TestTokenRetryBudgetNoRefillByDefault(t *testing.T) {
	b := NewTokenRetryBudget(1)
	b.Succeed()
	b.Succeed()
	if _, ok := b.Acquire(); !ok {
		t.Fatal("initial token missing")
	}
	if _, ok := b.Acquire(); ok {
		t.Fatal("Succeed refilled without WithRefillOnSuccess")
	}
}

func TestTokenRetryBudgetRefillOnSuccessCapped(t *testing.T) {
	b := NewTokenRetryBudget(2, WithRefillOnSuccess())

	r1, _ := b.Acquire()
	r2, _ := b.Acquire()

	// Refund cannot grow beyond the initial capacity.
	b.Succeed()
	r1.Cancel()
	r2.Cancel()
	b.Succeed()
	b.Succeed()

	var n int
	for {
		if _, ok := b.Acquire(); !ok {
			break
		}
		n++
	}
	if n != 2 {
		t.Errorf("drained %d tokens after capped refill, want 2", n)
	}
}

func TestTokenRetryBudgetReservationCancelOnce(t *testing.T) {
	b := NewTokenRetryBudget(1)
	r, ok := b.Acquire()
	if !ok {
		t.Fatal("acquire failed")
	}
	r.Cancel()
	r.Cancel() // must be a no-op, not a panic or a second refund

	r2, ok := b.Acquire()
	if !ok {
		t.Fatal("acquire after cancel failed")
	}
	if _, ok := b.Acquire(); ok {
		t.Fatal("double cancel refunded twice")
	}
	r2.Cancel()
}

func TestTokenRetryBudgetConcurrent(t *testing.T) {
	const tokens = 100
	b := NewTokenRetryBudget(tokens)

	var wg sync.WaitGroup
	var granted, denied int64
	var mu sync.Mutex
	var reservations []Reservation

	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				r, ok := b.Acquire()
				if !ok {
					mu.Lock()
					denied++
					mu.Unlock()
					return
				}
				mu.Lock()
				granted++
				reservations = append(reservations, r)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if granted != tokens {
		t.Errorf("granted = %d, want %d", granted, tokens)
	}
	if denied != 20 {
		t.Errorf("denied = %d, want 20", denied)
	}

	// Return half during the wait, spend the other half; both outcomes leave a
	// consistent budget with no double refunds.
	for i, r := range reservations {
		if i%2 == 0 {
			r.Cancel()
		}
	}
	var refilled int
	for {
		if _, ok := b.Acquire(); !ok {
			break
		}
		refilled++
	}
	if refilled != tokens/2 {
		t.Errorf("refilled = %d, want %d", refilled, tokens/2)
	}
}

// TestTokenRetryBudgetSatisfiesInterface is a compile-time check.
func TestTokenRetryBudgetSatisfiesInterface(t *testing.T) {
	var _ RetryBudget = NewTokenRetryBudget(1)
	var _ RetryBudget = NewTokenRetryBudget(1, WithRefillOnSuccess())
	var _ Reservation = mustAcquire(t)
}

func mustAcquire(t *testing.T) Reservation {
	t.Helper()
	r, ok := NewTokenRetryBudget(1).Acquire()
	if !ok {
		t.Fatal("acquire failed")
	}
	return r
}
