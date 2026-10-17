package scoring

import (
	"math"
	"math/rand/v2"
	"testing"

	"pgregory.net/rapid"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai"
)

func genSignal(known, hard bool) *rapid.Generator[Signal] {
	return rapid.Custom(func(t *rapid.T) Signal {
		return Signal{
			Code:     rapid.SampledFrom([]string{"a", "b", "c", "d"}).Draw(t, "code"),
			Weight:   rapid.Float64Range(-50, 50).Draw(t, "weight"),
			Known:    known && rapid.Bool().Draw(t, "known"),
			Hard:     hard && rapid.Bool().Draw(t, "hard"),
			Evidence: rapid.SampledFrom([]string{"", "x", "y"}).Draw(t, "evidence"),
		}
	})
}

func genAI() *rapid.Generator[*ai.Verdict] {
	return rapid.Custom(func(t *rapid.T) *ai.Verdict {
		if rapid.Bool().Draw(t, "nil") {
			return nil
		}
		return &ai.Verdict{
			Label:      rapid.SampledFrom([]ai.Label{ai.LabelSpam, ai.LabelNotSpam, ai.LabelUnsure, "SPAM"}).Draw(t, "label"),
			Confidence: rapid.SampledFrom([]float64{0, 0.5, 0.89, 0.9, 1, 1.1, math.NaN()}).Draw(t, "conf"),
		}
	})
}

// §18: heuristics never delete or ban on their own, whatever the AI says unless it is
// confident spam; an AI-only spam verdict is never source known.
func TestPropertyRulesOnlyNeverReachDeleteLine(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		sigs := rapid.SliceOf(genSignal(false, false)).Draw(t, "signals")
		w := Weights{Version: "t", Bias: rapid.Float64Range(-10, 10).Draw(t, "bias")}
		th := Thresholds{Review: rapid.Float64Range(0.01, 0.99).Draw(t, "review"), AIConfidence: 0.9}
		out := genAI().Draw(t, "ai")
		v := Classify(Score(w, sigs), th)
		if v.DeletesAsSpam() {
			t.Fatalf("got delete before the AI, want none (heuristics alone never reach the delete line)")
		}
		r := Resolve(v, out, th)
		confidentSpam := out != nil && out.Label == ai.LabelSpam && out.Confidence >= 0.9 && out.Confidence <= 1
		if r.DeletesAsSpam() != (confidentSpam && v.Band == BandUncertain) {
			t.Fatalf("got delete=%v for ai=%+v band=%s, want delete only on confident AI spam", r.DeletesAsSpam(), out, v.Band)
		}
		if r.Source == SourceKnown {
			t.Fatalf("got source known without known evidence, want rules/ai/none (the matrix bans on known)")
		}
	})
}

// Any permutation of the same signals gives the same P and the same band, even with
// duplicate codes that tie on |weight| but differ in sign, evidence or Known.
func TestPropertyOrderIndependent(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		sigs := rapid.SliceOf(genSignal(true, true)).Draw(t, "signals")
		if len(sigs) > 1 && rapid.Bool().Draw(t, "tie") {
			sigs[1].Code, sigs[1].Weight = sigs[0].Code, -sigs[0].Weight
		}
		w := DefaultWeights()
		a := Score(w, sigs)
		shuffled := append([]Signal(nil), sigs...)
		rand.New(rand.NewPCG(rapid.Uint64().Draw(t, "seed"), 1)).Shuffle(len(shuffled), func(i, j int) {
			shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
		})
		b := Score(w, shuffled)
		if a.P != b.P || a.LogOdds != b.LogOdds {
			t.Fatalf("got P %v vs %v after shuffling, want equal (score must not depend on order)", a.P, b.P)
		}
		va, vb := Classify(a, DefaultThresholds()), Classify(b, DefaultThresholds())
		if va.Band != vb.Band || va.Source != vb.Source {
			t.Fatalf("got %s/%s vs %s/%s after shuffling, want equal", va.Band, va.Source, vb.Band, vb.Source)
		}
	})
}

func TestScoreHugeWeightsStayFinite(t *testing.T) {
	sigs := []Signal{
		{Code: "a", Weight: math.MaxFloat64},
		{Code: "b", Weight: math.MaxFloat64},
		{Code: "c", Weight: -math.MaxFloat64},
		{Code: "d", Weight: -math.MaxFloat64},
	}
	r := Score(Weights{Version: "t", Bias: math.Inf(1)}, sigs)
	if math.IsNaN(r.P) || math.IsNaN(r.LogOdds) {
		t.Fatalf("got P %v, LogOdds %v, want finite (Inf-Inf must not reach the score)", r.P, r.LogOdds)
	}
}

func TestDefaultWeightsIsACopy(t *testing.T) {
	w := DefaultWeights()
	w.Signals["x"] = 99
	if _, ok := DefaultWeights().Signals["x"]; ok {
		t.Fatal("got a shared map, want a copy (callers must not change the default table)")
	}
}

func TestParseWeightsRejectsSecondDocument(t *testing.T) {
	if _, err := ParseWeights([]byte("version: a\n---\nversion: b\n")); err == nil {
		t.Fatal("got nil error for two documents, want an error (which one applies would be ambiguous)")
	}
}

func TestResolveInvalidThresholdsNeverSettle(t *testing.T) {
	v := Verdict{Band: BandUncertain, Source: SourceRules}
	out := &ai.Verdict{Label: ai.LabelSpam, Confidence: 0.1}
	if r := Resolve(v, out, Thresholds{Review: 0.6}); r.Band != BandStillUncertain {
		t.Fatalf("got %s with AIConfidence 0, want still_uncertain (a broken threshold must not delete)", r.Band)
	}
}

func TestResolveCopiesAIVerdict(t *testing.T) {
	out := &ai.Verdict{Label: ai.LabelUnsure, Categories: []string{"crypto"}}
	r := Resolve(Verdict{Band: BandUncertain}, out, DefaultThresholds())
	out.Categories[0] = "changed"
	if r.AI.Categories[0] != "crypto" {
		t.Fatal("got the caller's slice, want a copy (a verdict must not change after the fact)")
	}
}

func FuzzParseWeights(f *testing.F) {
	f.Add([]byte("version: v\nbias: -1\nsignals: {a: 1}\n"))
	f.Add([]byte("version: v\nbias: .nan\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		w, err := ParseWeights(data)
		if err != nil {
			return
		}
		if w.Version == "" || !finite(w.Bias) {
			t.Fatalf("accepted %q with version %q bias %v", data, w.Version, w.Bias)
		}
		for c, v := range w.Signals {
			if c == "" || !finite(v) {
				t.Fatalf("accepted signal %q=%v", c, v)
			}
		}
	})
}
