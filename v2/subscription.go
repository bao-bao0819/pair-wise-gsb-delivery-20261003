package gobreaker

import (
	"sync"
	"sync/atomic"
	"time"
)

// defaultSubscriptionBuffer is the event buffer size used when
// Subscribe is called with a non-positive bufferSize.
const defaultSubscriptionBuffer = 16

// StateChangeEvent is an immutable snapshot of a single state transition.
//
// Seq is a monotonically increasing sequence number assigned to every actual
// state transition of the CircuitBreaker, starting at 1. A subscriber always
// observes events in increasing Seq order (events dropped due to overflow
// simply leave gaps in the sequence).
//
// Counts is a snapshot of the internal counters taken at the moment of the
// transition, just before they are reset for the new generation.
type StateChangeEvent struct {
	Seq       uint64
	Name      string
	From      State
	To        State
	Counts    Counts
	Timestamp time.Time
}

// Subscription represents an active state-change subscription.
type Subscription interface {
	// Unsubscribe removes the subscription. It is idempotent and safe to
	// call from any goroutine, including from within the subscriber's own
	// callback. After Unsubscribe returns, no new callback invocation is
	// started for this subscription; a callback that is already running is
	// allowed to finish.
	Unsubscribe()

	// Dropped returns the number of events that were dropped for this
	// subscription because the callback did not keep up (see Subscribe).
	Dropped() uint64
}

type subscription struct {
	events      chan StateChangeEvent
	callback    func(StateChangeEvent)
	unsubscribe func()
	once        sync.Once
	closed      atomic.Bool
	dropped     atomic.Uint64
}

// Subscribe registers callback to be notified of every state transition of
// the CircuitBreaker and returns a Subscription that can be used to cancel
// the registration.
//
// Each subscription has its own dedicated goroutine and a buffered queue of
// bufferSize events (defaultSubscriptionBuffer if bufferSize <= 0), so the
// callback is never invoked while the CircuitBreaker holds its internal
// state lock. It is therefore safe for the callback to call State, Counts or
// Unsubscribe without risking a deadlock.
//
// Backpressure policy: events are enqueued without blocking the breaker. If
// the queue is full because the callback is too slow, the oldest undelivered
// event is dropped (drop-oldest) so that the newest transition is always
// delivered, and the subscription's Dropped counter is incremented.
//
// A panic in the callback is recovered by the subscription goroutine and
// never affects the CircuitBreaker: the panic is isolated from the state
// machine, the permit accounting and the other subscribers, and the
// subscription keeps delivering subsequent events.
func (cb *CircuitBreaker[T]) Subscribe(callback func(StateChangeEvent), bufferSize int) Subscription {
	if callback == nil {
		panic("gobreaker: Subscribe called with nil callback")
	}
	if bufferSize <= 0 {
		bufferSize = defaultSubscriptionBuffer
	}

	sub := &subscription{
		events:   make(chan StateChangeEvent, bufferSize),
		callback: callback,
	}

	cb.mutex.Lock()
	cb.nextSubscriberID++
	id := cb.nextSubscriberID
	cb.subscribers[id] = sub
	cb.mutex.Unlock()

	sub.unsubscribe = func() {
		cb.mutex.Lock()
		defer cb.mutex.Unlock()
		if _, ok := cb.subscribers[id]; ok {
			delete(cb.subscribers, id)
			close(sub.events)
		}
	}

	go sub.run()
	return sub
}

// publish enqueues the event for every subscriber. It is called with
// cb.mutex held, so events are enqueued in transition order and never race
// with the channel close performed by Unsubscribe.
func (cb *CircuitBreaker[T]) publish(event StateChangeEvent) {
	for _, sub := range cb.subscribers {
		select {
		case sub.events <- event:
		default:
			// The subscriber is too slow: drop the oldest undelivered
			// event to make room for the newest one.
			select {
			case <-sub.events:
				sub.dropped.Add(1)
			default:
			}
			select {
			case sub.events <- event:
			default:
			}
		}
	}
}

func (sub *subscription) run() {
	for event := range sub.events {
		if sub.closed.Load() {
			// Unsubscribed: drain remaining events without
			// starting new callback invocations.
			continue
		}
		sub.invoke(event)
	}
}

func (sub *subscription) invoke(event StateChangeEvent) {
	defer func() {
		// A panicking callback must not kill the subscription
		// goroutine nor propagate into the CircuitBreaker.
		_ = recover()
	}()
	sub.callback(event)
}

func (sub *subscription) Unsubscribe() {
	sub.once.Do(func() {
		sub.closed.Store(true)
		sub.unsubscribe()
	})
}

func (sub *subscription) Dropped() uint64 {
	return sub.dropped.Load()
}
