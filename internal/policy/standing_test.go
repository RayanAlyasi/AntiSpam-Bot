package policy

import (
	"math"
	"testing"
	"time"

	"pgregory.net/rapid"
)

const day = 24 * time.Hour

func TestJustJoinedWeight(t *testing.T) {
	on := Defaults().NewMembers.JustJoined
	off := on
	off.Enabled = false
	tests := []struct {
		name     string
		cfg      JustJoined
		joinedAt time.Time
		want     float64
	}{
		{"just now", on, t0, 0.8},
		{"6 h ago", on, t0.Add(-6 * time.Hour), 0.6},
		{"12 h ago", on, t0.Add(-12 * time.Hour), 0.4},
		{"end of window", on, t0.Add(-24 * time.Hour), 0},
		{"long ago", on, t0.Add(-30 * day), 0},
		{"future join (clock skew)", on, t0.Add(time.Minute), 0.8},
		{"unknown join (pre-existing)", on, time.Time{}, 0},
		{"switched off", off, t0, 0},
		{"custom weight and window", JustJoined{Enabled: true, Weight: 2, Window: Duration(time.Hour)}, t0.Add(-30 * time.Minute), 1},
	}
	for _, tt := range tests {
		if got := JustJoinedWeight(tt.cfg, tt.joinedAt, t0); math.Abs(got-tt.want) > 1e-9 {
			t.Errorf("%s: weight %v, want %v (§8.2: decays linearly to 0 over the window)", tt.name, got, tt.want)
		}
	}
	if s, ok := JustJoinedSignal(on, t0.Add(-time.Hour), t0); !ok || s.Code != SignalJustJoined || s.Prior {
		t.Errorf("JustJoinedSignal = %+v, %v; want the just-joined signal (not an identity prior)", s, ok)
	}
	if _, ok := JustJoinedSignal(on, t0.Add(-2*day), t0); ok {
		t.Error("JustJoinedSignal after the window = ok, want none")
	}
}

func TestJustJoinedDecays(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		cfg := JustJoined{Enabled: true, Weight: rapid.Float64Range(0, 5).Draw(t, "w"), Window: Duration(time.Duration(rapid.IntRange(1, 72).Draw(t, "h")) * time.Hour)}
		a := time.Duration(rapid.Int64Range(0, int64(100*time.Hour)).Draw(t, "a"))
		b := time.Duration(rapid.Int64Range(int64(a), int64(100*time.Hour)).Draw(t, "b"))
		wa, wb := JustJoinedWeight(cfg, t0.Add(-a), t0), JustJoinedWeight(cfg, t0.Add(-b), t0)
		if wb > wa || wa > cfg.Weight || wb < 0 {
			t.Fatalf("weight at %v = %v, at %v = %v; want non-increasing within [0, %v]", a, wa, b, wb, cfg.Weight)
		}
	})
}

func TestTrust(t *testing.T) {
	p := DefaultTrustParams()
	tests := []struct {
		name     string
		m        MemberRecord
		min, max float64
	}{
		{"brand new", MemberRecord{JoinedAt: t0}, 0, 0},
		{"a week, no messages", MemberRecord{JoinedAt: t0.Add(-7 * day)}, 0.5, 0.5},
		{"a week, 50 clean", MemberRecord{JoinedAt: t0.Add(-7 * day), CleanMessages: 50}, 1, 1},
		{"farming past the cap adds nothing", MemberRecord{JoinedAt: t0.Add(-7 * day), CleanMessages: 5000}, 1, 1},
		{"pre-existing", MemberRecord{PreExisting: true}, 0.5, 0.5},
		{"vouched", MemberRecord{JoinedAt: t0, Vouched: true}, 1, 1},
		{"allow-listed", MemberRecord{JoinedAt: t0, Allowlisted: true}, 1, 1},
		{"fresh strike", MemberRecord{JoinedAt: t0.Add(-7 * day), CleanMessages: 50, Strikes: []time.Time{t0}}, 0.5, 0.5},
		{"strike a half-life ago", MemberRecord{JoinedAt: t0.Add(-60 * day), CleanMessages: 50, Strikes: []time.Time{t0.Add(-30 * day)}}, 0.75, 0.75},
		{"many strikes floor at 0", MemberRecord{JoinedAt: t0.Add(-7 * day), Strikes: []time.Time{t0, t0, t0}}, 0, 0},
	}
	for _, tt := range tests {
		if got := Trust(tt.m, t0, p); got < tt.min-1e-9 || got > tt.max+1e-9 {
			t.Errorf("%s: trust %v, want in [%v, %v] (§8.1)", tt.name, got, tt.min, tt.max)
		}
	}
}

func TestTrustProperties(t *testing.T) {
	p := DefaultTrustParams()
	rapid.Check(t, func(t *rapid.T) {
		m := MemberRecord{
			JoinedAt:      t0.Add(-time.Duration(rapid.Int64Range(0, int64(60*day)).Draw(t, "age"))),
			CleanMessages: rapid.IntRange(0, 500).Draw(t, "clean"),
			PreExisting:   rapid.Bool().Draw(t, "pre"),
		}
		base := Trust(m, t0, p)
		if base < 0 || base > 1 {
			t.Fatalf("trust %v outside [0, 1]", base)
		}
		more := m
		more.CleanMessages++
		if Trust(more, t0, p) < base {
			t.Fatal("a clean message lowered trust")
		}
		struck := m
		struck.Strikes = []time.Time{t0.Add(-time.Duration(rapid.Int64Range(0, int64(90*day)).Draw(t, "strike")))}
		if Trust(struck, t0, p) > base {
			t.Fatal("a strike raised trust")
		}
	})
}

func TestEstablished(t *testing.T) {
	e := Defaults().Enforcement.Established
	tests := []struct {
		name string
		m    MemberRecord
		want bool
	}{
		{"7 days and 20 clean", MemberRecord{JoinedAt: t0.Add(-7 * day), CleanMessages: 20}, true},
		{"7 days, 19 clean", MemberRecord{JoinedAt: t0.Add(-7 * day), CleanMessages: 19}, false},
		{"6 days, 100 clean", MemberRecord{JoinedAt: t0.Add(-6 * day), CleanMessages: 100}, false},
		{"pre-existing", MemberRecord{PreExisting: true}, true},
		{"vouched newcomer", MemberRecord{JoinedAt: t0, Vouched: true}, true},
		{"unknown join", MemberRecord{CleanMessages: 100}, false},
	}
	for _, tt := range tests {
		if got := IsEstablished(tt.m, t0, e); got != tt.want {
			t.Errorf("%s: established %v, want %v (§8.1)", tt.name, got, tt.want)
		}
	}
}

func TestStandingOf(t *testing.T) {
	s := Defaults()
	s.Allowlist.Members = []string{"42@lid"}
	st := StandingOf(MemberRecord{LID: "42@lid", JoinedAt: t0.Add(-time.Hour)}, false, s, t0)
	if !st.Allowlisted || !st.New || !st.Established || st.Trust != 1 || st.Role != RoleMember {
		t.Errorf("StandingOf = %+v, want allow-listed by the group list (so established, full trust), new, member", st)
	}
	st = StandingOf(MemberRecord{LID: "43@lid", JoinedAt: t0.Add(-time.Hour)}, false, s, t0)
	if st.Allowlisted || st.Established || !st.New {
		t.Errorf("StandingOf newcomer = %+v, want new, not allow-listed, not established", st)
	}
	st = StandingOf(MemberRecord{Role: RoleAdmin, PreExisting: true}, false, s, t0)
	if st.New || !st.Established || st.Role != RoleAdmin {
		t.Errorf("StandingOf pre-existing admin = %+v, want established, not new, admin", st)
	}
}

// Standing never feeds the score: the just-joined weight depends only on the join time and
// the settings, and Decide never looks at Trust.
func TestStandingNeverChangesDetection(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s, f, st := genSettings(t), genFinding(t), genStanding(t)
		other := st
		other.Trust = rapid.Float64Range(0, 1).Draw(t, "trust2")
		if a, b := Decide(s, f, st), Decide(s, f, other); a.Route != b.Route || a.Delete != b.Delete || a.Ban != b.Ban {
			t.Fatalf("trust %v → %+v, trust %v → %+v; trust must not change the decision once standing is set", st.Trust, a, other.Trust, b)
		}
	})
}
