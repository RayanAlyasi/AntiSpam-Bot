package actions

import (
	"math/rand/v2"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)

// TestBucketNeverExceedsCapacityPlusRate drives the bucket with random demand and checks the
// token-bucket bound behind the write ceiling: in any interval of length W at most
// capacity + perMin·W tokens are handed out (DESIGN.md §10.3).
func TestBucketNeverExceedsCapacityPlusRate(t *testing.T) {
	for seed := range uint64(50) {
		rnd := rand.New(rand.NewPCG(seed, 7))
		capacity, perMin := float64(1+rnd.IntN(12)), float64(1+rnd.IntN(40))
		b := newBucket(capacity, perMin, t0)
		var taken []time.Time
		now := t0
		for range 2000 {
			now = now.Add(time.Duration(rnd.IntN(3000)) * time.Millisecond)
			if rnd.IntN(10) == 0 { // a slow-down or recovery changes the rate on the way
				perMin = float64(1 + rnd.IntN(int(perMin)))
				b.set(min(capacity, perMin), perMin, now)
			}
			if b.take(now) {
				taken = append(taken, now)
			}
		}
		for _, span := range []time.Duration{time.Second, 30 * time.Second, time.Minute, 10 * time.Minute} {
			j := 0
			for i := range taken {
				for taken[i].Sub(taken[j]) >= span {
					j++
				}
				// The rate only falls below its starting value, so the starting rate bounds it.
				if limit := capacity + float64(40)*span.Minutes() + 1; float64(i-j+1) > limit {
					t.Fatalf("seed %d: %d tokens within %s, want at most %g", seed, i-j+1, span, limit)
				}
			}
		}
	}
}

func TestBucketWait(t *testing.T) {
	b := newBucket(2, 20, t0) // a token every 3 s
	for i := range 2 {
		if !b.take(t0) {
			t.Fatalf("take %d from a full bucket of 2 failed", i)
		}
	}
	if d := b.wait(t0); d != 3*time.Second {
		t.Errorf("wait on an empty 20/min bucket = %s, want 3s", d)
	}
	if b.take(t0.Add(2900 * time.Millisecond)) {
		t.Error("took a token after 2.9 s, want none before 3 s")
	}
	if !b.take(t0.Add(3 * time.Second)) {
		t.Error("no token after 3 s, want one")
	}
	b.set(1, 2, t0.Add(3*time.Second))
	if d := b.wait(t0.Add(3 * time.Second)); d != 30*time.Second {
		t.Errorf("wait after slowing to 2/min = %s, want 30s", d)
	}
}

func TestWindow(t *testing.T) {
	w := window{limit: 3, span: time.Minute}
	for i := range 3 {
		if d := w.wait(t0.Add(time.Duration(i) * time.Second)); d != 0 {
			t.Fatalf("event %d waited %s, want room in the window", i, d)
		}
		w.add(t0.Add(time.Duration(i) * time.Second))
	}
	d := w.wait(t0.Add(10 * time.Second))
	if want := 50*time.Second + time.Nanosecond; d != want {
		t.Errorf("wait for a 4th event = %s, want %s (until the first leaves the 60 s window)", d, want)
	}
	if w.wait(t0.Add(10*time.Second+d)) != 0 {
		t.Error("no room once the first event left the window")
	}
}

func TestBackoff(t *testing.T) {
	top := func(float64) func() float64 { return func() float64 { return 0.999999 } }(0)
	cases := []struct {
		failures int
		max      time.Duration
	}{
		{1, 2 * time.Second},
		{2, 4 * time.Second},
		{3, 8 * time.Second},
		{5, 32 * time.Second},
		{8, 256 * time.Second},
		{9, 5 * time.Minute},
		{40, 5 * time.Minute},
		{70, 5 * time.Minute},
	}
	for _, tc := range cases {
		got := backoff(tc.failures, 2*time.Second, 5*time.Minute, top)
		if got > tc.max || got < tc.max*99/100 {
			t.Errorf("backoff after %d failures at the top of the jitter = %s, want just under %s (min(2^n·2 s, 5 min))", tc.failures, got, tc.max)
		}
		if zero := backoff(tc.failures, 2*time.Second, 5*time.Minute, func() float64 { return 0 }); zero != 0 {
			t.Errorf("backoff with zero jitter = %s, want 0 (full jitter starts at 0)", zero)
		}
	}
}
