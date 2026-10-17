package policy_test

import (
	"testing"

	"pgregory.net/rapid"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/policy"
)

// Ask-admins modes never ban on their own: not for known spam, not by a matrix cell, not by
// policy.action=ban, not through a review timeout fallback (DESIGN §8.3, §9.5, §18).
func TestAskModesNeverAutoBan(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		s := genSettings(rt)
		s.Enforcement.Mode = rapid.SampledFrom(askModes).Draw(rt, "ask_mode")
		f, st := genFinding(rt), genStanding(rt)
		d := policy.Decide(s, f, st)
		ctx := describe(f, st) + " => " + describeDecision(d)

		if d.Ban {
			rt.Fatalf("[%s] %s: got Ban=true, want false (ask-admins modes never ban on their own, DESIGN §8.3, §18)", s.Enforcement.Mode, ctx)
		}
		if d.Route == policy.RouteAutoBan {
			rt.Fatalf("[%s] %s: got route auto_ban, want ask/delete_only/allow/policy/none (no auto-ban route in ask-admins modes, DESIGN §8.3)", s.Enforcement.Mode, ctx)
		}
		if d.Review != nil && d.Review.OnTimeout == policy.TimeoutAutoBan {
			rt.Fatalf("[%s] %s: got review on_timeout=auto_ban, want a no-ban fallback (a timeout must never ban in ask-admins mode, DESIGN §9.5)", s.Enforcement.Mode, ctx)
		}
		if s.Enforcement.Mode == policy.EnforceAskBeforeAction && d.Delete {
			rt.Fatalf("[ask_before_action] %s: got Delete=true, want false (nothing is deleted until an admin decides, DESIGN §8.3 (b))", ctx)
		}

		p := policy.PlanFor(s, d)
		for name, list := range map[string][]policy.Action{"Enqueue": p.Enqueue, "Would": p.Would} {
			if count(list, policy.ActionBan) > 0 {
				rt.Fatalf("[%s] %s: Plan.%s has a ban action, want none (ask-admins modes never ban, DESIGN §8.3)", s.Enforcement.Mode, ctx, name)
			}
			for _, a := range list {
				if a.Review != nil && a.Review.OnTimeout == policy.TimeoutAutoBan {
					rt.Fatalf("[%s] %s: Plan.%s review has on_timeout=auto_ban, want a no-ban fallback (DESIGN §9.5)", s.Enforcement.Mode, ctx, name)
				}
			}
			if s.Enforcement.Mode == policy.EnforceAskBeforeAction && count(list, policy.ActionDelete) > 0 {
				rt.Fatalf("[ask_before_action] %s: Plan.%s has a delete action, want none (message stays visible until admins decide, DESIGN §8.3 (b))", ctx, name)
			}
		}
	})
}

// A policy violation alone is deleted silently with no ban and no review under every preset's
// defaults, however many earlier violations the sender has (policy.repeat is off), DESIGN §9.3.
func TestPolicyViolationAloneNeverBansByDefault(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		preset := rapid.SampledFrom(namedPresets).Draw(rt, "preset")
		s := mustPreset(rt, preset)
		ct := rapid.SampledFrom(gridTypes).Draw(rt, "type")
		// Scoring reports a hard violation without known spam as CERTAIN with no source,
		// or the message may be clean apart from the rule; both are policy-only.
		band := rapid.SampledFrom([]policy.Band{policy.BandClean, policy.BandCertain}).Draw(rt, "band")
		f := policy.Finding{
			Band: band, Source: policy.SourceNone, Type: ct, Score: rapid.Float64Range(0, 0.5).Draw(rt, "score"),
			Violations: []policy.Violation{{Rule: "content." + string(ct), Detail: "blocked type"}}, At: t0,
		}
		st := genMember(rt)
		st.Compromised = false
		d := policy.Decide(s, f, st)
		ctx := string(preset) + " " + describe(f, st) + " => " + describeDecision(d)

		if d.Ban {
			rt.Fatalf("%s: got Ban=true, want false (a policy violation alone never bans by default, DESIGN §9.3, §18)", ctx)
		}
		if d.Review != nil {
			rt.Fatalf("%s: got a review, want none (default policy.action is a silent delete; ask and repeat are opt-in, DESIGN §9.3)", ctx)
		}
		if !d.Delete {
			rt.Fatalf("%s: got Delete=false, want true (a violation is deleted by default, DESIGN §9.3)", ctx)
		}
		if d.Verdict != policy.VerdictPolicy || d.Route != policy.RoutePolicy {
			rt.Fatalf("%s: got verdict %s route %s, want policy/policy (rule-breaking is not spam, DESIGN §9.3, §14 detections)", ctx, d.Verdict, d.Route)
		}
		p := policy.PlanFor(s, d)
		if len(p.Enqueue) != 1 || p.Enqueue[0].Kind != policy.ActionDelete || p.Enqueue[0].Priority != 1 {
			rt.Fatalf("%s: got Enqueue %+v, want exactly one delete at P1 (silent delete, no ban, no message, DESIGN §9.3, §10.2)", ctx, p.Enqueue)
		}
	})
}

// Established and possibly hacked members are never auto-banned, whatever the matrix, mode,
// policy action or timeout fallback; their spam is still deleted and the ban goes to admins
// (DESIGN §8.1, §8.3, §8.7, §9.5, §18).
func TestEstablishedAndCompromisedNeverAutoBanned(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		s := genSettings(rt)
		st := genMember(rt)
		if rapid.Bool().Draw(rt, "established_not_compromised") {
			st.Established = true
		} else {
			st.Compromised = true
		}
		f := genFinding(rt)
		if rapid.Bool().Draw(rt, "force_spam") {
			f = genSpamFinding(rt)
		}
		d := policy.Decide(s, f, st)
		ctx := describe(f, st) + " => " + describeDecision(d)

		if d.Ban || d.Route == policy.RouteAutoBan {
			rt.Fatalf("%s: got Ban=%v route=%s, want no ban (established and possibly hacked members are never auto-banned, DESIGN §8.1, §8.7)", ctx, d.Ban, d.Route)
		}
		if d.Review != nil && d.Review.OnTimeout == policy.TimeoutAutoBan {
			rt.Fatalf("%s: got review on_timeout=auto_ban, want a no-ban fallback (not allowed for established or hacked members, DESIGN §9.5)", ctx)
		}

		spam := len(f.Violations) == 0 && ((f.Band == policy.BandCertain && f.Source == policy.SourceKnown) ||
			(f.Band == policy.BandUncertain && f.Source == policy.SourceAI))
		if !spam || s.Enforcement.Mode == policy.EnforceAskBeforeAction {
			return
		}
		eff := wantEffective(s.Enforcement.AutobanMatrix[f.Type], f.Source)
		if eff == policy.CellAllow {
			return
		}
		if !d.Delete {
			rt.Fatalf("%s: got Delete=false, want true (spam is deleted whoever sent it; trust only routes the ban, DESIGN §7.3, §8.1)", ctx)
		}
		if eff == policy.CellAutoBan || eff == policy.CellAsk {
			if d.Review == nil || !d.Review.ProposeBan {
				rt.Fatalf("%s: got no ban-proposing review, want admins asked to decide the ban (DESIGN §8.3 established/hacked rows)", ctx)
			}
			if st.Compromised && d.Review.Kind != policy.ReviewHacked {
				rt.Fatalf("%s: got review kind %s, want hacked (possibly compromised sender, DESIGN §8.7, §14 reviews.kind)", ctx, d.Review.Kind)
			}
		}
	})
}

// Admins, the bot itself and allow-listed members are never acted on; at most admins get an
// alert (a possibly hacked admin), DESIGN §8.3 last row, §8.6, §8.7.
func TestExemptSendersNeverActedOn(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		s := genSettings(rt)
		f := genFinding(rt)
		st := genStanding(rt)
		switch rapid.IntRange(0, 3).Draw(rt, "exempt_kind") {
		case 0:
			st.Role = policy.RoleAdmin
		case 1:
			st.Role = policy.RoleSuperAdmin
		case 2:
			st.IsBot = true
		case 3:
			st.Allowlisted = true
		}
		d := policy.Decide(s, f, st)
		ctx := describe(f, st) + " => " + describeDecision(d)

		if d.Delete || d.Ban {
			rt.Fatalf("%s: got Delete=%v Ban=%v, want neither (never act on admins, the bot or allow-listed members, DESIGN §8.6)", ctx, d.Delete, d.Ban)
		}
		if d.Review != nil && (d.Review.Kind != policy.ReviewAlert || d.Review.ProposeBan) {
			rt.Fatalf("%s: got review %+v, want none or a non-ban alert (an admin only triggers an alert, DESIGN §8.3, §8.7)", ctx, *d.Review)
		}
		p := policy.PlanFor(s, d)
		for name, list := range map[string][]policy.Action{"Enqueue": p.Enqueue, "Would": p.Would} {
			if count(list, policy.ActionDelete)+count(list, policy.ActionBan) > 0 {
				rt.Fatalf("%s: Plan.%s = %+v, want no delete or ban (exempt sender, DESIGN §8.6)", ctx, name, list)
			}
		}
	})
}

// Identity priors (§7.6) never act alone: a finding whose only positive signals are priors gets
// no delete, no ban and no review (DESIGN §7.6, §12.1 intel, §18 spammer intelligence).
func TestIdentityPriorsNeverActAlone(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		s := genSettings(rt)
		st := genMember(rt)
		f := policy.Finding{
			Band:   rapid.SampledFrom([]policy.Band{policy.BandClean, policy.BandUncertain, policy.BandStillUncertain}).Draw(rt, "band"),
			Source: rapid.SampledFrom([]policy.Source{policy.SourceNone, policy.SourceRules}).Draw(rt, "source"),
			Type:   rapid.SampledFrom(gridTypes).Draw(rt, "type"),
			Score:  rapid.Float64Range(0, 1).Draw(rt, "score"),
			At:     t0,
		}
		for i := range rapid.IntRange(1, 4).Draw(rt, "priors.n") {
			f.Signals = append(f.Signals, policy.Signal{Code: "prior." + string(rune('a'+i)), Weight: rapid.Float64Range(0.1, 10).Draw(rt, "prior.weight"), Prior: true})
		}
		for i := range rapid.IntRange(0, 3).Draw(rt, "others.n") {
			f.Signals = append(f.Signals, policy.Signal{Code: "neg." + string(rune('a'+i)), Weight: rapid.Float64Range(-5, 0).Draw(rt, "other.weight")})
		}
		d := policy.Decide(s, f, st)
		ctx := describe(f, st) + " => " + describeDecision(d)

		if d.Delete || d.Ban || d.Review != nil {
			rt.Fatalf("%s: got Delete=%v Ban=%v review=%v, want no action (identity priors are never enough on their own, DESIGN §7.6, §18)", ctx, d.Delete, d.Ban, d.Review != nil)
		}
		p := policy.PlanFor(s, d)
		if len(p.Enqueue) != 0 || len(p.Would) != 0 {
			rt.Fatalf("%s: got Enqueue=%+v Would=%+v, want both empty (priors alone never act, DESIGN §7.6)", ctx, p.Enqueue, p.Would)
		}
	})
}

// Heuristic-only (rules) and still-uncertain findings never ban, are not deleted unless
// uncertain.delete_while_pending is on, and go to admins (DESIGN §7.0, §7.3, §8.3, §18).
func TestStillUncertainGoesToAdminsNothingDeletedByDefault(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		s := genSettings(rt)
		s.Enforcement.Mode = rapid.SampledFrom([]policy.EnforcementMode{policy.EnforceAuto, policy.EnforceAskDeleteFirst}).Draw(rt, "enforcement.mode")
		st := genMember(rt)
		st.Compromised = false
		f := policy.Finding{
			Type: rapid.SampledFrom(gridTypes).Draw(rt, "type"), Score: rapid.Float64Range(0.6, 1).Draw(rt, "score"), At: t0,
			Signals: append([]policy.Signal{contentSignal}, genSignals(rt, "signals")...),
		}
		if rapid.Bool().Draw(rt, "ai_pending") {
			f.Band, f.Source = policy.BandUncertain, policy.SourceRules
		} else {
			f.Band = policy.BandStillUncertain
			f.Source = rapid.SampledFrom([]policy.Source{policy.SourceRules, policy.SourceNone}).Draw(rt, "source")
		}
		d := policy.Decide(s, f, st)
		ctx := describe(f, st) + " => " + describeDecision(d)

		if d.Ban || d.Route == policy.RouteAutoBan {
			rt.Fatalf("%s: got Ban=%v route=%s, want no ban (heuristics and unverified findings never ban, DESIGN §7.3, §18)", ctx, d.Ban, d.Route)
		}
		if !s.Uncertain.DeleteWhilePending && d.Delete {
			rt.Fatalf("%s: got Delete=true, want false (still-uncertain messages stay visible by default, uncertain.delete_while_pending=false, DESIGN §7.0)", ctx)
		}
		if d.Review == nil || d.Review.Kind != policy.ReviewUncertain {
			rt.Fatalf("%s: got review %v, want kind uncertain (admins decide delete and ban, DESIGN §7.0, §8.3)", ctx, d.Review)
		}
	})
}

// Matrix cells for spam from new/unknown senders in auto mode: delete_only never bans, allow
// never deletes, ask deletes and asks, auto_ban bans only on a listed source and otherwise
// falls back to else (DESIGN §9.2, §18).
func TestMatrixCellSemanticsInDecide(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		s := mustPreset(rt, rapid.SampledFrom(namedPresets).Draw(rt, "preset"))
		s.Enforcement.Mode = policy.EnforceAuto
		s.Mode = policy.ModeEnforce
		f := genSpamFinding(rt)
		cell := genCell(rt, "cell")
		s.Enforcement.AutobanMatrix[f.Type] = cell
		st := policy.Standing{Role: policy.RoleMember, New: rapid.Bool().Draw(rt, "new"), Trust: rapid.Float64Range(0, 1).Draw(rt, "trust")}
		d := policy.Decide(s, f, st)
		eff := wantEffective(cell, f.Source)
		ctx := describe(f, st) + " cell=" + string(cell.Action) + " effective=" + string(eff) + " => " + describeDecision(d)

		if d.Verdict != policy.VerdictSpam {
			rt.Fatalf("%s: got verdict %s, want spam (CERTAIN/known and AI-verified findings are spam, DESIGN §7.0)", ctx, d.Verdict)
		}
		if d.Ban && eff != policy.CellAutoBan {
			rt.Fatalf("%s: got Ban=true, want false (only auto_ban with a listed require_source may ban, DESIGN §9.2, §18)", ctx)
		}
		switch eff {
		case policy.CellAllow:
			if d.Delete || d.Ban || d.Review != nil || d.Route != policy.RouteAllow {
				rt.Fatalf("%s: want route allow with no delete, ban or review (allow never acts on spam, DESIGN §9.2, §18)", ctx)
			}
		case policy.CellDeleteOnly:
			if !d.Delete || d.Ban || d.Review != nil || d.Route != policy.RouteDeleteOnly {
				rt.Fatalf("%s: want route delete_only, delete, no ban, no review (delete_only never bans, DESIGN §9.2, §18)", ctx)
			}
		case policy.CellAsk:
			if !d.Delete || d.Ban || d.Route != policy.RouteAsk || d.Review == nil || d.Review.Kind != policy.ReviewBanDecision || !d.Review.ProposeBan {
				rt.Fatalf("%s: want route ask, delete, no ban, a ban_decision review proposing a ban (DESIGN §8.3, §9.2)", ctx)
			}
		case policy.CellAutoBan:
			if !d.Delete {
				rt.Fatalf("%s: want Delete=true (spam under an auto_ban cell is deleted, DESIGN §8.3)", ctx)
			}
			if f.Source == policy.SourceKnown && (!d.Ban || d.Route != policy.RouteAutoBan) {
				rt.Fatalf("%s: want route auto_ban with Ban=true (CERTAIN known spam from a new sender under auto_ban, DESIGN §8.3)", ctx)
			}
		}
	})
}
