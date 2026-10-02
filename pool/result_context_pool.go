package pool

import (
	"context"
)

// ResultContextPool is a pool that runs tasks that take a context and return a
// result. The context passed to the task will be canceled if any of the tasks
// return an error, which makes its functionality different than just capturing
// a context with the task closure.
//
// The configuration methods (With*) will panic if they are used after calling
// Go() for the first time.
type ResultContextPool[T any] struct {
	contextPool    ContextPool
	agg            resultAggregator[T]
	collectErrored bool
}

// Go submits a task to the pool. If all goroutines in the pool
// are busy, a call to Go() will block until the task can be started.
func (p *ResultContextPool[T]) Go(f func(context.Context) (T, error)) {
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
// Note that ctx only governs the submission of the task. The task itself
// still runs under the pool's context, so cancelling ctx after the task has
// been accepted has no effect.
func (p *ResultContextPool[T]) GoContext(ctx context.Context, f func(context.Context) (T, error)) error {
	idx := p.agg.nextIndex()
	err := p.contextPool.GoContext(ctx, func(ctx context.Context) error {
		res, err := f(ctx)
		p.agg.save(idx, res, err != nil)
		return err
	})
	if err != nil {
		// The task was not accepted, so it will never save a result.
		// Release the reserved slot so it does not leave a hole in the
		// collected results.
		p.agg.cancelIndex(idx)
	}
	return err
}

// Wait cleans up all spawned goroutines, propagates any panics, and
// returns an error if any of the tasks errored.
func (p *ResultContextPool[T]) Wait() ([]T, error) {
	err := p.contextPool.Wait()
	results := p.agg.collect(p.collectErrored)
	p.agg = resultAggregator[T]{}
	return results, err
}

// WithCollectErrored configures the pool to still collect the result of a task
// even if the task returned an error. By default, the result of tasks that errored
// are ignored and only the error is collected.
func (p *ResultContextPool[T]) WithCollectErrored() *ResultContextPool[T] {
	p.panicIfInitialized()
	p.collectErrored = true
	return p
}

// WithFirstError configures the pool to only return the first error
// returned by a task. By default, Wait() will return a combined error.
func (p *ResultContextPool[T]) WithFirstError() *ResultContextPool[T] {
	p.panicIfInitialized()
	p.contextPool.WithFirstError()
	return p
}

// WithCancelOnError configures the pool to cancel its context as soon as
// any task returns an error. By default, the pool's context is not
// canceled until the parent context is canceled.
func (p *ResultContextPool[T]) WithCancelOnError() *ResultContextPool[T] {
	p.panicIfInitialized()
	p.contextPool.WithCancelOnError()
	return p
}

// WithFailFast is an alias for the combination of WithFirstError and
// WithCancelOnError. By default, the errors from all tasks are returned and
// the pool's context is not canceled until the parent context is canceled.
func (p *ResultContextPool[T]) WithFailFast() *ResultContextPool[T] {
	p.panicIfInitialized()
	p.contextPool.WithFailFast()
	return p
}

// WithMaxGoroutines limits the number of goroutines in a pool.
// Defaults to unlimited. Panics if n < 1.
func (p *ResultContextPool[T]) WithMaxGoroutines(n int) *ResultContextPool[T] {
	p.panicIfInitialized()
	p.contextPool.WithMaxGoroutines(n)
	return p
}

func (p *ResultContextPool[T]) panicIfInitialized() {
	p.contextPool.panicIfInitialized()
}
