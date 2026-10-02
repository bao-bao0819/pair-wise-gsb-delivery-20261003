package backoff

// Budget is a shared limit on how many retry attempts a group of Retry calls
// may make. While WithMaxTries caps each Retry call on its own, a Budget caps
// the combined number of retries across every Retry call that shares it —
// useful to keep a struggling downstream from being hit by a retry storm.
//
// A Budget may be shared between concurrent Retry calls when its
// implementation is safe for concurrent use.
//
// Retry uses a Budget as follows:
//   - The first attempt of each Retry call is free: it never touches the
//     budget.
//   - Before waiting to retry, Retry reserves one allowance with Acquire.
//     If Acquire reports the budget is spent, Retry stops with a *RetryError
//     whose Cause is ErrBudgetExhausted.
//   - A reservation is consumed when its wait completes and the attempt
//     enters the operation. Consumed reservations are never returned,
//     whether the attempt succeeds or fails. Replenishing the allowance —
//     on success, over time, or by any other policy — is up to the Budget
//     implementation.
//   - If the context is cancelled while Retry waits, the attempt never ran,
//     so its unused reservation is returned with Release.
//
// Retry acquires only once it has decided to retry: a permanent error,
// WithMaxTries, WithMaxElapsedTime, a Stop from the BackOff policy, or a
// cancelled context all stop Retry without touching the budget.
type Budget interface {
	// Acquire reserves allowance for one retry attempt and reports whether
	// the reservation was granted. It must not block: when the budget is
	// spent it returns false and Retry stops with ErrBudgetExhausted.
	Acquire() bool

	// Release returns a reservation made by Acquire that was not consumed
	// because its wait was cancelled. Retry calls it at most once per
	// successful Acquire, and never for a reservation whose attempt already
	// entered the operation.
	Release()
}
