package detect

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/scoring"
)

// DefaultBudgets are the per-tier deadlines. The fast tiers get about ten times their DESIGN.md
// §7.1 targets (< 1 ms, < 5 ms), so a busy machine doesn't drop their signals; the slow tiers
// get their documented upper bound (the AI's own timeout is configured separately, §7.4).
func DefaultBudgets() map[Tier]time.Duration {
	return map[Tier]time.Duration{
		T0: 10 * time.Millisecond,
		T1: 50 * time.Millisecond,
		T2: 3 * time.Second,
		T3: 5 * time.Second,
		T4: 10 * time.Second,
	}
}

// Config configures a Runner. Zero values take the defaults.
type Config struct {
	Weights    scoring.Weights
	Thresholds scoring.Thresholds
	Budgets    map[Tier]time.Duration // missing tiers use DefaultBudgets
}

// Runner runs a registry's analyzers tier by tier (DESIGN.md §7.1). It's safe for concurrent use.
type Runner[E, P any] struct {
	reg     *Registry[E, P]
	weights scoring.Weights
	th      scoring.Thresholds
	budgets map[Tier]time.Duration
}

// NewRunner returns a runner over reg. Weights without a version use scoring.DefaultWeights and zero
// Thresholds use scoring.DefaultThresholds; invalid thresholds or a non-positive budget are errors.
func NewRunner[E, P any](reg *Registry[E, P], cfg Config) (*Runner[E, P], error) {
	if reg == nil {
		return nil, errors.New("new runner: nil registry")
	}
	r := &Runner[E, P]{reg: reg, weights: cfg.Weights, th: cfg.Thresholds, budgets: DefaultBudgets()}
	if r.weights.Version == "" {
		r.weights = scoring.DefaultWeights()
	}
	if r.th == (scoring.Thresholds{}) {
		r.th = scoring.DefaultThresholds()
	}
	if err := r.th.Validate(); err != nil {
		return nil, fmt.Errorf("new runner: %w", err)
	}
	for t, d := range cfg.Budgets {
		if !t.Valid() || d <= 0 {
			return nil, fmt.Errorf("new runner: budget %v for %v: want a positive duration for T0–T4", d, t)
		}
		r.budgets[t] = d
	}
	return r, nil
}

// Lane picks which tiers one Run covers and the signals it starts from: the fast lane runs
// T0–T1 synchronously, and the slow lane continues with T2–T4 from the fast lane's signals.
type Lane struct {
	From, To Tier
	Prior    []Signal // signals from an earlier run, e.g. the fast lane's
}

// Fast is the fast lane, T0–T1.
func Fast() Lane { return Lane{From: T0, To: T1} }

// Slow is the slow lane, T2–T4, continuing from the fast lane's signals.
func Slow(prior []Signal) Lane { return Lane{From: T2, To: T4, Prior: prior} }

// StopReason says why a run ended before its last tier.
type StopReason string

// Stop reasons.
const (
	StopNone    StopReason = ""        // every tier in the lane ran
	StopCertain StopReason = "certain" // known spam or a hard policy violation
	StopClean   StopReason = "clean"   // below T_review once the fast lane was done
)

// Failure is an analyzer that errored, panicked or missed its tier's budget. Its signals were
// dropped; nothing else changed.
type Failure struct {
	Analyzer string
	Tier     Tier
	Err      error
}

// ErrBudget is the Failure.Err of an analyzer that returned after its tier's budget ran out.
var ErrBudget = errors.New("analyzer missed its tier budget")

// Outcome is the result of a Run.
type Outcome struct {
	Signals  []Signal        // Lane.Prior plus every signal this run kept, tier by tier
	Verdict  scoring.Verdict // scoring.Classify of Signals; Verdict.Score holds the score
	Ran      []Tier          // tiers whose analyzers ran
	Stop     StopReason
	Failures []Failure
}

// Run executes the lane's tiers in order. Within a tier the applicable, enabled analyzers run
// concurrently under the tier's budget, and their signals are merged in registration order, so
// the outcome is deterministic. The run stops early when the signals so far are CERTAIN, or when
// they score clean and the fast lane is done (DESIGN.md §7.1).
//
// A failing analyzer only loses its own signals and is listed in Outcome.Failures; a failure
// never adds evidence. Run returns ctx.Err() if ctx ends, with the outcome so far.
func (r *Runner[E, P]) Run(ctx context.Context, e *E, p *P, lane Lane) (Outcome, error) {
	if !lane.From.Valid() || !lane.To.Valid() || lane.From > lane.To {
		return Outcome{}, fmt.Errorf("run: invalid lane %v–%v", lane.From, lane.To)
	}
	out := Outcome{Signals: slices.Clone(lane.Prior)}
	for t := lane.From; t <= lane.To; t++ {
		v := r.classify(out.Signals)
		if v.Band == scoring.BandCertain {
			out.Stop = StopCertain
			break
		}
		if t > FastLane && v.Band == scoring.BandClean {
			out.Stop = StopClean
			break
		}
		if err := ctx.Err(); err != nil {
			out.Verdict = v
			return out, err
		}
		sigs, fails := r.runTier(ctx, t, e, p, &State{Signals: slices.Clone(out.Signals)})
		out.Ran = append(out.Ran, t)
		out.Signals = append(out.Signals, sigs...)
		out.Failures = append(out.Failures, fails...)
	}
	out.Verdict = r.classify(out.Signals)
	return out, ctx.Err()
}

func (r *Runner[E, P]) classify(sigs []Signal) scoring.Verdict {
	return scoring.Classify(scoring.Score(r.weights, sigs), r.th)
}

type tierResult struct {
	signals []Signal
	err     error
}

func (r *Runner[E, P]) runTier(ctx context.Context, t Tier, e *E, p *P, s *State) ([]Signal, []Failure) {
	var (
		run   []Analyzer[E, P]
		fails []Failure
	)
	for _, a := range r.reg.tier(t) {
		ok, err := applies(a, e, p)
		if err != nil {
			fails = append(fails, Failure{Analyzer: a.Name(), Tier: t, Err: err})
		} else if ok {
			run = append(run, a)
		}
	}
	if len(run) == 0 {
		return nil, fails
	}

	tctx, cancel := context.WithTimeout(ctx, r.budgets[t])
	defer cancel()
	results := make([]tierResult, len(run))
	var wg sync.WaitGroup
	for i, a := range run {
		wg.Go(func() { results[i] = analyze(tctx, a, e, s) })
	}
	wg.Wait()

	var sigs []Signal
	for i, res := range results {
		if res.err != nil {
			fails = append(fails, Failure{Analyzer: run[i].Name(), Tier: t, Err: res.err})
			continue
		}
		sigs = append(sigs, res.signals...)
	}
	return sigs, fails
}

func applies[E, P any](a Analyzer[E, P], e *E, p *P) (ok bool, err error) {
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("applies panicked: %v", v)
		}
	}()
	return a.Applies(e, p), nil
}

// analyze runs one analyzer, turning a panic into an error and a late answer into ErrBudget.
func analyze[E, P any](ctx context.Context, a Analyzer[E, P], e *E, s *State) (res tierResult) {
	defer func() {
		if v := recover(); v != nil {
			res = tierResult{err: fmt.Errorf("analyze panicked: %v", v)}
		}
	}()
	sigs, err := a.Analyze(ctx, e, s)
	switch {
	case ctx.Err() != nil:
		return tierResult{err: fmt.Errorf("%w: %w", ErrBudget, ctx.Err())}
	case err != nil:
		return tierResult{err: err}
	}
	return tierResult{signals: sigs}
}
