package actions

import (
	"math"
	"time"
)

// bucket is a token bucket: up to capacity tokens, refilled continuously at perMin per minute.
// In any interval of length W it hands out at most capacity + perMin·W/1min tokens.
type bucket struct {
	tokens   float64
	capacity float64
	perMin   float64
	at       time.Time // last refill
}

func newBucket(capacity, perMin float64, now time.Time) bucket {
	return bucket{tokens: capacity, capacity: capacity, perMin: perMin, at: now}
}

func (b *bucket) refill(now time.Time) {
	if now.After(b.at) {
		b.tokens = math.Min(b.capacity, b.tokens+now.Sub(b.at).Minutes()*b.perMin)
		b.at = now
	}
}

// set changes the capacity and rate from now on; tokens above the new capacity are dropped.
func (b *bucket) set(capacity, perMin float64, now time.Time) {
	b.refill(now)
	b.capacity, b.perMin = capacity, perMin
	b.tokens = math.Min(b.tokens, capacity)
}

// wait returns how long until a token is available (zero if one is).
func (b *bucket) wait(now time.Time) time.Duration {
	b.refill(now)
	if b.tokens >= 1 {
		return 0
	}
	if b.perMin <= 0 {
		return time.Duration(math.MaxInt64)
	}
	return time.Duration(math.Ceil((1 - b.tokens) / b.perMin * float64(time.Minute)))
}

// take uses a token if one is available.
func (b *bucket) take(now time.Time) bool {
	if b.wait(now) > 0 {
		return false
	}
	b.tokens--
	return true
}

// window counts events in a sliding window: at most limit in any span of length span.
type window struct {
	limit int
	span  time.Duration
	at    []time.Time
}

func (w *window) prune(now time.Time) {
	n := 0
	for n < len(w.at) && !w.at[n].After(now.Add(-w.span)) {
		n++
	}
	w.at = w.at[n:]
}

// wait returns how long until another event fits in the window (zero if one does).
func (w *window) wait(now time.Time) time.Duration {
	w.prune(now)
	if len(w.at) < w.limit {
		return 0
	}
	return w.at[len(w.at)-w.limit].Add(w.span).Sub(now) + time.Nanosecond
}

func (w *window) add(now time.Time) { w.at = append(w.at, now) }

// backoff is the full-jitter delay after the failures-th failed attempt (failures ≥ 1):
// a uniform random 0–min(2^(failures−1)·base, max) (DESIGN.md §10.5).
func backoff(failures int, base, maxDelay time.Duration, rnd func() float64) time.Duration {
	ceiling := maxDelay
	if failures-1 < 30 {
		if d := base << (failures - 1); d > 0 && d < maxDelay {
			ceiling = d
		}
	}
	return time.Duration(rnd() * float64(ceiling))
}
