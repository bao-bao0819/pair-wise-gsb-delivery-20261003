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

var (
	// ErrTaskCanceled is returned by Cancel if the task was already canceled.
	ErrTaskCanceled = errors.New("task already canceled")
	// ErrTaskRunning is returned by Cancel if the task has already been
	// claimed by a worker. A running task runs to completion; Cancel does not
	// interrupt it.
	ErrTaskRunning = errors.New("task already started")
	// ErrTaskDone is returned by Cancel if the task has already completed.
	ErrTaskDone = errors.New("task already finished")
)

// Task lifecycle state stored in taskEntry.state.
const (
	taskWaiting  int32 = iota // received, still on the waiting queue
	taskRunning               // claimed by a worker, executing
	taskDone                  // finished executing
	taskCanceled              // removed from the waiting queue before starting
)

// TaskHandle refers to a task submitted with [WorkerPool.SubmitHandle]. It is
// used to cancel the task while it is still waiting to be picked up by a
// worker.
type TaskHandle struct {
	entry *taskEntry
	pool  *WorkerPool
}

// taskEntry is the mutable, uniquely-identified record of a cancelable task.
// The dispatcher stores *taskEntry values (wrapped in queuedTask) on the
// waiting queue, so a task can be located and physically removed by identity.
type taskEntry struct {
	fn    func()
	state atomic.Int32
}

// cancel transitions the entry to taskCanceled if it is still waiting.
func (e *taskEntry) cancel() bool {
	return e.state.CompareAndSwap(taskWaiting, taskCanceled)
}

// run executes the task on behalf of a worker and records the terminal state.
func (e *taskEntry) run() {
	e.state.Store(taskRunning)
	e.fn()
	e.fn = nil
	// State becomes done only after the task function has returned, so Cancel
	// can distinguish a task that is still executing from one that finished.
	e.state.Store(taskDone)
}

// queuedTask is the unit of work received by the dispatcher and stored on the
// waiting queue. Plain tasks (submitted via Do or Submit) leave entry nil and
// incur no bookkeeping. For cancelable tasks, fn is the entry's run method,
// which performs the task's lifecycle bookkeeping.
type queuedTask struct {
	fn    func()
	entry *taskEntry
}

// claim marks a delivered cancelable task as running. It is called by the
// dispatcher only once delivery to a worker has succeeded, so the state never
// shows "running" while the task is still on the waiting queue.
func (t *queuedTask) claim() {
	if t.entry != nil {
		t.entry.state.Store(taskRunning)
	}
}

// cancelRequest asks the dispatcher to cancel a waiting task.
type cancelRequest struct {
	entry *taskEntry
	reply chan error
}

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
		taskQueue:   make(chan *queuedTask),
		cancelQueue: make(chan cancelRequest),
		workerReady: make(chan chan func(), maxWorkers),
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
	taskQueue    chan *queuedTask
	cancelQueue  chan cancelRequest
	workerReady  chan chan func()
	stopLock     sync.Mutex
	stopOnce     sync.Once
	stopped      bool
	waiting      atomic.Int32
	wait         bool
	stoppedChan  chan struct{}
	stopSignal   chan struct{}
	waitingQueue deque.Deque[*queuedTask]
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
	p.taskQueue <- &queuedTask{fn: task}
	return
}

// SubmitHandle enqueues a function for a worker to execute and returns a
// TaskHandle that can be used to cancel the task before a worker starts it.
// Returns ErrStopped if the worker pool is stopped.
//
// Submission never blocks, just like Do: the queue remains unbounded. The
// returned handle is only useful while the task is waiting; after a worker
// claims the task it runs to completion and cannot be interrupted.
func (p *WorkerPool) SubmitHandle(task func()) (*TaskHandle, error) {
	if task == nil {
		return nil, nil
	}
	// Fast, race-free rejection once stop has begun. This does not change the
	// contract: a submit concurrent with stop may still be accepted if it
	// reaches the dispatcher first, exactly as Do/Submit allow.
	p.stopLock.Lock()
	stopped := p.stopped
	p.stopLock.Unlock()
	if stopped {
		return nil, ErrStopped
	}
	entry := &taskEntry{fn: task}
	qt := &queuedTask{fn: entry.run, entry: entry}
	defer func() {
		if recover() != nil {
			entry = nil
		}
	}()
	p.taskQueue <- qt
	if entry == nil {
		return nil, ErrStopped
	}
	return &TaskHandle{entry: entry, pool: p}, nil
}

// Submit enqueues the given function without blocking. Panics if the worker
// pool is stopped.
func (p *WorkerPool) Submit(task func()) {
	if err := p.Do(task); err != nil {
		panic(err)
	}
}

// Cancel removes the task from the waiting queue if a worker has not yet
// started it. If the task was still waiting, it is physically dequeued, its
// captured closure is released, WaitingQueueSize is decremented, and Cancel
// returns nil. Every accepted task has a single terminal state, so:
//
//   - nil means this call canceled the waiting task.
//   - ErrTaskCanceled means the task was already canceled (an earlier Cancel
//     succeeded); canceling twice is harmless.
//   - ErrTaskRunning means a worker has already claimed and started the task;
//     the task is not interrupted and runs to completion.
//   - ErrTaskDone means the task already finished executing.
//
// Canceling a task never affects the order of any other task; tasks ahead of
// and behind a canceled task are still delivered in FIFO order.
func (h *TaskHandle) Cancel() error {
	if h == nil {
		return ErrTaskDone
	}
	// Only terminal states can be decided without the dispatcher. Whether a
	// task that is still waiting can be removed is decided by the dispatcher,
	// which is the sole owner of waiting-queue membership.
	switch h.entry.state.Load() {
	case taskDone:
		return ErrTaskDone
	case taskCanceled:
		return ErrTaskCanceled
	}

	req := cancelRequest{entry: h.entry, reply: make(chan error, 1)}

	// Dispatch a cancel request without blocking if the pool has stopped.
	select {
	case h.pool.cancelQueue <- req:
	case <-h.pool.stoppedChan:
		return taskStateError(h.entry.state.Load())
	}

	select {
	case err := <-req.reply:
		return err
	case <-h.pool.stoppedChan:
		return taskStateError(h.entry.state.Load())
	}
}

// taskStateError maps a terminal (or claimed) task state to the Cancel result.
func taskStateError(state int32) error {
	switch state {
	case taskCanceled:
		return ErrTaskCanceled
	case taskRunning:
		return ErrTaskRunning
	default:
		return ErrTaskDone
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
		pauseTask := func() {
			ready.Done()
			select {
			case <-ctx.Done():
			case <-p.stopSignal:
			}
		}
		p.taskQueue <- &queuedTask{fn: pauseTask}
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
		// A ready worker is consumed only through this case, so every worker
		// token is freshly obtained on the iteration that uses it; no send is
		// ever parked inside a select expression.
		var readyWorker chan chan func()
		if p.waitingQueue.Len() != 0 {
			readyWorker = p.workerReady
		}
		select {
		case task, ok := <-p.taskQueue:
			if !ok {
				break Loop
			}
			if p.waitingQueue.Len() != 0 {
				p.waitingQueue.PushBack(task)
				p.updateWaiting()
				idle = false
				continue
			}
			// Hand directly to a ready worker, or create one up to the
			// limit, or enqueue.
			task.claim()
			select {
			case w := <-p.workerReady:
				w <- task.fn
			default:
				if workerCount < p.maxWorkers {
					wg.Add(1)
					go newWorker(task.fn, p.workerReady, &wg)
					workerCount++
				} else {
					// No worker was available: revert to waiting so the task
					// remains cancelable while in the queue.
					if task.entry != nil {
						task.entry.state.Store(taskWaiting)
					}
					p.waitingQueue.PushBack(task)
					p.updateWaiting()
				}
			}
			idle = false
		case req := <-p.cancelQueue:
			req.reply <- p.handleCancel(req)
		case w := <-readyWorker:
			// A worker became ready and the waiting queue is non-empty: hand
			// it the front task. Claim and remove the entry before the
			// blocking send; after the send the task may already be done.
			task := p.waitingQueue.Front()
			p.waitingQueue.PopFront()
			task.claim()
			w <- task.fn
			p.updateWaiting()
			idle = false
		case <-idleTO:
			// Timed out waiting for work to arrive. Kill a ready worker if
			// pool has been idle for a whole timeout.
			if idle && workerCount > 0 {
				if p.reapIdleWorker() {
					workerCount--
				}
			}
			idle = true
			timeout.Reset(idleTimeout)
		}
	}

	// If instructed to wait, then run tasks that are already queued.
	if p.wait {
		p.drainQueuedTasks(&workerCount, &wg)
	} else {
		// Stop abandons queued tasks, but each received task still needs a
		// single settled state so concurrent Cancel callers get a definitive
		// answer instead of observing a task stuck in the waiting state.
		for i := 0; i < p.waitingQueue.Len(); i++ {
			if t := p.waitingQueue.At(i); t.entry != nil {
				t.entry.cancel()
			}
		}
	}

	// Stop all remaining workers as they become ready.
	for range workerCount {
		w := <-p.workerReady
		w <- nil
	}
	wg.Wait()
}

// newWorker runs the initial task, then keeps receiving tasks via the
// dispatcher. It registers itself as ready by handing its per-worker task
// channel to the dispatcher on workerReady. Receiving nil stops the worker.
func newWorker(task func(), workerReady chan chan func(), wg *sync.WaitGroup) {
	// Run the initial task, then rendezvous with the dispatcher: offer the
	// per-worker channel (registered only while blocked on the receive) and
	// execute whatever the dispatcher sends next; nil stops the worker.
	wq := make(chan func())
	if task != nil {
		task()
	}
	for {
		workerReady <- wq
		next := <-wq
		if next == nil {
			break
		}
		next()
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

// updateWaiting mirrors the waiting-queue length into the atomic counter.
func (p *WorkerPool) updateWaiting() {
	p.waiting.Store(int32(p.waitingQueue.Len()))
}

// handleCancel physically removes a still-waiting cancelable task from the
// waiting queue. It runs on the dispatcher goroutine, which is the sole
// owner of the waiting queue.
func (p *WorkerPool) handleCancel(req cancelRequest) error {
	state := req.entry.state.Load()
	if state != taskWaiting {
		return taskStateError(state)
	}

	idx := p.waitingQueue.Index(func(t *queuedTask) bool {
		return t.entry == req.entry
	})
	if idx < 0 {
		// The entry was waiting but is no longer in the queue: it was popped
		// and claimed during a handoff to a worker.
		return ErrTaskRunning
	}

	if !req.entry.cancel() {
		// Lost the race against dispatch; re-read the settled state.
		return taskStateError(req.entry.state.Load())
	}

	removed := p.waitingQueue.Remove(idx)
	// Release the caller's closure promptly so its captured values can be
	// collected even though the entry record outlives the dequeue.
	removed.entry.fn = nil
	p.waiting.Store(int32(p.waitingQueue.Len()))
	return nil
}

// reapIdleWorker tells one currently idle (registered) worker to exit.
func (p *WorkerPool) reapIdleWorker() bool {
	select {
	case w := <-p.workerReady:
		w <- nil
		return true
	default:
		return false
	}
}

// drainQueuedTasks delivers every task left on the waiting queue to workers,
// creating additional workers up to the pool limit as needed. Used by
// StopWait: paused workers have already been unpaused by the stop signal.
func (p *WorkerPool) drainQueuedTasks(workerCount *int, wg *sync.WaitGroup) {
	for p.waitingQueue.Len() != 0 {
		task := p.waitingQueue.Front()
		var w chan func()
		select {
		case w = <-p.workerReady:
		default:
			if *workerCount < p.maxWorkers {
				wg.Add(1)
				*workerCount++
				go newWorker(nil, p.workerReady, wg)
				w = <-p.workerReady
			} else {
				w = <-p.workerReady
			}
		}
		task.claim()
		w <- task.fn
		p.waitingQueue.PopFront()
		p.updateWaiting()
	}
}
