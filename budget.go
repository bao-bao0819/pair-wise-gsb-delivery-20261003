package backoff

import (
	"sync"
)

// RetryBudget bounds how many additional attempts a group of Retry calls may
// make. Share one budget between concurrent Retry calls to cap the total
// number of retries they can collectively spend, for example to keep a fleet
// of rate-limited clients from piling onto a struggling backend.
//
// The budget pays only for retries, never for first attempts: every Retry
// call runs its operation once without touching the budget.
//
// All methods must be safe for concurrent use.
type RetryBudget interface {
	// Acquire reserves permission for one additional attempt. It is called
	// after a failed attempt, before the backoff wait for the next one, and
	// only when Retry has already established that it otherwise would retry:
	// a Permanent error, WithMaxTries, WithMaxElapsedTime, a BackOff that
	// returns Stop, or a cancelled context always stops Retry without
	// reaching the budget.
	//
	// It returns a Reservation when one unit of budget is available. The
	// reservation is still held during the following wait: if the context
	// ends while waiting, Retry calls its Cancel method and the unconsumed
	// reservation must be returned to the budget. Once the next attempt has
	// actually started, the reservation is consumed and Cancel is not
	// called.
	//
	// It returns ok == false when no budget remains; Retry then stops with a
	// RetryError whose Cause is ErrBudgetExhausted.
	Acquire() (reservation Reservation, ok bool)

	// Succeed is called after every successful attempt, including the first
	// (free) one. A budget may use it to add budget back, so steady progress
	// earns capacity for future failures; it may also ignore the call
	// entirely. Whether success replenishes anything is up to the
	// implementation.
	Succeed()
}

// Reservation represents one unit of budget held while a Retry call waits
// between attempts. It is created by RetryBudget.Acquire.
//
// Exactly one of these outcomes applies to every reservation:
//
//   - The wait completes and the next attempt starts: the reservation is
//     consumed. Cancel is never called.
//   - The context is cancelled during the wait: Cancel is called exactly
//     once and the held unit must be returned to the budget.
//
// Cancel must be safe for concurrent use. It must not panic if called more
// than once, and only the first call returns the unit.
type Reservation interface {
	// Cancel returns this reservation to its budget because the attempt it
	// paid for never started. An attempt that started is not cancelled even
	// if it later fails.
	Cancel()
}

// TokenRetryBudget is a RetryBudget backed by a fixed number of tokens. Each
// Acquire takes one token; a reservation cancelled before its attempt starts
// puts the token back.
//
// The zero value is not usable; create one with NewTokenRetryBudget.
type TokenRetryBudget struct {
	mu     sync.Mutex
	tokens uint
	cap    uint
	refill bool
}

// TokenBudgetOption configures a TokenRetryBudget.
type TokenBudgetOption func(*TokenRetryBudget)

// NewTokenRetryBudget returns a TokenRetryBudget holding n tokens.
//
// By default Succeed is a no-op: the budget funds exactly n extra attempts
// across all Retry calls that share it, and cancelled waits return their
// tokens so they remain available.
//
// Pass WithRefillOnSuccess to instead give a token back after every
// successful attempt (up to the initial n), so a group that keeps succeeding
// keeps its failure allowance.
func NewTokenRetryBudget(n uint, opts ...TokenBudgetOption) *TokenRetryBudget {
	b := &TokenRetryBudget{tokens: n, cap: n}
	for _, opt := range opts {
		opt(b)
	}
	return b
}

// WithRefillOnSuccess makes Succeed return one token to the budget, capped at
// the initial token count.
func WithRefillOnSuccess() TokenBudgetOption {
	return func(b *TokenRetryBudget) {
		b.refill = true
	}
}

// Acquire takes one token. It returns a reservation, or false when no tokens
// remain.
func (b *TokenRetryBudget) Acquire() (Reservation, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.tokens == 0 {
		return nil, false
	}
	b.tokens--
	return &tokenReservation{budget: b}, true
}

// Succeed refunds one token after a successful attempt when the budget was
// created with WithRefillOnSuccess, capped at the initial token count.
func (b *TokenRetryBudget) Succeed() {
	if !b.refill {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.tokens < b.cap {
		b.tokens++
	}
}

// tokenReservation is the Reservation returned by TokenRetryBudget.Acquire.
type tokenReservation struct {
	budget *TokenRetryBudget
	cancel sync.Once
}

// Cancel returns the held token exactly once.
func (r *tokenReservation) Cancel() {
	r.cancel.Do(func() {
		r.budget.mu.Lock()
		defer r.budget.mu.Unlock()
		if r.budget.tokens < r.budget.cap {
			r.budget.tokens++
		}
	})
}
