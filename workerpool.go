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

// CancelResult describes the outcome of a call to Task.Cancel.
type CancelResult int

const (
	// CancelCanceled indicates that the task had not started executing and
	// has been canceled. The task is removed from the waiting queue and
	// will never run.
	CancelCanceled CancelResult = iota
	// CancelRunning indicates that the task had already started executing,
	// or was committed to run next, and could not be canceled.
	CancelRunning
	// CancelDone indicates that the task had already finished executing.
	CancelDone
)

// String returns a string representation of the CancelResult.
func (r CancelResult) String() string {
	switch r {
	case CancelCanceled:
		return "canceled"
	case CancelRunning:
		return "running"
	case CancelDone:
		return "done"
	}
	return "unknown"
}

type taskState int

const (
	taskPending taskState = iota
	taskRunning
	taskDone
	taskCanceled
)

// Task is a handle to a task submitted to the worker pool with SubmitHandle.
// It allows a task that has not yet started executing to be canceled.
type Task struct {
	fn    func()
	pool  *WorkerPool
	state taskState
}

// Cancel cancels the task if it has not started executing, and reports the
// outcome:
//
//   - CancelCanceled: the task was still waiting and is now canceled. It is
//     removed from the waiting queue, its function reference is released, and
//     it will never run.
//   - CancelRunning: the task had already started executing, or the
//     dispatcher has committed it to run next.
//   - CancelDone: the task had already finished executing.
//
// Canceling a task that was already canceled reports CancelCanceled again.
// A task has exactly one terminal state: it either runs to completion or is
// canceled, even when Cancel races with Stop, StopWait, or Pause.
func (t *Task) Cancel() CancelResult {
	p := t.pool
	p.queueLock.Lock()
	defer p.queueLock.Unlock()
	switch t.state {
	case taskPending:
		// Remove the task from the waiting queue if it is there. If it is
		// not, then it is in flight between submission and the dispatcher,
		// and the dispatcher will drop it without running it.
		idx := p.waitingQueue.Index(func(queued *Task) bool { return queued == t })
		if idx >= 0 {
			p.waitingQueue.Remove(idx)
			p.waiting.Store(int32(p.waitingQueue.Len()))
		}
		t.state = taskCanceled
		t.fn = nil
		return CancelCanceled
	case taskRunning:
		return CancelRunning
	case taskDone:
		return CancelDone
	default:
		// Already canceled.
		return CancelCanceled
	}
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
		taskQueue:   make(chan *Task),
		workerQueue: make(chan *Task),
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
	taskQueue    chan *Task
	workerQueue  chan *Task
	stoppedChan  chan struct{}
	stopSignal   chan struct{}
	waitingQueue deque.Deque[*Task]
	queueLock    sync.Mutex
	stopLock     sync.Mutex
	stopOnce     sync.Once
	stopped      bool
	waiting      atomic.Int32
	wait         bool
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
	return p.do(&Task{fn: task, pool: p})
}

func (p *WorkerPool) do(task *Task) (err error) {
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

// SubmitHandle enqueues the given function without blocking and returns a
// handle to the submitted task. The handle's Cancel method can be used to
// cancel the task while it is still waiting to be executed. Panics if the
// worker pool is stopped. Returns nil if task is nil.
func (p *WorkerPool) SubmitHandle(task func()) *Task {
	if task == nil {
		return nil
	}
	t := &Task{fn: task, pool: p}
	if err := p.do(t); err != nil {
		panic(err)
	}
	return t
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
		p.taskQueue <- &Task{
			fn: func() {
				ready.Done()
				select {
				case <-ctx.Done():
				case <-p.stopSignal:
				}
			},
			pool: p,
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
		if p.waiting.Load() != 0 {
			if !p.processWaitingQueue() {
				break Loop
			}
			continue
		}

		select {
		case task, ok := <-p.taskQueue:
			if !ok {
				break Loop
			}
			// Got a task to do.
			p.queueLock.Lock()
			if task.state == taskCanceled {
				// Task was canceled while in flight; drop it.
				p.queueLock.Unlock()
				idle = false
				continue
			}
			select {
			case p.workerQueue <- task:
				task.state = taskRunning
			default:
				// Create a new worker, if not at max.
				if workerCount < p.maxWorkers {
					task.state = taskRunning
					wg.Add(1)
					go worker(task, p.workerQueue, &wg, p)
					workerCount++
				} else {
					// Enqueue task to be executed by next available worker.
					p.waitingQueue.PushBack(task)
					p.waiting.Store(int32(p.waitingQueue.Len()))
				}
			}
			p.queueLock.Unlock()
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
		p.workerQueue <- nil
		workerCount--
	}
	wg.Wait()
}

// worker executes tasks and stops when it receives a nil task.
func worker(task *Task, workerQueue chan *Task, wg *sync.WaitGroup, p *WorkerPool) {
	for task != nil {
		task.fn()
		p.queueLock.Lock()
		task.state = taskDone
		task.fn = nil
		p.queueLock.Unlock()
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
	p.queueLock.Lock()
	if p.waitingQueue.Len() == 0 {
		// Canceled tasks may have emptied the waiting queue since it was
		// last checked.
		p.queueLock.Unlock()
		return true
	}
	// Reserve the front task; it is committed to run as soon as a worker is
	// ready, so it can no longer be canceled. It stays on the waiting queue
	// until it is handed to a worker.
	task := p.waitingQueue.Front()
	task.state = taskRunning
	p.queueLock.Unlock()

	select {
	case newTask, ok := <-p.taskQueue:
		if !ok {
			return false
		}
		p.queueLock.Lock()
		if newTask.state == taskPending {
			p.waitingQueue.PushBack(newTask)
			p.waiting.Store(int32(p.waitingQueue.Len()))
		}
		// Else, the task was canceled while in flight; drop it.
		p.queueLock.Unlock()
	case p.workerQueue <- task:
		// A worker was ready, so gave task to worker.
		p.queueLock.Lock()
		p.waitingQueue.PopFront()
		p.waiting.Store(int32(p.waitingQueue.Len()))
		p.queueLock.Unlock()
	}
	return true
}

func (p *WorkerPool) killIdleWorker() bool {
	select {
	case p.workerQueue <- nil:
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
	for {
		p.queueLock.Lock()
		if p.waitingQueue.Len() == 0 {
			p.queueLock.Unlock()
			return
		}
		task := p.waitingQueue.PopFront()
		task.state = taskRunning
		p.waiting.Store(int32(p.waitingQueue.Len()))
		p.queueLock.Unlock()
		// A worker is ready, so give task to worker.
		p.workerQueue <- task
	}
}
