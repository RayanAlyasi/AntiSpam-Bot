package detect

import (
	"context"
	"errors"
	"slices"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/scoring"
)

type env struct{}
type pol struct{ skip map[string]bool }

// fakeAnalyzer emits fixed signals. block makes it wait for ctx; calls counts Analyze calls.
type fakeAnalyzer struct {
	name    string
	tier    Tier
	signals []Signal
	err     error
	block   bool
	panics  bool
	sawPrev *[]Signal
	calls   atomic.Int32
}

func (f *fakeAnalyzer) Name() string { return f.name }
func (f *fakeAnalyzer) Tier() Tier   { return f.tier }
func (f *fakeAnalyzer) Applies(_ *env, p *pol) bool {
	return p == nil || !p.skip[f.name]
}
func (f *fakeAnalyzer) Analyze(ctx context.Context, _ *env, s *State) ([]Signal, error) {
	f.calls.Add(1)
	if f.sawPrev != nil {
		*f.sawPrev = slices.Clone(s.Signals)
	}
	if f.panics {
		panic("boom")
	}
	if f.block {
		<-ctx.Done()
		return f.signals, nil // a late answer: its signals must be dropped
	}
	return f.signals, f.err
}

var (
	suspicious = Signal{Code: "text.suspicious", Weight: 4}   // with bias -3: p ≈ 0.73 ≥ 0.60
	mild       = Signal{Code: "text.mild", Weight: 1}         // p ≈ 0.12: clean
	known      = Signal{Code: "url.blocklisted", Weight: 5, Known: true}
	hard       = Signal{Code: "policy.video", Hard: true, Evidence: "videos not allowed"}
)

func newRunner(t *testing.T, as ...*fakeAnalyzer) *Runner[env, pol] {
	t.Helper()
	reg := NewRegistry[env, pol]()
	for _, a := range as {
		if err := reg.Register(a); err != nil {
			t.Fatal(err)
		}
	}
	r, err := NewRunner(reg, Config{})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRegistryRejectsBadRegistrations(t *testing.T) {
	reg := NewRegistry[env, pol]()
	if err := reg.Register(&fakeAnalyzer{name: "a", tier: T0}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		a    Analyzer[env, pol]
	}{
		{"nil", nil},
		{"empty name", &fakeAnalyzer{tier: T0}},
		{"duplicate", &fakeAnalyzer{name: "a", tier: T1}},
		{"tier too high", &fakeAnalyzer{name: "b", tier: T4 + 1}},
		{"negative tier", &fakeAnalyzer{name: "c", tier: -1}},
	} {
		if err := reg.Register(tc.a); err == nil {
			t.Errorf("%s: got nil error, want a rejection (names and tiers must be unambiguous)", tc.name)
		}
	}
	if got := reg.Names(); !slices.Equal(got, []string{"a"}) {
		t.Errorf("got names %v, want [a] (rejected analyzers must not be registered)", got)
	}
}

func TestRegistryConfigure(t *testing.T) {
	reg := NewRegistry[env, pol]()
	for _, n := range []string{"a", "b"} {
		if err := reg.Register(&fakeAnalyzer{name: n, tier: T0}); err != nil {
			t.Fatal(err)
		}
	}
	if !reg.Enabled("a") || reg.Enabled("missing") {
		t.Fatal("got wrong default state, want registered analyzers enabled and unknown ones not")
	}
	if err := reg.Configure(map[string]bool{"a": false, "typo": true}); err == nil {
		t.Fatal("got nil error for an unknown name, want an error (a config typo must be visible)")
	}
	if !reg.Enabled("a") {
		t.Fatal("got a disabled after a failed Configure, want no change (configure is all or nothing)")
	}
	if err := reg.SetEnabled("a", false); err != nil || reg.Enabled("a") || !reg.Enabled("b") {
		t.Fatalf("got err=%v a=%v b=%v, want a off and b untouched", err, reg.Enabled("a"), reg.Enabled("b"))
	}
}

func TestRunStopsAndContinues(t *testing.T) {
	for _, tc := range []struct {
		name     string
		t0, t1   []Signal
		lane     Lane
		wantRan  []Tier
		wantStop StopReason
		wantBand scoring.Band
	}{
		{"known at T0 stops before T1", []Signal{known}, []Signal{mild}, Fast(), []Tier{T0}, StopCertain, scoring.BandCertain},
		{"hard at T0 stops before T1", []Signal{hard}, nil, Fast(), []Tier{T0}, StopCertain, scoring.BandCertain},
		{"clean T0 still runs T1", nil, []Signal{suspicious}, Fast(), []Tier{T0, T1}, StopNone, scoring.BandUncertain},
		{"clean fast lane runs to its end", nil, []Signal{mild}, Fast(), []Tier{T0, T1}, StopNone, scoring.BandClean},
		{"clean after the fast lane stops", nil, []Signal{mild}, Lane{From: T0, To: T4}, []Tier{T0, T1}, StopClean, scoring.BandClean},
		{"suspicious continues to T4", nil, []Signal{suspicious}, Lane{From: T0, To: T4}, []Tier{T0, T1, T2, T3, T4}, StopNone, scoring.BandUncertain},
		{"slow lane from clean prior never runs", nil, nil, Slow([]Signal{mild}), nil, StopClean, scoring.BandClean},
		{"slow lane from certain prior never runs", nil, nil, Slow([]Signal{known}), nil, StopCertain, scoring.BandCertain},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRunner(t,
				&fakeAnalyzer{name: "t0", tier: T0, signals: tc.t0},
				&fakeAnalyzer{name: "t1", tier: T1, signals: tc.t1},
				&fakeAnalyzer{name: "t2", tier: T2},
				&fakeAnalyzer{name: "t3", tier: T3},
				&fakeAnalyzer{name: "t4", tier: T4})
			out, err := r.Run(context.Background(), &env{}, &pol{}, tc.lane)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(out.Ran, tc.wantRan) || out.Stop != tc.wantStop || out.Verdict.Band != tc.wantBand {
				t.Fatalf("got ran=%v stop=%q band=%s, want ran=%v stop=%q band=%s (DESIGN §7.1 short-circuiting)",
					out.Ran, out.Stop, out.Verdict.Band, tc.wantRan, tc.wantStop, tc.wantBand)
			}
		})
	}
}

func TestRunMergesInRegistrationOrderAndPassesState(t *testing.T) {
	var saw []Signal
	a := &fakeAnalyzer{name: "a", tier: T0, signals: []Signal{{Code: "a", Weight: 1}}}
	b := &fakeAnalyzer{name: "b", tier: T0, signals: []Signal{{Code: "b", Weight: 1}}}
	c := &fakeAnalyzer{name: "c", tier: T1, sawPrev: &saw}
	for range 20 {
		out, err := newRunner(t, b, a, c).Run(context.Background(), &env{}, nil, Fast())
		if err != nil {
			t.Fatal(err)
		}
		if got := codes(out.Signals); !slices.Equal(got, []string{"b", "a"}) {
			t.Fatalf("got %v, want [b a] (signals merge in registration order, whatever finishes first)", got)
		}
		if got := codes(saw); !slices.Equal(got, []string{"b", "a"}) {
			t.Fatalf("T1 saw %v, want [b a] (State carries earlier tiers' signals)", got)
		}
	}
}

func TestRunSkipsDisabledAndInapplicable(t *testing.T) {
	off := &fakeAnalyzer{name: "off", tier: T0, signals: []Signal{known}}
	skipped := &fakeAnalyzer{name: "skipped", tier: T0, signals: []Signal{known}}
	r := newRunner(t, off, skipped)
	if err := r.reg.SetEnabled("off", false); err != nil {
		t.Fatal(err)
	}
	out, err := r.Run(context.Background(), &env{}, &pol{skip: map[string]bool{"skipped": true}}, Fast())
	if err != nil {
		t.Fatal(err)
	}
	if off.calls.Load() != 0 || skipped.calls.Load() != 0 || len(out.Signals) != 0 {
		t.Fatalf("got calls off=%d skipped=%d, want none (disabled or inapplicable analyzers must not run)",
			off.calls.Load(), skipped.calls.Load())
	}
}

func TestRunFailuresDropOnlyTheirSignals(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		good := &fakeAnalyzer{name: "good", tier: T0, signals: []Signal{mild}}
		failing := &fakeAnalyzer{name: "failing", tier: T0, signals: []Signal{known}, err: errors.New("cache down")}
		panicking := &fakeAnalyzer{name: "panicking", tier: T0, signals: []Signal{known}, panics: true}
		slow := &fakeAnalyzer{name: "slow", tier: T0, signals: []Signal{known}, block: true}
		start := time.Now()
		out, err := newRunner(t, good, failing, panicking, slow).Run(context.Background(), &env{}, nil, Fast())
		if err != nil {
			t.Fatal(err)
		}
		if waited := time.Since(start); waited != DefaultBudgets()[T0] {
			t.Errorf("waited %v, want the T0 budget %v", waited, DefaultBudgets()[T0])
		}
		if got := codes(out.Signals); !slices.Equal(got, []string{"text.mild"}) {
			t.Fatalf("got %v, want only the good analyzer's signal (failures never add evidence)", got)
		}
		if out.Verdict.Band != scoring.BandClean {
			t.Fatalf("got band %s, want clean (a failure must not make the outcome harsher)", out.Verdict.Band)
		}
		var names []string
		for _, f := range out.Failures {
			names = append(names, f.Analyzer)
		}
		if !slices.Equal(names, []string{"failing", "panicking", "slow"}) {
			t.Fatalf("got failures %v, want failing, panicking, slow (every failure is reported)", names)
		}
		if !errors.Is(out.Failures[2].Err, ErrBudget) {
			t.Fatalf("got %v, want ErrBudget for the late analyzer", out.Failures[2].Err)
		}
	})
}

func TestRunCancelledContext(t *testing.T) {
	a := &fakeAnalyzer{name: "a", tier: T0}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := newRunner(t, a).Run(ctx, &env{}, nil, Fast()); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled (cancellation must be respected)", err)
	}
	if a.calls.Load() != 0 {
		t.Fatal("got an analyzer call after cancellation, want none")
	}
}

func TestNewRunnerValidates(t *testing.T) {
	reg := NewRegistry[env, pol]()
	for _, tc := range []struct {
		name string
		cfg  Config
	}{
		{"bad thresholds", Config{Thresholds: scoring.Thresholds{Review: 2, AIConfidence: 0.9}}},
		{"zero budget", Config{Budgets: map[Tier]time.Duration{T0: 0}}},
		{"bad tier budget", Config{Budgets: map[Tier]time.Duration{9: time.Second}}},
	} {
		if _, err := NewRunner(reg, tc.cfg); err == nil {
			t.Errorf("%s: got nil error, want a rejection", tc.name)
		}
	}
	if _, err := NewRunner[env, pol](nil, Config{}); err == nil {
		t.Error("nil registry: got nil error, want a rejection")
	}
	r := newRunner(t)
	if _, err := r.Run(context.Background(), &env{}, nil, Lane{From: T2, To: T1}); err == nil {
		t.Error("reversed lane: got nil error, want a rejection")
	}
}

func codes(sigs []Signal) []string {
	out := []string{}
	for _, s := range sigs {
		out = append(out, s.Code)
	}
	return out
}
