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
	p.GoContext(context.Background(), f)
}

// GoContext submits a task to the pool, like Go, but allows the caller to
// cancel the wait for a free slot in the pool. The context only governs the
// submission itself - it is not propagated to the task.
//
// If GoContext returns nil, the task has been accepted by the pool and is
// guaranteed to be run (and counted by Wait). If the context is cancelled
// while waiting for a free slot, GoContext returns the context's error and
// the task is guaranteed to never run. Rejected tasks leave no trace in the
// results returned by Wait - no placeholder zero values and no gaps.
func (p *ResultPool[T]) GoContext(ctx context.Context, f func() T) error {
	idx := p.agg.nextIndex()
	err := p.pool.GoContext(ctx, func() {
		p.agg.save(idx, f(), false)
	})
	if err != nil {
		// The task was not accepted, so release its reserved slot to
		// avoid leaving a zero-valued hole in the results.
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

// cancelIndex releases a slot previously reserved with nextIndex for a task
// that was never accepted by the pool. The slot is skipped when collecting
// results so that no zero-valued placeholder or gap remains.
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

	var skipped []int
	if !collectErrored {
		skipped = append(skipped, r.errored...)
	}
	skipped = append(skipped, r.cancelled...)

	if len(skipped) == 0 {
		return r.results
	}

	filtered := r.results[:0]
	sort.Ints(skipped)
	prev := 0
	for _, s := range skipped {
		filtered = append(filtered, r.results[prev:s]...)
		prev = s + 1
	}
	filtered = append(filtered, r.results[prev:]...)
	return filtered
}
