// Package normtest has test helpers shared by the normalize packages.
package normtest

import (
	"testing"
	"time"
)

// Linear fails t if f takes more than 10 times as long on input(4) as on input(1), where input(k)
// builds an input k times the base size. Comparing two runs on the same machine, instead of checking
// a wall-clock limit, keeps the check stable when the machine is busy: linear work gives a ratio near
// 4, quadratic work near 16.
func Linear(t testing.TB, name string, input func(k int) string, f func(string)) {
	t.Helper()
	small, large := fastest(f, input(1)), fastest(f, input(4))
	if ratio := float64(large) / float64(max(small, time.Microsecond)); ratio > 10 {
		t.Errorf("%s: 4x the input took %.1fx the time (%v vs %v), want linear growth", name, ratio, large, small)
	}
}

// fastest returns the quickest of three runs, which filters out scheduler and GC pauses.
func fastest(f func(string), in string) time.Duration {
	var best time.Duration
	for i := range 3 {
		start := time.Now()
		f(in)
		if d := time.Since(start); i == 0 || d < best {
			best = d
		}
	}
	return best
}
