# AGENTS.md

This file provides guidance to AI coding agents working in this repository.

## Overview

`workerpool` is a zero-dependency-surface Go library (single external dependency: `github.com/gammazero/deque`) providing a concurrency limiting goroutine pool. It limits the concurrency of task execution, not the number of tasks queued, and task submission never blocks. There is no main program; the deliverable is the public API in the root package plus the independent `pacer` subpackage.

## Commands

```
go build ./...
go vet ./...
go test -race ./...
```

- Run a single test: `go test -race -run TestName .` (or `./pacer` for pacer tests)
- CI runs `go vet`, `go build`, and `go test -race -coverprofile=...` on Go stable, plus golangci-lint (v2.x). Run `golangci-lint run` locally if available.

## Architecture

The pool lives in three files: `workerpool.go` (mechanics), `options.go` (functional options), and `fence.go` (repeatable completion barriers). Understanding it requires seeing how three queues interact:

- `taskQueue` (unbuffered chan of `poolTask`): where `Submit`/`Do` hand tasks to the dispatcher.
- `workerQueue` (unbuffered chan of `poolTask`): where the dispatcher hands tasks to workers. Sending a zero `poolTask` (nil `fn`) on it kills a worker.
- `waitingQueue` (`deque.Deque[poolTask]`, owned solely by the dispatcher): overflow buffer when all `maxWorkers` workers are busy. Its length is mirrored into the atomic `waiting` counter so `WaitingQueueSize()` is lock-free.

`poolTask` carries a submission sequence number (`seq`, zero for internal Pause tasks) so that `fence.go` can track completion. `Fence()` samples the highest assigned seq; the tracker maintains a contiguous completion watermark plus a map of out-of-order completions (holes), so memory scales with unmerged holes, not task history. `Fence.Wait(ctx)` returns nil on completion, the context error on cancellation, or `ErrFenceDiscarded` when `Stop` discards a covered queued task.

A single dispatcher goroutine (`dispatch`), started by `New`, is the only goroutine that touches `waitingQueue` and the worker count. It creates workers lazily up to `maxWorkers`, and while the waiting queue is non-empty it routes all traffic through `processWaitingQueue` to preserve task order. When the pool is idle for a full idle timeout (default 2s, configurable or disabled via `WithIdleTimeout`), it reaps one idle worker per timeout period.

Non-obvious invariants to preserve when changing this code:

- Task submission must never block, no matter how many tasks are queued.
- `Do` detects a stopped pool by recovering from the panic caused by sending on the closed `taskQueue`; `Submit` panics on a stopped pool while `Do` returns `ErrStopped`.
- `Stop` abandons queued tasks; `StopWait` drains them (`p.wait` flag read by the dispatcher after its loop exits). Both are safe to call multiple times via `stopOnce`, and both wait for `stoppedChan`.
- On `Stop` (not `StopWait`), the dispatcher must report every queued task's seq to the fence tracker (`discardQueuedTasks`) so fences fail with `ErrFenceDiscarded` instead of hanging. `Do`'s recover path must complete the seq of a task the stopped pool never accepted, or fences would wait on a hole forever.
- `Pause` works by submitting `maxWorkers` blocking tasks that wait on the caller's Context or on `stopSignal`; `stopLock` orders Pause against Stop so a stop always unpauses workers before closing `taskQueue`.

`pacer/` is a self-contained rate limiter for staggering goroutine start times. It is independent of the pool (its tests import workerpool, not the other way around).

## Conventions

- All behavior changes need coverage in `workerpool_test.go` or `pacer/pacer_test.go`; tests must pass under `-race` since the whole point of the package is concurrency.
- Exported API is documented in godoc style; `doc.go` carries the package overview. Keep README.md examples in sync with API changes.
- The pool intentionally has no upper bound on queued tasks; do not add one (see "Usage Note" in README.md).
