package budget

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai/llm"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
)

type alerts struct {
	mu   sync.Mutex
	keys []string
}

func (a *alerts) Alert(_ context.Context, key, _ string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.keys = append(a.keys, key)
}

func (a *alerts) got() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.keys...)
}

var start = time.Date(2026, 10, 17, 23, 0, 0, 0, time.FixedZone("UTC+3", 3*3600)) // 20:00 UTC

func newBudget(t *testing.T, lim Limits) (*Budget, *MemStore, *clock.Fake, *alerts) {
	t.Helper()
	st, clk, al := NewMemStore(), clock.NewFake(start), &alerts{}
	b, err := New(st, clk, lim, al, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return b, st, clk, al
}

func TestAllowEnforcesEachLimit(t *testing.T) {
	tests := []struct {
		name      string
		lim       Limits
		calls     map[string]int // group → calls attempted, in order a, b
		wantOK    int
		exhausted Scope
	}{
		{"group per day", Limits{GroupPerDay: 3}, map[string]int{"a": 5}, 3, ScopeGroupDay},
		{"other group unaffected", Limits{GroupPerDay: 3}, map[string]int{"a": 3, "b": 3}, 6, ""},
		{"instance per day", Limits{GroupPerDay: 10, InstancePerDay: 4}, map[string]int{"a": 3, "b": 3}, 4, ScopeInstanceDay},
		{"instance per month", Limits{InstancePerMonth: 2}, map[string]int{"a": 1, "b": 2}, 2, ScopeInstanceMonth},
		{"unlimited", Limits{}, map[string]int{"a": 50}, 50, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, _, _, _ := newBudget(t, tt.lim)
			ok, last := 0, Decision{}
			for _, g := range []string{"a", "b"} {
				for range tt.calls[g] {
					d, err := b.Allow(t.Context(), g)
					if err != nil {
						t.Fatal(err)
					}
					if d.Allowed {
						ok++
					} else {
						last = d
					}
				}
			}
			if ok != tt.wantOK {
				t.Errorf("allowed %d calls, want %d (an exhausted budget sends items to admins)", ok, tt.wantOK)
			}
			if last.Exhausted != tt.exhausted {
				t.Errorf("exhausted scope = %q, want %q", last.Exhausted, tt.exhausted)
			}
		})
	}
}

func TestWindowsAreUTCDaysAndMonths(t *testing.T) {
	b, st, clk, _ := newBudget(t, Limits{GroupPerDay: 1, InstancePerMonth: 3})
	allow := func() bool {
		d, err := b.Allow(t.Context(), "g")
		if err != nil {
			t.Fatal(err)
		}
		return d.Allowed
	}
	if !allow() || allow() {
		t.Fatal("want exactly one call on the first UTC day")
	}
	if got := st.Get(start, "g").Calls; got != 1 {
		t.Fatalf("row for %s has %d calls, want 1 (counted on the UTC day 2026-10-17)", Day(start).Format(time.DateOnly), got)
	}
	clk.Advance(4 * time.Hour) // 00:00 UTC on 2026-10-18: a new group day, same month
	if !allow() {
		t.Fatal("want a fresh group budget on the next UTC day")
	}
	clk.Advance(24 * time.Hour)
	if !allow() || func() bool { clk.Advance(24 * time.Hour); return allow() }() {
		t.Fatal("want the monthly cap of 3 to hold across days")
	}
	clk.Advance(15 * 24 * time.Hour) // November
	if !allow() {
		t.Fatal("want a fresh monthly budget in the next UTC month")
	}
}

func TestAllowIsAtomicUnderConcurrency(t *testing.T) {
	for _, lim := range []Limits{{GroupPerDay: 7}, {InstancePerDay: 11}, {GroupPerDay: 100, InstancePerMonth: 5}} {
		b, st, _, al := newBudget(t, lim)
		var wg sync.WaitGroup
		var mu sync.Mutex
		ok := 0
		for i := range 64 {
			g := []string{"a", "b"}[i%2]
			wg.Go(func() {
				d, err := b.Allow(t.Context(), g)
				if err != nil {
					t.Error(err)
				}
				if d.Allowed {
					mu.Lock()
					ok++
					mu.Unlock()
				}
			})
		}
		wg.Wait()
		want := map[Limits]int{{GroupPerDay: 7}: 14, {InstancePerDay: 11}: 11, {GroupPerDay: 100, InstancePerMonth: 5}: 5}[lim]
		if ok != want || st.Get(start, "a").Calls+st.Get(start, "b").Calls != want {
			t.Errorf("limits %+v: allowed %d, want %d (concurrent callers must never exceed a budget)", lim, ok, want)
		}
		if n := len(al.got()); n > 3 {
			t.Errorf("limits %+v: %d alerts, want at most one per budget window", lim, n)
		}
	}
}

func TestAlertOnceAt80Percent(t *testing.T) {
	b, _, clk, al := newBudget(t, Limits{GroupPerDay: 10, InstancePerDay: 20, InstancePerMonth: 100})
	for range 12 {
		if _, err := b.Allow(t.Context(), "g1"); err != nil {
			t.Fatal(err)
		}
	}
	if got := al.got(); len(got) != 1 || got[0] != "ai_budget:group_day:2026-10-17:g1" {
		t.Fatalf("alerts = %v, want one group alert at the 8th of 10 calls", got)
	}
	for range 10 {
		_, _ = b.Allow(t.Context(), "g2")
	}
	got := al.got()
	if len(got) != 3 || got[1] != "ai_budget:instance_day:2026-10-17" || got[2] != "ai_budget:group_day:2026-10-17:g2" {
		t.Fatalf("alerts = %v, want the instance day alert at 16 of 20, then g2's group alert at 8 of 10", got)
	}
	// A restarted process sees the same counts and must not alert again for this window.
	b2, err := New(b.store, clk, b.limits, al, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = b2.Allow(t.Context(), "g3")
	if n := len(al.got()); n != 3 {
		t.Fatalf("alerts after restart = %d, want still 3 (one alert per budget per window)", n)
	}
}

func TestCrosses80(t *testing.T) {
	for limit := 1; limit <= 300; limit++ {
		n := 0
		for used := 1; used <= limit; used++ {
			if crosses80(used, limit) {
				n++
				if used*5 < limit*4 || (used-1)*5 >= limit*4 {
					t.Fatalf("crosses80(%d, %d) fired off the 80%% line", used, limit)
				}
			}
		}
		if n != 1 {
			t.Fatalf("limit %d: crossed %d times, want exactly once", limit, n)
		}
	}
}

func TestRecordAddsUsage(t *testing.T) {
	b, st, _, _ := newBudget(t, Limits{})
	p := Prices{InputPerMTok: 1_000_000, OutputPerMTok: 5_000_000, CacheReadPerMTok: 100_000}
	u := p.Usage(llm.Usage{InputTokens: 100, OutputTokens: 30, CacheReadTokens: 900})
	if u != (Usage{InputTokens: 1000, OutputTokens: 30, CostMicros: 340}) {
		t.Fatalf("Prices.Usage() = %+v, want 1000 input, 30 output, 340 micros", u)
	}
	if err := b.Record(t.Context(), "g", u); err != nil {
		t.Fatal(err)
	}
	if err := b.Record(t.Context(), "g", u); err != nil {
		t.Fatal(err)
	}
	if r := st.Get(start, "g"); r.InputTokens != 2000 || r.OutputTokens != 60 || r.CostMicros != 680 || r.Calls != 0 {
		t.Fatalf("row = %+v, want tokens and cost summed and no call counted by Record", r)
	}
	if got := (Prices{InputPerMTok: 1}).Usage(llm.Usage{InputTokens: 1}).CostMicros; got != 1 {
		t.Fatalf("tiny cost = %d micros, want it rounded up to 1", got)
	}
}

type failStore struct{ *MemStore }

func (failStore) Reserve(context.Context, time.Time, string, Limits) (Counts, bool, error) {
	return Counts{}, false, errors.New("db down")
}

func TestAllowReturnsStoreErrors(t *testing.T) {
	b, err := New(failStore{NewMemStore()}, clock.NewFake(start), DefaultLimits(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	d, err := b.Allow(t.Context(), "g")
	if err == nil || d.Allowed {
		t.Fatalf("Allow() = %+v, %v; want an error and no reservation (caller asks admins)", d, err)
	}
}

func TestNewValidates(t *testing.T) {
	if _, err := New(NewMemStore(), clock.NewFake(start), Limits{GroupPerDay: -1}, nil, nil); err == nil {
		t.Error("negative limit accepted")
	}
	if _, err := New(nil, clock.NewFake(start), Limits{}, nil, nil); err == nil {
		t.Error("nil store accepted")
	}
	if err := (Prices{OutputPerMTok: -1}).Validate(); err == nil {
		t.Error("negative price accepted")
	}
}
