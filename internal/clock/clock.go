package clock

import (
	"context"
	"slices"
	"sort"
	"sync"
	"time"
)

// Clock is the source of time for code that must be testable.
type Clock interface {
	Now() time.Time
	// After returns a channel that receives the time once d has passed.
	After(d time.Duration) <-chan time.Time
	// Sleep waits for d or until ctx is done, whichever comes first, and returns ctx.Err() if
	// the context ended the wait.
	Sleep(ctx context.Context, d time.Duration) error
}

// Real is the Clock backed by the time package. Inside a testing/synctest bubble it runs on
// the bubble's virtual time.
type Real struct{}

var _ Clock = Real{}

// Now returns the current time.
func (Real) Now() time.Time { return time.Now() }

// After waits for d and then sends the current time on the returned channel.
func (Real) After(d time.Duration) <-chan time.Time { return time.After(d) }

// Sleep waits for d or until ctx is done.
func (Real) Sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Fake is a Clock that only moves when Advance is called. It's safe for concurrent use.
type Fake struct {
	mu      sync.Mutex
	now     time.Time
	waiters []waiter
}

type waiter struct {
	at time.Time
	ch chan time.Time
}

var _ Clock = (*Fake)(nil)

// NewFake returns a fake clock set to start.
func NewFake(start time.Time) *Fake { return &Fake{now: start} }

// Now returns the fake's current time.
func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

// After returns a channel that receives the fake time once Advance has moved it by d.
// A non-positive d fires at once.
func (f *Fake) After(d time.Duration) <-chan time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	ch := make(chan time.Time, 1)
	if d <= 0 {
		ch <- f.now
		return ch
	}
	f.waiters = append(f.waiters, waiter{at: f.now.Add(d), ch: ch})
	return ch
}

// Sleep waits until Advance has moved the fake time by d, or until ctx is done.
func (f *Fake) Sleep(ctx context.Context, d time.Duration) error {
	ch := f.After(d)
	select {
	case <-ctx.Done():
		f.remove(ch)
		return ctx.Err()
	case <-ch:
		return nil
	}
}

// remove forgets a waiter that nobody listens to anymore, so Waiters stays accurate.
func (f *Fake) remove(ch <-chan time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.waiters = slices.DeleteFunc(f.waiters, func(w waiter) bool { return w.ch == ch })
}

// Advance moves the fake time forward by d and fires every waiter that is now due, earliest first.
func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
	sort.SliceStable(f.waiters, func(i, j int) bool { return f.waiters[i].at.Before(f.waiters[j].at) })
	n := 0
	for n < len(f.waiters) && !f.waiters[n].at.After(f.now) {
		f.waiters[n].ch <- f.now
		n++
	}
	f.waiters = f.waiters[n:]
}

// Waiters returns how many After or Sleep calls are still waiting, so a test can tell when a
// goroutine has blocked on the clock before advancing it.
func (f *Fake) Waiters() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.waiters)
}
