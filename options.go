package workerpool

import "time"

const (
	// DefaultIdleTimeout is default amount of time the worker pool must be
	// idle before stopping a worker.
	DefaultIdleTimeout = 2 * time.Second
)

type config struct {
	idleTimeout  time.Duration
	panicHandler func(any, []byte)
}

// Option is a function that sets a value in a config.
type Option func(*config)

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
// The handler receives the recovered panic value and the stack trace of the
// panicking goroutine, captured at the point of the panic.
//
// When a panic handler is set, a panicking task is counted as finished and
// its worker continues with the next task, which is useful for background
// batch processing that must keep draining subsequent tasks. When unset, a
// task panic propagates and crashes the program, as before.
//
// The handler is called synchronously on the worker goroutine, outside of
// any internal pool lock, so it may safely query pool state such as
// WaitingQueueSize or Stopped. No additional goroutines are started to
// report panics, so a slow handler only slows its own worker. If the handler
// itself panics, the panic is discarded so that the worker and the pool's
// shutdown accounting (Stop and StopWait) are not affected.
func WithPanicHandler(handler func(panicValue any, stack []byte)) Option {
	return func(c *config) {
		c.panicHandler = handler
	}
}
