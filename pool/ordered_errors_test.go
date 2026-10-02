package pool_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sourcegraph/conc/pool"

	"github.com/stretchr/testify/require"
)

type orderedTestError struct {
	name string
}

func (e *orderedTestError) Error() string {
	return e.name
}

func makeReverseCompletingErrors() (chan struct{}, error, *orderedTestError) {
	firstCanComplete := make(chan struct{})
	err0 := errors.New("first task")
	err1 := &orderedTestError{name: "second task"}

	return firstCanComplete, err0, err1
}

func requireTaskErrors(t *testing.T, err error, expected pool.TaskErrors) {
	t.Helper()

	var taskErrs pool.TaskErrors
	require.ErrorAs(t, err, &taskErrs)
	require.Equal(t, expected, taskErrs)
	for i, taskErr := range taskErrs {
		require.Equal(t, expected[i].Index, taskErr.TaskIndex())
		require.ErrorIs(t, err, expected[i].Err)
	}
}

func TestErrorPoolWithOrderedErrors(t *testing.T) {
	t.Parallel()

	t.Run("errors are ordered by submission index", func(t *testing.T) {
		t.Parallel()
		firstCanComplete, err0, err1 := makeReverseCompletingErrors()
		p := pool.New().WithErrors().WithOrderedErrors()

		p.Go(func() error {
			<-firstCanComplete
			time.Sleep(50 * time.Millisecond)
			return err0
		})
		p.Go(func() error {
			close(firstCanComplete)
			return err1
		})

		err := p.Wait()
		require.EqualError(t, err, "first task\nsecond task")
		require.ErrorIs(t, err, err0)
		require.ErrorIs(t, err, err1)

		var originalErr *orderedTestError
		require.ErrorAs(t, err, &originalErr)
		require.Equal(t, "second task", originalErr.name)

		requireTaskErrors(t, err, pool.TaskErrors{
			{Index: 0, Err: err0},
			{Index: 1, Err: err1},
		})
	})

	t.Run("WithFirstError keeps the first completed error", func(t *testing.T) {
		t.Parallel()
		firstCanComplete, err0, err1 := makeReverseCompletingErrors()
		p := pool.New().WithErrors().WithOrderedErrors().WithFirstError()

		p.Go(func() error {
			<-firstCanComplete
			time.Sleep(50 * time.Millisecond)
			return err0
		})
		p.Go(func() error {
			close(firstCanComplete)
			return err1
		})

		err := p.Wait()
		require.ErrorIs(t, err, err1)
		require.NotErrorIs(t, err, err0)

		var taskErr pool.TaskError
		require.ErrorAs(t, err, &taskErr)
		require.Equal(t, 1, taskErr.Index)
		require.Equal(t, err1, taskErr.Err)

		var taskErrs pool.TaskErrors
		require.False(t, errors.As(err, &taskErrs))
	})

	t.Run("reuse resets indexes and errors", func(t *testing.T) {
		t.Parallel()
		err0 := errors.New("first round")
		err1 := errors.New("second round")
		p := pool.New().WithErrors().WithOrderedErrors()

		p.Go(func() error { return err0 })
		require.ErrorIs(t, p.Wait(), err0)

		p.Go(func() error { return nil })
		p.Go(func() error { return err1 })
		err := p.Wait()
		require.NotErrorIs(t, err, err0)
		requireTaskErrors(t, err, pool.TaskErrors{{Index: 1, Err: err1}})
	})
}

func TestContextPoolWithOrderedErrors(t *testing.T) {
	t.Parallel()

	firstCanComplete, err0, err1 := makeReverseCompletingErrors()
	p := pool.New().
		WithContext(context.Background()).
		WithOrderedErrors()

	p.Go(func(context.Context) error {
		<-firstCanComplete
		time.Sleep(50 * time.Millisecond)
		return err0
	})
	p.Go(func(context.Context) error {
		close(firstCanComplete)
		return err1
	})

	err := p.Wait()
	require.ErrorIs(t, err, err0)
	require.ErrorIs(t, err, err1)
	requireTaskErrors(t, err, pool.TaskErrors{
		{Index: 0, Err: err0},
		{Index: 1, Err: err1},
	})
}

func TestResultErrorPoolWithOrderedErrors(t *testing.T) {
	t.Parallel()
	err0 := errors.New("task 0")
	err2 := errors.New("task 2")

	t.Run("successful results are filtered while errors retain indexes", func(t *testing.T) {
		t.Parallel()
		p := pool.NewWithResults[int]().WithErrors().WithOrderedErrors()

		p.Go(func() (int, error) { return 10, err0 })
		p.Go(func() (int, error) { return 11, nil })
		p.Go(func() (int, error) { return 12, err2 })
		p.Go(func() (int, error) { return 13, nil })

		results, err := p.Wait()
		require.Equal(t, []int{11, 13}, results)
		requireTaskErrors(t, err, pool.TaskErrors{
			{Index: 0, Err: err0},
			{Index: 2, Err: err2},
		})
	})

	t.Run("WithCollectErrored keeps original result positions", func(t *testing.T) {
		t.Parallel()
		p := pool.NewWithResults[int]().
			WithErrors().
			WithOrderedErrors().
			WithCollectErrored()

		p.Go(func() (int, error) { return 10, err0 })
		p.Go(func() (int, error) { return 11, nil })
		p.Go(func() (int, error) { return 12, err2 })

		results, err := p.Wait()
		require.Equal(t, []int{10, 11, 12}, results)
		requireTaskErrors(t, err, pool.TaskErrors{
			{Index: 0, Err: err0},
			{Index: 2, Err: err2},
		})
	})

	t.Run("reuse resets results, indexes, and errors", func(t *testing.T) {
		t.Parallel()
		err1 := errors.New("second round")
		p := pool.NewWithResults[int]().WithErrors().WithOrderedErrors()

		p.Go(func() (int, error) { return 1, err0 })
		results, err := p.Wait()
		require.Empty(t, results)
		require.ErrorIs(t, err, err0)

		p.Go(func() (int, error) { return 20, nil })
		p.Go(func() (int, error) { return 21, err1 })
		results, err = p.Wait()
		require.Equal(t, []int{20}, results)
		require.NotErrorIs(t, err, err0)
		requireTaskErrors(t, err, pool.TaskErrors{{Index: 1, Err: err1}})
	})
}

func TestResultContextPoolWithOrderedErrors(t *testing.T) {
	t.Parallel()
	err0 := errors.New("task 0")
	err2 := errors.New("task 2")

	t.Run("successful results are filtered while errors retain indexes", func(t *testing.T) {
		t.Parallel()
		p := pool.NewWithResults[int]().
			WithContext(context.Background()).
			WithOrderedErrors()

		p.Go(func(context.Context) (int, error) { return 10, err0 })
		p.Go(func(context.Context) (int, error) { return 11, nil })
		p.Go(func(context.Context) (int, error) { return 12, err2 })
		p.Go(func(context.Context) (int, error) { return 13, nil })

		results, err := p.Wait()
		require.Equal(t, []int{11, 13}, results)
		requireTaskErrors(t, err, pool.TaskErrors{
			{Index: 0, Err: err0},
			{Index: 2, Err: err2},
		})
	})

	t.Run("WithCollectErrored keeps original result positions", func(t *testing.T) {
		t.Parallel()
		p := pool.NewWithResults[int]().
			WithContext(context.Background()).
			WithOrderedErrors().
			WithCollectErrored()

		p.Go(func(context.Context) (int, error) { return 10, err0 })
		p.Go(func(context.Context) (int, error) { return 11, nil })
		p.Go(func(context.Context) (int, error) { return 12, err2 })

		results, err := p.Wait()
		require.Equal(t, []int{10, 11, 12}, results)
		requireTaskErrors(t, err, pool.TaskErrors{
			{Index: 0, Err: err0},
			{Index: 2, Err: err2},
		})
	})
}
