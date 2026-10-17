package policy

import (
	"math"
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
)

// logistic is sigmoid(x), for building findings whose score matches their signals.
func logistic(x float64) float64 { return 1 / (1 + math.Exp(-x)) }

func TestFilterFindingForReDerivesFromKilledEvidence(t *testing.T) {
	s := Defaults()
	tests := []struct {
		name   string
		killed []string
		f      Finding
		band   Band
		source Source
	}{
		{
			"violation with a heuristic below T_review",
			[]string{"content.videos"},
			Finding{Band: BandCertain, Score: 0.3, Signals: []Signal{{Code: "kw", Weight: 0.5}}, Violations: []Violation{{Rule: "content.videos"}}},
			BandClean, SourceNone,
		},
		{
			"violation with a heuristic above T_review",
			[]string{"content.videos"},
			Finding{Band: BandCertain, Score: 0.7, Signals: []Signal{{Code: "kw", Weight: 2}}, Violations: []Violation{{Rule: "content.videos"}}},
			BandStillUncertain, SourceRules,
		},
		{
			"signal-less known spam keeps its band when a content rule is killed",
			[]string{"content.links"},
			Finding{Band: BandCertain, Source: SourceKnown, Violations: []Violation{{Rule: "content.links"}}},
			BandCertain, SourceKnown,
		},
		{
			"known spam with another known signal left",
			[]string{"blocklist.a"},
			Finding{Band: BandCertain, Source: SourceKnown, Signals: []Signal{{Code: "blocklist.a", Weight: 3, Known: true}, {Code: "blocklist.b", Weight: 3, Known: true}}},
			BandCertain, SourceKnown,
		},
		{
			"killed known rule, only just-joined left",
			[]string{"url.blocklisted"},
			Finding{
				Band: BandCertain, Source: SourceKnown, Score: logistic(-2 + 5 + 0.73),
				Signals: []Signal{{Code: "url.blocklisted", Weight: 5, Known: true}, {Code: SignalJustJoined, Weight: 0.73}},
			},
			BandClean, SourceNone,
		},
		{
			"killed known rule, strong heuristic left",
			[]string{"url.blocklisted"},
			Finding{
				Band: BandCertain, Source: SourceKnown, Score: logistic(-2 + 5 + 3),
				Signals: []Signal{{Code: "url.blocklisted", Weight: 5, Known: true}, {Code: "kw.crypto", Weight: 3}},
			},
			BandStillUncertain, SourceRules,
		},
		{
			"killed heuristic, only just-joined left",
			[]string{"kw.crypto"},
			Finding{
				Band: BandStillUncertain, Source: SourceRules, Score: logistic(-2 + 2 + 0.73),
				Signals: []Signal{{Code: "kw.crypto", Weight: 2}, {Code: SignalJustJoined, Weight: 0.73}},
			},
			BandClean, SourceNone,
		},
		{
			"killed heuristic, only a prior left above T_review",
			[]string{"kw.crypto"},
			Finding{
				Band: BandStillUncertain, Source: SourceRules, Score: logistic(-2 + 2 + 3),
				Signals: []Signal{{Code: "kw.crypto", Weight: 2}, {Code: "intel.prefix", Weight: 3, Prior: true}},
			},
			BandClean, SourceNone,
		},
		{
			"heuristics all killed",
			[]string{"kw"},
			Finding{Band: BandStillUncertain, Source: SourceRules, Score: 0.7, Signals: []Signal{{Code: "kw", Weight: 2}}},
			BandClean, SourceNone,
		},
		{
			"ai-verified stays",
			[]string{"kw"},
			Finding{Band: BandUncertain, Source: SourceAI, Score: 0.7, Signals: []Signal{{Code: "kw", Weight: 2}}},
			BandUncertain, SourceAI,
		},
	}
	for _, tt := range tests {
		r := NewRails(clock.NewFake(t0))
		for _, k := range tt.killed {
			r.KillRule(k)
		}
		got := r.FilterFindingFor(tt.f, s)
		if got.Band != tt.band || got.Source != tt.source {
			t.Errorf("%s: band %s source %q (score %.2f), want %s %q (§8.6: a killed rule stops acting)",
				tt.name, got.Band, got.Source, got.Score, tt.band, tt.source)
		}
		if d := Decide(s, got, Standing{Role: RoleMember}); tt.band == BandClean && (d.Delete || d.Ban || d.Review != nil) {
			t.Errorf("%s: decision %+v after the kill, want nothing", tt.name, d)
		}
	}
}

func TestFilterFindingForRecomputesScore(t *testing.T) {
	r := NewRails(clock.NewFake(t0))
	r.KillRule("kw")
	f := Finding{
		Band: BandStillUncertain, Source: SourceRules, Score: logistic(1.5),
		Signals: []Signal{{Code: "kw", Weight: 1}, {Code: "x", Weight: 2}},
	}
	if got := r.FilterFindingFor(f, Defaults()); math.Abs(got.Score-logistic(0.5)) > 1e-9 {
		t.Errorf("score after killing a +1 signal = %v, want %v", got.Score, logistic(0.5))
	}
	if got := r.FilterFindingFor(Finding{Band: BandStillUncertain, Source: SourceRules, Score: 1, Signals: []Signal{{Code: "kw", Weight: 1}}}, Defaults()); math.IsNaN(got.Score) || got.Score < 0 || got.Score > 1 {
		t.Errorf("score from p = 1 = %v, want a finite probability", got.Score)
	}
}

func TestEngineRailsAndNilSafety(t *testing.T) {
	e := NewEngine(clock.NewFake(t0))
	if e.Rails() == nil {
		t.Fatal("NewEngine has no rails")
	}
}
