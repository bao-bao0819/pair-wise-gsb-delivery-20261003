# workerpool

[![GoDoc](https://pkg.go.dev/badge/github.com/gammazero/workerpool)](https://pkg.go.dev/github.com/gammazero/workerpool)
[![Build Status](https://github.com/gammazero/workerpool/actions/workflows/go.yml/badge.svg)](https://github.com/gammazero/workerpool/actions/workflows/go.yml)
[![codecov](https://codecov.io/gh/gammazero/workerpool/graph/badge.svg?token=9BLO50ZRY1)](https://codecov.io/gh/gammazero/workerpool)
[![License](https://img.shields.io/badge/License-MIT-blue.svg)](https://github.com/gammazero/workerpool/blob/master/LICENSE)

Concurrency limiting goroutine pool. Limits the concurrency of task execution, not the number of tasks queued. Never blocks submitting tasks, no matter how many tasks are queued.

If you need a worker pool that blocks when all workers are busy, see [workers](https://github.com/gammazero/workers).

This implementation builds on ideas from the following:

- http://marcio.io/2015/07/handling-1-million-requests-per-minute-with-golang
- http://nesv.github.io/golang/2014/02/25/worker-queues-in-go.html

## Installation

To install this package, you need to setup your Go workspace. The simplest way to install the library is to run:
```
$ go get github.com/gammazero/workerpool
```

## Example

```go
package main

import (
	"fmt"
	"github.com/gammazero/workerpool"
)

func main() {
	wp := workerpool.New(2)
	requests := []string{"alpha", "beta", "gamma", "delta", "epsilon"}

	for _, r := range requests {
		wp.Submit(func() {
			fmt.Println("Handling request:", r)
		})
	}

	wp.StopWait()
}
```

[Example wrapper function](https://go.dev/play/p/BWnRhJYarZ1) to show start and finish time of submitted function.

## Fences

Use a fence to wait for "everything submitted so far" without stopping the pool, so that more tasks can be submitted afterwards. A fence covers only the tasks the pool had received when the fence was created; tasks submitted later do not delay it.

```go
fence := wp.Fence()
if err := fence.Wait(ctx); err != nil {
    // err is nil once all previously submitted tasks completed.
    // ErrFenceDropped means the pool was stopped with Stop and some
    // covered queued tasks were discarded.
    // ctx.Err() means the context was canceled first.
}
```

Fences can be created repeatedly and several may be awaited concurrently. An earlier fence is never released by later tasks finishing first; it waits for the exact prefix it sampled. Waiting is interruptible via `context.Context`. If the pool is stopped with `Stop`, queued tasks are abandoned and every fence covering them returns `ErrFenceDropped` rather than hanging. `StopWait` drains the queue as usual and satisfies outstanding fences with `nil`.

## Usage Note

There is no upper limit on the number of tasks queued, other than the limits of system resources. If the number of inbound tasks is too many to even queue for pending processing, then the solution is outside the scope of workerpool. It should be solved by distributing workload over multiple systems, and/or storing input for pending processing in intermediate storage such as a file system, distributed message queue, etc.
