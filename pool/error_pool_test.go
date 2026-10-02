package pool_test

import (
	"errors"
	"fmt"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sourcegraph/conc/pool"

	"github.com/stretchr/testify/require"
)

func ExampleErrorPool() {
	p := pool.New().WithErrors()
	for i := 0; i < 3; i++ {
		i := i
		p.Go(func() error {
			if i == 2 {
				return errors.New("oh no!")
			}
			return nil
		})
	}
	err := p.Wait()
	fmt.Println(err)
	// Output:
	// oh no!
}

func TestErrorPool(t *testing.T) {
	t.Parallel()

	err1 := errors.New("err1")
	err2 := errors.New("err2")

	t.Run("panics on configuration after init", func(t *testing.T) {
		t.Run("before wait", func(t *testing.T) {
			t.Parallel()
			g := pool.New().WithErrors()
			g.Go(func() error { return nil })
			require.Panics(t, func() { g.WithMaxGoroutines(10) })
		})

		t.Run("after wait", func(t *testing.T) {
			t.Parallel()
			g := pool.New().WithErrors()
			g.Go(func() error { return nil })
			_ = g.Wait()
			require.Panics(t, func() { g.WithMaxGoroutines(10) })
		})
	})

	t.Run("wait returns no error if no errors", func(t *testing.T) {
		t.Parallel()
		g := pool.New().WithErrors()
		g.Go(func() error { return nil })
		require.NoError(t, g.Wait())
	})

	t.Run("wait error if func returns error", func(t *testing.T) {
		t.Parallel()
		g := pool.New().WithErrors()
		g.Go(func() error { return err1 })
		require.ErrorIs(t, g.Wait(), err1)
	})

	t.Run("wait error is all returned errors", func(t *testing.T) {
		t.Parallel()
		g := pool.New().WithErrors()
		g.Go(func() error { return err1 })
		g.Go(func() error { return nil })
		g.Go(func() error { return err2 })
		err := g.Wait()
		require.ErrorIs(t, err, err1)
		require.ErrorIs(t, err, err2)
	})

	t.Run("ordered errors are aggregated in submission order", func(t *testing.T) {
		t.Parallel()
		const tasks = 10
		errs := make([]error, tasks)
		for i := range errs {
			errs[i] = fmt.Errorf("err%d", i)
		}
		g := pool.New().WithErrors().WithOrderedErrors()
		for i := 0; i < tasks; i++ {
			i := i
			g.Go(func() error {
				// Sleep so that tasks complete in reverse submission order.
				time.Sleep(time.Duration(tasks-i) * time.Millisecond)
				return errs[i]
			})
		}
		err := g.Wait()
		require.Error(t, err)

		// The original errors remain accessible via errors.Is.
		for _, want := range errs {
			require.ErrorIs(t, err, want)
		}

		// The aggregated errors are ordered by submission index, and each is
		// wrapped in a *TaskError that locates it back to its task.
		joined, ok := err.(interface{ Unwrap() []error })
		require.True(t, ok)
		unwrapped := joined.Unwrap()
		require.Len(t, unwrapped, tasks)
		for i, u := range unwrapped {
			var terr *pool.TaskError
			require.ErrorAs(t, u, &terr)
			require.Equal(t, i, terr.Index)
			require.Equal(t, errs[i], terr.Err)
		}
	})

	t.Run("first error is first to fail, not lowest index, with ordered errors", func(t *testing.T) {
		t.Parallel()
		g := pool.New().WithErrors().WithFirstError().WithOrderedErrors()
		g.Go(func() error {
			// The task with the lowest submission index fails last.
			time.Sleep(100 * time.Millisecond)
			return err1
		})
		g.Go(func() error { return err2 })
		err := g.Wait()
		require.Equal(t, err2, err)
		require.NotErrorIs(t, err, err1)
	})

	t.Run("propagates panics", func(t *testing.T) {
		t.Parallel()
		g := pool.New().WithErrors()
		for i := 0; i < 10; i++ {
			i := i
			g.Go(func() error {
				if i == 5 {
					panic("fatal")
				}
				return nil
			})
		}
		require.Panics(t, func() { _ = g.Wait() })
	})

	t.Run("limit", func(t *testing.T) {
		t.Parallel()
		for _, maxGoroutines := range []int{1, 10, 100} {
			t.Run(strconv.Itoa(maxGoroutines), func(t *testing.T) {
				g := pool.New().WithErrors().WithMaxGoroutines(maxGoroutines)

				var currentConcurrent atomic.Int64
				taskCount := maxGoroutines * 10
				for i := 0; i < taskCount; i++ {
					g.Go(func() error {
						cur := currentConcurrent.Add(1)
						if cur > int64(maxGoroutines) {
							return fmt.Errorf("expected no more than %d concurrent goroutine", maxGoroutines)
						}
						time.Sleep(time.Millisecond)
						currentConcurrent.Add(-1)
						return nil
					})
				}
				require.NoError(t, g.Wait())
				require.Equal(t, int64(0), currentConcurrent.Load())
			})
		}
	})

	t.Run("reuse", func(t *testing.T) {
		// Test for https://github.com/sourcegraph/conc/issues/128
		p := pool.New().WithErrors()

		p.Go(func() error { return err1 })
		wait1 := p.Wait()
		require.ErrorIs(t, wait1, err1)

		p.Go(func() error { return err2 })
		wait2 := p.Wait()
		// On reuse, only the new error should be returned
		require.ErrorIs(t, wait2, err2)
		require.NotErrorIs(t, wait1, err2)
	})

	t.Run("reuse resets ordered error indices", func(t *testing.T) {
		p := pool.New().WithErrors().WithOrderedErrors()

		p.Go(func() error { return nil })
		p.Go(func() error { return err1 })
		wait1 := p.Wait()
		var terr1 *pool.TaskError
		require.ErrorAs(t, wait1, &terr1)
		require.Equal(t, 1, terr1.Index)
		require.ErrorIs(t, wait1, err1)

		p.Go(func() error { return err2 })
		wait2 := p.Wait()
		var terr2 *pool.TaskError
		require.ErrorAs(t, wait2, &terr2)
		// On reuse, submission indices start over and old errors are gone.
		require.Equal(t, 0, terr2.Index)
		require.ErrorIs(t, wait2, err2)
		require.NotErrorIs(t, wait2, err1)
	})
}
