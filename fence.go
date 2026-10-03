package workerpool

import (
	"context"
	"errors"
	"slices"
	"sync"
)

// ErrFenceDiscarded is returned by Fence.Wait when one or more tasks covered
// by the fence were discarded by Stop before they could run.
var ErrFenceDiscarded = errors.New("fence covers tasks discarded by worker pool stop")

// Fence is a barrier that covers the tasks accepted by a WorkerPool up to the
// moment the fence was taken. Tasks submitted after the fence do not affect
// it, so a fence can be taken repeatedly to wait for batches of work without
// stopping the pool. Multiple fences may be waited on concurrently.
type Fence struct {
	tracker *fenceTracker
	seq     uint64
}

// Fence returns a Fence covering all tasks accepted by the worker pool so
// far, including tasks that are queued or still running.
func (p *WorkerPool) Fence() *Fence {
	return p.tracker.fence()
}

// Wait blocks until every task covered by the fence has completed, the given
// Context is canceled, or a covered task is discarded by Stop. It returns nil
// when all covered tasks complete, the Context's error when canceled, or
// ErrFenceDiscarded when Stop discards a covered task. A nil Context waits
// without cancellation.
func (f *Fence) Wait(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	t := f.tracker
	t.mu.Lock()
	for {
		// The discarded sequence numbers are sorted, so the fence covers a
		// discarded task when the smallest one is within its range.
		if len(t.discarded) != 0 && t.discarded[0] <= f.seq {
			t.mu.Unlock()
			return ErrFenceDiscarded
		}
		if t.watermark >= f.seq {
			t.mu.Unlock()
			return nil
		}
		changed := t.changed
		t.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			return ctx.Err()
		}
		t.mu.Lock()
	}
}

// fenceTracker tracks task completion as a contiguous watermark plus the set
// of out-of-order completions that have not merged into the watermark yet.
// Its memory use is proportional to the number of unmerged holes, not to the
// total number of tasks ever submitted.
type fenceTracker struct {
	mu        sync.Mutex
	changed   chan struct{}
	next      uint64
	watermark uint64
	pending   map[uint64]struct{}
	discarded []uint64 // sorted
}

func newFenceTracker() *fenceTracker {
	return &fenceTracker{
		changed: make(chan struct{}),
		pending: make(map[uint64]struct{}),
	}
}

// submit assigns a sequence number to a task and returns the task wrapped to
// report its completion to the tracker.
func (t *fenceTracker) submit(task func()) (uint64, func()) {
	t.mu.Lock()
	t.next++
	seq := t.next
	t.mu.Unlock()
	return seq, func() {
		defer t.complete(seq)
		task()
	}
}

// fence returns a Fence covering every task submitted so far.
func (t *fenceTracker) fence() *Fence {
	t.mu.Lock()
	seq := t.next
	t.mu.Unlock()
	return &Fence{tracker: t, seq: seq}
}

// complete records that the task with the given sequence number has finished,
// merging contiguous completions into the watermark.
func (t *fenceTracker) complete(seq uint64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if seq <= t.watermark {
		return
	}
	if seq != t.watermark+1 {
		// Out-of-order completion; keep it as a hole until the missing
		// sequence numbers merge. Later completions must not advance the
		// watermark past unfinished earlier tasks.
		t.pending[seq] = struct{}{}
		return
	}
	t.watermark = seq
	for {
		if _, ok := t.pending[t.watermark+1]; !ok {
			break
		}
		delete(t.pending, t.watermark+1)
		t.watermark++
	}
	t.broadcastLocked()
}

// discardAll records tasks dropped by Stop so that fences covering them fail
// with ErrFenceDiscarded instead of waiting forever.
func (t *fenceTracker) discardAll(seqs []uint64) {
	if len(seqs) == 0 {
		return
	}
	slices.Sort(seqs)
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, seq := range seqs {
		if seq <= t.watermark {
			continue
		}
		delete(t.pending, seq)
		if n := len(t.discarded); n != 0 && t.discarded[n-1] >= seq {
			continue
		}
		t.discarded = append(t.discarded, seq)
	}
	t.broadcastLocked()
}

// broadcastLocked wakes all fence waiters so they can re-check the watermark.
// The caller must hold t.mu.
func (t *fenceTracker) broadcastLocked() {
	close(t.changed)
	t.changed = make(chan struct{})
}
