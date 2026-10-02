package pool

import (
	"context"
	"sort"
	"sync"
)

// NewWithResults creates a new ResultPool for tasks with a result of type T.
//
// The configuration methods (With*) will panic if they are used after calling
// Go() for the first time.
func NewWithResults[T any]() *ResultPool[T] {
	return &ResultPool[T]{
		pool: *New(),
	}
}

// ResultPool is a pool that executes tasks that return a generic result type.
// Tasks are executed in the pool with Go(), then the results of the tasks are
// returned by Wait().
//
// The order of the results is guaranteed to be the same as the order the
// tasks were submitted.
type ResultPool[T any] struct {
	pool Pool
	agg  resultAggregator[T]
}

// Go submits a task to the pool. If all goroutines in the pool
// are busy, a call to Go() will block until the task can be started.
func (p *ResultPool[T]) Go(f func() T) {
	// A background context can never be cancelled, so this can never fail.
	_ = p.GoContext(context.Background(), f)
}

// GoContext submits a task to the pool, like Go(), but the wait for a free
// slot can be cancelled with ctx. If all goroutines in the pool are busy,
// GoContext() blocks until the task can be started or ctx is cancelled.
//
// The task is considered accepted exactly when GoContext() returns nil: an
// accepted task is guaranteed to be executed and is accounted for by Wait().
// If GoContext() returns an error (ctx.Err()), the task was not accepted and
// will never run. Rejected tasks do not leave a gap in the results returned
// by Wait().
//
// Note that ctx only governs the submission of the task. It is not
// propagated to the task itself, so cancelling ctx after the task has been
// accepted has no effect.
func (p *ResultPool[T]) GoContext(ctx context.Context, f func() T) error {
	idx := p.agg.nextIndex()
	err := p.pool.GoContext(ctx, func() {
		p.agg.save(idx, f(), false)
	})
	if err != nil {
		// The task was not accepted, so it will never save a result.
		// Release the reserved slot so it does not leave a hole in the
		// collected results.
		p.agg.cancelIndex(idx)
	}
	return err
}

// Wait cleans up all spawned goroutines, propagating any panics, and returning
// a slice of results from tasks that did not panic.
func (p *ResultPool[T]) Wait() []T {
	p.pool.Wait()
	results := p.agg.collect(true)
	p.agg = resultAggregator[T]{} // reset for reuse
	return results
}

// MaxGoroutines returns the maximum size of the pool.
func (p *ResultPool[T]) MaxGoroutines() int {
	return p.pool.MaxGoroutines()
}

// WithErrors converts the pool to an ResultErrorPool so the submitted tasks
// can return errors.
func (p *ResultPool[T]) WithErrors() *ResultErrorPool[T] {
	p.panicIfInitialized()
	return &ResultErrorPool[T]{
		errorPool: *p.pool.WithErrors(),
	}
}

// WithContext converts the pool to a ResultContextPool for tasks that should
// run under the same context, such that they each respect shared cancellation.
// For example, WithCancelOnError can be configured on the returned pool to
// signal that all goroutines should be cancelled upon the first error.
func (p *ResultPool[T]) WithContext(ctx context.Context) *ResultContextPool[T] {
	p.panicIfInitialized()
	return &ResultContextPool[T]{
		contextPool: *p.pool.WithContext(ctx),
	}
}

// WithMaxGoroutines limits the number of goroutines in a pool.
// Defaults to unlimited. Panics if n < 1.
func (p *ResultPool[T]) WithMaxGoroutines(n int) *ResultPool[T] {
	p.panicIfInitialized()
	p.pool.WithMaxGoroutines(n)
	return p
}

func (p *ResultPool[T]) panicIfInitialized() {
	p.pool.panicIfInitialized()
}

// resultAggregator is a utility type that lets us safely append from multiple
// goroutines. The zero value is valid and ready to use.
type resultAggregator[T any] struct {
	mu        sync.Mutex
	len       int
	results   []T
	errored   []int
	cancelled []int
}

// nextIndex reserves a slot for a result. The returned value should be passed
// to save() when adding a result to the aggregator.
func (r *resultAggregator[T]) nextIndex() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	nextIdx := r.len
	r.len += 1
	return nextIdx
}

// cancelIndex releases a slot reserved by nextIndex() for a task that will
// never save a result, so the slot is skipped when collecting results.
func (r *resultAggregator[T]) cancelIndex(i int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.cancelled = append(r.cancelled, i)
}

func (r *resultAggregator[T]) save(i int, res T, errored bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if i >= len(r.results) {
		old := r.results
		r.results = make([]T, r.len)
		copy(r.results, old)
	}

	r.results[i] = res

	if errored {
		r.errored = append(r.errored, i)
	}
}

// collect returns the set of aggregated results.
func (r *resultAggregator[T]) collect(collectErrored bool) []T {
	if !r.mu.TryLock() {
		panic("collect should not be called until all goroutines have exited")
	}

	if len(r.cancelled) == 0 && (collectErrored || len(r.errored) == 0) {
		return r.results
	}

	sort.Ints(r.errored)
	sort.Ints(r.cancelled)

	filtered := r.results[:0]
	erroredIdx := 0
	cancelledIdx := 0
	for i, res := range r.results {
		for cancelledIdx < len(r.cancelled) && r.cancelled[cancelledIdx] < i {
			cancelledIdx++
		}
		if cancelledIdx < len(r.cancelled) && r.cancelled[cancelledIdx] == i {
			continue
		}
		for erroredIdx < len(r.errored) && r.errored[erroredIdx] < i {
			erroredIdx++
		}
		if !collectErrored && erroredIdx < len(r.errored) && r.errored[erroredIdx] == i {
			continue
		}
		filtered = append(filtered, res)
	}
	return filtered
}
