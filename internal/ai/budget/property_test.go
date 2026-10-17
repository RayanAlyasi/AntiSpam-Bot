package budget

import (
	"fmt"
	"sync"
	"testing"

	"pgregory.net/rapid"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
)

// Property: however many callers race, for any limits and groups, no window ends past its limit
// and each budget alerts at most once.
func TestPropertyReservationsNeverExceedLimits(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		lim := Limits{
			GroupPerDay:      rapid.IntRange(0, 12).Draw(rt, "group"),
			InstancePerDay:   rapid.IntRange(0, 20).Draw(rt, "instance_day"),
			InstancePerMonth: rapid.IntRange(0, 25).Draw(rt, "instance_month"),
		}
		groups := rapid.IntRange(1, 4).Draw(rt, "groups")
		callers := rapid.IntRange(1, 40).Draw(rt, "callers")
		st, al := NewMemStore(), &alerts{}
		b, err := New(st, clock.NewFake(start), lim, al, nil)
		if err != nil {
			rt.Fatal(err)
		}
		var wg sync.WaitGroup
		for i := range callers {
			wg.Go(func() {
				if _, err := b.Allow(t.Context(), fmt.Sprint("g", i%groups)); err != nil {
					rt.Error(err)
				}
			})
		}
		wg.Wait()
		total := 0
		for g := range groups {
			n := st.Get(start, fmt.Sprint("g", g)).Calls
			if lim.GroupPerDay > 0 && n > lim.GroupPerDay {
				rt.Fatalf("group g%d has %d calls, limit %d", g, n, lim.GroupPerDay)
			}
			total += n
		}
		for _, l := range []int{lim.InstancePerDay, lim.InstancePerMonth} {
			if l > 0 && total > l {
				rt.Fatalf("instance has %d calls, limit %d", total, l)
			}
		}
		seen := map[string]bool{}
		for _, k := range al.got() {
			if seen[k] {
				rt.Fatalf("alert %s raised twice", k)
			}
			seen[k] = true
		}
	})
}

func TestAllowLimitUsesTheGroupsOwnLimit(t *testing.T) {
	b, _, _, _ := newBudget(t, Limits{GroupPerDay: 200, InstancePerDay: 1000})
	ok := 0
	for range 5 {
		if d, _ := b.AllowLimit(t.Context(), "small", 3); d.Allowed {
			ok++
		}
	}
	if ok != 3 {
		t.Fatalf("allowed %d, want the group's own limit of 3", ok)
	}
	if d, _ := b.AllowLimit(t.Context(), "other", 0); !d.Allowed {
		t.Fatal("limit 0 must fall back to the instance default")
	}
}
