package policy

import (
	"math"
	"slices"
	"time"
)

// MemberRecord is what routing needs to know about one member of one group (from the members
// table, DESIGN.md §14).
type MemberRecord struct {
	LID           string    // the person's LID, to match the group allow-list
	Role          Role      // member, admin or superadmin
	JoinedAt      time.Time // the join event, or first seen for a pre-existing member
	PreExisting   bool      // already in the group when the bot joined
	CleanMessages int
	Strikes       []time.Time // strike times; their weight decays
	Violations    []time.Time // earlier content-rule violations, for policy.repeat
	Vouched       bool        // an admin vouched for them or allow-listed them
	Allowlisted   bool
	Compromised   bool // an open "possibly compromised" event (DESIGN.md §8.7)
}

// TrustParams shape the trust curve (DESIGN.md §8.1).
type TrustParams struct {
	FullTenure     time.Duration // tenure at which the tenure half of trust is full
	CleanCap       int           // clean messages beyond this add nothing (no farming)
	StrikePenalty  float64       // trust removed by a fresh strike
	StrikeHalfLife time.Duration // a strike's penalty halves over this time
}

// DefaultTrustParams returns a 7-day tenure ramp, a cap of 50 clean messages, and strikes
// worth 0.5 trust with a 30-day half-life.
func DefaultTrustParams() TrustParams {
	return TrustParams{FullTenure: 7 * 24 * time.Hour, CleanCap: 50, StrikePenalty: 0.5, StrikeHalfLife: 30 * 24 * time.Hour}
}

// Trust is a member's trust in one group, in [0, 1]. It grows with tenure (pre-existing
// members count as fully tenured) and with clean messages (log-scaled and capped), drops with
// strikes (each decaying with the half-life), and is full for vouched or allow-listed members.
// Trust only routes the ban decision; it never changes the score (DESIGN.md §7.3).
func Trust(m MemberRecord, now time.Time, p TrustParams) float64 {
	if m.Vouched || m.Allowlisted {
		return 1
	}
	tenure := 1.0
	if !m.PreExisting {
		tenure = ratio(now.Sub(m.JoinedAt), p.FullTenure)
		if m.JoinedAt.IsZero() {
			tenure = 0
		}
	}
	clean := 0.0
	if p.CleanCap > 0 {
		n := min(max(m.CleanMessages, 0), p.CleanCap)
		clean = math.Log1p(float64(n)) / math.Log1p(float64(p.CleanCap))
	}
	t := 0.5*tenure + 0.5*clean
	for _, at := range m.Strikes {
		age := max(now.Sub(at), 0)
		decay := 1.0
		if p.StrikeHalfLife > 0 {
			decay = math.Exp2(-float64(age) / float64(p.StrikeHalfLife))
		}
		t -= p.StrikePenalty * decay
	}
	return min(max(t, 0), 1)
}

func ratio(d, full time.Duration) float64 {
	if full <= 0 {
		return 1
	}
	return min(max(float64(d)/float64(full), 0), 1)
}

// IsEstablished reports whether the member is established (DESIGN.md §8.1): vouched or
// allow-listed, pre-existing at onboarding, or at least MinDays in the group with at least
// MinCleanMessages clean messages. Established members are never auto-banned.
func IsEstablished(m MemberRecord, now time.Time, e Established) bool {
	if m.Vouched || m.Allowlisted || m.PreExisting {
		return true
	}
	if m.JoinedAt.IsZero() {
		return false
	}
	return now.Sub(m.JoinedAt) >= time.Duration(e.MinDays)*24*time.Hour && m.CleanMessages >= e.MinCleanMessages
}

// IsNew reports whether the member joined within the just-joined window. Pre-existing members
// and members with no known join time are not new.
func IsNew(m MemberRecord, now time.Time, window time.Duration) bool {
	if m.PreExisting || m.JoinedAt.IsZero() {
		return false
	}
	return now.Sub(m.JoinedAt) < window
}

// StandingOf builds the routing standing of a member from their record and the group's
// settings. isBot marks the bot's own account.
func StandingOf(m MemberRecord, isBot bool, s Settings, now time.Time) Standing {
	allow := m.Allowlisted || (m.LID != "" && slices.Contains(s.Allowlist.Members, m.LID))
	m.Allowlisted = allow
	role := m.Role
	if role == "" {
		role = RoleMember
	}
	return Standing{
		Role:             role,
		IsBot:            isBot,
		Allowlisted:      allow,
		Established:      IsEstablished(m, now, s.Enforcement.Established),
		Compromised:      m.Compromised,
		New:              IsNew(m, now, time.Duration(s.NewMembers.JustJoined.Window)),
		Trust:            Trust(m, now, DefaultTrustParams()),
		RecentViolations: slices.Clone(m.Violations),
	}
}
