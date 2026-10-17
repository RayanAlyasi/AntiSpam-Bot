package scoring_test

import (
	"fmt"
	"math"
	"math/rand/v2"
	"strings"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/scoring"
)

const eps = 1e-9

func sigmoid(x float64) float64 { return 1 / (1 + math.Exp(-x)) }

func near(a, b float64) bool { return math.Abs(a-b) <= eps }

// propIters is the number of generated cases per property test. The generator is seeded,
// so a failure reproduces from the printed seed and iteration.
const propIters = 3000

func newRand(seed uint64) *rand.Rand { return rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15)) }

// genWeights builds a random weight table: any bias, overrides for some codes (including 0 and negative).
func genWeights(r *rand.Rand) scoring.Weights {
	w := scoring.Weights{Version: "prop-v1", Bias: r.Float64()*16 - 10, Signals: map[string]float64{}}
	for i := range 8 {
		if r.IntN(3) == 0 {
			w.Signals[fmt.Sprintf("c%d", i)] = r.Float64()*20 - 8
		}
	}
	return w
}

// genHeuristics builds 0..10 signals that are neither Known nor Hard (rules-only evidence).
func genHeuristics(r *rand.Rand) []scoring.Signal {
	n := r.IntN(11)
	out := make([]scoring.Signal, 0, n)
	for range n {
		wt := r.Float64()*20 - 8
		if r.IntN(15) == 0 {
			wt = 60 // a heuristic far beyond any threshold
		}
		code := fmt.Sprintf("c%d", r.IntN(8))
		out = append(out, scoring.Signal{Code: code, Weight: wt, Evidence: "heuristic " + code})
	}
	return out
}

// genAny builds signals that may be Known or Hard.
func genAny(r *rand.Rand) []scoring.Signal {
	out := genHeuristics(r)
	for i := range out {
		out[i].Known = r.IntN(6) == 0
		out[i].Hard = r.IntN(8) == 0
	}
	return out
}

func genThresholds(r *rand.Rand) scoring.Thresholds {
	return scoring.Thresholds{Review: 0.01 + r.Float64()*0.98, AIConfidence: 0.5 + r.Float64()*0.5}
}

// genNotConfidentSpam returns any AI outcome except "spam with confidence >= AIConfidence".
func genNotConfidentSpam(r *rand.Rand, t scoring.Thresholds) (*ai.Verdict, string) {
	switch r.IntN(8) {
	case 0:
		return nil, "nil (AI off, budget, timeout or error)"
	case 1:
		c := r.Float64()
		return &ai.Verdict{Label: ai.LabelUnsure, Confidence: c}, fmt.Sprintf("unsure %.3f", c)
	case 2:
		c := r.Float64()
		return &ai.Verdict{Label: ai.LabelNotSpam, Confidence: c}, fmt.Sprintf("not_spam %.3f", c)
	case 3:
		c := r.Float64() * t.AIConfidence * 0.999
		return &ai.Verdict{Label: ai.LabelSpam, Confidence: c}, fmt.Sprintf("spam low %.4f", c)
	case 4:
		return &ai.Verdict{Label: ai.LabelSpam, Confidence: math.NaN()}, "spam NaN confidence"
	case 5:
		return &ai.Verdict{Label: ai.LabelSpam, Confidence: 1.5}, "spam out-of-range confidence 1.5"
	case 6:
		return &ai.Verdict{Label: "SPAM", Confidence: 0.99}, "unknown label SPAM"
	default:
		return &ai.Verdict{Label: ai.LabelSpam, Confidence: -0.5}, "spam negative confidence"
	}
}

func describe(sigs []scoring.Signal) string {
	parts := make([]string, len(sigs))
	for i, s := range sigs {
		parts[i] = fmt.Sprintf("{%s %.3f known=%v hard=%v}", s.Code, s.Weight, s.Known, s.Hard)
	}
	return "[" + strings.Join(parts, " ") + "]"
}

// uncertainVerdict returns a rules-only verdict that Classify puts in the UNCERTAIN band
// (P = sigmoid(2) ≈ 0.88 ≥ T_review 0.60).
func uncertainVerdict() scoring.Verdict {
	w := scoring.Weights{Version: "t1", Bias: 0}
	r := scoring.Score(w, []scoring.Signal{{Code: "link.new_domain", Weight: 2, Evidence: "new domain"}})
	return scoring.Classify(r, scoring.DefaultThresholds())
}
