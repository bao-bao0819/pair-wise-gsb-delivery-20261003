package pool

import (
	"context"
	"errors"
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

	mu            sync.Mutex
	nextTaskIndex int
	errs          []error
	orderedErrs   []error
	firstErr      error
}

// Go submits a task to the pool. If all goroutines in the pool
// are busy, a call to Go() will block until the task can be started.
func (p *ErrorPool) Go(f func() error) {
	idx := p.nextIndex()
	p.goWithIndex(idx, f)
}

func (p *ErrorPool) goWithIndex(idx int, f func() error) {
	p.pool.Go(func() {
		p.addErr(idx, f())
	})
}

func (p *ErrorPool) nextIndex() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	idx := p.nextTaskIndex
	p.nextTaskIndex++
	return idx
}

// Wait cleans up any spawned goroutines, propagating any panics and
// returning any errors from tasks.
func (p *ErrorPool) Wait() error {
	defer p.reset()
	p.pool.Wait()

	if p.onlyFirstError {
		return p.firstErr
	} else if p.orderedErrors {
		if len(p.orderedErrs) == 0 {
			return nil
		}
		taskErrs := make(TaskErrors, 0, len(p.orderedErrs))
		for _, err := range p.orderedErrs {
			if err == nil {
				continue
			}
			taskErrs = append(taskErrs, err.(TaskError))
		}
		return taskErrs
	}

	if len(p.errs) == 0 {
		return nil
	}
	return errors.Join(p.errs...)
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
// The first error is determined by task completion order, not by task index.
// When ordered errors are enabled, it is still wrapped in a TaskError.
func (p *ErrorPool) WithFirstError() *ErrorPool {
	p.panicIfInitialized()
	p.onlyFirstError = true
	return p
}

// WithOrderedErrors configures the pool to return errors in the order that
// tasks were submitted, rather than the order in which tasks completed.
// Each error is wrapped in a TaskError so it can be associated with its
// original task.
//
// WithFirstError takes precedence when both options are configured: that
// option keeps the error from the task that failed first at runtime, not the
// lowest-indexed task.
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
		defer p.mu.Unlock()

		if p.onlyFirstError {
			if p.firstErr == nil {
				if p.orderedErrors {
					p.firstErr = TaskError{Index: idx, Err: err}
				} else {
					p.firstErr = err
				}
			}
			return
		}

		if p.orderedErrors {
			if idx >= len(p.orderedErrs) {
				old := p.orderedErrs
				p.orderedErrs = make([]error, idx+1)
				copy(p.orderedErrs, old)
			}
			p.orderedErrs[idx] = TaskError{Index: idx, Err: err}
		} else {
			p.errs = append(p.errs, err)
		}
	}
}

func (p *ErrorPool) reset() {
	p.mu.Lock()
	p.nextTaskIndex = 0
	p.errs = nil
	p.orderedErrs = nil
	p.firstErr = nil
	p.mu.Unlock()
}
