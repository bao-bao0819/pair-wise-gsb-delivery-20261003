package pool

import (
	"context"
)

// ResultErrorPool is a pool that executes tasks that return a generic result
// type and an error. Tasks are executed in the pool with Go(), then the
// results of the tasks are returned by Wait().
//
// The order of the results is guaranteed to be the same as the order the
// tasks were submitted.
//
// The configuration methods (With*) will panic if they are used after calling
// Go() for the first time.
type ResultErrorPool[T any] struct {
	errorPool      ErrorPool
	agg            resultAggregator[T]
	collectErrored bool
}

// Go submits a task to the pool. If all goroutines in the pool
// are busy, a call to Go() will block until the task can be started.
func (p *ResultErrorPool[T]) Go(f func() (T, error)) {
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
func (p *ResultErrorPool[T]) GoContext(ctx context.Context, f func() (T, error)) error {
	idx := p.agg.nextIndex()
	err := p.errorPool.GoContext(ctx, func() error {
		res, err := f()
		p.agg.save(idx, res, err != nil)
		return err
	})
	if err != nil {
		// The task was not accepted, so release its reserved slot to
		// avoid leaving a zero-valued hole in the results.
		p.agg.cancelIndex(idx)
	}
	return err
}

// Wait cleans up any spawned goroutines, propagating any panics and
// returning the results and any errors from tasks.
func (p *ResultErrorPool[T]) Wait() ([]T, error) {
	err := p.errorPool.Wait()
	results := p.agg.collect(p.collectErrored)
	p.agg = resultAggregator[T]{} // reset for reuse
	return results, err
}

// WithCollectErrored configures the pool to still collect the result of a task
// even if the task returned an error. By default, the result of tasks that errored
// are ignored and only the error is collected.
func (p *ResultErrorPool[T]) WithCollectErrored() *ResultErrorPool[T] {
	p.panicIfInitialized()
	p.collectErrored = true
	return p
}

// WithContext converts the pool to a ResultContextPool for tasks that should
// run under the same context, such that they each respect shared cancellation.
// For example, WithCancelOnError can be configured on the returned pool to
// signal that all goroutines should be cancelled upon the first error.
func (p *ResultErrorPool[T]) WithContext(ctx context.Context) *ResultContextPool[T] {
	p.panicIfInitialized()
	return &ResultContextPool[T]{
		contextPool: *p.errorPool.WithContext(ctx),
	}
}

// WithFirstError configures the pool to only return the first error
// returned by a task. By default, Wait() will return a combined error.
func (p *ResultErrorPool[T]) WithFirstError() *ResultErrorPool[T] {
	p.panicIfInitialized()
	p.errorPool.WithFirstError()
	return p
}

// WithMaxGoroutines limits the number of goroutines in a pool.
// Defaults to unlimited. Panics if n < 1.
func (p *ResultErrorPool[T]) WithMaxGoroutines(n int) *ResultErrorPool[T] {
	p.panicIfInitialized()
	p.errorPool.WithMaxGoroutines(n)
	return p
}

func (p *ResultErrorPool[T]) panicIfInitialized() {
	p.errorPool.panicIfInitialized()
}
