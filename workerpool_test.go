package workerpool

import (
	"context"
	"errors"
	"sync"
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
		if wp.maxWorkers != 1 {
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

		if wp.reapIdleWorker() {
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

// A one-worker pool with its single worker blocked is the deterministic setup
// for cancellation tests: every task submitted after the first is guaranteed
// to be sitting in the waiting queue rather than running.
func blockedPool(t *testing.T) (*WorkerPool, chan struct{}, chan struct{}) {
	t.Helper()
	wp := New(1)
	started := make(chan struct{})
	release := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
		wp.StopWait()
	})
	wp.Submit(func() {
		close(started)
		<-release
	})
	<-started
	return wp, started, release
}

// waitWaitingSize waits until WaitingQueueSize reports want.
func waitWaitingSize(t *testing.T, wp *WorkerPool, want int) {
	t.Helper()
	deadline := time.After(time.Second)
	for wp.WaitingQueueSize() != want {
		select {
		case <-deadline:
			t.Fatalf("waiting queue size = %d, want %d", wp.WaitingQueueSize(), want)
		default:
			time.Sleep(time.Millisecond)
		}
	}
}

// waitIdle blocks until a freshly submitted plain task has run. Since the
// dispatcher and workers preserve FIFO, this establishes a happens-after edge
// with the completion (including terminal state store) of every prior task.
func waitIdle(t *testing.T, wp *WorkerPool) {
	t.Helper()
	done := make(chan struct{})
	if err := wp.Do(func() { close(done) }); err != nil {
		return // pool stopped; no further ordering is observable
	}
	<-done
	synctest.Wait()
}

func TestCancelWaiting(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		wp, _, release := blockedPool(t)

		ran := make(chan struct{}, 1)
		h, err := wp.SubmitHandle(func() {
			ran <- struct{}{}
		})
		if err != nil {
			t.Fatal("SubmitHandle:", err)
		}

		waitWaitingSize(t, wp, 1)

		if err := h.Cancel(); err != nil {
			t.Fatal("first Cancel should succeed, got", err)
		}

		// WaitingQueueSize must reflect the physical dequeue.
		if size := wp.WaitingQueueSize(); size != 0 {
			t.Fatal("waiting queue size should be 0 after cancel, got", size)
		}

		// A second Cancel is harmless and reports the existing terminal state.
		if err := h.Cancel(); err != ErrTaskCanceled {
			t.Fatal("second Cancel should return ErrTaskCanceled, got", err)
		}

		// The task never runs.
		select {
		case <-ran:
			t.Fatal("canceled task should not have run")
		case <-time.After(time.Millisecond):
		}

		// The captured closure must have been released.
		if h.entry.fn != nil {
			t.Fatal("closure reference should have been released after cancel")
		}

		close(release)
		synctest.Wait()
	})
}

func TestCancelRunningAndDone(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		wp := New(1)
		defer wp.Stop()

		started := make(chan struct{})
		release := make(chan struct{})
		running, err := wp.SubmitHandle(func() {
			close(started)
			<-release
		})
		if err != nil {
			t.Fatal("SubmitHandle:", err)
		}
		<-started

		// The task has been claimed by a worker and is executing.
		if err := running.Cancel(); err != ErrTaskRunning {
			t.Fatal("Cancel of running task should return ErrTaskRunning, got", err)
		}

		finished := make(chan struct{})
		running2, err := wp.SubmitHandle(func() {
			close(finished)
		})
		if err != nil {
			t.Fatal("SubmitHandle:", err)
		}

		close(release)
		<-finished
		waitIdle(t, wp)
		if err := running2.Cancel(); err != ErrTaskDone {
			t.Fatal("Cancel of finished task should return ErrTaskDone, got", err)
		}
		if err := running.Cancel(); err != ErrTaskDone {
			t.Fatal("Cancel after completion should return ErrTaskDone, got", err)
		}
	})
}

func TestCancelPreservesFIFO(t *testing.T) {
	const (
		total    = 300
		cancelLo = 50
		cancelHi = 250 // cancel every third task in [cancelLo, cancelHi)
	)

	synctest.Test(t, func(t *testing.T) {
		wp, _, release := blockedPool(t)

		var mu sync.Mutex
		var ran []int

		handles := make([]*TaskHandle, total)
		for i := range total {
			i := i
			h, err := wp.SubmitHandle(func() {
				mu.Lock()
				ran = append(ran, i)
				mu.Unlock()
			})
			if err != nil {
				t.Fatal("SubmitHandle:", err)
			}
			handles[i] = h
		}
		waitWaitingSize(t, wp, total)

		canceled := make(map[int]bool)
		for i := cancelLo; i < cancelHi; i += 3 {
			if err := handles[i].Cancel(); err != nil {
				t.Fatalf("cancel task %d: %v", i, err)
			}
			canceled[i] = true
		}

		// Queue size must track the real deque length exactly.
		wantSize := total - len(canceled)
		if got := wp.WaitingQueueSize(); got != wantSize {
			t.Fatalf("waiting queue size = %d, want %d", got, wantSize)
		}
		if got := wp.waitingQueue.Len(); got != wantSize {
			t.Fatalf("deque len = %d, want %d", got, wantSize)
		}

		// Idempotency and closure release.
		for i := range canceled {
			if err := handles[i].Cancel(); err != ErrTaskCanceled {
				t.Fatalf("second cancel of task %d returned %v", i, err)
			}
			if handles[i].entry.fn != nil {
				t.Fatalf("closure of canceled task %d not released", i)
			}
		}

		// Release the single worker and let the remaining queue drain.
		close(release)
		waitIdle(t, wp)

		mu.Lock()
		defer mu.Unlock()
		if len(ran) != total-len(canceled) {
			t.Fatalf("ran %d tasks, want %d", len(ran), total-len(canceled))
		}
		var prev int = -1
		for _, n := range ran {
			if canceled[n] {
				t.Fatal("canceled task", n, "was executed")
			}
			if n <= prev {
				t.Fatal("tasks did not run in FIFO order")
			}
			prev = n
		}
	})
}

func TestCancelMixedWithSubmit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		wp, _, release := blockedPool(t)

		var mu sync.Mutex
		var ran []int

		var handles []*TaskHandle
		for i := range 9 {
			i := i
			if i%2 == 0 {
				wp.Submit(func() {
					mu.Lock()
					ran = append(ran, i)
					mu.Unlock()
				})
			} else {
				h, err := wp.SubmitHandle(func() {
					mu.Lock()
					ran = append(ran, i)
					mu.Unlock()
				})
				if err != nil {
					t.Fatal("SubmitHandle:", err)
				}
				handles = append(handles, h)
			}
		}
		waitWaitingSize(t, wp, 9)

		// Cancel the handle for task 7 (queue order: 1,3,5,7 -> handles 0..3).
		if err := handles[3].Cancel(); err != nil {
			t.Fatal("cancel task 7:", err)
		}

		close(release)
		waitIdle(t, wp)

		mu.Lock()
		defer mu.Unlock()
		want := []int{0, 1, 2, 3, 4, 5, 6, 8}
		if len(ran) != len(want) {
			t.Fatalf("ran %v, want %v", ran, want)
		}
		for i := range want {
			if ran[i] != want[i] {
				t.Fatalf("ran %v, want %v", ran, want)
			}
		}
	})
}

func TestCancelWhilePaused(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		wp := New(1, WithIdleTimeout(-1))
		defer wp.Stop()

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		wp.Pause(ctx)

		h, err := wp.SubmitHandle(func() {})
		if err != nil {
			t.Fatal("SubmitHandle:", err)
		}
		synctest.Wait()
		if wp.WaitingQueueSize() != 1 {
			t.Fatal("task should be queued while paused")
		}

		if err := h.Cancel(); err != nil {
			t.Fatal("cancel while paused:", err)
		}
		if wp.WaitingQueueSize() != 0 {
			t.Fatal("queue should be empty after cancel")
		}

		ran := make(chan struct{})
		h2, err := wp.SubmitHandle(func() { close(ran) })
		if err != nil {
			t.Fatal("SubmitHandle:", err)
		}
		synctest.Wait()

		// Unpause; the remaining task must still run once.
		cancel()
		waitIdle(t, wp)
		select {
		case <-ran:
		default:
			t.Fatal("queued task did not run after unpause")
		}
		if err := h2.Cancel(); err != ErrTaskDone {
			t.Fatal("finished task should report ErrTaskDone, got", err)
		}
	})
}

func TestSubmitHandleAfterStop(t *testing.T) {
	wp := New(1)
	wp.Stop()

	h, err := wp.SubmitHandle(func() {})
	if err != ErrStopped {
		t.Fatal("expected ErrStopped, got", err)
	}
	if h != nil {
		t.Fatal("handle should be nil after stopped submit")
	}

	// Do/Submit behavior is unchanged.
	if err := wp.Do(func() {}); err != ErrStopped {
		t.Fatal("Do should return ErrStopped")
	}
	assertPanics(t, "Submit", func() {
		wp.Submit(func() {})
	})
}

func TestHandleClosureReleasedAfterRun(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		wp := New(1)
		defer wp.Stop()

		done := make(chan struct{})
		h, err := wp.SubmitHandle(func() { close(done) })
		if err != nil {
			t.Fatal("SubmitHandle:", err)
		}
		<-done
		waitIdle(t, wp)
		if h.entry.fn != nil {
			t.Fatal("closure should be released after the task finished")
		}
		if err := h.Cancel(); err != ErrTaskDone {
			t.Fatal("expected ErrTaskDone, got", err)
		}
	})
}

func TestCancelStress(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		wp := New(8, WithIdleTimeout(-1))
		defer wp.StopWait()

		const submitters = 8
		const perSubmitter = 200
		var wg sync.WaitGroup
		handles := make(chan *TaskHandle, submitters*perSubmitter)

		for range submitters {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for range perSubmitter {
					h, err := wp.SubmitHandle(func() {})
					if err != nil {
						t.Error("SubmitHandle:", err)
						return
					}
					handles <- h
				}
			}()
		}

		cancelerDone := make(chan struct{})
		go func() {
			for h := range handles {
				h.Cancel()
			}
			close(cancelerDone)
		}()

		wg.Wait()
		close(handles)
		<-cancelerDone

		if s := wp.WaitingQueueSize(); s != 0 {
			t.Fatal("expected empty waiting queue, got", s)
		}
	})
}

func TestCancelConcurrentWithStop(t *testing.T) {
	for _, stopWait := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			wp := New(4, WithIdleTimeout(-1))

			started := make(chan struct{}, 4)
			release := make(chan struct{})
			for range 4 {
				wp.Submit(func() {
					started <- struct{}{}
					<-release
				})
			}
			for range 4 {
				<-started
			}

			const n = 80
			handles := make([]*TaskHandle, n)
			for i := range n {
				h, err := wp.SubmitHandle(func() {})
				if err != nil {
					t.Fatal("SubmitHandle:", err)
				}
				handles[i] = h
			}

			// Two goroutines cancel the same handles concurrently with stop.
			results := make(chan [n]error, 2)
			for range 2 {
				go func() {
					var errs [n]error
					for i, h := range handles {
						errs[i] = h.Cancel()
					}
					results <- errs
				}()
			}

			stopDone := make(chan struct{})
			go func() {
				if stopWait {
					wp.StopWait()
				} else {
					wp.Stop()
				}
				close(stopDone)
			}()

			// Allow cancel goroutines and stop to make progress while workers
			// are still blocked, then release the workers so Stop (and the
			// Cancel calls it may be waiting for) can finish.
			synctest.Wait()
			close(release)

			select {
			case <-stopDone:
			case <-time.After(time.Second):
				t.Fatal("stop did not return")
			}

			r1 := <-results
			r2 := <-results

			// Each accepted task has a single terminal state: at most one
			// Cancel caller wins; any other outcome is a settled error.
			for i := range n {
				canceled := r1[i] == nil || r2[i] == nil
				if r1[i] == nil && r2[i] == nil {
					t.Fatalf("task %d canceled twice", i)
				}
				for _, err := range []error{r1[i], r2[i]} {
					switch err {
					case nil, ErrTaskCanceled, ErrTaskRunning, ErrTaskDone:
					default:
						t.Fatalf("task %d unexpected Cancel result: %v", i, err)
					}
				}
				settled := handles[i].entry.state.Load()
				if canceled && settled != taskCanceled {
					t.Fatalf("task %d reported canceled but state = %d", i, settled)
				}
				if !canceled && settled != taskDone && settled != taskRunning &&
					settled != taskCanceled {
					t.Fatalf("task %d has no terminal state: %d", i, settled)
				}
			}
		})
	}
}

func anyReady(w *WorkerPool) bool {
	select {
	case wq := <-w.workerReady:
		// Hand back a harmless blocking task so the worker stays around.
		release := make(chan struct{})
		wq <- func() { <-release }
		return true
	default:
	}
	return false
}

func countReady(w *WorkerPool) int {
	// Consume ready workers and park each one on a blocking task.
	timeout := time.After(100 * time.Millisecond)
	release := make(chan struct{})
	var readyCount int
	for i := 0; i < max; i++ {
		select {
		case wq := <-w.workerReady:
			wq <- func() { <-release }
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
