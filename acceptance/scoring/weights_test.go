package scoring_test

import (
	"math"
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/scoring"
)

func TestParseWeightsAcceptsValidFile(t *testing.T) {
	data := []byte(`version: "2026-10-a"
bias: -2.5
signals:
  link.new_domain: 2.1
  sender.just_joined: 0.8
  domain.established: -1
`)
	w, err := scoring.ParseWeights(data)
	if err != nil {
		t.Fatalf("ParseWeights(valid file) error = %v, want nil", err)
	}
	if w.Version != "2026-10-a" || w.Bias != -2.5 {
		t.Errorf("got version %q bias %v, want %q and -2.5", w.Version, w.Bias, "2026-10-a")
	}
	want := map[string]float64{"link.new_domain": 2.1, "sender.just_joined": 0.8, "domain.established": -1}
	if len(w.Signals) != len(want) {
		t.Errorf("got %d signal weights, want %d: %v", len(w.Signals), len(want), w.Signals)
	}
	for k, v := range want {
		if got, ok := w.Signals[k]; !ok || got != v {
			t.Errorf("Signals[%q] = %v (present %v), want %v", k, got, ok, v)
		}
	}
	if r := scoring.Score(w, nil); r.WeightsVersion != "2026-10-a" {
		t.Errorf("Score().WeightsVersion = %q, want the parsed file's version (DESIGN §7.3: weights are versioned)", r.WeightsVersion)
	}
}

func TestParseWeightsRejectsInvalid(t *testing.T) {
	cases := map[string]string{
		"missing version":    "bias: -2\nsignals:\n  a: 1\n",
		"empty version":      "version: \"\"\nbias: -2\n",
		"NaN bias":           "version: v1\nbias: .nan\n",
		"infinite bias":      "version: v1\nbias: .inf\n",
		"NaN signal weight":  "version: v1\nsignals:\n  a: .nan\n",
		"-Inf signal weight": "version: v1\nsignals:\n  a: -.inf\n",
		"empty signal code":  "version: v1\nsignals:\n  \"\": 1\n",
		"unknown top-level":  "version: v1\nbias: -2\nthreshold: 0.6\n",
		"non-numeric weight": "version: v1\nsignals:\n  a: high\n",
		"signals not a map":  "version: v1\nsignals: 3\n",
		"malformed yaml":     "version: [v1\n",
	}
	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			if w, err := scoring.ParseWeights([]byte(doc)); err == nil {
				t.Errorf("ParseWeights(%q) = %+v, nil; want an error (a bad weight file must be refused, not silently score messages)", doc, w)
			}
		})
	}
}

func TestDefaultWeightsVersionedAndCleanByDefault(t *testing.T) {
	w := scoring.DefaultWeights()
	if w.Version == "" {
		t.Fatalf("DefaultWeights().Version is empty, want a version (the weight file is versioned, DESIGN §7.3)")
	}
	if math.IsNaN(w.Bias) || math.IsInf(w.Bias, 0) {
		t.Errorf("DefaultWeights().Bias = %v, want finite", w.Bias)
	}
	for code, v := range w.Signals {
		if code == "" || math.IsNaN(v) || math.IsInf(v, 0) {
			t.Errorf("DefaultWeights() has entry %q = %v, want a non-empty code with a finite weight", code, v)
		}
	}
	r := scoring.Score(w, nil)
	if r.WeightsVersion != w.Version {
		t.Errorf("Score(DefaultWeights()).WeightsVersion = %q, want %q", r.WeightsVersion, w.Version)
	}
	if v := scoring.Classify(r, scoring.DefaultThresholds()); v.Band != scoring.BandClean {
		t.Errorf("a message with no signals got band %q (P = %v), want clean (messages with no suspicious signal never reach the AI, DESIGN §7.4)", v.Band, r.P)
	}
}

func TestDefaultThresholdsAndValidate(t *testing.T) {
	d := scoring.DefaultThresholds()
	if d.Review != 0.60 || d.AIConfidence != 0.9 {
		t.Fatalf("DefaultThresholds() = %+v, want Review 0.60 and AIConfidence 0.9 (DESIGN §7.3 defaults)", d)
	}
	if err := d.Validate(); err != nil {
		t.Errorf("DefaultThresholds().Validate() = %v, want nil", err)
	}
	cases := []struct {
		name string
		th   scoring.Thresholds
		ok   bool
	}{
		{"low review", scoring.Thresholds{Review: 0.01, AIConfidence: 0.9}, true},
		{"ai confidence 1", scoring.Thresholds{Review: 0.6, AIConfidence: 1}, true},
		{"review 0", scoring.Thresholds{Review: 0, AIConfidence: 0.9}, false},
		{"review 1", scoring.Thresholds{Review: 1, AIConfidence: 0.9}, false},
		{"review negative", scoring.Thresholds{Review: -0.1, AIConfidence: 0.9}, false},
		{"review NaN", scoring.Thresholds{Review: math.NaN(), AIConfidence: 0.9}, false},
		{"review Inf", scoring.Thresholds{Review: math.Inf(1), AIConfidence: 0.9}, false},
		{"ai confidence 0", scoring.Thresholds{Review: 0.6, AIConfidence: 0}, false},
		{"ai confidence above 1", scoring.Thresholds{Review: 0.6, AIConfidence: 1.0001}, false},
		{"ai confidence NaN", scoring.Thresholds{Review: 0.6, AIConfidence: math.NaN()}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.th.Validate()
			if tc.ok && err != nil {
				t.Errorf("Validate(%+v) = %v, want nil", tc.th, err)
			}
			if !tc.ok && err == nil {
				t.Errorf("Validate(%+v) = nil, want an error (need finite 0 < Review < 1 and 0 < AIConfidence <= 1)", tc.th)
			}
		})
	}
}
