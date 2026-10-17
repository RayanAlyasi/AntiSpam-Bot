package policy

import (
	"time"
)

// Band is a certainty band (DESIGN.md §7.0).
type Band string

// Bands.
const (
	BandClean          Band = "clean"
	BandCertain        Band = "certain"
	BandUncertain      Band = "uncertain"
	BandStillUncertain Band = "still_uncertain"
)

// Role is a member's role in the group.
type Role string

// Roles.
const (
	RoleMember     Role = "member"
	RoleAdmin      Role = "admin"
	RoleSuperAdmin Role = "superadmin"
)

// Signal is one piece of scoring evidence as the decision engine sees it.
type Signal struct {
	Code   string
	Weight float64
	Prior  bool // a §7.6 identity prior: never enough on its own
	Known  bool // known-spam evidence (confidence source known)
}

// Violation is a hard content-rule violation (DESIGN.md §9.3).
type Violation struct {
	Rule   string // the dotted content key, e.g. "content.videos"
	Detail string
}

// Finding is what detection concluded about one message: its band and confidence source
// from scoring, and the content-rule violations evaluated separately from spam.
type Finding struct {
	Band       Band
	Source     Source
	Score      float64
	Type       ContentType
	Signals    []Signal
	Violations []Violation
	At         time.Time
}

// Standing is who sent the message, as far as routing cares (DESIGN.md §8.1). It never
// changes the score; it only decides who decides the ban.
type Standing struct {
	Role             Role
	IsBot            bool
	Allowlisted      bool
	Established      bool
	Compromised      bool // possibly hacked (DESIGN.md §8.7)
	New              bool // joined within the just-joined window (for block_for_new)
	Trust            float64
	RecentViolations []time.Time // earlier violations in this group, for policy.repeat
}

// Route is the decision route, as stored in detections.route (DESIGN.md §14).
type Route string

// Routes.
const (
	RouteNone       Route = "none"
	RouteAutoBan    Route = "auto_ban"
	RouteAsk        Route = "ask"
	RouteDeleteOnly Route = "delete_only"
	RouteAllow      Route = "allow"
	RoutePolicy     Route = "policy"
)

// Verdict is what the message was judged to be, as stored in detections.verdict.
type Verdict string

// Verdicts.
const (
	VerdictSpam   Verdict = "spam"
	VerdictPolicy Verdict = "policy"
	VerdictClean  Verdict = "clean"
)

// ReviewKind is the kind of admin review a decision asks for (DESIGN.md §9.5, reviews.kind).
type ReviewKind string

// Review kinds.
const (
	ReviewBanDecision ReviewKind = "ban_decision"
	ReviewUncertain   ReviewKind = "uncertain"
	ReviewHacked      ReviewKind = "hacked"
	ReviewAskBefore   ReviewKind = "ask_before"
	ReviewAlert       ReviewKind = "alert"
)

// ReviewRequest asks the admins to decide. OnTimeout is what happens if nobody answers.
type ReviewRequest struct {
	Kind       ReviewKind
	ProposeBan bool
	Timeout    time.Duration
	OnTimeout  TimeoutAction
	// Batch, when set, merges every request with the same value into one review item, such
	// as the "kick the remaining N?" item for bans over the hourly cap.
	Batch string
}

// Decision is the route chosen for one message and what it does.
type Decision struct {
	Verdict Verdict
	Route   Route
	Delete  bool           // the message is deleted now
	Ban     bool           // the sender is banned automatically (kick + rejoin block)
	Review  *ReviewRequest // nil when admins aren't asked
	Reason  string
}

// isSpam reports whether the finding is spam the bot may act on: CERTAIN known spam, or an
// UNCERTAIN message the AI verified. Heuristics (rules), anything still uncertain, and any
// other band/source combination never count.
func isSpam(f Finding) bool {
	switch f.Source {
	case SourceKnown:
		return f.Band == BandCertain
	case SourceAI:
		return f.Band == BandUncertain
	default:
		return false
	}
}

// priorsOnly reports whether every signal that pushes towards spam is an identity prior.
// Priors are never enough on their own (DESIGN.md §7.6): a finding that isn't known or
// AI-verified spam and rests only on priors is not acted on at all.
func priorsOnly(f Finding) bool {
	n := 0
	for _, s := range f.Signals {
		if s.Weight <= 0 {
			continue
		}
		if !s.Prior {
			return false
		}
		n++
	}
	return n > 0
}

// Decide maps a finding, the sender's standing and the group's settings to one route of the
// §8.3 table. The guarantees, whatever the settings say:
//   - the bot, admins and allow-listed members are never deleted or banned (a possibly hacked
//     admin only raises an alert);
//   - ask modes, established members and possibly hacked accounts are never auto-banned, and
//     no review they get can ban on timeout;
//   - heuristics, identity priors and still-uncertain verdicts never delete (unless the group
//     opted in to delete while pending) and never ban;
//   - a content-rule violation alone is a silent delete unless the group opted in to more;
//   - a message that is both spam and a violation follows the spam route.
//
// Call it once the AI tier has resolved the band. Shadow and paused modes are applied
// afterwards by PlanFor, and the safety rails by Rails.Admit.
func Decide(s Settings, f Finding, st Standing) Decision {
	if st.IsBot {
		return Decision{Verdict: VerdictClean, Route: RouteNone, Reason: "the bot itself is never acted on"}
	}
	spam := isSpam(f)
	verdict := VerdictClean
	switch {
	case spam:
		verdict = VerdictSpam
	case len(f.Violations) > 0:
		verdict = VerdictPolicy
	}

	if st.Role == RoleAdmin || st.Role == RoleSuperAdmin {
		if spam && st.Compromised && s.Compromised.Enabled {
			// A possibly hacked admin is never acted on: the other admins get an alert
			// (DESIGN.md §8.7). compromised.admins: delete_known_spam isn't honoured yet; the
			// exemption of admins wins (see harness/deviations/F-059.md).
			return Decision{Verdict: verdict, Route: RouteNone, Reason: "possibly hacked admin: alert the other admins only",
				Review: &ReviewRequest{Kind: ReviewAlert, Timeout: time.Duration(s.Review.Timeout), OnTimeout: TimeoutDismiss}}
		}
		return Decision{Verdict: verdict, Route: RouteNone, Reason: "admins are never acted on"}
	}
	if st.Allowlisted {
		return Decision{Verdict: verdict, Route: RouteNone, Reason: "allow-listed members are never acted on"}
	}

	switch {
	case spam:
		return decideSpam(s, f, st)
	case len(f.Violations) > 0:
		return decideViolation(s, f, st)
	case f.Band == BandClean || f.Band == "":
		return Decision{Verdict: VerdictClean, Route: RouteNone, Reason: "not suspicious"}
	case priorsOnly(f):
		return Decision{Verdict: VerdictClean, Route: RouteNone, Reason: "identity priors alone never act"}
	default:
		return decideUncertain(s, f)
	}
}

func decideSpam(s Settings, f Finding, st Standing) Decision {
	c, ok := s.Enforcement.AutobanMatrix[f.Type]
	if !ok {
		c = Cell{Action: CellAsk} // a type the matrix doesn't list: admins decide
	}
	cellAction := c.Effective(f.Source)
	d := Decision{Verdict: VerdictSpam}
	switch {
	case cellAction == CellAllow:
		d.Route, d.Reason = RouteAllow, "spam, but the matrix allows "+string(f.Type)+": recorded only"
	case s.Enforcement.Mode == EnforceAskBeforeAction:
		d.Route, d.Reason = RouteAsk, "ask_before_action: nothing happens until an admin decides"
		d.Review = askBefore(s, cellAction != CellDeleteOnly)
	case cellAction == CellDeleteOnly:
		d.Route, d.Delete, d.Reason = RouteDeleteOnly, true, "spam deleted; the matrix never bans for "+string(f.Type)
	case st.Compromised && s.Compromised.Enabled:
		d.Route, d.Delete, d.Reason = RouteAsk, true, "possibly hacked account: spam deleted, admins decide"
		d.Review = &ReviewRequest{Kind: ReviewHacked, ProposeBan: true, Timeout: time.Duration(s.Review.Timeout), OnTimeout: TimeoutKeepDeletedNoBan}
	case st.Compromised:
		// The group turned the hacked-account response off, but an open "possibly compromised"
		// event (from another group, or from before) still means the bot never bans (§8.7).
		d.Route, d.Delete, d.Reason = RouteAsk, true, "possibly hacked account: spam deleted, admins decide the ban"
		d.Review = banReview(s, ReviewBanDecision, false)
	case st.Established:
		d.Route, d.Delete, d.Reason = RouteAsk, true, "established member: spam deleted, admins decide the ban"
		d.Review = banReview(s, ReviewBanDecision, false)
	case s.Enforcement.Mode == EnforceAskDeleteFirst:
		d.Route, d.Delete, d.Reason = RouteAsk, true, "ask_delete_first: spam deleted, admins decide the ban"
		d.Review = banReview(s, ReviewBanDecision, false)
	case cellAction == CellAutoBan:
		d.Route, d.Delete, d.Ban, d.Reason = RouteAutoBan, true, true, "certain spam from a new or unknown sender: deleted and banned"
	default:
		d.Route, d.Delete, d.Reason = RouteAsk, true, "spam deleted, admins decide the ban"
		d.Review = banReview(s, ReviewBanDecision,
			f.Source == SourceKnown && s.Enforcement.Mode == EnforceAuto && !st.Established && !st.Compromised)
	}
	return d
}

func decideUncertain(s Settings, f Finding) Decision {
	d := Decision{Verdict: VerdictClean, Route: RouteAsk, Reason: "still uncertain: admins decide"}
	if s.Enforcement.Mode == EnforceAskBeforeAction {
		d.Review = askBefore(s, true)
		d.Review.Kind = ReviewUncertain
		if d.Review.OnTimeout == TimeoutDeleteNoBan {
			// The ask_before fallback deletes certain spam; an unverified message is only left.
			d.Review.OnTimeout = TimeoutLeave
		}
		return d
	}
	d.Delete = s.Uncertain.DeleteWhilePending
	d.Review = banReview(s, ReviewUncertain, false)
	if d.Delete {
		d.Reason = "still uncertain: hidden while admins decide (uncertain.delete_while_pending)"
	}
	return d
}

func decideViolation(s Settings, f Finding, st Standing) Decision {
	action := s.Policy.Action
	if r := s.Policy.Repeat; r != nil && r.Count > 0 {
		since := f.At.Add(-time.Duration(r.Window))
		n := 1 // this one
		for _, t := range st.RecentViolations {
			// Only earlier violations: the current one is counted above, even if the caller
			// already recorded it.
			if t.After(since) && t.Before(f.At) {
				n++
			}
		}
		if n >= r.Count && severity(r.Then) > severity(action) {
			action = r.Then
		}
	}
	d := Decision{Verdict: VerdictPolicy, Route: RoutePolicy}
	if s.Enforcement.Mode == EnforceAskBeforeAction {
		d.Reason = "content-rule violation; ask_before_action: nothing happens until an admin decides"
		d.Review = askBefore(s, action != ViolationDeleteOnly)
		return d
	}
	d.Delete = true
	switch {
	case action == ViolationDeleteOnly:
		d.Reason = "content-rule violation: deleted silently"
	case action == ViolationBan && !st.Established && !st.Compromised && s.Enforcement.Mode == EnforceAuto:
		d.Ban, d.Reason = true, "content-rule violation: deleted and banned (policy.action ban)"
	default:
		d.Reason = "content-rule violation: deleted, admins decide the ban"
		d.Review = banReview(s, ReviewBanDecision, false)
	}
	return d
}

func severity(a ViolationAction) int {
	switch a {
	case ViolationAsk:
		return 1
	case ViolationBan:
		return 2
	default:
		return 0
	}
}

// banReview asks admins to decide a ban, with the group's review timeout. The timeout can only
// ban when timeoutMayBan is set: never in ask modes, for established or hacked members, or on
// evidence other than known spam.
func banReview(s Settings, kind ReviewKind, timeoutMayBan bool) *ReviewRequest {
	on := s.Review.OnTimeout
	if on == TimeoutAutoBan && !timeoutMayBan {
		on = TimeoutKeepDeletedNoBan
	}
	return &ReviewRequest{Kind: kind, ProposeBan: true, Timeout: time.Duration(s.Review.Timeout), OnTimeout: on}
}

// askBefore is the ask_before_action review: the message stays until admins decide, and the
// fallback only deletes or leaves it (DESIGN.md §8.3 (b)).
func askBefore(s Settings, proposeBan bool) *ReviewRequest {
	on := s.Enforcement.AskBefore.OnTimeout
	if on != TimeoutLeave {
		on = TimeoutDeleteNoBan
	}
	return &ReviewRequest{Kind: ReviewAskBefore, ProposeBan: proposeBan, Timeout: time.Duration(s.Enforcement.AskBefore.Timeout), OnTimeout: on}
}
