package cache

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
)

type tv struct {
	Label  string  `json:"label"`
	Conf   float64 `json:"confidence"`
	Reason string  `json:"reason"`
}

func definitive(v tv) bool { return v.Label == "spam" || v.Label == "not_spam" }

var t0 = time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)

func newCache(t *testing.T, mod func(*Options[tv])) (*Cache[tv], *MemStore, *clock.Fake) {
	t.Helper()
	st, clk := NewMemStore(), clock.NewFake(t0)
	o := Options[tv]{Definitive: definitive}
	if mod != nil {
		mod(&o)
	}
	c, err := New(st, clk, o)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return c, st, clk
}

// counter returns a classify func answering v and counting calls.
func counter(v tv, err error) (func(context.Context) (tv, error), *atomic.Int32) {
	var n atomic.Int32
	return func(context.Context) (tv, error) { n.Add(1); return v, err }, &n
}

func req(text, cluster string) Request {
	return Request{Key: Key(text, "ctx0", "model-a", "p1"), Cluster: cluster, Model: "model-a", PromptVersion: "p1"}
}

func TestResultCacheHitAndMiss(t *testing.T) {
	c, st, clk := newCache(t, nil)
	spam := tv{Label: "spam", Conf: 0.97, Reason: "crypto"}
	classify, calls := counter(spam, nil)

	v, src, err := c.Do(t.Context(), req("free btc", ""), classify)
	if err != nil || v != spam || src != SourceFresh {
		t.Fatalf("first Do() = %+v, %v, %v; want the fresh verdict", v, src, err)
	}
	v, src, err = c.Do(t.Context(), req("free btc", ""), classify)
	if err != nil || v != spam || src != SourceCached || calls.Load() != 1 {
		t.Fatalf("second Do() = %+v, %v, %v after %d calls; want a cache hit and one provider call", v, src, err, calls.Load())
	}
	if st.Len() != 1 {
		t.Fatalf("stored entries = %d, want 1", st.Len())
	}
	clk.Advance(DefaultTTL + time.Second)
	if _, src, _ := c.Do(t.Context(), req("free btc", ""), classify); src != SourceFresh || calls.Load() != 2 {
		t.Fatalf("after 30 days: source %v, calls %d; want the entry expired and a new call", src, calls.Load())
	}
}

func TestKeyChangesWithEachPart(t *testing.T) {
	base := Key("text", "ctx", "model", "p1")
	variants := map[string]string{
		"text":           Key("text2", "ctx", "model", "p1"),
		"context":        Key("text", "ctx2", "model", "p1"),
		"model":          Key("text", "ctx", "model-b", "p1"),
		"prompt version": Key("text", "ctx", "model", "p2"),
		"field split":    Key("tex", "tctx", "model", "p1"),
	}
	for name, k := range variants {
		if k == base {
			t.Errorf("changing the %s kept the key %s; a verdict must not be reused across it", name, k)
		}
	}
	if len(base) != 64 {
		t.Errorf("Key() = %q, want hex sha256", base)
	}
}

func FuzzKeyInjective(f *testing.F) {
	f.Add("a", "b", "c", "d", "ab", "", "c", "d")
	f.Fuzz(func(t *testing.T, a, b, c, d, a2, b2, c2, d2 string) {
		same := a == a2 && b == b2 && c == c2 && d == d2
		if (Key(a, b, c, d) == Key(a2, b2, c2, d2)) != same {
			t.Fatalf("Key collision or mismatch for %q vs %q", []string{a, b, c, d}, []string{a2, b2, c2, d2})
		}
	})
}

func TestOnlyDefinitiveVerdictsAreKept(t *testing.T) {
	tests := []struct {
		name string
		v    tv
		err  error
	}{
		{"unsure", tv{Label: "unsure", Conf: 0.5}, nil},
		{"timeout", tv{Label: "unsure"}, errors.New("ai: timeout")},
		{"empty", tv{}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, st, _ := newCache(t, nil)
			classify, calls := counter(tt.v, tt.err)
			for range 2 {
				_, src, err := c.Do(t.Context(), req("msg", "cl-1"), classify)
				if src != SourceFresh || !errors.Is(err, tt.err) {
					t.Fatalf("Do() = %v, %v; want fresh with the classify error %v", src, err, tt.err)
				}
			}
			if calls.Load() != 2 || st.Len() != 0 {
				t.Fatalf("calls=%d stored=%d, want 2 calls and nothing stored (unsure and failures are retried)", calls.Load(), st.Len())
			}
		})
	}
}

func TestClusterReusesFirstVerdictAcrossGroups(t *testing.T) {
	c, _, _ := newCache(t, nil)
	spam := tv{Label: "spam", Conf: 0.95}
	classify, calls := counter(spam, nil)
	for i := range 40 { // 40 slightly different copies in 40 groups, one campaign
		v, src, err := c.Do(t.Context(), req(fmt.Sprintf("join my channel %d", i), "campaign-7"), classify)
		if err != nil || v != spam {
			t.Fatalf("copy %d: %+v, %v", i, v, err)
		}
		if want := map[bool]Source{true: SourceFresh, false: SourceCluster}[i == 0]; src != want {
			t.Fatalf("copy %d: source %v, want %v", i, src, want)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("provider calls = %d, want 1 for a 40-group campaign (DESIGN §7.4)", calls.Load())
	}
}

func TestClusterSingleFlightUnderConcurrency(t *testing.T) {
	c, _, _ := newCache(t, nil)
	release, started := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	classify := func(context.Context) (tv, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		return tv{Label: "spam", Conf: 0.99}, nil
	}
	var wg sync.WaitGroup
	srcs := make(chan Source, 40)
	for i := range 40 {
		wg.Go(func() {
			_, src, err := c.Do(t.Context(), req(fmt.Sprintf("copy %d", i), "cl-x"), classify)
			if err != nil {
				t.Error(err)
			}
			srcs <- src
		})
	}
	<-started // the first provider call is in flight; later copies wait on it or reuse its verdict
	close(release)
	wg.Wait()
	close(srcs)
	fresh := 0
	for s := range srcs {
		if s == SourceFresh {
			fresh++
		}
	}
	if calls.Load() != 1 || fresh != 1 {
		t.Fatalf("calls=%d fresh=%d, want one provider call and one fresh answer for 40 concurrent copies", calls.Load(), fresh)
	}
}

func TestClusterReuseExpires(t *testing.T) {
	c, _, clk := newCache(t, func(o *Options[tv]) { o.ClusterTTL = time.Hour })
	classify, calls := counter(tv{Label: "not_spam", Conf: 0.95}, nil)
	_, _, _ = c.Do(t.Context(), req("a", "cl"), classify)
	clk.Advance(time.Hour)
	if _, src, _ := c.Do(t.Context(), req("b", "cl"), classify); src != SourceFresh || calls.Load() != 2 {
		t.Fatalf("after cluster TTL: source %v calls %d, want a fresh call", src, calls.Load())
	}
}

func TestClusterEntriesAreBounded(t *testing.T) {
	c, _, clk := newCache(t, func(o *Options[tv]) { o.MaxClusters = 3 })
	classify, _ := counter(tv{Label: "spam", Conf: 0.95}, nil)
	for i := range 10 {
		_, _, _ = c.Do(t.Context(), req(fmt.Sprint(i), fmt.Sprint("cl", i)), classify)
		clk.Advance(time.Second)
	}
	if n := len(c.clusters); n > 3 {
		t.Fatalf("cluster entries = %d, want at most 3", n)
	}
	if _, ok := c.cluster("cl9"); !ok {
		t.Fatal("newest cluster evicted, want the oldest ones evicted first")
	}
}

func TestSanitizeAppliesToStoredAndReused(t *testing.T) {
	c, st, _ := newCache(t, func(o *Options[tv]) {
		o.Sanitize = func(v tv) tv { v.Reason = ""; return v }
	})
	classify, _ := counter(tv{Label: "spam", Conf: 0.95, Reason: "contains +971 50 123 4567"}, nil)
	v, _, _ := c.Do(t.Context(), req("m1", "cl"), classify)
	if v.Reason == "" {
		t.Fatal("the fresh verdict lost its reason; only stored copies are sanitized")
	}
	if v, src, _ := c.Do(t.Context(), req("m2", "cl"), classify); src != SourceCluster || v.Reason != "" {
		t.Fatalf("reused verdict = %+v (%v), want the sanitized copy", v, src)
	}
	e, _, _ := st.Get(t.Context(), req("m1", "").Key, time.Time{})
	if string(e.Result) != `{"label":"spam","confidence":0.95,"reason":""}` || e.Model != "model-a" || e.PromptVersion != "p1" {
		t.Fatalf("stored entry = %+v %s, want sanitized result with model and prompt version", e, e.Result)
	}
}

type brokenStore struct{ *MemStore }

func (brokenStore) Get(context.Context, string, time.Time) (Entry, bool, error) {
	return Entry{}, false, errors.New("db down")
}

func TestStoreErrorStillClassifies(t *testing.T) {
	c, err := New(brokenStore{NewMemStore()}, clock.NewFake(t0), Options[tv]{Definitive: definitive})
	if err != nil {
		t.Fatal(err)
	}
	classify, calls := counter(tv{Label: "spam", Conf: 0.95}, nil)
	v, src, err := c.Do(t.Context(), req("m", ""), classify)
	if err == nil || v.Label != "spam" || src != SourceFresh || calls.Load() != 1 {
		t.Fatalf("Do() = %+v, %v, %v; want the fresh verdict plus the store error for logging", v, src, err)
	}
}

func TestDoRespectsCallerContext(t *testing.T) {
	c, _, _ := newCache(t, nil)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	block := make(chan struct{})
	defer close(block)
	_, _, err := c.Do(ctx, req("m", ""), func(context.Context) (tv, error) { <-block; return tv{}, nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Do() error = %v, want context.Canceled", err)
	}
}

func TestNewValidates(t *testing.T) {
	if _, err := New(NewMemStore(), clock.NewFake(t0), Options[tv]{}); err == nil {
		t.Error("missing Definitive accepted")
	}
	if _, err := New[tv](nil, clock.NewFake(t0), Options[tv]{Definitive: definitive}); err == nil {
		t.Error("nil store accepted")
	}
	c, _, _ := newCache(t, nil)
	if _, _, err := c.Do(t.Context(), Request{}, nil); err == nil {
		t.Error("empty key accepted")
	}
}
