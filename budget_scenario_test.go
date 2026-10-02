package backoff

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestRetryBudgetConcurrentCount drives two concurrent Retry loops sharing one
// token and asserts the exact number of extra attempts across the group.
func TestRetryBudgetConcurrentCount(t *testing.T) {
	budget := newRecordingBudget(1, false)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	a, _ := startHarness(ctx, budget, alwaysFail())
	b, _ := startHarness(ctx, budget, alwaysFail())

	// First attempts are free: both loops run their operation once without
	// claiming budget.
	a.startCall(t)
	b.startCall(t)

	// Both loops want to retry; exactly one gets the single token and enters a
	// wait, the other is denied and terminates with ErrBudgetExhausted.
	var winner, loser *retryHarness
	select {
	case <-a.timer.notify:
		winner = a
		loser = b
	case <-b.timer.notify:
		winner = b
		loser = a
	case <-time.After(time.Second):
		t.Fatal("neither loop entered a wait")
	}

	// The loser ends without a wait.
	if err := loser.await(t); !errors.Is(err, ErrBudgetExhausted) {
		t.Fatalf("denied loop err = %v, want ErrBudgetExhausted", err)
	}

	// Release the winner: its funded second attempt starts (consuming the
	// reservation), fails, and is then denied a third attempt.
	winner.releaseWait()
	winner.startCall(t)
	if err := winner.await(t); !errors.Is(err, ErrBudgetExhausted) {
		t.Fatalf("winner err = %v, want ErrBudgetExhausted", err)
	}

	acquires, granted, denied, consumed, cancelled, succeeded, tokens := budget.state()
	if granted != 1 || consumed != 1 || cancelled != 0 {
		t.Errorf("granted=%d consumed=%d cancelled=%d, want 1/1/0", granted, consumed, cancelled)
	}
	if denied != 2 {
		t.Errorf("denied=%d, want 2 (loser first, winner after retry)", denied)
	}
	if acquires != 3 || acquires != granted+denied {
		t.Errorf("acquires=%d, want granted+denied=%d", acquires, granted+denied)
	}
	if succeeded != 0 || tokens != 0 {
		t.Errorf("succeeded=%d tokens=%d, want 0/0", succeeded, tokens)
	}
	budget.assertNoDangling(t)
}

// TestRetryBudgetCancelDuringWait verifies that cancelling while a loop waits
// returns the unconsumed reservation, and the refund funds another loop.
func TestRetryBudgetCancelDuringWait(t *testing.T) {
	t.Run("reservation is returned to a waiting caller", func(t *testing.T) {
		budget := newRecordingBudget(1, false)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		first, _ := startHarness(ctx, budget, alwaysFail())
		second, _ := startHarness(ctx, budget, alwaysFail())

		// Both first attempts fail.
		first.startCall(t)
		second.startCall(t)

		// One loop claims the token and waits; identify it.
		var waiter, denied *retryHarness
		select {
		case <-first.timer.notify:
			waiter, denied = first, second
		case <-second.timer.notify:
			waiter, denied = second, first
		}

		if err := denied.await(t); !errors.Is(err, ErrBudgetExhausted) {
			t.Fatalf("denied loop err = %v, want ErrBudgetExhausted", err)
		}

		// Cancel the waiter's context during the wait: reservation returned.
		cancel()
		if err := waiter.await(t); !errors.Is(err, context.Canceled) {
			t.Fatalf("waiter err = %v, want context.Canceled", err)
		}

		_, granted, _, consumed, cancelled, _, tokens := budget.state()
		if granted != 1 || consumed != 0 || cancelled != 1 || tokens != 1 {
			t.Errorf("granted=%d consumed=%d cancelled=%d tokens=%d, want 1/0/1/1",
				granted, consumed, cancelled, tokens)
		}

		// A fresh loop can now spend the returned token: first attempt free,
		// the funded retry runs and is marked consumed.
		fresh, _ := startHarness(context.Background(), budget, []attempt{
			{err: boom},
			{res: 1},
		})
		fresh.startCall(t)
		fresh.waitFor(t)
		fresh.releaseWait()
		fresh.startCall(t)
		if err := fresh.await(t); err != nil {
			t.Fatalf("fresh Retry: %v", err)
		}

		_, granted, _, consumed, cancelled, succeeded, tokens := budget.state()
		if granted != 2 || consumed != 1 || cancelled != 1 {
			t.Errorf("granted=%d consumed=%d cancelled=%d, want 2/1/1",
				granted, consumed, cancelled)
		}
		if succeeded != 1 || tokens != 0 {
			t.Errorf("succeeded=%d tokens=%d, want 1/0", succeeded, tokens)
		}
		budget.assertNoDangling(t)
	})

	t.Run("an attempt that started is not returned", func(t *testing.T) {
		// Even if cancellation lands right as the funded attempt is running,
		// the attempt already started so the reservation stays consumed.
		budget := newRecordingBudget(1, false)
		ctx, cancel := context.WithCancel(context.Background())

		h, _ := startHarness(ctx, budget, alwaysFail())
		h.startCall(t) // free first attempt fails
		h.waitFor(t)
		h.releaseWait()
		h.startCall(t) // funded attempt started: reservation consumed
		cancel()       // too late to return it
		if err := h.await(t); !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}

		_, granted, _, consumed, cancelled, _, tokens := budget.state()
		if granted != 1 || consumed != 1 || cancelled != 0 || tokens != 0 {
			t.Errorf("granted=%d consumed=%d cancelled=%d tokens=%d, want 1/1/0/0",
				granted, consumed, cancelled, tokens)
		}
		budget.assertNoDangling(t)
	})
}

// TestRetryBudgetPriority proves that every stopping branch which does not
// lead to an additional attempt is resolved before the budget is touched:
// Permanent, MaxTries, MaxElapsedTime, a BackOff returning Stop, and an
// already-cancelled context never claim (let alone leave) a reservation.
func TestRetryBudgetPriority(t *testing.T) {
	t.Run("permanent error does not claim budget", func(t *testing.T) {
		budget := newRecordingBudget(1, false)
		h, _ := startHarness(context.Background(), budget,
			[]attempt{{err: Permanent(boom)}})
		h.startCall(t)
		if err := h.await(t); !errors.Is(err, ErrPermanent) {
			t.Fatalf("err = %v, want ErrPermanent", err)
		}
		assertUntouched(t, budget)
	})

	t.Run("max tries does not claim budget", func(t *testing.T) {
		budget := newRecordingBudget(1, false)
		h, _ := startHarness(context.Background(), budget, alwaysFail(), WithMaxTries(2))
		h.startCall(t)
		h.waitFor(t)
		h.releaseWait()
		h.startCall(t) // second attempt, which hits the limit
		if err := h.await(t); !errors.Is(err, ErrExhausted) {
			t.Fatalf("err = %v, want ErrExhausted", err)
		}
		acquires, granted, denied, consumed, cancelled, _, _ := budget.state()
		if acquires != 1 || granted != 1 || denied != 0 || consumed != 1 || cancelled != 0 {
			t.Errorf("acquires=%d granted=%d denied=%d consumed=%d cancelled=%d, want 1/1/0/1/0",
				acquires, granted, denied, consumed, cancelled)
		}
		budget.assertNoDangling(t)
	})

	t.Run("max elapsed time does not claim budget", func(t *testing.T) {
		budget := newRecordingBudget(1, false)
		h, _ := startHarness(context.Background(), budget, alwaysFail(),
			WithMaxElapsedTime(1), WithBackOff(NewConstantBackOff(time.Hour)))
		h.startCall(t)
		if err := h.await(t); !errors.Is(err, ErrMaxElapsedTime) {
			t.Fatalf("err = %v, want ErrMaxElapsedTime", err)
		}
		assertUntouched(t, budget)
	})

	t.Run("backoff stop does not claim budget", func(t *testing.T) {
		budget := newRecordingBudget(1, false)
		h, _ := startHarness(context.Background(), budget, alwaysFail(),
			WithBackOff(&StopBackOff{}))
		h.startCall(t)
		if err := h.await(t); !errors.Is(err, ErrExhausted) {
			t.Fatalf("err = %v, want ErrExhausted", err)
		}
		assertUntouched(t, budget)
	})

	t.Run("cancelled context does not claim budget", func(t *testing.T) {
		budget := newRecordingBudget(1, false)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		h, _ := startHarness(ctx, budget, alwaysFail())
		h.startCall(t)
		if err := h.await(t); !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
		assertUntouched(t, budget)
	})

	t.Run("immediate success does not claim budget", func(t *testing.T) {
		budget := newRecordingBudget(1, false)
		h, _ := startHarness(context.Background(), budget, succeedOnce())
		h.startCall(t)
		if err := h.await(t); err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		acquires, _, denied, _, _, succeeded, tokens := budget.state()
		if acquires != 0 || denied != 0 || succeeded != 1 || tokens != 1 {
			t.Errorf("acquires=%d denied=%d succeeded=%d tokens=%d, want 0/0/1/1",
				acquires, denied, succeeded, tokens)
		}
		budget.assertNoDangling(t)
	})
}

func assertUntouched(t *testing.T, b *recordingBudget) {
	t.Helper()
	acquires, granted, denied, consumed, cancelled, succeeded, tokens := b.state()
	if acquires != 0 || granted != 0 || denied != 0 || consumed != 0 || cancelled != 0 {
		t.Errorf("budget touched: acquires=%d granted=%d denied=%d consumed=%d cancelled=%d",
			acquires, granted, denied, consumed, cancelled)
	}
	if succeeded != 0 || tokens != 1 {
		t.Errorf("succeeded=%d tokens=%d, want 0/1", succeeded, tokens)
	}
	b.assertNoDangling(t)
}
