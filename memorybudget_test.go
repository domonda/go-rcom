package rcom

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// reservedBytes returns the reserved bytes of the budget
func (b *memoryBudget) reservedBytes() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.used
}

type reservation struct {
	release func()
	err     error
}

// reserveAsync reserves n bytes of b in a goroutine
// and returns after the reservation waits or succeeded
func reserveAsync(t *testing.T, ctx context.Context, b *memoryBudget, shuttingDown chan struct{}, n int64) chan reservation {
	t.Helper()
	b.mu.Lock()
	waiting := b.waiters.Len()
	b.mu.Unlock()

	reserved := make(chan reservation, 1)
	go func() {
		release, err := b.reserve(ctx, shuttingDown, n)
		reserved <- reservation{release, err}
	}()
	require.Eventually(t, func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		return b.waiters.Len() > waiting || len(reserved) > 0
	}, 5*time.Second, time.Millisecond)
	return reserved
}

func requireReserved(t *testing.T, reserved chan reservation) reservation {
	t.Helper()
	select {
	case r := <-reserved:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("still waiting for the reservation")
		return reservation{}
	}
}

func requireWaiting(t *testing.T, reserved chan reservation) {
	t.Helper()
	select {
	case r := <-reserved:
		t.Fatalf("reserved instead of waiting, error: %v", r.err)
	case <-time.After(50 * time.Millisecond):
	}
}

func Test_memoryBudget(t *testing.T) {
	t.Run("no limit", func(t *testing.T) {
		assert.Nil(t, newMemoryBudget(0))
		assert.Nil(t, newMemoryBudget(-1))
		release, err := newMemoryBudget(0).reserve(t.Context(), nil, 1<<40)
		require.NoError(t, err)
		release()
	})

	t.Run("waits for released bytes", func(t *testing.T) {
		budget := newMemoryBudget(10)
		release, err := budget.reserve(t.Context(), nil, 6)
		require.NoError(t, err)

		reserved := reserveAsync(t, t.Context(), budget, nil, 5)
		requireWaiting(t, reserved)
		release()
		second := requireReserved(t, reserved)
		require.NoError(t, second.err)
		release()
		assert.Equal(t, int64(5), budget.reservedBytes(), "releasing twice releases once")
		second.release()
		assert.Equal(t, int64(0), budget.reservedBytes())
	})

	t.Run("larger than the budget", func(t *testing.T) {
		budget := newMemoryBudget(10)
		release, err := budget.reserve(t.Context(), nil, 100)
		require.NoError(t, err)
		assert.Equal(t, int64(10), budget.reservedBytes(), "the whole budget")
		release()
		assert.Equal(t, int64(0), budget.reservedBytes())
	})

	t.Run("in the order of the requests", func(t *testing.T) {
		budget := newMemoryBudget(10)
		release, err := budget.reserve(t.Context(), nil, 8)
		require.NoError(t, err)

		// The small request would fit but waits behind the large one,
		// which would wait forever if small requests kept coming
		large := reserveAsync(t, t.Context(), budget, nil, 10)
		small := reserveAsync(t, t.Context(), budget, nil, 1)
		requireWaiting(t, small)

		release()
		largeReserved := requireReserved(t, large)
		require.NoError(t, largeReserved.err)
		requireWaiting(t, small)
		largeReserved.release()
		require.NoError(t, requireReserved(t, small).err)
	})

	t.Run("canceled while waiting", func(t *testing.T) {
		budget := newMemoryBudget(10)
		release, err := budget.reserve(t.Context(), nil, 8)
		require.NoError(t, err)
		defer release()

		ctx, cancel := context.WithCancel(t.Context())
		canceled := reserveAsync(t, ctx, budget, nil, 10)
		next := reserveAsync(t, t.Context(), budget, nil, 2)
		cancel()
		assert.ErrorIs(t, requireReserved(t, canceled).err, errNoMemory)
		require.NoError(t, requireReserved(t, next).err, "the next request fits now")
	})

	t.Run("shutting down", func(t *testing.T) {
		budget := newMemoryBudget(10)
		release, err := budget.reserve(t.Context(), nil, 10)
		require.NoError(t, err)
		defer release()

		shuttingDown := make(chan struct{})
		reserved := reserveAsync(t, t.Context(), budget, shuttingDown, 1)
		close(shuttingDown)
		err = requireReserved(t, reserved).err
		assert.ErrorIs(t, err, errNoMemory)
		assert.ErrorContains(t, err, "shutting down")
		assert.Equal(t, int64(10), budget.reservedBytes())
	})
}
