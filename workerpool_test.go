package workerpool

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

const max = 20

func TestExample(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		wp := New(2)
		requests := []string{"alpha", "beta", "gamma", "delta", "epsilon"}

		rspChan := make(chan string, len(requests))
		for _, r := range requests {
			wp.Submit(func() {
				rspChan <- r
			})
		}

		wp.StopWait()

		close(rspChan)
		rspSet := map[string]struct{}{}
		for rsp := range rspChan {
			rspSet[rsp] = struct{}{}
		}
		if len(rspSet) < len(requests) {
			t.Fatal("Did not handle all requests")
		}
		for _, req := range requests {
			if _, ok := rspSet[req]; !ok {
				t.Fatal("Missing expected values:", req)
			}
		}
	})
}

func TestMaxWorkers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		wp := New(0)
		wp.Stop()
		if wp.Size() != 1 {
			t.Fatal("should have created one worker")
		}

		wp = New(max)
		defer wp.Stop()

		if wp.Size() != max {
			t.Fatal("wrong size returned")
		}

		started := make(chan struct{}, max)
		release := make(chan struct{})

		// Start workers, and have them all wait on a channel before completing.
		for range max {
			wp.Submit(func() {
				started <- struct{}{}
				<-release
			})
		}

		// Wait for all queued tasks to be dispatched to workers.
		if wp.waitingQueue.Len() != wp.WaitingQueueSize() {
			t.Fatal("Working Queue size returned should not be 0")
		}
		timeout := time.After(5 * time.Second)
		for startCount := 0; startCount < max; {
			select {
			case <-started:
				startCount++
			case <-timeout:
				t.Fatal("timed out waiting for workers to start")
			}
		}

		// Release workers.
		close(release)
	})
}

func TestSetSizeErrors(t *testing.T) {
	wp := New(3)
	defer wp.Stop()

	// Zero and negative sizes are rejected.
	for _, size := range []int{0, -1, -100} {
		if err := wp.SetSize(size); !errors.Is(err, ErrInvalidSize) {
			t.Fatalf("expected ErrInvalidSize for size %d, got %v", size, err)
		}
	}
	if wp.Size() != 3 {
		t.Fatal("invalid size changed pool size")
	}

	// Setting the same size is a no-op.
	if err := wp.SetSize(3); err != nil {
		t.Fatal("setting same size should succeed:", err)
	}

	if err := wp.SetSize(7); err != nil {
		t.Fatal(err)
	}
	if wp.Size() != 7 {
		t.Fatal("size not updated")
	}

	// Resizing a stopped pool returns a determinable error.
	wp.Stop()
	if err := wp.SetSize(5); !errors.Is(err, ErrStopped) {
		t.Fatal("expected ErrStopped, got", err)
	}
}

func TestSetSizeGrow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		wp := New(2)
		defer wp.Stop()

		release := make(chan struct{})
		started := make(chan struct{}, 8)
		for range 8 {
			wp.Submit(func() {
				started <- struct{}{}
				<-release
			})
		}

		// Only the first 2 tasks can run; the rest are queued.
		for range 2 {
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("timed out waiting for task to start")
			}
		}
		select {
		case <-started:
			t.Fatal("more tasks started than worker limit")
		default:
		}

		// Growing the pool must wake up the backlog of waiting tasks.
		if err := wp.SetSize(8); err != nil {
			t.Fatal(err)
		}
		for range 6 {
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("growing pool did not start waiting tasks")
			}
		}
		close(release)
	})
}

func TestSetSizeShrink(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		wp := New(10)
		defer wp.Stop()

		// Occupy all workers with tasks that block until released.
		release1 := make(chan struct{})
		entered1 := make(chan struct{}, 10)
		for range 10 {
			wp.Submit(func() {
				entered1 <- struct{}{}
				<-release1
			})
		}
		for range 10 {
			select {
			case <-entered1:
			case <-time.After(time.Second):
				t.Fatal("timed out waiting for tasks to start")
			}
		}

		// Shrink the pool. The running tasks cannot be interrupted; excess
		// workers are retired at task boundaries.
		if err := wp.SetSize(3); err != nil {
			t.Fatal(err)
		}
		if wp.Size() != 3 {
			t.Fatal("wrong size after shrink")
		}

		// Submit more tasks than the new limit. These must wait for the
		// running tasks to finish, and their concurrency must not exceed
		// the new limit.
		var active, peak atomic.Int32
		release2 := make(chan struct{})
		entered2 := make(chan struct{}, 7)
		for range 7 {
			wp.Submit(func() {
				n := active.Add(1)
				for {
					p := peak.Load()
					if n <= p || peak.CompareAndSwap(p, n) {
						break
					}
				}
				entered2 <- struct{}{}
				<-release2
				active.Add(-1)
			})
		}

		// None of the new tasks may start while the original tasks occupy
		// the workers.
		synctest.Wait()
		select {
		case <-entered2:
			t.Fatal("new task started before running tasks completed")
		default:
		}

		// Let the original tasks finish. Excess workers retire at task
		// boundaries, and the active task peak drops to the new limit.
		close(release1)
		for range 3 {
			select {
			case <-entered2:
			case <-time.After(time.Second):
				t.Fatal("timed out waiting for tasks after shrink")
			}
		}
		synctest.Wait()
		select {
		case <-entered2:
			t.Fatal("more tasks running than new worker limit")
		default:
		}
		if p := peak.Load(); p != 3 {
			t.Fatal("active task peak should have dropped to 3, is", p)
		}

		// The remaining queued tasks run as workers become available.
		close(release2)
		for range 4 {
			select {
			case <-entered2:
			case <-time.After(time.Second):
				t.Fatal("timed out waiting for remaining tasks")
			}
		}
		synctest.Wait()
		if n := countReady(wp); n != 3 {
			t.Fatal("expected 3 workers after shrink, have", n)
		}
	})
}

func TestSetSizeShrinkThenGrow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		wp := New(8)
		defer wp.Stop()

		release := make(chan struct{})
		entered := make(chan struct{}, 8)
		for range 8 {
			wp.Submit(func() {
				entered <- struct{}{}
				<-release
			})
		}
		for range 8 {
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("timed out waiting for tasks to start")
			}
		}

		// Shrink, then grow again before the shrink completes. The most
		// recently set size wins.
		if err := wp.SetSize(1); err != nil {
			t.Fatal(err)
		}
		if err := wp.SetSize(6); err != nil {
			t.Fatal(err)
		}
		if wp.Size() != 6 {
			t.Fatal("latest size should win")
		}

		// Let the running tasks finish, and submit new tasks to push
		// workers through task boundaries.
		close(release)
		done := make(chan struct{}, 4)
		for range 4 {
			wp.Submit(func() {
				done <- struct{}{}
			})
		}
		for range 4 {
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("timed out waiting for tasks")
			}
		}
		synctest.Wait()
		if n := countReady(wp); n != 6 {
			t.Fatal("expected 6 workers after shrink-then-grow, have", n)
		}
	})
}

func TestSetSizeWhilePaused(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		wp := New(4)
		defer wp.Stop()

		ctx, cancel := context.WithCancel(context.Background())
		wp.Pause(ctx)

		// Shrink while all workers are occupied by the pause.
		if err := wp.SetSize(2); err != nil {
			t.Fatal(err)
		}

		// A queued task must not run while paused.
		ran := make(chan struct{})
		wp.Submit(func() {
			close(ran)
		})
		select {
		case <-ran:
			t.Fatal("task ran while paused")
		case <-time.After(time.Millisecond):
		}

		// Unpause. Paused workers are retired at task boundaries as the
		// pause releases them, and the queued task runs.
		cancel()
		select {
		case <-ran:
		case <-time.After(time.Second):
			t.Fatal("task did not run after unpause")
		}
		synctest.Wait()
		if n := countReady(wp); n != 2 {
			t.Fatal("expected 2 workers after shrink, have", n)
		}
	})
}

func TestSetSizeStopWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		wp := New(8)

		release := make(chan struct{})
		started := make(chan struct{}, 8)
		for range 8 {
			wp.Submit(func() {
				started <- struct{}{}
				<-release
			})
		}
		for range 8 {
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("timed out waiting for tasks to start")
			}
		}

		// Shrink while workers are busy, then queue more tasks.
		if err := wp.SetSize(2); err != nil {
			t.Fatal(err)
		}
		const extra = 20
		var executed atomic.Int32
		for range extra {
			wp.Submit(func() {
				executed.Add(1)
			})
		}

		// StopWait drains all queued tasks even with a shrink in progress.
		go func() {
			time.Sleep(10 * time.Millisecond)
			close(release)
		}()
		wp.StopWait()
		if n := executed.Load(); n != extra {
			t.Fatal("StopWait did not drain queued tasks:", n, "of", extra)
		}
	})
}

func TestSetSizeConcurrent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		wp := New(4)
		defer wp.Stop()

		const numTasks = 300
		runs := make([]atomic.Int32, numTasks)
		var active, maxActive atomic.Int32
		var allDone sync.WaitGroup
		allDone.Add(numTasks)

		// Continuously resize the pool while tasks are submitted and run.
		var resizers sync.WaitGroup
		for _, size := range []int{1, 16, 3, 32, 2, 8} {
			resizers.Add(1)
			go func() {
				defer resizers.Done()
				for range 20 {
					if err := wp.SetSize(size); err != nil {
						t.Error("SetSize failed:", err)
						return
					}
				}
			}()
		}

		for i := range numTasks {
			wp.Submit(func() {
				n := active.Add(1)
				for {
					p := maxActive.Load()
					if n <= p || maxActive.CompareAndSwap(p, n) {
						break
					}
				}
				runs[i].Add(1)
				time.Sleep(time.Millisecond)
				active.Add(-1)
				allDone.Done()
			})
		}

		allDone.Wait()
		resizers.Wait()

		// No task was lost or run more than once.
		for i := range numTasks {
			if n := runs[i].Load(); n != 1 {
				t.Fatalf("task %d ran %d times", i, n)
			}
		}
		// Concurrency never exceeded the largest size ever set.
		if n := maxActive.Load(); n > 32 {
			t.Fatal("active tasks exceeded largest pool size:", n)
		}
	})
}

func TestReuseWorkers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		wp := New(5)
		defer wp.Stop()

		release := make(chan struct{})

		// Cause worker to be created, and available for reuse before next task.
		for range 10 {
			wp.Submit(func() { <-release })
			release <- struct{}{}
			synctest.Wait()
		}
		close(release)

		// If the same worker was always reused, then only one worker would have
		// been created and there should only be one ready.
		if countReady(wp) > 1 {
			t.Fatal("Worker not reused")
		}
	})
}

func TestWorkerTimeout(t *testing.T) {
	const idleTimeout = time.Second
	synctest.Test(t, func(t *testing.T) {
		wp := New(max, WithIdleTimeout(idleTimeout))
		defer wp.Stop()

		// Start workers, and have them all wait on ctx before completing.
		ctx, cancel := context.WithCancel(context.Background())
		wp.Pause(ctx)

		if anyReady(wp) {
			t.Fatal("number of ready workers should be zero")
		}

		if wp.killIdleWorker() {
			t.Fatal("should have been no idle workers to kill")
		}

		// Release workers.
		cancel()

		if countReady(wp) != max {
			t.Fatal("Expected", max, "ready workers")
		}

		// Check that a worker timed out.
		time.Sleep(idleTimeout*2 + idleTimeout/2)
		synctest.Wait()
		if countReady(wp) != max-1 {
			t.Fatal("First worker did not timeout")
		}

		// Check that another worker timed out.
		time.Sleep(idleTimeout)
		synctest.Wait()
		if countReady(wp) != max-2 {
			t.Fatal("Second worker did not timeout")
		}
	})
}

func TestWorkerNoTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		wp := New(max, WithIdleTimeout(-1))
		defer wp.Stop()

		// Start workers, and have them all wait on ctx before completing.
		ctx, cancel := context.WithCancel(context.Background())
		wp.Pause(ctx)
		cancel()

		// Check that a worker timed out.
		time.Sleep(time.Hour)
		synctest.Wait()
		if countReady(wp) != max {
			t.Fatal("no workers should have timed out")
		}
		// Check again.
		time.Sleep(time.Hour)
		synctest.Wait()
		if countReady(wp) != max {
			t.Fatal("no workers should have timed out")
		}
	})
}

func TestStop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		wp := New(max)

		// Start workers, and have them all wait on ctx before completing.
		ctx, cancel := context.WithCancel(context.Background())
		wp.Pause(ctx)

		// Release workers.
		cancel()

		if wp.Stopped() {
			t.Fatal("pool should not be stopped")
		}

		wp.Stop()
		if anyReady(wp) {
			t.Fatal("should have zero workers after stop")
		}

		if !wp.Stopped() {
			t.Fatal("pool should be stopped")
		}

		// Start workers, and have them all wait on a channel before completing.
		wp = New(5)

		release := make(chan struct{})
		finished := make(chan struct{}, max)
		for range max {
			wp.Submit(func() {
				<-release
				finished <- struct{}{}
			})
		}

		// Call Stop() and see that only the already running tasks were completed.
		go func() {
			time.Sleep(10 * time.Millisecond)
			close(release)
		}()
		wp.Stop()
		var count int
	Count:
		for count < max {
			select {
			case <-finished:
				count++
			default:
				break Count
			}
		}
		if count > 5 {
			t.Fatal("Should not have completed any queued tasks, did", count)
		}

		// Check that calling Stop() again is OK.
		wp.Stop()
	})
}

func TestStopWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// Start workers, and have them all wait on a channel before completing.
		wp := New(5)
		release := make(chan struct{})
		finished := make(chan struct{}, max)
		for range max {
			wp.Submit(func() {
				<-release
				finished <- struct{}{}
			})
		}

		// Call StopWait() and see that all tasks were completed.
		go func() {
			time.Sleep(10 * time.Millisecond)
			close(release)
		}()
		wp.StopWait()
		for range max {
			select {
			case <-finished:
			default:
				t.Fatal("Should have completed all queued tasks")
			}
		}

		if anyReady(wp) {
			t.Fatal("should have zero workers after stopwait")
		}

		if !wp.Stopped() {
			t.Fatal("pool should be stopped")
		}

		// Make sure that calling StopWait() with no queued tasks is OK.
		wp = New(5)
		wp.StopWait()

		if anyReady(wp) {
			t.Fatal("should have zero workers after stopwait")
		}

		// Check that calling StopWait() again is OK.
		wp.StopWait()
	})
}

func TestSubmitWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		wp := New(1)
		defer wp.Stop()

		// Check that these are noop.
		wp.Submit(nil)
		wp.SubmitWait(nil)

		done1 := make(chan struct{})
		wp.Submit(func() {
			time.Sleep(100 * time.Millisecond)
			close(done1)
		})
		select {
		case <-done1:
			t.Fatal("Submit did not return immediately")
		default:
		}

		done2 := make(chan struct{})
		wp.SubmitWait(func() {
			time.Sleep(100 * time.Millisecond)
			close(done2)
		})
		select {
		case <-done2:
		default:
			t.Fatal("SubmitWait did not wait for function to execute")
		}
	})
}

func TestOverflow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		wp := New(2)
		defer wp.Stop()
		releaseChan := make(chan struct{})

		// Start workers, and have them all wait on a channel before completing.
		for range 64 {
			wp.Submit(func() { <-releaseChan })
		}

		// Start a goroutine to free the workers after calling stop.  This way
		// the dispatcher can exit, then when this goroutine runs, the workerpool
		// can exit.
		go func() {
			<-time.After(time.Millisecond)
			close(releaseChan)
		}()
		wp.Stop()

		// Now that the worker pool has exited, it is safe to inspect its waiting
		// queue without causing a race.
		qlen := wp.waitingQueue.Len()
		if qlen != 62 {
			t.Fatal("Expected 62 tasks in waiting queue, have", qlen)
		}
	})
}

func TestStopRace(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		wp := New(max)
		defer wp.Stop()

		workRelChan := make(chan struct{})

		var started sync.WaitGroup
		started.Add(max)

		// Start workers, and have them all wait on a channel before completing.
		for range max {
			wp.Submit(func() {
				started.Done()
				<-workRelChan
			})
		}

		started.Wait()

		const doneCallers = 5
		stopDone := make(chan struct{}, doneCallers)
		for range doneCallers {
			go func() {
				wp.Stop()
				stopDone <- struct{}{}
			}()
		}

		select {
		case <-stopDone:
			t.Fatal("Stop should not return in any goroutine")
		default:
		}

		close(workRelChan)

		timeout := time.After(time.Second)
		synctest.Wait()

		for range doneCallers {
			select {
			case <-stopDone:
			case <-timeout:
				wp.Stop()
				t.Fatal("timedout waiting for Stop to return")
			}
		}
	})
}

// Run this test with race detector to test that using WaitingQueueSize has no
// race condition
func TestWaitingQueueSizeRace(t *testing.T) {
	const (
		goroutines = 10
		tasks      = 20
		workers    = 5
	)

	synctest.Test(t, func(t *testing.T) {
		wp := New(workers)
		defer wp.Stop()

		maxChan := make(chan int)
		for range goroutines {
			go func() {
				max := 0
				// Submit 100 tasks, checking waiting queue size each time.  Report
				// the maximum queue size seen.
				for range tasks {
					wp.Submit(func() {
						time.Sleep(time.Microsecond)
					})
					waiting := wp.WaitingQueueSize()
					if waiting > max {
						max = waiting
					}
				}
				maxChan <- max
			}()
		}

		// Find maximum queuesize seen by any goroutine.
		maxMax := 0
		for range goroutines {
			max := <-maxChan
			if max > maxMax {
				maxMax = max
			}
		}
		if maxMax == 0 {
			t.Error("expected to see waiting queue size > 0")
		}
		if maxMax >= goroutines*tasks {
			t.Error("should not have seen all tasks on waiting queue")
		}
	})
}

func TestPause(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		wp := New(25)
		defer wp.Stop()

		ctx, cancel := context.WithCancel(context.Background())

		ran := make(chan struct{})
		wp.Submit(func() {
			time.Sleep(time.Millisecond)
			close(ran)
		})

		wp.Pause(ctx)

		// Check that Pause waits for all previously submitted tasks to run.
		select {
		case <-ran:
		default:
			t.Error("did not run all tasks before returning from Pause")
		}

		ran = make(chan struct{})
		wp.Submit(func() {
			close(ran)
		})

		// Check that a new task did not run while paused
		select {
		case <-ran:
			t.Error("ran while paused")
		case <-time.After(time.Millisecond):
		}

		// Check that task was enqueued
		if wp.WaitingQueueSize() != 1 {
			t.Error("waiting queue size should be 1")
		}

		// Cancel context to unpause workers.
		cancel()

		// Check that task was run after unpausing.
		select {
		case <-ran:
		case <-time.After(time.Second):
			t.Error("did not run after canceling pause")
		}

		// ---- Test pause while paused

		ctx, cancel = context.WithCancel(context.Background())
		wp.Pause(ctx)

		ctx2, cancel2 := context.WithCancel(context.Background())

		pauseDone := make(chan struct{})
		go func() {
			wp.Pause(ctx2)
			close(pauseDone)
		}()

		// Check that second pause does not return until first pause in canceled
		select {
		case <-pauseDone:
			wp.Stop()
			t.Fatal("second Pause should not have returned")
		case <-time.After(time.Millisecond):
		}

		cancel() // cancel 1st pause

		// Check that second pause returns
		select {
		case <-pauseDone:
		case <-time.After(time.Second):
			wp.Stop()
			t.Fatal("timed out waiting for Pause to return")
		}

		cancel2() // cancel 2nd pause

		// ---- Test concurrent pauses

		ctx, cancel = context.WithCancel(context.Background())
		ctx2, cancel2 = context.WithCancel(context.Background())
		pauseDone = make(chan struct{})
		pause2Done := make(chan struct{})
		go func() {
			wp.Pause(ctx)
			close(pauseDone)
		}()
		go func() {
			wp.Pause(ctx2)
			close(pause2Done)
		}()

		select {
		case <-pauseDone:
			cancel()
			<-pause2Done
			cancel2()
		case <-pause2Done:
			cancel2()
			<-pauseDone
			cancel()
		case <-time.After(time.Second):
			t.Fatal("concurrent pauses deadlocked")
		}

		// ---- Test stopping paused pool ----

		ctx, cancel = context.WithCancel(context.Background())
		ctx2, cancel2 = context.WithCancel(context.Background())

		// Stack up two pauses
		wp.Pause(ctx)
		go wp.Pause(ctx2)

		ran = make(chan struct{})
		wp.Submit(func() {
			close(ran)
		})

		stopDone := make(chan struct{})
		go func() {
			wp.StopWait()
			close(stopDone)
		}()

		// Check that task was run after calling StopWait
		select {
		case <-stopDone:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for StopWait to return")
		}

		// Check that task was run after calling StopWait
		select {
		case <-ran:
		default:
			t.Error("did not run after canceling pause")
		}

		defer cancel()
		defer cancel2()

		// ---- Test pause after stop ----

		ctx, cancel = context.WithCancel(context.Background())
		pauseDone = make(chan struct{})
		go func() {
			wp.Pause(ctx)
			close(pauseDone)
		}()
		select {
		case <-pauseDone:
		case <-time.After(time.Second):
			t.Fatal("pause after stop did not return")
		}
		cancel()
	})
}

func TestWorkerLeak(t *testing.T) {
	const workerCount = 100

	synctest.Test(t, func(t *testing.T) {
		wp := New(workerCount)

		// Start workers, and have them all wait on a channel before completing.
		for range workerCount {
			wp.Submit(func() {
				time.Sleep(time.Millisecond)
			})
		}

		// If wp..Stop() is not waiting for all workers to complete, then goleak
		// should catch that
		wp.Stop()
	})
}

func TestPanicDo(t *testing.T) {
	wp := New(1)
	wp.Stop()

	done := make(chan struct{})
	err := wp.Do(func() {
		close(done)
	})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !errors.Is(err, ErrStopped) {
		t.Fatal("wrong error:", err)
	}

	assertPanics(t, "Submit", func() {
		wp.Submit(func() {
			close(done)
		})
	})
	select {
	case <-done:
		t.Fatal("task function should not have called")
	default:
	}

	assertPanics(t, "SubmitWait", func() {
		wp.SubmitWait(func() {
			close(done)
		})
	})
	select {
	case <-done:
		t.Fatal("task function should not have called")
	default:
	}
}

func assertPanics(t *testing.T, name string, f func()) {
	t.Helper()
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("%s: didn't panic as expected", name)
		}
	}()

	f()
}

func anyReady(w *WorkerPool) bool {
	release := make(chan struct{})
	wait := func() {
		<-release
	}
	select {
	case w.workerQueue <- wait:
		close(release)
		return true
	default:
	}
	return false
}

func countReady(w *WorkerPool) int {
	// Try to stop max workers.
	timeout := time.After(100 * time.Millisecond)
	release := make(chan struct{})
	wait := func() {
		<-release
	}
	var readyCount int
	for i := 0; i < max; i++ {
		select {
		case w.workerQueue <- wait:
			readyCount++
		case <-timeout:
			i = max
		}
	}

	close(release)
	return readyCount
}

/*

Run benchmarking with: go test -bench '.'

*/

func BenchmarkEnqueue(b *testing.B) {
	wp := New(1)
	defer wp.Stop()
	releaseChan := make(chan struct{})

	b.ResetTimer()

	// Start workers, and have them all wait on a channel before completing.
	for i := 0; i < b.N; i++ {
		wp.Submit(func() { <-releaseChan })
	}
	close(releaseChan)
}

func BenchmarkEnqueue2(b *testing.B) {
	wp := New(2)
	defer wp.Stop()

	b.ResetTimer()

	// Start workers, and have them all wait on a channel before completing.
	for i := 0; i < b.N; i++ {
		releaseChan := make(chan struct{})
		for range 64 {
			wp.Submit(func() { <-releaseChan })
		}
		close(releaseChan)
	}
}

func BenchmarkExecute1Worker(b *testing.B) {
	benchmarkExecWorkers(1, b)
}

func BenchmarkExecute2Worker(b *testing.B) {
	benchmarkExecWorkers(2, b)
}

func BenchmarkExecute4Workers(b *testing.B) {
	benchmarkExecWorkers(4, b)
}

func BenchmarkExecute16Workers(b *testing.B) {
	benchmarkExecWorkers(16, b)
}

func BenchmarkExecute64Workers(b *testing.B) {
	benchmarkExecWorkers(64, b)
}

func BenchmarkExecute1024Workers(b *testing.B) {
	benchmarkExecWorkers(1024, b)
}

func benchmarkExecWorkers(n int, b *testing.B) {
	wp := New(n)
	defer wp.Stop()
	var allDone sync.WaitGroup
	allDone.Add(b.N * n)

	b.ResetTimer()

	// Start workers, and have them all wait on a channel before completing.
	for i := 0; i < b.N; i++ {
		for range n {
			wp.Submit(func() {
				//time.Sleep(100 * time.Microsecond)
				allDone.Done()
			})
		}
	}
	allDone.Wait()
}
