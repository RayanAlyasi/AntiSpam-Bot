package budget

import (
	"fmt"
	"sync"
	"testing"

	"pgregory.net/rapid"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
)

// Property: racing callers get exactly as many calls as the budgets allow, no fewer:
// min(Σ min(demand_g, group limit), instance limits).
func TestPropertyReservationsGrantExactlyTheCapacity(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		lim := Limits{
			GroupPerDay:      rapid.IntRange(0, 10).Draw(rt, "group"),
			InstancePerDay:   rapid.IntRange(0, 20).Draw(rt, "instance_day"),
			InstancePerMonth: rapid.IntRange(0, 25).Draw(rt, "instance_month"),
		}
		groups := rapid.IntRange(1, 4).Draw(rt, "groups")
		callers := rapid.IntRange(0, 40).Draw(rt, "callers")
		b, err := New(NewMemStore(), clock.NewFake(start), lim, nil, nil)
		if err != nil {
			rt.Fatal(err)
		}
		var mu sync.Mutex
		granted := 0
		var wg sync.WaitGroup
		demand := make([]int, groups)
		for i := range callers {
			demand[i%groups]++
			wg.Go(func() {
				d, err := b.Allow(t.Context(), fmt.Sprint("g", i%groups))
				if err != nil {
					rt.Error(err)
				}
				if d.Allowed {
					mu.Lock()
					granted++
					mu.Unlock()
				}
			})
		}
		wg.Wait()
		want := 0
		for _, n := range demand {
			if lim.GroupPerDay > 0 {
				n = min(n, lim.GroupPerDay)
			}
			want += n
		}
		for _, l := range []int{lim.InstancePerDay, lim.InstancePerMonth} {
			if l > 0 {
				want = min(want, l)
			}
		}
		if granted != want {
			rt.Fatalf("granted %d calls, want exactly %d for limits %+v and demand %v", granted, want, lim, demand)
		}
	})
}
