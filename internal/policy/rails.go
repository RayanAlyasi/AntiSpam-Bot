package policy

import (
	"fmt"
	"math"
	"slices"
	"sync"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
)

// AlertKind is a kind of safety-rail alert.
type AlertKind string

// Alert kinds.
const (
	AlertBanCap          AlertKind = "ban_cap"          // bans over the hourly cap went to admins
	AlertDeletionCircuit AlertKind = "deletion_circuit" // deletions over the per-minute limit paused the group
)

// Alert tells admins and the operator that a safety rail tripped (DESIGN.md §8.6).
type Alert struct {
	Group string
	Kind  AlertKind
	At    time.Time
}

// Rails are the per-group safety rails on top of every route (DESIGN.md §8.6, §10.4): the
// hourly ban cap, the deletions-per-minute circuit, and the global and per-rule kill switches.
// They only ever take actions away; they never add a delete or a ban. Safe for concurrent use.
type Rails struct {
	clock clock.Clock

	mu      sync.Mutex
	bans    map[string][]time.Time // admitted bans per group, within the last hour
	deletes map[string][]time.Time // admitted deletions per group, within the last minute
	open    map[string]time.Time   // groups whose deletion circuit is open, since when
	alerted map[string]time.Time   // ban-cap batches already alerted, and when
	global  bool                   // the global kill switch
	killed  map[string]bool        // rule or signal codes switched off
}

// NewRails returns rails that read time from c.
func NewRails(c clock.Clock) *Rails {
	return &Rails{
		clock:   c,
		bans:    map[string][]time.Time{},
		deletes: map[string][]time.Time{},
		open:    map[string]time.Time{},
		alerted: map[string]time.Time{},
		killed:  map[string]bool{},
	}
}

// NoteBans seeds a group's hourly ban count with bans made elsewhere or before a restart
// (from group_bans), so the cap holds across restarts and manual paths.
func (r *Rails) NoteBans(group string, at ...time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.bans[group] = append(r.bans[group], at...)
	slices.SortFunc(r.bans[group], time.Time.Compare)
}

// SetGlobalKill turns the global kill switch on or off. While it is on, nothing is enqueued
// in any group; decisions are still recorded, as shadow.
func (r *Rails) SetGlobalKill(on bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.global = on
}

// KillRule switches a rule (by its rule or signal code) off at once, and ReviveRule switches
// it back on.
func (r *Rails) KillRule(code string) { r.setKilled(code, true) }

// ReviveRule switches a killed rule back on.
func (r *Rails) ReviveRule(code string) { r.setKilled(code, false) }

func (r *Rails) setKilled(code string, on bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if on {
		r.killed[code] = true
	} else {
		delete(r.killed, code)
	}
}

// Killed reports whether a rule is switched off.
func (r *Rails) Killed(code string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.killed[code]
}

// FilterFinding removes the signals and violations of killed rules from a finding without
// knowing the group's thresholds: whatever spam evidence is left (a positive signal that isn't
// an identity prior) goes to admins as still uncertain. Engine.Route uses FilterFindingFor,
// which also takes the killed weights out of the score.
func (r *Rails) FilterFinding(f Finding) Finding {
	return r.filter(f, nil)
}

// FilterFindingFor removes the signals and violations of killed rules from a finding (DESIGN.md
// §8.6: a killed rule stops acting at once). The score is recomputed without the killed
// weights, and the band is re-derived only where the killed items were its evidence:
//   - known spam whose only known evidence was killed,
//   - a CERTAIN violation-only finding whose violations were all killed,
//   - a heuristic (rules) finding that lost any of its signals
//
// each become still uncertain (admins decide) when spam evidence that isn't a prior remains
// and the recomputed score still reaches T_review, and clean otherwise. AI-verified findings
// keep their band: the AI judged the message itself (see harness/deviations/F-076.md).
func (r *Rails) FilterFindingFor(f Finding, s Settings) Finding {
	review := s.Detection.Thresholds.Review
	return r.filter(f, &review)
}

func (r *Rails) filter(f Finding, review *float64) Finding {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.killed) == 0 {
		return f
	}
	out := f
	out.Signals = slices.DeleteFunc(slices.Clone(f.Signals), func(s Signal) bool { return r.killed[s.Code] })
	out.Violations = slices.DeleteFunc(slices.Clone(f.Violations), func(v Violation) bool { return r.killed[v.Rule] })
	if len(out.Signals) == len(f.Signals) && len(out.Violations) == len(f.Violations) {
		return out
	}
	killedWeight, removedKnown, removedSignal := 0.0, false, false
	for _, s := range f.Signals {
		if r.killed[s.Code] {
			killedWeight += s.Weight
			removedSignal = true
			removedKnown = removedKnown || (s.Known && s.Weight > 0)
		}
	}
	if removedSignal {
		out.Score = withoutLogOdds(f.Score, killedWeight)
	}
	evidence, known := false, false
	for _, s := range out.Signals {
		evidence = evidence || (s.Weight > 0 && !s.Prior)
		known = known || (s.Known && s.Weight > 0)
	}
	// What's left is still worth an admin's look only if it is real evidence and, when the
	// group's T_review is known, the remaining score reaches it.
	stillSuspicious := evidence && (review == nil || out.Score >= *review)
	rederive := func() {
		if stillSuspicious {
			out.Band, out.Source = BandStillUncertain, SourceRules
		} else {
			out.Band, out.Source = BandClean, SourceNone
		}
	}
	switch {
	case f.Source == SourceKnown && removedKnown && !known:
		rederive()
	case f.Band == BandCertain && f.Source == SourceNone && len(out.Violations) < len(f.Violations) && len(out.Violations) == 0:
		rederive()
	case f.Source == SourceRules && removedSignal:
		rederive()
	}
	return out
}

// withoutLogOdds returns the probability p with weight taken out of its log-odds. p is
// clamped away from 0 and 1 first so the log-odds stay finite.
func withoutLogOdds(p, weight float64) float64 {
	const eps = 1e-9
	p = min(max(p, eps), 1-eps)
	x := math.Log(p/(1-p)) - weight
	return 1 / (1 + math.Exp(-x))
}

// CircuitOpen reports whether a group's deletion circuit is open.
func (r *Rails) CircuitOpen(group string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.open[group]
	return ok
}

// ResetCircuit closes a group's deletion circuit after an admin or the operator has looked.
func (r *Rails) ResetCircuit(group string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.open, group)
	delete(r.deletes, group)
}

// Admit applies the rails to a plan for group before it is enqueued:
//   - with the global kill switch on or the group's deletion circuit open, nothing is
//     enqueued and the plan is recorded as shadow;
//   - a deletion beyond limits.max_deletions_per_minute opens the circuit (the group pauses,
//     one alert) and nothing from this plan is enqueued;
//   - a ban beyond limits.max_bans_per_hour isn't dropped: it becomes a review proposing the
//     ban, batched per group and hour into one "kick the remaining N?" item (one alert per
//     batch).
//
// Shadow and paused plans pass through untouched.
func (r *Rails) Admit(group string, s Settings, p Plan) (Plan, []Alert) {
	if len(p.Enqueue) == 0 {
		return p, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.clock.Now()
	if _, open := r.open[group]; r.global || open {
		return asShadow(p), nil
	}

	var alerts []Alert
	dels := pruneBefore(r.deletes[group], now.Add(-time.Minute))
	nDel := 0
	for _, a := range p.Enqueue {
		if a.Kind == ActionDelete {
			nDel++
		}
	}
	if nDel > 0 && len(dels)+nDel > s.Limits.MaxDeletionsPerMinute {
		r.open[group] = now
		r.deletes[group] = dels
		return asShadow(p), []Alert{{Group: group, Kind: AlertDeletionCircuit, At: now}}
	}
	for range nDel {
		dels = append(dels, now)
	}
	r.deletes[group] = dels

	bans := pruneBefore(r.bans[group], now.Add(-time.Hour))
	for k, at := range r.alerted {
		if now.Sub(at) > 2*time.Hour {
			delete(r.alerted, k)
		}
	}
	out := p
	out.Enqueue = make([]Action, 0, len(p.Enqueue))
	for _, a := range p.Enqueue {
		if a.Kind != ActionBan {
			out.Enqueue = append(out.Enqueue, a)
			continue
		}
		if len(bans) < s.Limits.MaxBansPerHour {
			bans = append(bans, now)
			out.Enqueue = append(out.Enqueue, a)
			continue
		}
		// Over the cap: admins decide. The batch key starts when the oldest ban in the
		// window leaves it, so every ban deferred in the same stretch joins one item.
		batch := fmt.Sprintf("ban_cap:%s:%d", group, bans[0].Unix())
		out.Enqueue = append(out.Enqueue, Action{Kind: ActionReview, Priority: P3, Review: &ReviewRequest{
			Kind: ReviewBanDecision, ProposeBan: true, Timeout: time.Duration(s.Review.Timeout),
			OnTimeout: TimeoutKeepDeletedNoBan, Batch: batch,
		}})
		if _, done := r.alerted[batch]; !done {
			r.alerted[batch] = now
			alerts = append(alerts, Alert{Group: group, Kind: AlertBanCap, At: now})
		}
	}
	r.bans[group] = bans
	return out, alerts
}

func asShadow(p Plan) Plan {
	p.Enqueue = nil
	p.Mode = DetectionShadow
	return p
}

func pruneBefore(ts []time.Time, cutoff time.Time) []time.Time {
	return slices.DeleteFunc(ts, func(t time.Time) bool { return !t.After(cutoff) })
}
