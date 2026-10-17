package scoring_test

import (
	"math"
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/scoring"
)

func TestClassifyBands(t *testing.T) {
	th := scoring.DefaultThresholds()
	known := scoring.Signal{Code: "blocklist.domain", Weight: 3, Known: true, Evidence: "blocklisted domain"}
	hard := scoring.Signal{Code: "policy.video", Hard: true, Evidence: "videos are not allowed here"}
	cases := []struct {
		name      string
		w         scoring.Weights
		sigs      []scoring.Signal
		band      scoring.Band
		source    scoring.Source
		violation bool
		why       string
	}{
		{
			"known spam with low score",
			scoring.Weights{Version: "v", Bias: -4},
			[]scoring.Signal{known},
			scoring.BandCertain, scoring.SourceKnown, false, "known spam is CERTAIN whatever the score (DESIGN §7.0)",
		},
		{
			"known overridden to zero",
			scoring.Weights{Version: "v", Bias: -4, Signals: map[string]float64{"blocklist.domain": 0}},
			[]scoring.Signal{known},
			scoring.BandClean, scoring.SourceNone, false, "a known signal only makes a verdict CERTAIN when its applied weight is positive",
		},
		{
			"known with negative weight",
			scoring.Weights{Version: "v", Bias: -4},
			[]scoring.Signal{{Code: "k", Weight: -2, Known: true}},
			scoring.BandClean, scoring.SourceNone, false, "a known signal only makes a verdict CERTAIN when its applied weight is positive",
		},
		{
			"hard policy only",
			scoring.Weights{Version: "v", Bias: -4},
			[]scoring.Signal{hard},
			scoring.BandCertain, scoring.SourceNone, true, "a hard policy violation is CERTAIN but is not spam evidence (DESIGN §7.3, §9.3)",
		},
		{
			"hard and known",
			scoring.Weights{Version: "v", Bias: -4},
			[]scoring.Signal{hard, known},
			scoring.BandCertain, scoring.SourceKnown, true, "if a message is both, the spam route applies (DESIGN §7.3)",
		},
		{
			"heuristics above review",
			scoring.Weights{Version: "v", Bias: -1},
			[]scoring.Signal{{Code: "h", Weight: 2}},
			scoring.BandUncertain, scoring.SourceRules, false, "suspicious but not certain goes to the AI with source rules (DESIGN §7.0)",
		},
		{
			"heuristics below review",
			scoring.Weights{Version: "v", Bias: -2},
			[]scoring.Signal{{Code: "h", Weight: 1}},
			scoring.BandClean, scoring.SourceNone, false, "below T_review a message is clean (DESIGN §7.3)",
		},
		{
			"no signals at P 0.5",
			scoring.Weights{Version: "v", Bias: 0},
			nil,
			scoring.BandClean, scoring.SourceNone, false, "P 0.5 is below the default T_review 0.60",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := scoring.Score(tc.w, tc.sigs)
			v := scoring.Classify(r, th)
			if v.Band != tc.band || v.Source != tc.source || v.Violation != tc.violation {
				t.Errorf("got band %q source %q violation %v (P = %.3f), want %q %q %v (%s)",
					v.Band, v.Source, v.Violation, r.P, tc.band, tc.source, tc.violation, tc.why)
			}
			if v.Score.P != r.P || v.Score.WeightsVersion != r.WeightsVersion {
				t.Errorf("Verdict.Score = %+v, want the classified Result (detections store their signals and weights)", v.Score)
			}
		})
	}
}

func TestClassifyThresholdBoundary(t *testing.T) {
	r := scoring.Score(scoring.Weights{Version: "v", Bias: 0.4}, nil)
	at := scoring.Thresholds{Review: r.P, AIConfidence: 0.9}
	if v := scoring.Classify(r, at); v.Band != scoring.BandUncertain || v.Source != scoring.SourceRules {
		t.Errorf("P == T_review (%v): got %q/%q, want uncertain/rules (at or above T_review goes to the AI, DESIGN §7.3)", r.P, v.Band, v.Source)
	}
	above := scoring.Thresholds{Review: math.Nextafter(r.P, 1), AIConfidence: 0.9}
	if v := scoring.Classify(r, above); v.Band != scoring.BandClean {
		t.Errorf("P just below T_review: got %q, want clean (below T_review a message is clean, DESIGN §7.3)", v.Band)
	}
}

func TestResolveAIOutcomes(t *testing.T) {
	type want struct {
		band   scoring.Band
		source scoring.Source
		del    bool
	}
	still := want{scoring.BandStillUncertain, scoring.SourceRules, false}
	verified := want{scoring.BandUncertain, scoring.SourceAI, true}
	cases := []struct {
		name string
		th   scoring.Thresholds
		out  *ai.Verdict
		want want
		why  string
	}{
		{"AI off or failed", scoring.DefaultThresholds(), nil, still, "AI off, budget, timeout or error asks admins and deletes nothing (DESIGN §7.4)"},
		{"confident spam", scoring.DefaultThresholds(), &ai.Verdict{Label: ai.LabelSpam, Confidence: 0.95, Reason: "crypto scam"}, verified, "spam ≥ 0.9 is AI-verified and deleted (DESIGN §7.0)"},
		{"spam exactly at threshold", scoring.DefaultThresholds(), &ai.Verdict{Label: ai.LabelSpam, Confidence: 0.9}, verified, "confidence ≥ ai.spam_confidence counts (DESIGN §7.3)"},
		{"spam full confidence", scoring.DefaultThresholds(), &ai.Verdict{Label: ai.LabelSpam, Confidence: 1}, verified, "confidence 1 is in range"},
		{"spam just below threshold", scoring.DefaultThresholds(), &ai.Verdict{Label: ai.LabelSpam, Confidence: 0.8999}, still, "below 0.9 goes to admins (DESIGN §7.0)"},
		{"spam NaN confidence", scoring.DefaultThresholds(), &ai.Verdict{Label: ai.LabelSpam, Confidence: math.NaN()}, still, "non-finite confidence is not confidence"},
		{"spam infinite confidence", scoring.DefaultThresholds(), &ai.Verdict{Label: ai.LabelSpam, Confidence: math.Inf(1)}, still, "non-finite confidence is not confidence"},
		{"spam confidence above 1", scoring.DefaultThresholds(), &ai.Verdict{Label: ai.LabelSpam, Confidence: 1.5}, still, "out-of-range output is invalid and treated as unsure (DESIGN §7.4)"},
		{"spam negative confidence", scoring.DefaultThresholds(), &ai.Verdict{Label: ai.LabelSpam, Confidence: -0.2}, still, "out-of-range output is invalid and treated as unsure (DESIGN §7.4)"},
		{"confident not spam", scoring.DefaultThresholds(), &ai.Verdict{Label: ai.LabelNotSpam, Confidence: 0.95}, want{scoring.BandClean, scoring.SourceAI, false}, "not spam ≥ 0.9: nothing happens (DESIGN §7.0)"},
		{"unconfident not spam", scoring.DefaultThresholds(), &ai.Verdict{Label: ai.LabelNotSpam, Confidence: 0.5}, still, "low confidence goes to admins (DESIGN §7.0)"},
		{"unsure", scoring.DefaultThresholds(), &ai.Verdict{Label: ai.LabelUnsure, Confidence: 0.99}, still, "unsure goes to admins (DESIGN §7.0)"},
		{"empty label", scoring.DefaultThresholds(), &ai.Verdict{Label: "", Confidence: 0.99}, still, "an unknown label is treated as unsure"},
		{"wrong-case label", scoring.DefaultThresholds(), &ai.Verdict{Label: "SPAM", Confidence: 0.99}, still, "an unknown label is treated as unsure"},
		{"stricter group threshold", scoring.Thresholds{Review: 0.6, AIConfidence: 0.95}, &ai.Verdict{Label: ai.LabelSpam, Confidence: 0.92}, still, "the group's ai.spam_confidence applies, not a constant"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := uncertainVerdict()
			if before.Band != scoring.BandUncertain {
				t.Fatalf("setup: Classify gave %q, want uncertain", before.Band)
			}
			v := scoring.Resolve(before, tc.out, tc.th)
			if v.Band != tc.want.band || v.Source != tc.want.source || v.DeletesAsSpam() != tc.want.del {
				t.Errorf("got band %q source %q deletes %v, want %q %q %v (%s)",
					v.Band, v.Source, v.DeletesAsSpam(), tc.want.band, tc.want.source, tc.want.del, tc.why)
			}
			if v.Source == scoring.SourceKnown {
				t.Errorf("source known after an AI outcome, want never (only fingerprints and blocklists are known evidence, DESIGN §7.3)")
			}
			if tc.out == nil && v.AI != nil {
				t.Errorf("AI = %+v with no AI outcome, want nil", *v.AI)
			}
			if tc.out != nil {
				if v.AI == nil {
					t.Fatalf("AI = nil, want the AI outcome recorded (every detection stores its evidence, DESIGN §7.3)")
				}
				if v.AI.Label != tc.out.Label || v.AI.Reason != tc.out.Reason ||
					(v.AI.Confidence != tc.out.Confidence && !(math.IsNaN(v.AI.Confidence) && math.IsNaN(tc.out.Confidence))) {
					t.Errorf("AI = %+v, want %+v recorded unchanged", *v.AI, *tc.out)
				}
			}
			if v.Score.P != before.Score.P {
				t.Errorf("Score.P changed from %v to %v, want unchanged (the AI doesn't rescore)", before.Score.P, v.Score.P)
			}
		})
	}
}

func TestResolveLeavesCertainAndCleanUnchanged(t *testing.T) {
	th := scoring.DefaultThresholds()
	w := scoring.Weights{Version: "v", Bias: -3}
	knownV := scoring.Classify(scoring.Score(w, []scoring.Signal{{Code: "fp.sha256", Weight: 4, Known: true}}), th)
	policyV := scoring.Classify(scoring.Score(w, []scoring.Signal{{Code: "policy.video", Hard: true}}), th)
	cleanV := scoring.Classify(scoring.Score(w, nil), th)
	outs := map[string]*ai.Verdict{
		"nil":            nil,
		"confident spam": {Label: ai.LabelSpam, Confidence: 0.99},
		"confident ham":  {Label: ai.LabelNotSpam, Confidence: 0.99},
		"unsure":         {Label: ai.LabelUnsure, Confidence: 0.4},
	}
	for vname, v := range map[string]scoring.Verdict{"certain known": knownV, "certain policy": policyV, "clean": cleanV} {
		for oname, out := range outs {
			got := scoring.Resolve(v, out, th)
			if got.Band != v.Band || got.Source != v.Source || got.Violation != v.Violation || got.DeletesAsSpam() != v.DeletesAsSpam() {
				t.Errorf("%s + AI %s: got %q/%q violation %v deletes %v, want unchanged %q/%q violation %v deletes %v (the AI only verifies the UNCERTAIN band, DESIGN §7.0, §7.4)",
					vname, oname, got.Band, got.Source, got.Violation, got.DeletesAsSpam(), v.Band, v.Source, v.Violation, v.DeletesAsSpam())
			}
		}
	}
	if !knownV.DeletesAsSpam() {
		t.Errorf("CERTAIN known spam: DeletesAsSpam = false, want true (CERTAIN evidence reaches the delete line, DESIGN §7.3)")
	}
	if policyV.DeletesAsSpam() {
		t.Errorf("policy-only violation: DeletesAsSpam = true, want false (policy is a silent delete by the policy action, never spam, DESIGN §9.3)")
	}
}

func TestDeletesAsSpamOnlyKnownOrAIVerified(t *testing.T) {
	bands := []scoring.Band{scoring.BandClean, scoring.BandCertain, scoring.BandUncertain, scoring.BandStillUncertain}
	sources := []scoring.Source{scoring.SourceNone, scoring.SourceRules, scoring.SourceKnown, scoring.SourceAI}
	for _, b := range bands {
		for _, s := range sources {
			for _, viol := range []bool{false, true} {
				v := scoring.Verdict{Band: b, Source: s, Violation: viol}
				want := (b == scoring.BandCertain && s == scoring.SourceKnown) || (b == scoring.BandUncertain && s == scoring.SourceAI)
				if got := v.DeletesAsSpam(); got != want {
					t.Errorf("band %q source %q violation %v: DeletesAsSpam = %v, want %v (only CERTAIN known evidence or an AI-verified verdict reaches the delete line; heuristics alone never do, DESIGN §7.3)",
						b, s, viol, got, want)
				}
			}
		}
	}
}
