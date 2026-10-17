package policy

import (
	"testing"
	"time"

	"pgregory.net/rapid"
)

var t0 = time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)

func genCell(t *rapid.T, label string) Cell {
	a := rapid.SampledFrom([]CellAction{CellAutoBan, CellAsk, CellDeleteOnly, CellAllow}).Draw(t, label+".action")
	if a != CellAutoBan {
		return Cell{Action: a}
	}
	src := rapid.SliceOfDistinct(rapid.SampledFrom([]Source{SourceKnown, SourceAI}), func(s Source) Source { return s }).Draw(t, label+".require")
	els := rapid.SampledFrom([]CellAction{"", CellAsk, CellDeleteOnly, CellAllow}).Draw(t, label+".else")
	return Cell{Action: a, RequireSource: src, Else: els}
}

// genSettings draws a valid settings document: any preset, any matrix, any modes and options.
func genSettings(t *rapid.T) Settings {
	s, _ := PresetSettings(rapid.SampledFrom([]Preset{PresetRelaxed, PresetStandard, PresetStrict, PresetAskAdmins, PresetCustom}).Draw(t, "preset"))
	s.Mode = rapid.SampledFrom([]Mode{ModeShadow, ModeEnforce, ModePaused}).Draw(t, "mode")
	s.Enforcement.Mode = rapid.SampledFrom([]EnforcementMode{EnforceAuto, EnforceAskDeleteFirst, EnforceAskBeforeAction}).Draw(t, "enforcement")
	for _, ct := range ContentTypes() {
		if rapid.Bool().Draw(t, "override."+string(ct)) {
			s.Enforcement.AutobanMatrix[ct] = genCell(t, string(ct))
		}
	}
	s.Enforcement.AskBefore.OnTimeout = rapid.SampledFrom([]TimeoutAction{TimeoutDeleteNoBan, TimeoutLeave}).Draw(t, "askbefore.on_timeout")
	timeouts := []TimeoutAction{TimeoutKeepDeletedNoBan, TimeoutDeleteNoBan, TimeoutDismiss}
	if !s.Enforcement.Mode.AsksAdmins() {
		timeouts = append(timeouts, TimeoutAutoBan)
	}
	s.Review.OnTimeout = rapid.SampledFrom(timeouts).Draw(t, "review.on_timeout")
	s.Uncertain.DeleteWhilePending = rapid.Bool().Draw(t, "delete_while_pending")
	s.Policy.Action = rapid.SampledFrom([]ViolationAction{ViolationDeleteOnly, ViolationAsk, ViolationBan}).Draw(t, "policy.action")
	if rapid.Bool().Draw(t, "repeat") {
		s.Policy.Repeat = &Repeat{
			Count:  rapid.IntRange(1, 5).Draw(t, "repeat.count"),
			Window: Duration(time.Duration(rapid.IntRange(1, 200).Draw(t, "repeat.hours")) * time.Hour),
			Then:   rapid.SampledFrom([]ViolationAction{ViolationAsk, ViolationBan}).Draw(t, "repeat.then"),
		}
	}
	s.Compromised.Admins = rapid.SampledFrom([]string{"alert_only", "delete_known_spam"}).Draw(t, "compromised.admins")
	s.Compromised.Enabled = rapid.Bool().Draw(t, "compromised.enabled")
	s.Ladder.Enabled = rapid.Bool().Draw(t, "ladder.enabled")
	if err := s.Validate(); err != nil {
		t.Fatalf("generator drew invalid settings: %v", err)
	}
	return s
}

func genFinding(t *rapid.T) Finding {
	f := Finding{
		Band:   rapid.SampledFrom([]Band{BandClean, BandCertain, BandUncertain, BandStillUncertain}).Draw(t, "band"),
		Source: rapid.SampledFrom([]Source{SourceNone, SourceKnown, SourceRules, SourceAI}).Draw(t, "source"),
		Score:  rapid.Float64Range(0, 1).Draw(t, "score"),
		Type:   rapid.SampledFrom(append(ContentTypes(), "")).Draw(t, "type"),
		At:     t0,
	}
	for i := range rapid.IntRange(0, 4).Draw(t, "nsignals") {
		_ = i
		f.Signals = append(f.Signals, Signal{
			Code:   rapid.SampledFrom([]string{"kw", "url", "intel.prefix", "just_joined"}).Draw(t, "code"),
			Weight: rapid.Float64Range(-2, 3).Draw(t, "weight"),
			Prior:  rapid.Bool().Draw(t, "prior"),
			Known:  rapid.Bool().Draw(t, "known"),
		})
	}
	if rapid.Bool().Draw(t, "violation") {
		f.Violations = []Violation{{Rule: "content.videos", Detail: "video (block)"}}
	}
	return f
}

func genStanding(t *rapid.T) Standing {
	st := Standing{
		Role:        rapid.SampledFrom([]Role{RoleMember, RoleAdmin, RoleSuperAdmin}).Draw(t, "role"),
		IsBot:       rapid.Bool().Draw(t, "bot"),
		Allowlisted: rapid.Bool().Draw(t, "allowlisted"),
		Established: rapid.Bool().Draw(t, "established"),
		Compromised: rapid.Bool().Draw(t, "compromised"),
		New:         rapid.Bool().Draw(t, "new"),
		Trust:       rapid.Float64Range(0, 1).Draw(t, "trust"),
	}
	for i := range rapid.IntRange(0, 5).Draw(t, "nviolations") {
		_ = i
		st.RecentViolations = append(st.RecentViolations, t0.Add(-time.Duration(rapid.IntRange(1, 400).Draw(t, "ago.h"))*time.Hour))
	}
	return st
}

func bansOnTimeout(r *ReviewRequest) bool { return r != nil && r.OnTimeout == TimeoutAutoBan }

// The §18 decision invariants, over every settings document, finding and standing.
func TestDecisionInvariants(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s, f, st := genSettings(t), genFinding(t), genStanding(t)
		d := Decide(s, f, st)
		acts := d.Delete || d.Ban || d.Review != nil

		if s.Enforcement.Mode.AsksAdmins() && (d.Ban || bansOnTimeout(d.Review)) {
			t.Fatalf("ask mode %s: got %+v, want no ban and no ban on timeout (ask-admins mode never auto-bans, §8.3)", s.Enforcement.Mode, d)
		}
		if s.Enforcement.Mode == EnforceAskBeforeAction && d.Delete {
			t.Fatalf("ask_before_action: got delete, want nothing deleted until an admin decides (§8.3 (b))")
		}
		if d.Review != nil && d.Review.Kind == ReviewAskBefore && d.Review.OnTimeout != TimeoutDeleteNoBan && d.Review.OnTimeout != TimeoutLeave {
			t.Fatalf("ask_before fallback %s, want delete_no_ban or leave (the fallback can never ban)", d.Review.OnTimeout)
		}
		if st.Established && (d.Ban || bansOnTimeout(d.Review)) {
			t.Fatalf("established member: got %+v, want no auto-ban (§8.1)", d)
		}
		if st.Compromised && (d.Ban || bansOnTimeout(d.Review)) {
			t.Fatalf("possibly hacked account: got %+v, want no auto-ban (§8.7)", d)
		}
		if st.IsBot && acts {
			t.Fatalf("the bot itself: got %+v, want nothing (§8.6)", d)
		}
		if (st.Role == RoleAdmin || st.Role == RoleSuperAdmin) && (d.Ban || (d.Review != nil && d.Review.Kind != ReviewAlert)) {
			t.Fatalf("admin: got %+v, want no action beyond an alert (§8.6, §8.7)", d)
		}
		if (st.Role == RoleAdmin || st.Role == RoleSuperAdmin) && d.Delete {
			t.Fatalf("admin: got delete, want admins never acted on (§8.6)")
		}
		if st.Allowlisted && st.Role == RoleMember && acts {
			t.Fatalf("allow-listed member: got %+v, want nothing (§8.6)", d)
		}
		if d.Ban && f.Source != SourceKnown && len(f.Violations) == 0 &&
			(f.Source != SourceAI || !cellAllows(s, f.Type, SourceAI)) {
			t.Fatalf("ban on source %q without a matrix cell allowing it: %+v (only CERTAIN evidence auto-bans by default, §9.2)", f.Source, d)
		}
		if f.Source == SourceRules && len(f.Violations) == 0 && (d.Ban || (d.Delete && !s.Uncertain.DeleteWhilePending)) {
			t.Fatalf("heuristics alone: got %+v, want no delete and no ban (§7.3)", d)
		}
		if priorsOnly(f) && !isSpam(f) && len(f.Violations) == 0 && acts {
			t.Fatalf("identity priors alone: got %+v, want nothing (§7.6)", d)
		}
		if f.Band == BandStillUncertain && len(f.Violations) == 0 && (d.Ban || (d.Delete && !s.Uncertain.DeleteWhilePending)) {
			t.Fatalf("still uncertain: got %+v, want admins and nothing deleted by default (§7.0)", d)
		}
		if isSpam(f) {
			c, ok := s.Enforcement.AutobanMatrix[f.Type]
			if ok && c.Effective(f.Source) == CellDeleteOnly && d.Ban {
				t.Fatalf("delete_only cell banned: %+v", d)
			}
			if ok && c.Effective(f.Source) == CellAllow && (d.Delete || d.Ban) {
				t.Fatalf("allow cell acted on spam: %+v (allow never deletes spam, §9.2)", d)
			}
		}
		if !isSpam(f) && len(f.Violations) > 0 && s.Policy.Action == ViolationDeleteOnly && s.Policy.Repeat == nil && d.Ban {
			t.Fatalf("policy violation alone banned with default policy: %+v (§9.3)", d)
		}
	})
}

func cellAllows(s Settings, ct ContentType, src Source) bool {
	c, ok := s.Enforcement.AutobanMatrix[ct]
	return ok && c.Effective(src) == CellAutoBan
}

// Known spam is deleted whatever the sender's trust: trust and standing only route the ban.
func TestKnownSpamDeletedWhateverTheTrust(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := genSettings(t)
		ct := rapid.SampledFrom(ContentTypes()).Draw(t, "type")
		c := s.Enforcement.AutobanMatrix[ct]
		if c.Effective(SourceKnown) == CellAllow || s.Enforcement.Mode == EnforceAskBeforeAction {
			t.Skip("the matrix allows this type or the group asks before acting")
		}
		f := Finding{Band: BandCertain, Source: SourceKnown, Type: ct, Score: 1, At: t0}
		st := Standing{
			Role: RoleMember, Established: rapid.Bool().Draw(t, "est"), Trust: rapid.Float64Range(0, 1).Draw(t, "trust"),
			Compromised: rapid.Bool().Draw(t, "hacked"), New: rapid.Bool().Draw(t, "new"),
		}
		if d := Decide(s, f, st); !d.Delete {
			t.Fatalf("known spam from %+v: got %+v, want deleted (no trust bonus, §7.3)", st, d)
		}
	})
}

// Raising the score never makes the decision milder for the same band and source (thresholds
// are monotonic): routing doesn't look at the score once the band is set.
func TestScoreDoesNotChangeRoute(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s, f, st := genSettings(t), genFinding(t), genStanding(t)
		g := f
		g.Score = rapid.Float64Range(f.Score, 1).Draw(t, "higher")
		a, b := Decide(s, f, st), Decide(s, g, st)
		if a.Delete && !b.Delete || a.Ban && !b.Ban {
			t.Fatalf("score %v → %+v but higher score %v → %+v; want at least as strong", f.Score, a, g.Score, b)
		}
	})
}

func TestRoutes(t *testing.T) {
	std := Defaults()
	askA, _ := PresetSettings(PresetAskAdmins)
	askB := askA.Clone()
	askB.Enforcement.Mode = EnforceAskBeforeAction
	relaxed, _ := PresetSettings(PresetRelaxed)
	allowVideos := Defaults()
	allowVideos.Enforcement.AutobanMatrix[TypeVideos] = Cell{Action: CellAllow}
	aiCell := Defaults()
	aiCell.Enforcement.AutobanMatrix[TypeText] = Cell{Action: CellAutoBan, RequireSource: []Source{SourceKnown, SourceAI}, Else: CellAsk}
	pending := Defaults()
	pending.Uncertain.DeleteWhilePending = true
	policyAsk := Defaults()
	policyAsk.Policy.Action = ViolationAsk
	policyBan := Defaults()
	policyBan.Policy.Action = ViolationBan
	repeat := Defaults()
	repeat.Policy.Repeat = &Repeat{Count: 3, Window: Duration(168 * time.Hour), Then: ViolationAsk}
	timeoutBan := Defaults()
	timeoutBan.Review.OnTimeout = TimeoutAutoBan
	hackedOff := timeoutBan.Clone()
	hackedOff.Compromised.Enabled = false

	known := func(ct ContentType) Finding {
		return Finding{Band: BandCertain, Source: SourceKnown, Type: ct, Score: 1, At: t0}
	}
	ai := Finding{Band: BandUncertain, Source: SourceAI, Type: TypeText, Score: 0.7, At: t0}
	unsure := Finding{Band: BandStillUncertain, Source: SourceRules, Type: TypeText, Score: 0.7, At: t0}
	violation := Finding{Band: BandClean, Type: TypeVideos, Violations: []Violation{{Rule: "content.videos"}}, At: t0}
	spamAndViolation := known(TypeVideos)
	spamAndViolation.Violations = violation.Violations
	priors := Finding{
		Band: BandUncertain, Source: SourceRules, Type: TypeText, Score: 0.7, At: t0,
		Signals: []Signal{{Code: "intel.prefix", Weight: 1.2, Prior: true}, {Code: "x", Weight: -0.3}},
	}
	newbie := Standing{Role: RoleMember, New: true}
	est := Standing{Role: RoleMember, Established: true}
	twoBefore := Standing{Role: RoleMember, RecentViolations: []time.Time{t0.Add(-time.Hour), t0.Add(-48 * time.Hour)}}
	oldOnes := Standing{Role: RoleMember, RecentViolations: []time.Time{t0.Add(-200 * time.Hour), t0.Add(-300 * time.Hour)}}

	tests := []struct {
		name    string
		s       Settings
		f       Finding
		st      Standing
		route   Route
		verdict Verdict
		del     bool
		ban     bool
		review  ReviewKind
		timeout TimeoutAction
	}{
		{"clean", std, Finding{Band: BandClean, Type: TypeText}, newbie, RouteNone, VerdictClean, false, false, "", ""},
		{"known spam from a newcomer: auto-ban", std, known(TypeText), newbie, RouteAutoBan, VerdictSpam, true, true, "", ""},
		{"known spam, cell ask", std, known(TypeAudio), newbie, RouteAsk, VerdictSpam, true, false, ReviewBanDecision, TimeoutKeepDeletedNoBan},
		{"known spam, cell delete_only", relaxed, known(TypeText), newbie, RouteDeleteOnly, VerdictSpam, true, false, "", ""},
		{"known spam, cell allow", allowVideos, known(TypeVideos), newbie, RouteAllow, VerdictSpam, false, false, "", ""},
		{"known spam, established", std, known(TypeText), est, RouteAsk, VerdictSpam, true, false, ReviewBanDecision, TimeoutKeepDeletedNoBan},
		{"known spam, hacked", std, known(TypeText), Standing{Role: RoleMember, Compromised: true}, RouteAsk, VerdictSpam, true, false, ReviewHacked, TimeoutKeepDeletedNoBan},
		{"known spam, ask (a)", askA, known(TypeText), newbie, RouteAsk, VerdictSpam, true, false, ReviewBanDecision, TimeoutKeepDeletedNoBan},
		{"known spam, ask (b)", askB, known(TypeText), newbie, RouteAsk, VerdictSpam, false, false, ReviewAskBefore, TimeoutDeleteNoBan},
		{"ai spam: admins decide (early-AI safety)", std, ai, newbie, RouteAsk, VerdictSpam, true, false, ReviewBanDecision, TimeoutKeepDeletedNoBan},
		{"ai spam, cell allows ai", aiCell, ai, newbie, RouteAutoBan, VerdictSpam, true, true, "", ""},
		{"ai spam: timeout never bans", timeoutBan, ai, newbie, RouteAsk, VerdictSpam, true, false, ReviewBanDecision, TimeoutKeepDeletedNoBan},
		{"known spam, cell ask: timeout may ban", timeoutBan, known(TypeAudio), newbie, RouteAsk, VerdictSpam, true, false, ReviewBanDecision, TimeoutAutoBan},
		{"known spam, established: timeout never bans", timeoutBan, known(TypeAudio), est, RouteAsk, VerdictSpam, true, false, ReviewBanDecision, TimeoutKeepDeletedNoBan},
		{"still uncertain", std, unsure, newbie, RouteAsk, VerdictClean, false, false, ReviewUncertain, TimeoutKeepDeletedNoBan},
		{"still uncertain, delete while pending", pending, unsure, newbie, RouteAsk, VerdictClean, true, false, ReviewUncertain, TimeoutKeepDeletedNoBan},
		{"still uncertain, ask (b)", askB, unsure, newbie, RouteAsk, VerdictClean, false, false, ReviewUncertain, TimeoutLeave},
		{"certain from heuristics is not trusted", std, Finding{Band: BandCertain, Source: SourceRules, Type: TypeText, At: t0}, newbie, RouteAsk, VerdictClean, false, false, ReviewUncertain, TimeoutKeepDeletedNoBan},
		{"priors alone", std, priors, newbie, RouteNone, VerdictClean, false, false, "", ""},
		{"violation: silent delete", std, violation, newbie, RoutePolicy, VerdictPolicy, true, false, "", ""},
		{"violation, established: silent delete", std, violation, est, RoutePolicy, VerdictPolicy, true, false, "", ""},
		{"violation, policy ask", policyAsk, violation, newbie, RoutePolicy, VerdictPolicy, true, false, ReviewBanDecision, TimeoutKeepDeletedNoBan},
		{"violation, policy ban", policyBan, violation, newbie, RoutePolicy, VerdictPolicy, true, true, "", ""},
		{"violation, policy ban, established", policyBan, violation, est, RoutePolicy, VerdictPolicy, true, false, ReviewBanDecision, TimeoutKeepDeletedNoBan},
		{"violation, ask (b)", askB, violation, newbie, RoutePolicy, VerdictPolicy, false, false, ReviewAskBefore, TimeoutDeleteNoBan},
		{"violation, third in a week", repeat, violation, twoBefore, RoutePolicy, VerdictPolicy, true, false, ReviewBanDecision, TimeoutKeepDeletedNoBan},
		{"violation, earlier ones expired", repeat, violation, oldOnes, RoutePolicy, VerdictPolicy, true, false, "", ""},
		{"spam and violation: spam route", std, spamAndViolation, newbie, RouteAutoBan, VerdictSpam, true, true, "", ""},
		{"admin", std, known(TypeText), Standing{Role: RoleAdmin}, RouteNone, VerdictSpam, false, false, "", ""},
		{"hacked admin: alert only", std, known(TypeText), Standing{Role: RoleAdmin, Compromised: true}, RouteNone, VerdictSpam, false, false, ReviewAlert, TimeoutDismiss},
		{"hacked member, cell delete_only", relaxed, known(TypeText), Standing{Role: RoleMember, Compromised: true}, RouteDeleteOnly, VerdictSpam, true, false, "", ""},
		{"allow-listed", std, known(TypeText), Standing{Role: RoleMember, Allowlisted: true}, RouteNone, VerdictSpam, false, false, "", ""},
		{"the bot", std, known(TypeText), Standing{IsBot: true}, RouteNone, VerdictClean, false, false, "", ""},
		{"unknown type: admins", std, known("reels"), newbie, RouteAsk, VerdictSpam, true, false, ReviewBanDecision, TimeoutKeepDeletedNoBan},
		{"hacked, response off: still never banned", hackedOff, known(TypeText), Standing{Role: RoleMember, Compromised: true}, RouteAsk, VerdictSpam, true, false, ReviewBanDecision, TimeoutKeepDeletedNoBan},
		{"certain + ai is not spam", std, Finding{Band: BandCertain, Source: SourceAI, Type: TypeText, At: t0}, newbie, RouteAsk, VerdictClean, false, false, ReviewUncertain, TimeoutKeepDeletedNoBan},
		{"violation dated now counts once", repeat, violation, Standing{Role: RoleMember, RecentViolations: []time.Time{t0, t0.Add(-time.Hour)}}, RoutePolicy, VerdictPolicy, true, false, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := Decide(tt.s, tt.f, tt.st)
			if d.Route != tt.route || d.Verdict != tt.verdict || d.Delete != tt.del || d.Ban != tt.ban {
				t.Errorf("got route %s, verdict %s, delete %v, ban %v; want %s, %s, %v, %v (%s)",
					d.Route, d.Verdict, d.Delete, d.Ban, tt.route, tt.verdict, tt.del, tt.ban, d.Reason)
			}
			switch {
			case tt.review == "" && d.Review != nil:
				t.Errorf("got review %+v, want none", *d.Review)
			case tt.review != "" && d.Review == nil:
				t.Errorf("got no review, want %s", tt.review)
			case tt.review != "" && (d.Review.Kind != tt.review || d.Review.OnTimeout != tt.timeout):
				t.Errorf("got review %s on timeout %s, want %s on timeout %s", d.Review.Kind, d.Review.OnTimeout, tt.review, tt.timeout)
			}
			if d.Reason == "" {
				t.Error("decision has no reason; every detection must be explainable")
			}
		})
	}
}

func TestPlanFor(t *testing.T) {
	d := Decide(Defaults(), Finding{Band: BandCertain, Source: SourceKnown, Type: TypeText}, Standing{Role: RoleMember})
	want := []Action{{Kind: ActionDelete, Priority: P1}, {Kind: ActionBan, Priority: P2}}
	tests := []struct {
		mode    Mode
		record  bool
		dmode   DetectionMode
		enqueue int
		would   int
	}{
		{ModeEnforce, true, DetectionEnforce, 2, 2},
		{ModeShadow, true, DetectionShadow, 0, 2},
		{ModePaused, false, "", 0, 0},
	}
	for _, tt := range tests {
		s := Defaults()
		s.Mode = tt.mode
		p := PlanFor(s, d)
		if p.Record != tt.record || p.Mode != tt.dmode || len(p.Enqueue) != tt.enqueue || len(p.Would) != tt.would {
			t.Errorf("%s: got %+v, want record %v, mode %q, %d enqueued, %d would-be (§8.4)", tt.mode, p, tt.record, tt.dmode, tt.enqueue, tt.would)
		}
		for i := range p.Enqueue {
			if p.Enqueue[i].Kind != want[i].Kind || p.Enqueue[i].Priority != want[i].Priority {
				t.Errorf("%s: action %d = %+v, want %+v", tt.mode, i, p.Enqueue[i], want[i])
			}
		}
	}
	clean := Decide(Defaults(), Finding{Band: BandClean}, Standing{Role: RoleMember})
	if p := PlanFor(Defaults(), clean); p.Record || len(p.Enqueue) != 0 {
		t.Errorf("clean message: got %+v, want nothing recorded or enqueued", p)
	}
	review := Decide(Defaults(), Finding{Band: BandStillUncertain, Source: SourceRules, Type: TypeText}, Standing{Role: RoleMember})
	if p := PlanFor(Defaults(), review); !p.Record || len(p.Enqueue) != 1 || p.Enqueue[0].Kind != ActionReview || p.Enqueue[0].Priority != P3 {
		t.Errorf("still uncertain: got %+v, want one recorded P3 review", p)
	}
}

func TestShadowNeverEnqueues(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s, f, st := genSettings(t), genFinding(t), genStanding(t)
		p := PlanFor(s, Decide(s, f, st))
		switch s.Mode {
		case ModeShadow:
			if len(p.Enqueue) != 0 || (p.Record && p.Mode != DetectionShadow) {
				t.Fatalf("shadow: got %+v, want nothing enqueued and mode shadow (§8.4)", p)
			}
		case ModePaused:
			if len(p.Enqueue) != 0 || p.Record || len(p.Would) != 0 {
				t.Fatalf("paused: got %+v, want nothing at all", p)
			}
		}
	})
}

// The optional ladder isn't built; enabling it must not change any decision (§18: the ladder
// is inert unless enabled, and nothing reads it yet).
func TestLadderInert(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s, f, st := genSettings(t), genFinding(t), genStanding(t)
		other := s.Clone()
		other.Ladder.Enabled = !s.Ladder.Enabled
		a, b := Decide(s, f, st), Decide(other, f, st)
		if a.Route != b.Route || a.Delete != b.Delete || a.Ban != b.Ban || (a.Review == nil) != (b.Review == nil) {
			t.Fatalf("ladder.enabled %v → %+v, %v → %+v; want the same decision", s.Ladder.Enabled, a, other.Ladder.Enabled, b)
		}
	})
}
