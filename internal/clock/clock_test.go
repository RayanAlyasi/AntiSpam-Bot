package clock

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

var start = time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)

func TestFakeAdvanceFiresDueWaitersInOrder(t *testing.T) {
	f := NewFake(start)
	late := f.After(2 * time.Second)
	early := f.After(time.Second)
	if f.Waiters() != 2 {
		t.Fatalf("Waiters() = %d, want 2", f.Waiters())
	}

	f.Advance(time.Second)
	select {
	case got := <-early:
		if !got.Equal(start.Add(time.Second)) {
			t.Fatalf("early fired with %v, want %v", got, start.Add(time.Second))
		}
	default:
		t.Fatal("waiter due at +1s didn't fire after Advance(1s)")
	}
	select {
	case <-late:
		t.Fatal("waiter due at +2s fired after only 1s")
	default:
	}

	f.Advance(time.Second)
	if got := <-late; !got.Equal(start.Add(2 * time.Second)) {
		t.Fatalf("late fired with %v, want %v", got, start.Add(2*time.Second))
	}
	if f.Waiters() != 0 {
		t.Fatalf("Waiters() = %d after all fired, want 0", f.Waiters())
	}
}

func TestFakeAfterNonPositiveFiresAtOnce(t *testing.T) {
	f := NewFake(start)
	select {
	case got := <-f.After(0):
		if !got.Equal(start) {
			t.Fatalf("got %v, want %v", got, start)
		}
	default:
		t.Fatal("After(0) didn't fire at once")
	}
}

func TestFakeSleep(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := NewFake(start)
		done := make(chan error, 1)
		go func() { done <- f.Sleep(context.Background(), time.Minute) }()
		synctest.Wait() // the goroutine is now blocked on the fake clock
		if f.Waiters() != 1 {
			t.Fatalf("Waiters() = %d, want 1 sleeping goroutine", f.Waiters())
		}
		f.Advance(time.Minute)
		if err := <-done; err != nil {
			t.Fatalf("Sleep returned %v, want nil", err)
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f := NewFake(start)
	if err := f.Sleep(ctx, time.Minute); !errors.Is(err, context.Canceled) {
		t.Fatalf("Sleep with a cancelled context returned %v, want context.Canceled", err)
	}
	if f.Waiters() != 0 {
		t.Fatalf("Waiters() = %d after a cancelled Sleep, want 0 (tests use it to know a goroutine is blocked)", f.Waiters())
	}
}

func TestRealRunsOnSynctestTime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var c Clock = Real{}
		before := c.Now()
		if err := c.Sleep(context.Background(), time.Hour); err != nil {
			t.Fatalf("Sleep returned %v", err)
		}
		if got := c.Now().Sub(before); got != time.Hour {
			t.Fatalf("an hour's Sleep moved the clock by %v, want exactly 1h (virtual time)", got)
		}
		<-c.After(time.Minute)
		if got := c.Now().Sub(before); got != time.Hour+time.Minute {
			t.Fatalf("clock moved by %v, want 1h1m", got)
		}

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := c.Sleep(ctx, time.Hour); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Sleep past the context deadline returned %v, want context.DeadlineExceeded", err)
		}
	})
}
