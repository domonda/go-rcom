package rcom

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"sync"
)

// memoryBudget limits the bytes that requests reserve together.
// Waiting reservations are granted in the order they were made,
// so that a large request is not starved by smaller ones.
// A nil *memoryBudget means no limit.
type memoryBudget struct {
	size    int64
	mu      sync.Mutex
	used    int64
	waiters list.List // Of *budgetWaiter
}

type budgetWaiter struct {
	n        int64
	reserved chan struct{} // Closed when n bytes have been reserved
}

// newMemoryBudget returns a budget of size bytes,
// or nil for no limit if size is zero or negative
func newMemoryBudget(size int64) *memoryBudget {
	if size <= 0 {
		return nil
	}
	return &memoryBudget{size: size}
}

// errNoMemory is returned by memoryBudget.reserve if the request
// was canceled or the server shut down while waiting for memory
var errNoMemory = errors.New("no free memory for the request")

// reserve reserves n bytes of the budget, at most its whole size,
// and returns the function that releases them, which can be called
// more than once. It waits until the bytes are free and returns
// errNoMemory with the cause if ctx ends or shuttingDown is closed first.
// Like service.acquire, it reserves free bytes also while
// shutting down.
func (b *memoryBudget) reserve(ctx context.Context, shuttingDown <-chan struct{}, n int64) (release func(), err error) {
	if b == nil {
		return func() {}, nil
	}
	n = max(min(n, b.size), 0)

	b.mu.Lock()
	if b.waiters.Len() == 0 && b.used+n <= b.size {
		b.used += n
		b.mu.Unlock()
		return b.releaseFunc(n), nil
	}
	waiter := &budgetWaiter{n: n, reserved: make(chan struct{})}
	elem := b.waiters.PushBack(waiter)
	b.mu.Unlock()

	select {
	case <-waiter.reserved:
		return b.releaseFunc(n), nil
	case <-ctx.Done():
		err = fmt.Errorf("%w: %w", errNoMemory, context.Cause(ctx))
	case <-shuttingDown:
		err = fmt.Errorf("%w: server is shutting down", errNoMemory)
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	select {
	case <-waiter.reserved:
		// Reserved after ctx ended or the shutdown started
		b.used -= n
	default:
		b.waiters.Remove(elem)
	}
	// The waiters behind this one may fit now
	b.grantWaiters()
	return nil, err
}

func (b *memoryBudget) releaseFunc(n int64) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			b.mu.Lock()
			defer b.mu.Unlock()
			b.used -= n
			b.grantWaiters()
		})
	}
}

// grantWaiters reserves the bytes of the waiters
// in their order as long as they fit, b.mu must be locked
func (b *memoryBudget) grantWaiters() {
	for front := b.waiters.Front(); front != nil; front = b.waiters.Front() {
		waiter := front.Value.(*budgetWaiter)
		if b.used+waiter.n > b.size {
			return
		}
		b.used += waiter.n
		b.waiters.Remove(front)
		close(waiter.reserved)
	}
}
