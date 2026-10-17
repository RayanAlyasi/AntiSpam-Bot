package scoring

import "github.com/RayanAlyasi/AntiSpam-Bot/internal/ai"

// Stub for F-050/F-051: declares only the exported names and signatures from the contract
// (.agent/contracts/F-040.md). The builder replaces this file.

// Signal is one piece of evidence with a log-odds weight (positive = spammy).
type Signal struct {
	Code     string
	Weight   float64
	Known    bool
	Hard     bool
	Evidence string
}

// Source is the confidence source of a verdict (DESIGN.md §7.3).
type Source string

// Confidence sources.
const (
	SourceNone  Source = ""
	SourceRules Source = "rules"
	SourceKnown Source = "known"
	SourceAI    Source = "ai"
)

// Weights is a versioned weight table.
type Weights struct {
	Version string
	Bias    float64
	Signals map[string]float64
}

// ParseWeights parses a YAML weight file.
func ParseWeights(data []byte) (Weights, error) { panic("not implemented") }

// DefaultWeights returns the embedded, versioned default weights.
func DefaultWeights() Weights { panic("not implemented") }

// Scored is a signal as it was applied by Score.
type Scored struct {
	Signal
	Applied float64
	Counted bool
}

// Result is the outcome of Score.
type Result struct {
	WeightsVersion string
	Bias           float64
	LogOdds        float64
	P              float64
	Signals        []Scored
}

// Score computes p = sigmoid(bias + Σ applied weights).
func Score(w Weights, signals []Signal) Result { panic("not implemented") }

// Explain renders the human explanation of a result.
func (r Result) Explain() string { panic("not implemented") }

// Thresholds are the per-group decision thresholds.
type Thresholds struct {
	Review       float64
	AIConfidence float64
}

// DefaultThresholds returns T_review 0.60 and ai.spam_confidence 0.9.
func DefaultThresholds() Thresholds { panic("not implemented") }

// Validate reports whether the thresholds are usable.
func (t Thresholds) Validate() error { panic("not implemented") }

// Band is a certainty band (DESIGN.md §7.0).
type Band string

// Bands.
const (
	BandClean          Band = "clean"
	BandCertain        Band = "certain"
	BandUncertain      Band = "uncertain"
	BandStillUncertain Band = "still_uncertain"
)

// Verdict is a band plus its confidence source.
type Verdict struct {
	Band      Band
	Source    Source
	Violation bool
	Score     Result
	AI        *ai.Verdict
}

// Classify assigns a band before the AI runs.
func Classify(r Result, t Thresholds) Verdict { panic("not implemented") }

// Resolve applies the AI outcome to an UNCERTAIN verdict.
func Resolve(v Verdict, out *ai.Verdict, t Thresholds) Verdict { panic("not implemented") }

// DeletesAsSpam reports whether the verdict reaches the delete line as spam.
func (v Verdict) DeletesAsSpam() bool { panic("not implemented") }
