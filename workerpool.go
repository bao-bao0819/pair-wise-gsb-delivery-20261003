package workerpool

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gammazero/deque"
)

var ErrStopped = errors.New("submitting work to stopped workerpool")

// ErrFenceDropped is returned by Fence.Wait when one or more of the tasks
// covered by the fence were discarded because the worker pool was stopped with
// Stop. A fence created after the pool is stopped also reports this error.
var ErrFenceDropped = errors.New("fence reached discarded tasks")

// New creates and starts a pool of worker goroutines.
//
// The maxWorkers parameter specifies the maximum number of workers that can
// execute tasks concurrently. When there are no incoming tasks, workers are
// gradually stopped until there are no remaining workers.
func New(maxWorkers int, options ...Option) *WorkerPool {
	// There must be at least one worker.
	if maxWorkers < 1 {
		maxWorkers = 1
	}

	cfg := config{
		idleTimeout: DefaultIdleTimeout,
	}
	for _, opt := range options {
		opt(&cfg)
	}

	pool := &WorkerPool{
		maxWorkers:  maxWorkers,
		taskQueue:   make(chan any),
		workerQueue: make(chan poolTask),
		stopSignal:  make(chan struct{}),
		stoppedChan: make(chan struct{}),
	}

	// Start the task dispatcher.
	go pool.dispatch(cfg.idleTimeout)

	return pool
}

// WorkerPool is a collection of goroutines, where the number of concurrent
// goroutines processing requests does not exceed the specified maximum.
type WorkerPool struct {
	maxWorkers   int
	taskQueue    chan any
	workerQueue  chan poolTask
	stoppedChan  chan struct{}
	stopSignal   chan struct{}
	waitingQueue deque.Deque[poolTask]
	stopLock     sync.Mutex
	stopOnce     sync.Once
	stopped      bool
	waiting      atomic.Int32
	wait         bool
	tracker      taskTracker
}

// poolTask pairs a queued task with its assignment sequence number. Sequence
// numbers are assigned by the dispatcher in the order it receives tasks. A
// zero sequence marks an untracked task (worker kill signal or probes).
type poolTask struct {
	seq  uint64
	task func()
}

// fenceMarker is intercepted by the dispatcher instead of being executed. It
// registers a fence targeting the sequence watermark at the marker's position
// in the task stream.
type fenceMarker struct {
	fence *Fence
}

// Size returns the maximum number of concurrent workers.
func (p *WorkerPool) Size() int {
	return p.maxWorkers
}

// Stop stops the worker pool and waits for only currently running tasks to
// complete. Pending tasks that are not currently running are abandoned. Tasks
// must not be submitted to the worker pool after calling stop.
//
// Since creating the worker pool starts at least one goroutine, for the
// dispatcher, Stop() or StopWait() should be called when the worker pool is no
// longer needed.
func (p *WorkerPool) Stop() {
	p.stop(false)
}

// StopWait stops the worker pool and waits for all queued tasks tasks to
// complete. No additional tasks may be submitted, but all pending tasks are
// executed by workers before this function returns.
func (p *WorkerPool) StopWait() {
	p.stop(true)
}

// Stopped returns true if this worker pool has been stopped.
func (p *WorkerPool) Stopped() bool {
	p.stopLock.Lock()
	defer p.stopLock.Unlock()
	return p.stopped
}

// Fence returns a Fence that is released once all tasks received by the
// worker pool before the call to Fence have completed. Tasks submitted after
// the call do not delay the fence.
//
// Fences may be created repeatedly while the pool is running, and multiple
// fences may be waited on concurrently. Each fence only observes the state of
// the task stream at its own sampling point, so a later task finishing first
// never releases an older fence while an earlier task is still outstanding.
//
// If the pool is stopped with Stop, tasks still queued at that point are
// discarded and any fence covering them resolves with ErrFenceDropped instead
// of remaining blocked. Stopping with StopWait runs all queued tasks, so
// fences then resolve with nil. Fence returns an already-dropped fence if the
// pool is already stopped.
func (p *WorkerPool) Fence() (fence *Fence) {
	fence = &Fence{done: make(chan struct{})}
	defer func() {
		if recover() != nil {
			fence.resolve(ErrFenceDropped)
		}
	}()
	p.taskQueue <- fenceMarker{fence}
	return fence
}

// Do enqueues a function for a worker to execute. Returns ErrStopped if the
// worker pool is stopped.
//
// Any external values needed by the task function must be captured in a
// closure. Any return values should be returned over a channel that is
// captured in the task function closure.
//
// Do will not block regardless of the number of tasks submitted. Each task
// is immediately given to an available worker or to a newly started worker. If
// there are no available workers, and the maximum number of workers are
// already created, then the task is put onto a waiting queue.
//
// When there are tasks on the waiting queue, any additional new tasks are put
// on the waiting queue. Tasks are removed from the waiting queue as workers
// become available.
//
// As long as no new tasks arrive, one available worker is shutdown each time
// period until there are no more idle workers. Since the time to start new
// goroutines is not significant, there is no need to retain idle workers
// indefinitely.
func (p *WorkerPool) Do(task func()) (err error) {
	if task == nil {
		return
	}
	defer func() {
		if recover() != nil {
			err = ErrStopped
		}
	}()
	p.taskQueue <- task
	return
}

// Submit enqueues the given function without blocking. Panics if the worker
// pool is stopped.
func (p *WorkerPool) Submit(task func()) {
	if err := p.Do(task); err != nil {
		panic(err)
	}
}

// SubmitWait enqueues the given function, without blocking, and waits for it
// to be executed. Panics if the worker pool is stopped.
func (p *WorkerPool) SubmitWait(task func()) {
	if task == nil {
		return
	}
	doneChan := make(chan struct{})
	p.Submit(func() {
		task()
		close(doneChan)
	})
	<-doneChan
}

// WaitingQueueSize returns the count of tasks in the waiting queue.
func (p *WorkerPool) WaitingQueueSize() int {
	return int(p.waiting.Load())
}

// Fence tracks completion of the set of tasks received by a worker pool up to
// a sampling point. A Fence is created with WorkerPool.Fence and is safe for
// concurrent Waits; all waits return the same result.
type Fence struct {
	done   chan struct{}
	err    error
	target uint64
}

// Wait blocks until every task covered by the fence has completed, or until
// ctx is canceled. It returns nil when the covered tasks all completed. It
// returns ErrFenceDropped when the pool was stopped with Stop and a covered
// task was discarded without running. It returns ctx.Err() if ctx is
// canceled first; canceling ctx does not affect other waiters or the fence
// itself.
func (f *Fence) Wait(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-f.done:
		return f.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Pause causes all workers to wait on the given Context, thereby making them
// unavailable to run tasks. Pause returns when all workers are waiting. Tasks
// can continue to be queued to the workerpool, but are not executed until the
// Context is canceled or times out.
//
// Calling Pause when the worker pool is already paused causes Pause to wait
// until all previous pauses are canceled. This allows a goroutine to take
// control of pausing and unpausing the pool as soon as other goroutines have
// unpaused it.
//
// When the workerpool is stopped, workers are unpaused and queued tasks are
// executed during StopWait.
func (p *WorkerPool) Pause(ctx context.Context) {
	p.stopLock.Lock()
	defer p.stopLock.Unlock()
	if p.stopped {
		return
	}
	ready := new(sync.WaitGroup)
	ready.Add(p.maxWorkers)
	for i := 0; i < p.maxWorkers; i++ {
		p.taskQueue <- func() {
			ready.Done()
			select {
			case <-ctx.Done():
			case <-p.stopSignal:
			}
		}
	}
	// Wait for workers to all be paused
	ready.Wait()
}

// dispatch sends the next queued task to an available worker.
func (p *WorkerPool) dispatch(idleTimeout time.Duration) {
	defer close(p.stoppedChan)
	var idle bool
	var idleTO <-chan time.Time
	var workerCount int
	var wg sync.WaitGroup

	var timeout *time.Timer
	if idleTimeout > 0 {
		timeout = time.NewTimer(idleTimeout)
		defer timeout.Stop()
		idleTO = timeout.C
	}

Loop:
	for {
		// As long as tasks are in the waiting queue, incoming tasks are put
		// into the waiting queue and tasks to run are taken from the waiting
		// queue. Once the waiting queue is empty, then go back to submitting
		// incoming tasks directly to available workers.
		if p.waitingQueue.Len() != 0 {
			if !p.processWaitingQueue() {
				break Loop
			}
			continue
		}

		select {
		case item, ok := <-p.taskQueue:
			if !ok {
				break Loop
			}
			if marker, ok := item.(fenceMarker); ok {
				p.tracker.register(marker.fence)
				continue
			}
			// Got a task to do.
			queued := poolTask{seq: p.tracker.accept(), task: item.(func())}
			select {
			case p.workerQueue <- queued:
			default:
				// Create a new worker, if not at max.
				if workerCount < p.maxWorkers {
					wg.Add(1)
					go worker(queued, p.workerQueue, &p.tracker, &wg)
					workerCount++
				} else {
					// Enqueue task to be executed by next available worker.
					p.waitingQueue.PushBack(queued)
					p.waiting.Store(int32(p.waitingQueue.Len()))
				}
			}
			idle = false
		case <-idleTO:
			// Timed out waiting for work to arrive. Kill a ready worker if
			// pool has been idle for a whole timeout.
			if idle && workerCount > 0 {
				if p.killIdleWorker() {
					workerCount--
				}
			}
			idle = true
			timeout.Reset(idleTimeout)
		}
	}

	// If instructed to wait, then run tasks that are already queued.
	if p.wait {
		p.runQueuedTasks()
	}

	// Stop all remaining workers as they become ready.
	for workerCount > 0 {
		p.workerQueue <- poolTask{}
		workerCount--
	}
	wg.Wait()
	p.tracker.finish(p.wait)
}

// worker executes tasks and stops when it receives a zero-value poolTask.
func worker(task poolTask, workerQueue chan poolTask, tracker *taskTracker, wg *sync.WaitGroup) {
	for task.task != nil {
		task.task()
		if task.seq != 0 {
			tracker.completed(task.seq)
		}
		task = <-workerQueue
	}
	wg.Done()
}

// stop tells the dispatcher to exit, and whether or not to complete queued
// tasks.
func (p *WorkerPool) stop(wait bool) {
	p.stopOnce.Do(func() {
		// Signal that workerpool is stopping, to unpause any paused workers.
		close(p.stopSignal)
		// Acquire stopLock to wait for any pause in progress to complete. All
		// in-progress pauses will complete because the stopSignal unpauses the
		// workers.
		p.stopLock.Lock()
		// The stopped flag prevents any additional paused workers. This makes
		// it safe to close the taskQueue.
		p.stopped = true
		p.stopLock.Unlock()
		p.wait = wait
		// Close task queue and wait for currently running tasks to finish.
		close(p.taskQueue)
	})
	<-p.stoppedChan
}

// processWaitingQueue puts new tasks onto the waiting queue, and removes
// tasks from the waiting queue as workers become available. Returns false if
// worker pool is stopped.
func (p *WorkerPool) processWaitingQueue() bool {
	select {
	case item, ok := <-p.taskQueue:
		if !ok {
			return false
		}
		if marker, ok := item.(fenceMarker); ok {
			p.tracker.register(marker.fence)
		} else {
			p.waitingQueue.PushBack(poolTask{
				seq:  p.tracker.accept(),
				task: item.(func()),
			})
		}
	case p.workerQueue <- p.waitingQueue.Front():
		// A worker was ready, so gave task to worker.
		p.waitingQueue.PopFront()
	}
	p.waiting.Store(int32(p.waitingQueue.Len()))
	return true
}

func (p *WorkerPool) killIdleWorker() bool {
	select {
	case p.workerQueue <- poolTask{}:
		// Sent kill signal to worker.
		return true
	default:
		// No ready workers. All, if any, workers are busy.
		return false
	}
}

// runQueuedTasks removes each task from the waiting queue and gives it to
// workers until queue is empty.
func (p *WorkerPool) runQueuedTasks() {
	for p.waitingQueue.Len() != 0 {
		// A worker is ready, so give task to worker.
		p.workerQueue <- p.waitingQueue.PopFront()
		p.waiting.Store(int32(p.waitingQueue.Len()))
	}
}

// taskTracker maintains the ordered completion watermark for dispatched
// tasks. The dispatcher assigns sequence numbers and registers fences; worker
// goroutines report completions concurrently.
//
// received is one past the sequence number of the last received task. done is
// the prefix of tasks known to have completed. completedSeq holds only tasks
// that finished out of order, i.e. sequences greater than done; it shrinks to
// empty whenever the prefix is merged. Pending fences are kept in target order
// until released, so neither structure retains the full task history.
type taskTracker struct {
	mu           sync.Mutex
	received     uint64
	done         uint64
	completedSeq map[uint64]struct{}
	fences       deque.Deque[*Fence]
}

// accept assigns the next sequence number to a task received by the
// dispatcher.
func (t *taskTracker) accept() uint64 {
	t.received++
	return t.received
}

// completed reports that the task with the given sequence number finished. It
// merges the completed prefix and releases any fences whose targets are now
// covered.
func (t *taskTracker) completed(seq uint64) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if seq <= t.done {
		return
	}
	if t.completedSeq == nil {
		t.completedSeq = make(map[uint64]struct{})
	}
	t.completedSeq[seq] = struct{}{}
	for {
		if _, ok := t.completedSeq[t.done+1]; !ok {
			break
		}
		delete(t.completedSeq, t.done+1)
		t.done++
	}
	t.resolveReady()
}

// register attaches a fence to the watermark at the current sampling point:
// the fence covers exactly the tasks received so far. It is released at once
// when all of those tasks have already completed.
func (t *taskTracker) register(fence *Fence) {
	t.mu.Lock()
	defer t.mu.Unlock()

	fence.target = t.received
	t.fences.PushBack(fence)
	t.resolveReady()
}

// resolveReady releases, in target order, every pending fence whose covered
// tasks have all completed. Caller must hold t.mu.
func (t *taskTracker) resolveReady() {
	for t.fences.Len() != 0 {
		fence := t.fences.Front()
		if fence.target > t.done {
			break
		}
		t.fences.PopFront()
		fence.resolve(nil)
	}
}

// finish resolves all fences still pending when the dispatcher exits. When
// drain is true (StopWait) every queued task has been executed, so all fences
// complete successfully. When drain is false (Stop) queued tasks were
// abandoned: fences whose target is within the completed prefix succeed, and
// the first fence reaching a discarded task -- and every fence after it --
// gets ErrFenceDropped.
func (t *taskTracker) finish(drain bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if drain {
		t.done = t.received
		t.completedSeq = nil
	}
	dropped := false
	for t.fences.Len() != 0 {
		fence := t.fences.PopFront()
		if !dropped && fence.target <= t.done {
			fence.resolve(nil)
		} else {
			dropped = true
			fence.resolve(ErrFenceDropped)
		}
	}
	t.completedSeq = nil
}

// resolve marks the fence complete with the given result. Exactly one call to
// resolve ever happens per fence.
func (f *Fence) resolve(err error) {
	f.err = err
	close(f.done)
}
