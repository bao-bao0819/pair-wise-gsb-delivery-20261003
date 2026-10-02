package pool_test

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/sourcegraph/conc/pool"

	"github.com/stretchr/testify/require"
)

func TestPoolGoContext(t *testing.T) {
	t.Parallel()

	t.Run("already cancelled context rejects the task", func(t *testing.T) {
		t.Parallel()

		p := pool.New()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		var ran atomic.Bool
		err := p.GoContext(ctx, func() { ran.Store(true) })
		require.ErrorIs(t, err, context.Canceled)

		p.Wait()
		require.False(t, ran.Load())
	})

	t.Run("cancelled while waiting for a slot", func(t *testing.T) {
		t.Parallel()

		p := pool.New().WithMaxGoroutines(1)

		started := make(chan struct{})
		release := make(chan struct{})
		p.Go(func() {
			close(started)
			<-release
		})
		<-started

		ctx, cancel := context.WithCancel(context.Background())
		var ran atomic.Bool
		errCh := make(chan error, 1)
		go func() {
			errCh <- p.GoContext(ctx, func() { ran.Store(true) })
		}()
		cancel()
		require.ErrorIs(t, <-errCh, context.Canceled)

		close(release)
		p.Wait()
		require.False(t, ran.Load())
	})

	t.Run("accepted task is counted by Wait", func(t *testing.T) {
		t.Parallel()

		p := pool.New().WithMaxGoroutines(1)

		block := make(chan struct{})
		p.Go(func() { <-block })

		accepted := make(chan struct{})
		errCh := make(chan error, 1)
		go func() {
			errCh <- p.GoContext(context.Background(), func() { close(accepted) })
		}()

		close(block)
		<-accepted
		require.NoError(t, <-errCh)
		p.Wait()
	})
}

func TestErrorPoolGoContext(t *testing.T) {
	t.Parallel()

	p := pool.New().WithErrors()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var ran atomic.Bool
	err := p.GoContext(ctx, func() error { ran.Store(true); return nil })
	require.ErrorIs(t, err, context.Canceled)

	require.NoError(t, p.GoContext(context.Background(), func() error { return nil }))
	require.NoError(t, p.Wait())
	require.False(t, ran.Load())
}

func TestContextPoolGoContext(t *testing.T) {
	t.Parallel()

	p := pool.New().WithContext(context.Background())

	// Cancelling the submission context must not cancel the pool's context.
	submitCtx, cancel := context.WithCancel(context.Background())
	cancel()

	var ran atomic.Bool
	err := p.GoContext(submitCtx, func(ctx context.Context) error {
		ran.Store(true)
		return nil
	})
	require.ErrorIs(t, err, context.Canceled)

	var gotPoolCtx atomic.Bool
	require.NoError(t, p.GoContext(context.Background(), func(ctx context.Context) error {
		gotPoolCtx.Store(ctx.Err() == nil)
		return nil
	}))
	require.NoError(t, p.Wait())
	require.False(t, ran.Load())
	require.True(t, gotPoolCtx.Load())
}

func TestResultPoolGoContext(t *testing.T) {
	t.Parallel()

	p := pool.NewWithResults[int]()

	require.NoError(t, p.GoContext(context.Background(), func() int { return 1 }))
	require.NoError(t, p.GoContext(context.Background(), func() int { return 2 }))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := p.GoContext(ctx, func() int { return 3 })
	require.ErrorIs(t, err, context.Canceled)

	require.NoError(t, p.GoContext(context.Background(), func() int { return 4 }))

	// The rejected task must not leave a zero value or a gap in the results.
	require.Equal(t, []int{1, 2, 4}, p.Wait())
}

func TestResultErrorPoolGoContext(t *testing.T) {
	t.Parallel()

	p := pool.NewWithResults[int]().WithErrors()

	require.NoError(t, p.GoContext(context.Background(), func() (int, error) { return 1, nil }))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := p.GoContext(ctx, func() (int, error) { return 2, nil })
	require.ErrorIs(t, err, context.Canceled)

	require.NoError(t, p.GoContext(context.Background(), func() (int, error) { return 3, nil }))

	results, err := p.Wait()
	require.NoError(t, err)
	require.Equal(t, []int{1, 3}, results)
}

func TestResultContextPoolGoContext(t *testing.T) {
	t.Parallel()

	p := pool.NewWithResults[int]().WithContext(context.Background())

	require.NoError(t, p.GoContext(context.Background(), func(context.Context) (int, error) { return 1, nil }))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := p.GoContext(ctx, func(context.Context) (int, error) { return 2, nil })
	require.ErrorIs(t, err, context.Canceled)

	require.NoError(t, p.GoContext(context.Background(), func(context.Context) (int, error) { return 3, nil }))

	results, err := p.Wait()
	require.NoError(t, err)
	require.Equal(t, []int{1, 3}, results)
}
