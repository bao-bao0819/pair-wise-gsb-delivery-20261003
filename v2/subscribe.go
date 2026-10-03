package gobreaker

import (
	"sync/atomic"
	"time"
)

const defaultSubscriptionBufferSize = 16

// OverflowPolicy determines what happens to a StateTransition when the
// queue of a slow subscriber is full.
type OverflowPolicy int

const (
	// DropNewest discards the incoming StateTransition when the subscriber
	// queue is full. The subscriber keeps observing the oldest queued
	// transitions and can detect the gap through StateTransition.Seq.
	DropNewest OverflowPolicy = iota
	// DropOldest evicts the oldest queued StateTransition to make room for
	// the incoming one. The subscriber keeps observing the most recent
	// transitions and can detect the gap through StateTransition.Seq.
	DropOldest
)

// StateTransition is an immutable snapshot of a single state transition of
// a CircuitBreaker.
type StateTransition struct {
	// Name is the name of the CircuitBreaker.
	Name string
	// From is the state before the transition.
	From State
	// To is the state after the transition.
	To State
	// Seq is a monotonically increasing sequence number assigned to every
	// actual transition, starting at 1. All subscribers observe the same
	// Seq for the same transition; a gap in Seq means transitions were
	// dropped according to the OverflowPolicy of the subscription.
	Seq uint64
	// Counts is a snapshot of the internal Counts as they were right before
	// the transition reset them, i.e. the counts that led to the transition.
	Counts Counts
	// Time is the time at which the transition occurred.
	Time time.Time
}

// SubscribeOptions configures a subscription.
type SubscribeOptions struct {
	// BufferSize is the capacity of the per-subscriber queue.
	// If BufferSize is less than or equal to 0, a default capacity is used.
	BufferSize int
	// Overflow determines the policy applied when the queue is full.
	// If zero, DropNewest is used.
	Overflow OverflowPolicy
}

// Subscription is a handle to an active state-transition subscription.
type Subscription interface {
	// Unsubscribe removes the subscription. After Unsubscribe returns, no
	// new callback invocation is started for this subscription; a callback
	// that is already running is not interrupted. Unsubscribe is safe to
	// call multiple times and from within the callback itself.
	Unsubscribe()
}

type subscriber struct {
	id      uint64
	queue   chan StateTransition
	policy  OverflowPolicy
	done    chan struct{}
	stopped atomic.Bool
	remove  func(id uint64)
	fn      func(StateTransition)
}

// Subscribe registers a callback that is invoked for every state transition
// of the CircuitBreaker.
//
// Each transition is published as a StateTransition carrying a monotonically
// increasing sequence number and a snapshot of the counts that caused the
// transition. A single subscriber observes transitions in sequence order.
//
// The callback runs in a dedicated goroutine per subscription and is never
// invoked while the internal state lock is held, so the callback may safely
// call State, Counts and Unsubscribe without deadlocking. Publishing a
// transition never blocks the CircuitBreaker: if the queue of a slow
// subscriber is full, the transition is dropped according to the
// OverflowPolicy of the subscription.
//
// A panic in the callback is recovered and does not affect the
// CircuitBreaker, other subscribers, or subsequent notifications of the
// same subscription.
func (cb *CircuitBreaker[T]) Subscribe(fn func(StateTransition), opts ...SubscribeOptions) Subscription {
	var opt SubscribeOptions
	if len(opts) > 0 {
		opt = opts[0]
	}
	if opt.BufferSize <= 0 {
		opt.BufferSize = defaultSubscriptionBufferSize
	}

	sub := &subscriber{
		queue:  make(chan StateTransition, opt.BufferSize),
		policy: opt.Overflow,
		done:   make(chan struct{}),
		fn:     fn,
	}
	sub.remove = func(id uint64) {
		cb.subMu.Lock()
		delete(cb.subscribers, id)
		cb.subMu.Unlock()
	}

	cb.subMu.Lock()
	cb.nextSubscriberID++
	sub.id = cb.nextSubscriberID
	if cb.subscribers == nil {
		cb.subscribers = make(map[uint64]*subscriber)
	}
	cb.subscribers[sub.id] = sub
	cb.subMu.Unlock()

	go sub.run()

	return sub
}

// Unsubscribe implements Subscription.
func (s *subscriber) Unsubscribe() {
	if s.stopped.CompareAndSwap(false, true) {
		s.remove(s.id)
		close(s.done)
	}
}

// enqueue offers the transition to the subscriber queue without ever
// blocking the caller.
func (s *subscriber) enqueue(t StateTransition) {
	select {
	case s.queue <- t:
		return
	default:
	}

	if s.policy == DropOldest {
		select {
		case <-s.queue:
		default:
		}
		select {
		case s.queue <- t:
		default:
		}
	}
	// DropNewest: the transition is discarded.
}

func (s *subscriber) run() {
	for {
		select {
		case <-s.done:
			return
		case t := <-s.queue:
			if s.stopped.Load() {
				return
			}
			s.deliver(t)
		}
	}
}

func (s *subscriber) deliver(t StateTransition) {
	defer func() {
		_ = recover()
	}()
	s.fn(t)
}
