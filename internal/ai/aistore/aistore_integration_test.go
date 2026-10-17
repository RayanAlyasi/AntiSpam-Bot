//go:build integration

package aistore_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai/aistore"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai/budget"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai/cache"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store/storetest"
)

var t0 = time.Date(2026, 10, 31, 23, 30, 0, 0, time.UTC)

type alerts struct {
	mu   sync.Mutex
	keys []string
}

func (a *alerts) Alert(_ context.Context, key, _ string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.keys = append(a.keys, key)
}

func TestBudgetReservationsAreAtomic(t *testing.T) {
	pool := storetest.Pool(t)
	st := aistore.NewBudget(pool)
	al := &alerts{}
	b, err := budget.New(st, clock.NewFake(t0), budget.Limits{GroupPerDay: 7, InstancePerDay: 10}, al, nil)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	granted := 0
	for i := range 40 {
		wg.Go(func() {
			d, err := b.Allow(context.Background(), fmt.Sprint("g", i%2, "@g.us"))
			if err != nil {
				t.Error(err)
				return
			}
			if d.Allowed {
				mu.Lock()
				granted++
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	g0, _ := st.Row(context.Background(), t0, "g0@g.us")
	g1, _ := st.Row(context.Background(), t0, "g1@g.us")
	if granted != 10 || g0.Calls+g1.Calls != 10 || g0.Calls > 7 || g1.Calls > 7 {
		t.Fatalf("granted %d (g0 %d, g1 %d), want exactly 10 with neither group past 7 (concurrent callers never pass a budget)", granted, g0.Calls, g1.Calls)
	}
	seen := map[string]bool{}
	for _, k := range al.keys {
		if seen[k] {
			t.Fatalf("alert %s raised twice", k)
		}
		seen[k] = true
	}
	if !seen["ai_budget:instance_day:2026-10-31"] {
		t.Errorf("alerts = %v, want the instance 80%% alert", al.keys)
	}
}

func TestBudgetWindowsAreUTCDaysAndMonths(t *testing.T) {
	pool := storetest.Pool(t)
	st := aistore.NewBudget(pool)
	clk := clock.NewFake(t0) // 23:30 UTC on the last day of October
	b, _ := budget.New(st, clk, budget.Limits{GroupPerDay: 1, InstancePerMonth: 2}, nil, nil)
	allow := func() bool {
		d, err := b.Allow(context.Background(), "g@g.us")
		if err != nil {
			t.Fatal(err)
		}
		return d.Allowed
	}
	if !allow() || allow() {
		t.Fatal("want one call on 31 October")
	}
	clk.Advance(time.Hour) // 00:30 UTC on 1 November: new day and new month
	if !allow() || allow() {
		t.Fatal("want a fresh day and month in November")
	}
	if err := b.Record(context.Background(), "g@g.us", budget.Usage{InputTokens: 900, OutputTokens: 40, CostMicros: 77}); err != nil {
		t.Fatal(err)
	}
	r, err := st.Row(context.Background(), clk.Now(), "g@g.us")
	if err != nil || r.Calls != 1 || r.InputTokens != 900 || r.OutputTokens != 40 || r.CostMicros != 77 {
		t.Fatalf("November row = %+v, %v; want 1 call and the recorded tokens and cost", r, err)
	}
}

func TestCacheStoreRoundTripExpiryAndPurge(t *testing.T) {
	pool := storetest.Pool(t)
	st := aistore.NewCache(pool)
	clk := clock.NewFake(t0)
	c, err := cache.New(st, clk, cache.Options[ai.Verdict]{Definitive: func(v ai.Verdict) bool { return v.Label != ai.LabelUnsure }})
	if err != nil {
		t.Fatal(err)
	}
	req := cache.Request{Key: cache.Key("free btc", "none", "m", ai.PromptVersion), Model: "m", PromptVersion: ai.PromptVersion}
	calls := 0
	classify := func(context.Context) (ai.Verdict, error) {
		calls++
		return ai.Verdict{Label: ai.LabelSpam, Confidence: 0.97, Categories: []string{"crypto"}, Reason: "r"}, nil
	}
	if _, src, err := c.Do(context.Background(), req, classify); err != nil || src != cache.SourceFresh {
		t.Fatalf("first Do = %v, %v", src, err)
	}
	v, src, err := c.Do(context.Background(), req, classify)
	if err != nil || src != cache.SourceCached || calls != 1 || v.Label != ai.LabelSpam {
		t.Fatalf("second Do = %+v, %v, %v after %d calls; want a cache hit from Postgres", v, src, err, calls)
	}
	e, ok, err := st.Get(context.Background(), req.Key, time.Time{})
	if err != nil || !ok || e.Model != "m" || e.PromptVersion != ai.PromptVersion {
		t.Fatalf("stored entry = %+v, %v, %v", e, ok, err)
	}
	var stored ai.Verdict
	if err := json.Unmarshal(e.Result, &stored); err != nil || stored.Label != ai.LabelSpam {
		t.Fatalf("stored result %s: %v", e.Result, err)
	}

	clk.Advance(cache.DefaultTTL + time.Minute)
	if _, ok, _ := st.Get(context.Background(), req.Key, clk.Now().Add(-cache.DefaultTTL)); ok {
		t.Fatal("an entry older than 30 days was served")
	}
	n, err := st.Purge(context.Background(), clk.Now().Add(-cache.DefaultTTL))
	if err != nil || n != 1 {
		t.Fatalf("Purge = %d, %v; want the expired entry removed", n, err)
	}
}
