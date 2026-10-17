package detect

import (
	"context"
	"fmt"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/scoring"
)

// Tier is a detection tier (DESIGN.md §7.1). Tiers run in order, cheapest first.
type Tier int

// Tiers.
const (
	T0 Tier = iota // policy and exact matches: < 1 ms, no I/O beyond cache
	T1             // cheap heuristics: < 5 ms, cached I/O only
	T2             // enrichment: network, rate-limited and cached
	T3             // text AI
	T4             // vision AI
)

// FastLane is the last tier of the fast lane (T0–T1). From T2 on, a clean score stops the run.
const FastLane = T1

// Valid reports whether t is one of T0–T4.
func (t Tier) Valid() bool { return t >= T0 && t <= T4 }

func (t Tier) String() string {
	if !t.Valid() {
		return fmt.Sprintf("Tier(%d)", int(t))
	}
	return fmt.Sprintf("T%d", int(t))
}

// Signal is one piece of evidence with a log-odds weight; see scoring.Signal.
type Signal = scoring.Signal

// State is what an analyzer may know about the message beyond its envelope: the signals that
// earlier tiers emitted. It's shared by the analyzers of one tier and must not be modified.
type State struct {
	Signals []Signal
}

// Analyzer is one small, independent detection unit (DESIGN.md §6). E is the envelope type and
// P the group-policy type.
//
// Applies must be cheap and side-effect free. Analyze must respect ctx: the runner gives each
// tier a time budget and drops the signals of an analyzer that misses it. An analyzer reports
// what it found; it never acts.
type Analyzer[E, P any] interface {
	Name() string // unique, stable: used in config enable flags, logs and metrics
	Tier() Tier
	Applies(e *E, p *P) bool
	Analyze(ctx context.Context, e *E, s *State) ([]Signal, error)
}
