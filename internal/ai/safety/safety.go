// Package safety enforces the early-AI safety rule (DESIGN.md §9.2, §18): a ban resting on the
// AI's verdict (source ai) routes to "ask admins" in every preset until the AI has a track
// record on this instance, and even then only where a matrix cell explicitly lists ai. A
// near-duplicate below the tight CERTAIN thresholds (source rules) never acts on its own.
//
// The decision engine (internal/policy) accepts ai in a cell's require_source; this package is
// the gate in front of it. Callers decide through Decide instead of policy.Decide, and the
// commands and website ask AllowAISource before accepting an "auto … ai" cell.
package safety

import (
	"maps"
	"slices"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/policy"
)

// TrackRecord reports whether the AI source has earned auto-bans on this instance: at least 200
// admin decisions on reviews it decided, ≥ 98% confirmed as spam over 30 days, plus measured
// shadow-mode precision (DESIGN.md §9.2). Track-record tracking lands in v0.4.
type TrackRecord interface {
	AIMet() bool
}

// Unmet is the v0.1 track record: never met, so AI evidence never auto-bans.
type Unmet struct{}

// AIMet implements TrackRecord.
func (Unmet) AIMet() bool { return false }

// AllowAISource reports whether a matrix cell may list ai as an auto-ban source (the website
// greys it out and `.autoban … auto ai` is refused until this is true).
func AllowAISource(tr TrackRecord) bool { return tr != nil && tr.AIMet() }

// Settings returns s with ai removed from every auto-ban cell's require_source unless the track
// record is met. The input is not modified. A cell that listed only ai can't auto-ban on
// anything any more, so it becomes its else action (ask when none is set): known evidence keeps
// exactly the outcome the group configured for it.
func Settings(s policy.Settings, tr TrackRecord) policy.Settings {
	if AllowAISource(tr) {
		return s
	}
	m := maps.Clone(s.Enforcement.AutobanMatrix)
	for t, c := range m {
		if !slices.Contains(c.RequireSource, policy.SourceAI) {
			continue
		}
		c.RequireSource = slices.DeleteFunc(slices.Clone(c.RequireSource), func(src policy.Source) bool { return src == policy.SourceAI })
		if len(c.RequireSource) == 0 {
			// An empty list would mean the policy default [known]; this cell never allowed that.
			els := c.Else
			if els == "" || els == policy.CellAutoBan {
				els = policy.CellAsk
			}
			c = policy.Cell{Action: els}
		}
		m[t] = c
	}
	s.Enforcement.AutobanMatrix = m
	return s
}

// Decide is policy.Decide with the early-AI safety rule applied. Whatever the settings say, a
// finding whose source is ai never bans automatically while the track record is unmet: the
// message is still deleted where policy deletes it, and the ban goes to admins as a review that
// can't ban on timeout.
func Decide(s policy.Settings, f policy.Finding, st policy.Standing, tr TrackRecord) policy.Decision {
	d := policy.Decide(Settings(s, tr), f, st)
	if f.Source != policy.SourceAI || AllowAISource(tr) {
		return d
	}
	if d.Review != nil && d.Review.OnTimeout == policy.TimeoutAutoBan {
		r := *d.Review
		r.OnTimeout = policy.TimeoutKeepDeletedNoBan // nor through a review timing out
		d.Review = &r
	}
	if !d.Ban {
		return d
	}
	// Defence in depth: policy.Decide only bans on ai through a cell listing it, which Settings
	// removed, so this is reached only if the engine gains another path to an ai ban.
	d.Ban, d.Route = false, policy.RouteAsk
	d.Reason = "AI-verified spam: deleted, admins decide the ban until the AI has a track record"
	d.Review = &policy.ReviewRequest{
		Kind: policy.ReviewBanDecision, ProposeBan: true,
		Timeout: time.Duration(s.Review.Timeout), OnTimeout: policy.TimeoutKeepDeletedNoBan,
	}
	return d
}
