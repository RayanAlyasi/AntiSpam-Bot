package scoring

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"strings"
)

// Signal is one piece of evidence an analyzer emits (DESIGN.md §6).
type Signal struct {
	Code   string  // stable dotted name, e.g. "url.blocklisted"; the weight table is keyed by it
	Weight float64 // the analyzer's default log-odds contribution (+ spammy, - hammy)
	// Known marks known-spam evidence: an exact or tight near-duplicate fingerprint match, or a
	// blocklisted URL, domain, number, channel, invite or media hash. It makes the verdict
	// CERTAIN with confidence source known (DESIGN.md §7.0, §7.3).
	Known bool
	// Hard marks a policy violation ("videos not allowed"), not a spam probability. Hard signals
	// never enter the spam score (DESIGN.md §7.3, §9.3).
	Hard     bool
	Evidence string // human-readable, shown to admins
}

// Scored is a signal as Score applied it.
type Scored struct {
	Signal
	Applied float64 // the weight used: the table override, else the signal's own; non-finite → 0
	Counted bool    // false for hard signals and for a duplicate code that lost to a stronger one
}

// Result is a scored message: p = sigmoid(Bias + Σ applied weights of counted signals).
type Result struct {
	WeightsVersion string
	Bias           float64
	LogOdds        float64
	P              float64
	Signals        []Scored // every input signal, in input order
}

// maxContribution caps one signal's share of the log-odds sum, so a huge weight can't overflow
// it to ±Inf (and Inf - Inf to NaN). sigmoid(±1e6) is already exactly 1 or 0.
const maxContribution = 1e6

// Score combines signals into a spam probability under w (DESIGN.md §7.3). Each code counts
// once: when several signals share a code, the one with the largest |applied weight| wins, so
// two blocklisted URLs in one message don't double the evidence. The result doesn't depend on
// the order of signals.
func Score(w Weights, signals []Signal) Result {
	r := Result{WeightsVersion: w.Version, Bias: finiteOr0(w.Bias), Signals: make([]Scored, len(signals))}
	best := make(map[string]int, len(signals)) // code → index of the counted signal
	for i, s := range signals {
		applied := s.Weight
		if o, ok := w.Signals[s.Code]; ok {
			applied = o
		}
		r.Signals[i] = Scored{Signal: s, Applied: finiteOr0(applied)}
		if s.Hard {
			continue
		}
		if j, ok := best[s.Code]; !ok || stronger(r.Signals[i], r.Signals[j]) {
			best[s.Code] = i
		}
	}

	// Sum in code order so the floating-point result is the same for any input order.
	codes := make([]string, 0, len(best))
	for code, i := range best {
		r.Signals[i].Counted = true
		codes = append(codes, code)
	}
	slices.Sort(codes)
	r.LogOdds = r.Bias
	for _, code := range codes {
		r.LogOdds += max(-maxContribution, min(maxContribution, r.Signals[best[code]].Applied))
	}
	r.P = sigmoid(r.LogOdds)
	return r
}

// stronger reports whether a should be counted instead of b for the same code: the larger
// |weight| wins; ties go to the spammier weight, then to known evidence, then to the evidence
// text, so the choice never depends on input order.
func stronger(a, b Scored) bool {
	if c := cmp.Compare(math.Abs(a.Applied), math.Abs(b.Applied)); c != 0 {
		return c > 0
	}
	if a.Applied != b.Applied {
		return a.Applied > b.Applied
	}
	if a.Known != b.Known {
		return a.Known
	}
	return a.Evidence < b.Evidence
}

// Explain renders the signals behind a result for admins, e.g. "link to a domain registered
// 3 days ago (+2.1), sender joined 4 minutes ago (+0.8)". Counted signals come first, by applied
// weight descending, then policy violations as "<evidence> (policy)". The caller adds the
// leading phrase ("Deleted because: …") in the admin's language.
func (r Result) Explain() string {
	counted := make([]Scored, 0, len(r.Signals))
	var hard []Scored
	for _, s := range r.Signals {
		switch {
		case s.Hard:
			hard = append(hard, s)
		case s.Counted:
			counted = append(counted, s)
		}
	}
	slices.SortStableFunc(counted, func(a, b Scored) int {
		if c := cmp.Compare(b.Applied, a.Applied); c != 0 {
			return c
		}
		return cmp.Compare(a.Code, b.Code)
	})
	parts := make([]string, 0, len(counted)+len(hard))
	for _, s := range counted {
		parts = append(parts, fmt.Sprintf("%s (%+.1f)", s.label(), s.Applied))
	}
	for _, s := range hard {
		parts = append(parts, s.label()+" (policy)")
	}
	return strings.Join(parts, ", ")
}

func (s Scored) label() string {
	if s.Evidence != "" {
		return s.Evidence
	}
	return s.Code
}

// sigmoid is the logistic function, computed so that neither branch overflows.
func sigmoid(x float64) float64 {
	if x >= 0 {
		return 1 / (1 + math.Exp(-x))
	}
	e := math.Exp(x)
	return e / (1 + e)
}

func finite(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }

func finiteOr0(f float64) float64 {
	if finite(f) {
		return f
	}
	return 0
}
