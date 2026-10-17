package policy_test

import (
	"testing"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/policy"
)

func spam(band policy.Band, src policy.Source, ct policy.ContentType) policy.Finding {
	return policy.Finding{Band: band, Source: src, Type: ct, Score: 0.97, Signals: []policy.Signal{contentSignal}, At: t0}
}

// TestDefaultRouting walks the §8.3 routing table under Defaults() (Standard preset, auto mode).
func TestDefaultRouting(t *testing.T) {
	established := policy.Standing{Role: policy.RoleMember, Established: true, Trust: 1}
	hacked := policy.Standing{Role: policy.RoleMember, Established: true, Compromised: true, Trust: 1}
	admin := policy.Standing{Role: policy.RoleAdmin, Trust: 1}
	violation := policy.Finding{
		Band: policy.BandCertain, Type: policy.TypeVideos, Score: 0.1,
		Violations: []policy.Violation{{Rule: "content.videos"}}, At: t0,
	}

	cases := []struct {
		name       string
		f          policy.Finding
		st         policy.Standing
		verdict    policy.Verdict
		route      policy.Route
		del, ban   bool
		reviewKind policy.ReviewKind // "" means no review
		why        string
	}{
		{
			"not suspicious",
			policy.Finding{Band: policy.BandClean, Type: policy.TypeText, Score: 0.1, At: t0},
			newMember(),
			policy.VerdictClean, policy.RouteNone, false, false, "", "below T_review is untouched",
		},
		{
			"certain spam, new sender, auto_ban cell", spam(policy.BandCertain, policy.SourceKnown, policy.TypeText), newMember(),
			policy.VerdictSpam, policy.RouteAutoBan, true, true, "", "deleted at once and auto-banned",
		},
		{
			"certain spam, ask cell", spam(policy.BandCertain, policy.SourceKnown, policy.TypeAudio), newMember(),
			policy.VerdictSpam, policy.RouteAsk, true, false, policy.ReviewBanDecision, "deleted, admins decide the ban",
		},
		{
			"AI-verified spam", spam(policy.BandUncertain, policy.SourceAI, policy.TypeLinks), newMember(),
			policy.VerdictSpam, policy.RouteAsk, true, false, policy.ReviewBanDecision, "early-AI safety: deleted, ban to admins",
		},
		{
			"still uncertain", spam(policy.BandStillUncertain, policy.SourceRules, policy.TypeLinks), newMember(),
			"", "", false, false, policy.ReviewUncertain, "stays visible, admins decide",
		},
		{
			"certain spam, established sender", spam(policy.BandCertain, policy.SourceKnown, policy.TypeText), established,
			policy.VerdictSpam, "", true, false, policy.ReviewBanDecision, "deleted, never auto-banned",
		},
		{
			"certain spam, possibly hacked sender", spam(policy.BandCertain, policy.SourceKnown, policy.TypeLinks), hacked,
			policy.VerdictSpam, "", true, false, policy.ReviewHacked, "deleted, admins decide (§8.7)",
		},
		{
			"policy violation only", violation, newMember(),
			policy.VerdictPolicy, policy.RoutePolicy, true, false, "", "deleted silently, no ban",
		},
		{
			"admin sends known spam", spam(policy.BandCertain, policy.SourceKnown, policy.TypeText), admin,
			"", "", false, false, "", "admins are never acted on",
		},
	}
	s := policy.Defaults()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := policy.Decide(s, c.f, c.st)
			got := describeDecision(d)
			if c.verdict != "" && d.Verdict != c.verdict {
				t.Errorf("%s: verdict %s, want %s (%s, DESIGN §8.3)", got, d.Verdict, c.verdict, c.why)
			}
			if c.route != "" && d.Route != c.route {
				t.Errorf("%s: route %s, want %s (%s, DESIGN §8.3)", got, d.Route, c.route, c.why)
			}
			if d.Delete != c.del || d.Ban != c.ban {
				t.Errorf("%s: delete=%v ban=%v, want delete=%v ban=%v (%s, DESIGN §8.3)", got, d.Delete, d.Ban, c.del, c.ban, c.why)
			}
			switch {
			case c.reviewKind == "" && c.st.Role == policy.RoleMember && d.Review != nil:
				t.Errorf("%s: got a review, want none (%s, DESIGN §8.3)", got, c.why)
			case c.reviewKind != "" && (d.Review == nil || d.Review.Kind != c.reviewKind):
				t.Errorf("%s: want a %s review (%s, DESIGN §8.3, §9.5)", got, c.reviewKind, c.why)
			case c.reviewKind == policy.ReviewBanDecision:
				if !d.Review.ProposeBan || d.Review.Timeout != 6*time.Hour || d.Review.OnTimeout != policy.TimeoutKeepDeletedNoBan {
					t.Errorf("%s: review timeout %v, want a ban proposal with review.timeout 6h and on_timeout keep_deleted_no_ban (defaults, DESIGN §9.5)", got, d.Review.Timeout)
				}
			}
		})
	}
}

// In ask_delete_first, certain or AI-verified spam is deleted at once and the ban goes to
// admins, even where the matrix says auto_ban (DESIGN §8.3 (a)).
func TestAskDeleteFirstDeletesSpamAndAsksForBan(t *testing.T) {
	askPreset := mustPreset(t, policy.PresetAskAdmins)
	standardAsk := mustPreset(t, policy.PresetStandard)
	standardAsk.Enforcement.Mode = policy.EnforceAskDeleteFirst
	for name, s := range map[string]policy.Settings{"ask_admins preset": askPreset, "standard matrix in ask_delete_first": standardAsk} {
		for _, f := range []policy.Finding{spam(policy.BandCertain, policy.SourceKnown, policy.TypeText), spam(policy.BandUncertain, policy.SourceAI, policy.TypeLinks)} {
			d := policy.Decide(s, f, newMember())
			if !d.Delete || d.Ban || d.Review == nil || d.Review.Kind != policy.ReviewBanDecision || !d.Review.ProposeBan {
				t.Errorf("%s, %s: got %s, want delete, no ban, ban_decision review proposing a ban (DESIGN §8.3 (a))", name, describe(f, newMember()), describeDecision(d))
			}
		}
	}
}

// In ask_before_action the message stays visible, admins are asked, and the fallback never bans
// (DESIGN §8.3 (b), §9.5).
func TestAskBeforeActionLeavesMessageAndAsks(t *testing.T) {
	s := mustPreset(t, policy.PresetAskAdmins)
	s.Enforcement.Mode = policy.EnforceAskBeforeAction
	for _, f := range []policy.Finding{spam(policy.BandCertain, policy.SourceKnown, policy.TypeText), spam(policy.BandUncertain, policy.SourceAI, policy.TypeImages)} {
		d := policy.Decide(s, f, newMember())
		if d.Delete || d.Ban {
			t.Errorf("%s: got %s, want no delete and no ban (nothing happens until an admin decides, DESIGN §8.3 (b))", describe(f, newMember()), describeDecision(d))
		}
		if d.Review == nil || d.Review.Kind != policy.ReviewAskBefore || d.Review.OnTimeout == policy.TimeoutAutoBan {
			t.Errorf("%s: got %s, want an ask_before review whose fallback never bans (DESIGN §8.3 (b), §9.5)", describe(f, newMember()), describeDecision(d))
		}
	}
}

// A message that is both spam and a violation follows the spam route, even when the spam route
// is milder than policy.action (DESIGN §7.3, §9.3).
func TestSpamAndViolationFollowsSpamRoute(t *testing.T) {
	withViolation := func(f policy.Finding) policy.Finding {
		f.Violations = []policy.Violation{{Rule: "content." + string(f.Type)}}
		return f
	}

	s := policy.Defaults()
	s.Policy.Action = policy.ViolationBan
	s.Enforcement.AutobanMatrix[policy.TypeText] = delOnly
	f := withViolation(spam(policy.BandCertain, policy.SourceKnown, policy.TypeText))
	d := policy.Decide(s, f, newMember())
	if d.Verdict != policy.VerdictSpam || d.Route != policy.RouteDeleteOnly || !d.Delete || d.Ban {
		t.Errorf("delete_only cell + policy.action=ban: got %s, want spam/delete_only, deleted, not banned (the spam route applies, DESIGN §9.3)", describeDecision(d))
	}

	s = policy.Defaults()
	f = withViolation(spam(policy.BandCertain, policy.SourceKnown, policy.TypeVideos))
	d = policy.Decide(s, f, newMember())
	if d.Verdict != policy.VerdictSpam || d.Route != policy.RouteAutoBan || !d.Delete || !d.Ban {
		t.Errorf("known spam that also breaks a rule, new sender, standard matrix: got %s, want spam/auto_ban, deleted and banned (the spam route applies, DESIGN §8.3, §9.3)", describeDecision(d))
	}
}
