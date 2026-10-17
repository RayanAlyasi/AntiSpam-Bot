package policy

import "time"

// SignalJustJoined is the code of the invisible just-joined scoring signal (DESIGN.md §8.2).
const SignalJustJoined = "new_members.just_joined"

// JustJoinedWeight is the just-joined signal's log-odds weight for a sender who joined at
// joinedAt: the configured weight right after joining, falling linearly to 0 at the end of
// the window. It is 0 when the signal is off, the join time is unknown (pre-existing members
// have none), or the window has passed. A join time in the future (clock skew) counts as
// just now.
//
// The signal only feeds the score. Nothing about it is ever shown to members: there is no
// visible probation (DESIGN.md §8.2).
func JustJoinedWeight(cfg JustJoined, joinedAt, now time.Time) float64 {
	window := time.Duration(cfg.Window)
	if !cfg.Enabled || cfg.Weight <= 0 || window <= 0 || joinedAt.IsZero() {
		return 0
	}
	age := max(now.Sub(joinedAt), 0)
	if age >= window {
		return 0
	}
	return cfg.Weight * (1 - float64(age)/float64(window))
}

// JustJoinedSignal returns the just-joined signal for the sender, and false when it doesn't
// apply.
func JustJoinedSignal(cfg JustJoined, joinedAt, now time.Time) (Signal, bool) {
	w := JustJoinedWeight(cfg, joinedAt, now)
	if w <= 0 {
		return Signal{}, false
	}
	return Signal{Code: SignalJustJoined, Weight: w}, true
}
