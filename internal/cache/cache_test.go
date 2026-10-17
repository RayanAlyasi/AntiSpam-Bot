package cache

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
)

var t0 = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

func newCache(t *testing.T, opts Options) (*Cache[string, int], *clock.Fake) {
	t.Helper()
	clk := clock.NewFake(t0)
	opts.Clock = clk
	c, err := New[string, int](opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return c, clk
}

func TestNewRejectsBadOptions(t *testing.T) {
	for _, opts := range []Options{{}, {MaxSize: -1}, {MaxSize: 10, TTL: -time.Second}} {
		if _, err := New[string, int](opts); err == nil {
			t.Errorf("New(%+v): got nil error, want one (a cache must be bounded and have a sane TTL)", opts)
		}
	}
}

func TestTTL(t *testing.T) {
	tests := []struct {
		name    string
		ttl     time.Duration
		advance time.Duration
		want    bool
	}{
		{"fresh entry is served", time.Minute, 59 * time.Second, true},
		{"entry expires at its TTL", time.Minute, time.Minute, false},
		{"zero TTL keeps entries", 0, 365 * 24 * time.Hour, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, clk := newCache(t, Options{MaxSize: 10, TTL: tt.ttl})
			c.Set("k", 1)
			clk.Advance(tt.advance)
			if _, ok := c.Get("k"); ok != tt.want {
				t.Errorf("Get after %s with TTL %s: present=%v, want %v", tt.advance, tt.ttl, ok, tt.want)
			}
		})
	}
}

func TestGetWithAgeAndSetRestartsAge(t *testing.T) {
	c, clk := newCache(t, Options{MaxSize: 10})
	c.Set("k", 1)
	clk.Advance(7 * time.Minute)
	if _, age, ok := c.GetWithAge("k"); !ok || age != 7*time.Minute {
		t.Fatalf("GetWithAge: age=%s ok=%v, want 7m true (callers force refreshes by age)", age, ok)
	}
	c.Set("k", 2)
	if v, age, _ := c.GetWithAge("k"); v != 2 || age != 0 {
		t.Errorf("after Set: v=%d age=%s, want 2 and 0 (a write is fresh data)", v, age)
	}
}

func TestUpdateKeepsAgeAndCanRemove(t *testing.T) {
	c, clk := newCache(t, Options{MaxSize: 10})
	c.Update("k", func(old int, found bool) (int, bool) {
		if found {
			t.Error("Update on empty cache: found=true, want false")
		}
		return 5, true
	})
	clk.Advance(time.Minute)
	c.Update("k", func(old int, found bool) (int, bool) { return old + 1, true })
	if v, age, ok := c.GetWithAge("k"); !ok || v != 6 || age != time.Minute {
		t.Errorf("after in-place Update: v=%d age=%s ok=%v, want 6 1m true (an incremental update doesn't refresh the snapshot)", v, age, ok)
	}
	c.Update("k", func(int, bool) (int, bool) { return 0, false })
	if _, ok := c.Get("k"); ok {
		t.Error("Update returning keep=false: entry still present, want removed")
	}
}

func TestInvalidate(t *testing.T) {
	c, _ := newCache(t, Options{MaxSize: 10})
	c.Set("a", 1)
	c.Set("b", 2)
	c.Invalidate("a")
	if _, ok := c.Get("a"); ok {
		t.Error("Invalidate(a): a still present")
	}
	if _, ok := c.Get("b"); !ok {
		t.Error("Invalidate(a): b gone, want only a removed")
	}
	c.InvalidateAll()
	if _, ok := c.Get("b"); ok {
		t.Error("InvalidateAll: b still present")
	}
}

func TestLoadIsSingleFlightAndDoesNotCacheErrors(t *testing.T) {
	synctest.Test(t, testLoad)
}

func testLoad(t *testing.T) {
	c, _ := newCache(t, Options{MaxSize: 10})
	var calls atomic.Int32
	release := make(chan struct{})
	load := func(context.Context, string) (int, error) {
		calls.Add(1)
		<-release
		return 42, nil
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if v, err := c.Load(t.Context(), "k", load); err != nil || v != 42 {
				t.Errorf("Load: %d, %v; want 42, nil", v, err)
			}
		})
	}
	synctest.Wait() // every loader is now blocked on the same key
	close(release)
	wg.Wait()
	if n := calls.Load(); n != 1 {
		t.Errorf("loader ran %d times for one key, want 1 (concurrent misses must share one load)", n)
	}

	boom := errors.New("boom")
	if _, err := c.Load(t.Context(), "e", func(context.Context, string) (int, error) { return 0, boom }); !errors.Is(err, boom) {
		t.Fatalf("Load error: got %v, want boom", err)
	}
	if _, ok := c.Get("e"); ok {
		t.Error("failed load was cached, want errors never cached")
	}
}

func TestSizeBound(t *testing.T) {
	c, _ := newCache(t, Options{MaxSize: 50})
	for i := range 1000 {
		c.Set(string(rune('a'+i%26))+string(rune(i)), i)
	}
	c.c.CleanUp() // run otter's pending eviction now
	if n := c.Len(); n > 50 {
		t.Errorf("Len = %d after 1000 inserts, want ≤ 50 (caches must stay bounded)", n)
	}
}
