package workerpool

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gammazero/deque"
)

var (
	ErrStopped = errors.New("submitting work to stopped workerpool")

	// ErrInvalidSize is returned when attempting to set the worker pool size
	// to a value less than one.
	ErrInvalidSize = errors.New("workerpool size must be at least 1")
)

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
		taskQueue:    make(chan func()),
		workerQueue:  make(chan func()),
		resizeSignal: make(chan struct{}, 1),
		stopSignal:   make(chan struct{}),
		stoppedChan:  make(chan struct{}),
	}
	pool.maxWorkers.Store(int32(maxWorkers))

	// Start the task dispatcher.
	go pool.dispatch(cfg.idleTimeout)

	return pool
}

// WorkerPool is a collection of goroutines, where the number of concurrent
// goroutines processing requests does not exceed the specified maximum.
type WorkerPool struct {
	maxWorkers   atomic.Int32
	taskQueue    chan func()
	workerQueue  chan func()
	resizeSignal chan struct{}
	stoppedChan  chan struct{}
	stopSignal   chan struct{}
	waitingQueue deque.Deque[func()]
	stopLock     sync.Mutex
	stopOnce     sync.Once
	stopped      bool
	waiting      atomic.Int32
	wait         bool
}

// Size returns the maximum number of concurrent workers.
func (p *WorkerPool) Size() int {
	return int(p.maxWorkers.Load())
}

// SetSize changes the maximum number of workers that can execute tasks
// concurrently. The change takes effect without interrupting running tasks.
//
// Increasing the size immediately starts new workers to execute tasks that
// are already waiting in the queue. Decreasing the size retires workers at
// task boundaries, as they become ready for new work, so tasks that are
// already running always run to completion. If the size is changed again
// before a previous decrease has finished retiring workers, then the most
// recent size is the one that takes effect.
//
// SetSize returns ErrInvalidSize if size is less than one, or ErrStopped if
// the worker pool has been stopped.
func (p *WorkerPool) SetSize(size int) error {
	if size < 1 {
		return ErrInvalidSize
	}
	p.stopLock.Lock()
	defer p.stopLock.Unlock()
	if p.stopped {
		return ErrStopped
	}
	p.maxWorkers.Store(int32(size))
	// Notify the dispatcher of the size change. If a notification is already
	// pending, then the dispatcher reads the latest size when it wakes, so
	// there is no need to queue another one.
	select {
	case p.resizeSignal <- struct{}{}:
	default:
	}
	return nil
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
	size := int(p.maxWorkers.Load())
	ready.Add(size)
	for i := 0; i < size; i++ {
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

	// processWaitingQueue puts new tasks onto the waiting queue, and removes
	// tasks from the waiting queue as workers become available. Returns
	// false if worker pool is stopped.
	processWaitingQueue := func() bool {
		if workerCount > int(p.maxWorkers.Load()) {
			// The pool has shrunk, so retire an excess worker at a task
			// boundary instead of giving it another task. Busy workers are
			// never interrupted; they are retired as they become ready.
			select {
			case p.workerQueue <- nil:
				workerCount--
			case task, ok := <-p.taskQueue:
				if !ok {
					return false
				}
				p.waitingQueue.PushBack(task)
				p.waiting.Store(int32(p.waitingQueue.Len()))
			case <-p.resizeSignal:
				// Pool size changed; applied at the top of the loop.
			}
			return true
		}
		select {
		case task, ok := <-p.taskQueue:
			if !ok {
				return false
			}
			p.waitingQueue.PushBack(task)
		case p.workerQueue <- p.waitingQueue.Front():
			// A worker was ready, so gave task to worker.
			p.waitingQueue.PopFront()
		case <-p.resizeSignal:
			// Pool size changed; applied at the top of the loop.
		}
		p.waiting.Store(int32(p.waitingQueue.Len()))
		return true
	}

Loop:
	for {
		maxWorkers := int(p.maxWorkers.Load())

		// If the pool grew, start new workers to execute tasks that are
		// already waiting in the queue.
		for workerCount < maxWorkers && p.waitingQueue.Len() != 0 {
			wg.Add(1)
			go worker(p.waitingQueue.PopFront(), p.workerQueue, &wg)
			workerCount++
			p.waiting.Store(int32(p.waitingQueue.Len()))
		}

		// If the pool shrank, stop idle workers until the number of workers
		// is within the new limit. Busy workers are not interrupted; they
		// are retired at task boundaries as they become ready for work.
		for workerCount > maxWorkers && p.killIdleWorker() {
			workerCount--
		}

		// As long as tasks are in the waiting queue, incoming tasks are put
		// into the waiting queue and tasks to run are taken from the waiting
		// queue. Once the waiting queue is empty, then go back to submitting
		// incoming tasks directly to available workers.
		if p.waitingQueue.Len() != 0 {
			if !processWaitingQueue() {
				break Loop
			}
			continue
		}

		// When there are more workers than the current maximum, enable
		// retiring an excess worker as soon as one is ready for new work.
		var retireQueue chan func()
		if workerCount > maxWorkers {
			retireQueue = p.workerQueue
		}

		select {
		case task, ok := <-p.taskQueue:
			if !ok {
				break Loop
			}
			// Got a task to do.
			if workerCount > maxWorkers {
				// The pool has shrunk, so queue the task and let excess
				// workers retire instead of giving them more work.
				p.waitingQueue.PushBack(task)
				p.waiting.Store(int32(p.waitingQueue.Len()))
			} else {
				select {
				case p.workerQueue <- task:
				default:
					// Create a new worker, if not at max.
					if workerCount < maxWorkers {
						wg.Add(1)
						go worker(task, p.workerQueue, &wg)
						workerCount++
					} else {
						// Enqueue task to be executed by next available
						// worker.
						p.waitingQueue.PushBack(task)
						p.waiting.Store(int32(p.waitingQueue.Len()))
					}
				}
			}
			idle = false
		case retireQueue <- nil:
			// An excess worker is ready for work; retire it at this task
			// boundary instead of giving it another task.
			workerCount--
		case <-p.resizeSignal:
			// Pool size changed; applied at the top of the loop.
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
func worker(task func(), workerQueue chan func(), wg *sync.WaitGroup) {
	for task != nil {
		task()
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
	for p.waitingQueue.Len() != 0 {
		// A worker is ready, so give task to worker.
		p.workerQueue <- p.waitingQueue.PopFront()
		p.waiting.Store(int32(p.waitingQueue.Len()))
	}
}
