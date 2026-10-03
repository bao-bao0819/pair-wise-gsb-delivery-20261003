package workerpool

import "time"

const (
	// DefaultIdleTimeout is default amount of time the worker pool must be
	// idle before stopping a worker.
	DefaultIdleTimeout = 2 * time.Second
)

type config struct {
	idleTimeout  time.Duration
	panicHandler PanicHandler
}

// Option is a function that sets a value in a config.
type Option func(*config)

// PanicHandler is a function called when a task panics. It receives the
// value passed to panic and the stack trace captured at the point of the
// panic.
//
// The handler is called synchronously by the worker that ran the failed
// task, before that worker accepts the next task. The worker does not hold
// any of the pool's internal locks while calling the handler, so the handler
// may safely query the pool's state, for example by calling
// [WorkerPool.WaitingQueueSize], [WorkerPool.Size], or
// [WorkerPool.Stopped]. The handler must not call [WorkerPool.Stop],
// [WorkerPool.StopWait], or [WorkerPool.Pause], since those wait on workers
// and would deadlock.
//
// If the handler itself panics, the panic propagates and crashes the
// program, the same as a task panicking without a handler. The worker's
// accounting is released before the panic propagates, so it cannot cause a
// count leak that would hang a concurrent [WorkerPool.StopWait].
type PanicHandler func(panicValue any, stack []byte)

// WithIdleTimeout configures the amount of time that the worker pool must be
// idle before a worker is automatically stopped. If zero or unset the value
// defaults to DefaultIdleTimeout. A negative value disables the idle timeout.
func WithIdleTimeout(timeout time.Duration) Option {
	return func(c *config) {
		if timeout != 0 {
			c.idleTimeout = timeout
		}
	}
}

// WithPanicHandler configures a handler that is called when a task panics.
// When set, a panicking task is counted as finished and its worker continues
// with the next task instead of crashing the program. When unset, a task
// panic propagates out of the worker goroutine and crashes the program.
//
// See [PanicHandler] for the rules the handler must follow.
func WithPanicHandler(handler PanicHandler) Option {
	return func(c *config) {
		c.panicHandler = handler
	}
}
