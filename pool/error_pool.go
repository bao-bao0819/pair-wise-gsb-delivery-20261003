package pool

import (
	"context"
	"errors"
	"sort"
	"sync"
)

// ErrorPool is a pool that runs tasks that may return an error.
// Errors are collected and returned by Wait().
//
// The configuration methods (With*) will panic if they are used after calling
// Go() for the first time.
//
// A new ErrorPool should be created using `New().WithErrors()`.
type ErrorPool struct {
	pool Pool

	onlyFirstError bool
	orderedErrors  bool

	mu       sync.Mutex
	numTasks int
	errs     []indexedError
}

// TaskError is an error returned by a task submitted to a pool configured
// with WithOrderedErrors. It records the index of the task in the order it
// was submitted with Go(), so every error in the aggregated error can be
// traced back to the task that produced it. The original error is available
// via Err and remains accessible through errors.Is and errors.As.
type TaskError struct {
	// Index is the position of the task in submission order, starting at 0.
	Index int
	// Err is the error returned by the task.
	Err error
}

func (e *TaskError) Error() string { return e.Err.Error() }

func (e *TaskError) Unwrap() error { return e.Err }

// indexedError is an error collected from a task along with the task's
// submission index.
type indexedError struct {
	index int
	err   error
}

// Go submits a task to the pool. If all goroutines in the pool
// are busy, a call to Go() will block until the task can be started.
func (p *ErrorPool) Go(f func() error) {
	p.goWithIndex(p.nextIndex(), f)
}

// goWithIndex submits a task with a pre-reserved submission index.
func (p *ErrorPool) goWithIndex(idx int, f func() error) {
	p.pool.Go(func() {
		p.addErr(idx, f())
	})
}

// nextIndex reserves a submission index for a task.
func (p *ErrorPool) nextIndex() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	idx := p.numTasks
	p.numTasks++
	return idx
}

// Wait cleans up any spawned goroutines, propagating any panics and
// returning any errors from tasks.
func (p *ErrorPool) Wait() error {
	p.pool.Wait()

	errs := p.errs
	p.errs = nil   // reset errs
	p.numTasks = 0 // reset submission indices for reuse

	if len(errs) == 0 {
		return nil
	} else if p.onlyFirstError {
		// Errors are collected in completion order, so the first one is the
		// first task to fail. WithOrderedErrors does not change this: the
		// first error to occur is returned, not the error from the task with
		// the lowest submission index.
		return errs[0].err
	} else if p.orderedErrors {
		sort.Slice(errs, func(i, j int) bool { return errs[i].index < errs[j].index })
		ordered := make([]error, len(errs))
		for i, err := range errs {
			ordered[i] = &TaskError{Index: err.index, Err: err.err}
		}
		return errors.Join(ordered...)
	} else {
		unwrapped := make([]error, len(errs))
		for i, err := range errs {
			unwrapped[i] = err.err
		}
		return errors.Join(unwrapped...)
	}
}

// WithContext converts the pool to a ContextPool for tasks that should
// run under the same context, such that they each respect shared cancellation.
// For example, WithCancelOnError can be configured on the returned pool to
// signal that all goroutines should be cancelled upon the first error.
func (p *ErrorPool) WithContext(ctx context.Context) *ContextPool {
	p.panicIfInitialized()
	ctx, cancel := context.WithCancel(ctx)
	return &ContextPool{
		errorPool: p.deref(),
		ctx:       ctx,
		cancel:    cancel,
	}
}

// WithFirstError configures the pool to only return the first error
// returned by a task. By default, Wait() will return a combined error.
func (p *ErrorPool) WithFirstError() *ErrorPool {
	p.panicIfInitialized()
	p.onlyFirstError = true
	return p
}

// WithOrderedErrors configures the pool to aggregate errors in the order the
// tasks were submitted with Go(), rather than the order in which they
// happened to complete. Each error in the aggregated error is wrapped in a
// *TaskError that records the submission index of the task that returned it,
// so errors can always be traced back to their task. The original errors
// remain accessible via errors.Is and errors.As.
//
// WithOrderedErrors does not affect WithFirstError: if both are configured,
// Wait() still returns the first error to occur (not the error from the task
// with the lowest submission index), unwrapped.
func (p *ErrorPool) WithOrderedErrors() *ErrorPool {
	p.panicIfInitialized()
	p.orderedErrors = true
	return p
}

// WithMaxGoroutines limits the number of goroutines in a pool.
// Defaults to unlimited. Panics if n < 1.
func (p *ErrorPool) WithMaxGoroutines(n int) *ErrorPool {
	p.panicIfInitialized()
	p.pool.WithMaxGoroutines(n)
	return p
}

// deref is a helper that creates a shallow copy of the pool with the same
// settings. We don't want to just dereference the pointer because that makes
// the copylock lint angry.
func (p *ErrorPool) deref() ErrorPool {
	return ErrorPool{
		pool:           p.pool.deref(),
		onlyFirstError: p.onlyFirstError,
		orderedErrors:  p.orderedErrors,
	}
}

func (p *ErrorPool) panicIfInitialized() {
	p.pool.panicIfInitialized()
}

func (p *ErrorPool) addErr(idx int, err error) {
	if err != nil {
		p.mu.Lock()
		p.errs = append(p.errs, indexedError{index: idx, err: err})
		p.mu.Unlock()
	}
}
