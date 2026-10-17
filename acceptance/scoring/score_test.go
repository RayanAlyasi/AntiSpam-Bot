package scoring_test

import (
	"math"
	"strings"
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/scoring"
)

func TestScoreSigmoidOfBiasPlusWeights(t *testing.T) {
	cases := []struct {
		name    string
		bias    float64
		weights []float64
	}{
		{"no signals", -2, nil},
		{"zero bias no signals", 0, nil},
		{"one signal", -2, []float64{2.1}},
		{"several signals", -2.5, []float64{2.1, 3.0, 0.8, -1.0}},
		{"negative total", 1, []float64{-4, -0.5}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := scoring.Weights{Version: "w-2026-10", Bias: tc.bias}
			var sigs []scoring.Signal
			sum := tc.bias
			for i, wt := range tc.weights {
				sigs = append(sigs, scoring.Signal{Code: string(rune('a' + i)), Weight: wt})
				sum += wt
			}
			r := scoring.Score(w, sigs)
			if !near(r.LogOdds, sum) {
				t.Errorf("LogOdds = %v, want %v (bias + sum of weights, DESIGN §7.3)", r.LogOdds, sum)
			}
			if !near(r.P, sigmoid(sum)) {
				t.Errorf("P = %v, want sigmoid(%v) = %v (p = sigmoid(bias + Σw), DESIGN §7.3)", r.P, sum, sigmoid(sum))
			}
			if r.Bias != tc.bias {
				t.Errorf("Bias = %v, want %v (the result records the bias it used, for explanations)", r.Bias, tc.bias)
			}
			if r.WeightsVersion != "w-2026-10" {
				t.Errorf("WeightsVersion = %q, want %q (weights are versioned and every detection records which version scored it, DESIGN §7.3)", r.WeightsVersion, "w-2026-10")
			}
		})
	}
}

func TestScoreTableOverridesSignalWeight(t *testing.T) {
	w := scoring.Weights{Version: "v", Bias: -1, Signals: map[string]float64{"a": 0, "b": -1.5, "c": 3.25}}
	sigs := []scoring.Signal{{Code: "a", Weight: 4}, {Code: "b", Weight: 2}, {Code: "c", Weight: 0.1}, {Code: "d", Weight: 1}}
	r := scoring.Score(w, sigs)
	want := -1 + 0 - 1.5 + 3.25 + 1
	if !near(r.LogOdds, want) {
		t.Fatalf("LogOdds = %v, want %v (a code in the weight file overrides the analyzer's weight, even 0 or negative; codes not in the file keep their own weight)", r.LogOdds, want)
	}
	applied := map[string]float64{"a": 0, "b": -1.5, "c": 3.25, "d": 1}
	for _, s := range r.Signals {
		if wantA, ok := applied[s.Code]; ok && s.Counted && !near(s.Applied, wantA) {
			t.Errorf("signal %q Applied = %v, want %v (Applied is the weight that entered the sum)", s.Code, s.Applied, wantA)
		}
	}
}

func TestScoreCountsEachCodeOnce(t *testing.T) {
	w := scoring.Weights{Version: "v", Bias: 0}
	sigs := []scoring.Signal{
		{Code: "dup", Weight: 1.0, Evidence: "small"},
		{Code: "dup", Weight: -3.0, Evidence: "largest magnitude"},
		{Code: "dup", Weight: 2.0, Evidence: "medium"},
		{Code: "other", Weight: 0.5, Evidence: "other"},
	}
	r := scoring.Score(w, sigs)
	if !near(r.LogOdds, -3.0+0.5) {
		t.Fatalf("LogOdds = %v, want -2.5 (a code counts once per message and the duplicate with the largest |weight| wins, so repeating a signal can't inflate the score)", r.LogOdds)
	}
	counted := 0
	for _, s := range r.Signals {
		if s.Code == "dup" && s.Counted {
			counted++
		}
	}
	if counted != 1 {
		t.Errorf("%d counted entries for code dup, want exactly 1 (each code counts once)", counted)
	}
	if n := strings.Count(r.Explain(), "largest magnitude"); n != 1 || strings.Contains(r.Explain(), "small") {
		t.Errorf("Explain() = %q, want the winning duplicate listed once and the others not at all", r.Explain())
	}
}

func TestScoreExcludesHardSignals(t *testing.T) {
	w := scoring.Weights{Version: "v", Bias: -2, Signals: map[string]float64{"policy.video": 5}}
	sigs := []scoring.Signal{
		{Code: "policy.video", Weight: 5, Hard: true, Evidence: "videos are not allowed here"},
		{Code: "x", Weight: 1},
	}
	r := scoring.Score(w, sigs)
	if !near(r.LogOdds, -1) {
		t.Errorf("LogOdds = %v, want -1 (hard policy signals are not spam signals and never enter the score, DESIGN §7.3)", r.LogOdds)
	}
	found := false
	for _, s := range r.Signals {
		if s.Code == "policy.video" {
			found = true
			if s.Counted {
				t.Errorf("hard signal has Counted = true, want false (policy is evaluated separately from the spam score, DESIGN §7.3)")
			}
			if !s.Hard {
				t.Errorf("hard signal lost its Hard flag in the result, want it kept for the decision and explanation")
			}
		}
	}
	if !found {
		t.Errorf("hard signal missing from Result.Signals, want it kept (every detection stores its signals, DESIGN §7.3)")
	}
}

func TestScoreNonFiniteWeightsCountAsZero(t *testing.T) {
	w := scoring.Weights{Version: "v", Bias: -1.5}
	sigs := []scoring.Signal{
		{Code: "nan", Weight: math.NaN()},
		{Code: "pinf", Weight: math.Inf(1)},
		{Code: "ninf", Weight: math.Inf(-1)},
	}
	r := scoring.Score(w, sigs)
	if math.IsNaN(r.P) || math.IsNaN(r.LogOdds) {
		t.Fatalf("P = %v, LogOdds = %v, want finite (a broken analyzer weight must not poison the score)", r.P, r.LogOdds)
	}
	if !near(r.LogOdds, -1.5) || !near(r.P, sigmoid(-1.5)) {
		t.Errorf("LogOdds = %v, P = %v, want %v and %v (non-finite weights count as 0, so +Inf can't force a delete)", r.LogOdds, r.P, -1.5, sigmoid(-1.5))
	}
}

func TestScorePropertiesBoundedAndOrderIndependent(t *testing.T) {
	const seed = 50
	rnd := newRand(seed)
	for i := range propIters {
		w := genWeights(rnd)
		sigs := genAny(rnd)
		if rnd.IntN(10) == 0 {
			for j := range sigs {
				sigs[j].Weight *= 1e3 // extreme log-odds
			}
		}
		r := scoring.Score(w, sigs)
		if math.IsNaN(r.P) || r.P < 0 || r.P > 1 {
			t.Fatalf("seed %d iter %d: P = %v for %s, want a finite value in [0,1] (p is clipped to [0,1], DESIGN §7.3)", seed, i, r.P, describe(sigs))
		}
		perm := append([]scoring.Signal(nil), sigs...)
		rnd.Shuffle(len(perm), func(a, b int) { perm[a], perm[b] = perm[b], perm[a] })
		r2 := scoring.Score(w, perm)
		if math.Abs(r.P-r2.P) > 1e-12 {
			t.Fatalf("seed %d iter %d: P = %v, then %v after reordering %s, want equal (analyzer order must not change the score)", seed, i, r.P, r2.P, describe(sigs))
		}
		hasHard := false
		for _, s := range sigs {
			hasHard = hasHard || s.Hard
		}
		if !hasHard && r.Explain() != r2.Explain() {
			t.Fatalf("seed %d iter %d: Explain() differs after reordering:\n%q\n%q\nwant equal (explanations are sorted by weight then code)", seed, i, r.Explain(), r2.Explain())
		}
	}
}

func TestExplainFormatAndOrder(t *testing.T) {
	w := scoring.Weights{Version: "v", Bias: -2}
	cases := []struct {
		name string
		sigs []scoring.Signal
		want string
	}{
		{"nothing to list", nil, ""},
		{
			"design example sorted by weight, negative and policy last",
			[]scoring.Signal{
				{Code: "domain.young", Weight: 2.1, Evidence: "link to a domain registered 3 days ago"},
				{Code: "sender.just_joined", Weight: 0.8, Evidence: "sender joined 4 minutes ago"},
				{Code: "policy.video", Hard: true, Evidence: "videos are not allowed here"},
				{Code: "domain.established", Weight: -1.0},
				{Code: "simhash.near", Weight: 3.0, Evidence: "text 92% similar to a known crypto-scam message"},
			},
			"text 92% similar to a known crypto-scam message (+3.0), link to a domain registered 3 days ago (+2.1), " +
				"sender joined 4 minutes ago (+0.8), domain.established (-1.0), videos are not allowed here (policy)",
		},
		{
			"ties ordered by code, code fallback for hard",
			[]scoring.Signal{
				{Code: "b.code", Weight: 1.0, Evidence: "second"},
				{Code: "a.code", Weight: 1.0, Evidence: "first"},
				{Code: "policy.sticker", Hard: true},
			},
			"first (+1.0), second (+1.0), policy.sticker (policy)",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := scoring.Score(w, tc.sigs).Explain(); got != tc.want {
				t.Errorf("Explain() =\n  %q\nwant\n  %q\n(each counted signal as \"<evidence> (%%+.1f)\" by weight desc then code, hard signals as \"<evidence> (policy)\", DESIGN §7.3)", got, tc.want)
			}
		})
	}
}
