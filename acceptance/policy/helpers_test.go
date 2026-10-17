package policy_test

import (
	"fmt"
	"slices"
	"time"

	"pgregory.net/rapid"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/policy"
)

// t0 is a fixed "now" for findings; nothing here reads the wall clock.
var t0 = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

var (
	namedPresets = []policy.Preset{policy.PresetRelaxed, policy.PresetStandard, policy.PresetStrict, policy.PresetAskAdmins}

	// gridTypes is the §9.2 row order, kept here so generators don't depend on ContentTypes().
	gridTypes = []policy.ContentType{
		policy.TypeText, policy.TypeLinks, policy.TypeImages, policy.TypeVideos, policy.TypeAudio,
		policy.TypeStickers, policy.TypeDocuments, policy.TypeContacts, policy.TypeChannelShares,
		policy.TypeGroupInvites, policy.TypePolls, policy.TypeLocationsEvents, policy.TypeStatus,
		policy.TypeViewOnce,
	}
	allBands       = []policy.Band{policy.BandClean, policy.BandCertain, policy.BandUncertain, policy.BandStillUncertain}
	allSources     = []policy.Source{policy.SourceNone, policy.SourceKnown, policy.SourceRules, policy.SourceAI}
	allCellActions = []policy.CellAction{policy.CellAutoBan, policy.CellAsk, policy.CellDeleteOnly, policy.CellAllow}
	allEnforce     = []policy.EnforcementMode{policy.EnforceAuto, policy.EnforceAskDeleteFirst, policy.EnforceAskBeforeAction}
	askModes       = []policy.EnforcementMode{policy.EnforceAskDeleteFirst, policy.EnforceAskBeforeAction}
	allModes       = []policy.Mode{policy.ModeShadow, policy.ModeEnforce, policy.ModePaused}
	allTimeouts    = []policy.TimeoutAction{
		policy.TimeoutKeepDeletedNoBan, policy.TimeoutDeleteNoBan, policy.TimeoutAutoBan,
		policy.TimeoutDismiss, policy.TimeoutLeave,
	}
	allViolationActions = []policy.ViolationAction{policy.ViolationDeleteOnly, policy.ViolationAsk, policy.ViolationBan}
	allRoles            = []policy.Role{policy.RoleMember, policy.RoleAdmin, policy.RoleSuperAdmin}
)

// fataler is the part of *testing.T and *rapid.T that mustPreset needs.
type fataler interface {
	Helper()
	Fatalf(format string, args ...any)
}

func mustPreset(t fataler, p policy.Preset) policy.Settings {
	t.Helper()
	s, err := policy.PresetSettings(p)
	if err != nil {
		t.Fatalf("PresetSettings(%q) error = %v; want nil (every §12.2 preset is valid)", p, err)
	}
	return s
}

// wantEffective is the test's own reading of a §9.2 cell: auto_ban only for a listed source
// (empty list = [known]); otherwise Else, where empty or auto_ban means ask.
func wantEffective(c policy.Cell, src policy.Source) policy.CellAction {
	if c.Action != policy.CellAutoBan {
		return c.Action
	}
	req := c.RequireSource
	if len(req) == 0 {
		req = []policy.Source{policy.SourceKnown}
	}
	if src != policy.SourceNone && slices.Contains(req, src) {
		return policy.CellAutoBan
	}
	if c.Else == "" || c.Else == policy.CellAutoBan {
		return policy.CellAsk
	}
	return c.Else
}

// genCell draws any matrix cell, including odd ones (auto_ban listing rules/ai, else auto_ban).
func genCell(t *rapid.T, label string) policy.Cell {
	c := policy.Cell{Action: rapid.SampledFrom(allCellActions).Draw(t, label+".action")}
	if c.Action == policy.CellAutoBan {
		for _, src := range []policy.Source{policy.SourceKnown, policy.SourceRules, policy.SourceAI} {
			if rapid.Bool().Draw(t, label+".require."+string(src)) {
				c.RequireSource = append(c.RequireSource, src)
			}
		}
		c.Else = rapid.SampledFrom([]policy.CellAction{"", policy.CellAsk, policy.CellDeleteOnly, policy.CellAllow, policy.CellAutoBan}).Draw(t, label+".else")
	}
	return c
}

// genSettings draws a named preset, then any matrix, enforcement mode, run mode, timeout
// fallback, violation policy and delete-while-pending flag. Settings need not be valid:
// Decide must stay safe even on a document that Validate would reject.
func genSettings(t *rapid.T) policy.Settings {
	s, err := policy.PresetSettings(rapid.SampledFrom(namedPresets).Draw(t, "preset"))
	if err != nil {
		t.Fatalf("PresetSettings error = %v; want nil (every §12.2 preset is valid)", err)
	}
	m := make(map[policy.ContentType]policy.Cell, len(gridTypes))
	for _, ct := range gridTypes {
		m[ct] = genCell(t, "cell."+string(ct))
	}
	s.Enforcement.AutobanMatrix = m
	s.Enforcement.Mode = rapid.SampledFrom(allEnforce).Draw(t, "enforcement.mode")
	s.Mode = rapid.SampledFrom(allModes).Draw(t, "mode")
	s.Review.OnTimeout = rapid.SampledFrom(allTimeouts).Draw(t, "review.on_timeout")
	s.Policy.Action = rapid.SampledFrom(allViolationActions).Draw(t, "policy.action")
	if rapid.Bool().Draw(t, "policy.repeat.set") {
		s.Policy.Repeat = &policy.Repeat{
			Count:  rapid.IntRange(1, 5).Draw(t, "policy.repeat.count"),
			Window: policy.Duration(168 * time.Hour),
			Then:   rapid.SampledFrom(allViolationActions).Draw(t, "policy.repeat.then"),
		}
	}
	s.Uncertain.DeleteWhilePending = rapid.Bool().Draw(t, "uncertain.delete_while_pending")
	return s
}

func genSignals(t *rapid.T, label string) []policy.Signal {
	n := rapid.IntRange(0, 5).Draw(t, label+".n")
	out := make([]policy.Signal, 0, n)
	for i := range n {
		out = append(out, policy.Signal{
			Code:   fmt.Sprintf("s%d", i),
			Weight: rapid.Float64Range(-3, 5).Draw(t, fmt.Sprintf("%s.%d.weight", label, i)),
			Prior:  rapid.Bool().Draw(t, fmt.Sprintf("%s.%d.prior", label, i)),
		})
	}
	return out
}

// contentSignal is a non-prior positive signal, so a finding is not "priors only".
var contentSignal = policy.Signal{Code: "content.match", Weight: 2.5}

func genViolations(t *rapid.T, ct policy.ContentType) []policy.Violation {
	n := rapid.IntRange(0, 2).Draw(t, "violations.n")
	out := make([]policy.Violation, 0, n)
	for range n {
		out = append(out, policy.Violation{Rule: "content." + string(ct), Detail: "blocked type"})
	}
	return out
}

// genFinding draws any finding: any band, source, type, signals and violations.
func genFinding(t *rapid.T) policy.Finding {
	ct := rapid.SampledFrom(gridTypes).Draw(t, "type")
	return policy.Finding{
		Band:       rapid.SampledFrom(allBands).Draw(t, "band"),
		Source:     rapid.SampledFrom(allSources).Draw(t, "source"),
		Score:      rapid.Float64Range(0, 1).Draw(t, "score"),
		Type:       ct,
		Signals:    genSignals(t, "signals"),
		Violations: genViolations(t, ct),
		At:         t0,
	}
}

// genSpamFinding draws spam per §7.0: CERTAIN/known or UNCERTAIN/ai, with a content signal.
func genSpamFinding(t *rapid.T) policy.Finding {
	f := policy.Finding{Type: rapid.SampledFrom(gridTypes).Draw(t, "type"), Score: rapid.Float64Range(0.6, 1).Draw(t, "score"), At: t0}
	if rapid.Bool().Draw(t, "known") {
		f.Band, f.Source = policy.BandCertain, policy.SourceKnown
	} else {
		f.Band, f.Source = policy.BandUncertain, policy.SourceAI
	}
	f.Signals = append([]policy.Signal{contentSignal}, genSignals(t, "signals")...)
	return f
}

func genRecent(t *rapid.T) []time.Time {
	n := rapid.IntRange(0, 5).Draw(t, "recent_violations.n")
	out := make([]time.Time, 0, n)
	for i := range n {
		out = append(out, t0.Add(-time.Duration(i+1)*time.Hour))
	}
	return out
}

// genStanding draws any sender, including admins, the bot and allow-listed members.
func genStanding(t *rapid.T) policy.Standing {
	return policy.Standing{
		Role:             rapid.SampledFrom(allRoles).Draw(t, "role"),
		IsBot:            rapid.Bool().Draw(t, "is_bot"),
		Allowlisted:      rapid.Bool().Draw(t, "allowlisted"),
		Established:      rapid.Bool().Draw(t, "established"),
		Compromised:      rapid.Bool().Draw(t, "compromised"),
		New:              rapid.Bool().Draw(t, "new"),
		Trust:            rapid.Float64Range(0, 1).Draw(t, "trust"),
		RecentViolations: genRecent(t),
	}
}

// genMember draws an ordinary member: not an admin, not the bot, not allow-listed.
func genMember(t *rapid.T) policy.Standing {
	st := genStanding(t)
	st.Role, st.IsBot, st.Allowlisted = policy.RoleMember, false, false
	return st
}

func newMember() policy.Standing {
	return policy.Standing{Role: policy.RoleMember, New: true, Trust: 0.1}
}

func count(actions []policy.Action, k policy.ActionKind) int {
	n := 0
	for _, a := range actions {
		if a.Kind == k {
			n++
		}
	}
	return n
}

func describe(f policy.Finding, st policy.Standing) string {
	return fmt.Sprintf("band=%s source=%q type=%s violations=%d | role=%s bot=%v allow=%v established=%v compromised=%v new=%v",
		f.Band, f.Source, f.Type, len(f.Violations), st.Role, st.IsBot, st.Allowlisted, st.Established, st.Compromised, st.New)
}

func describeDecision(d policy.Decision) string {
	r := "nil"
	if d.Review != nil {
		r = fmt.Sprintf("{kind=%s proposeBan=%v onTimeout=%s}", d.Review.Kind, d.Review.ProposeBan, d.Review.OnTimeout)
	}
	return fmt.Sprintf("verdict=%s route=%s delete=%v ban=%v review=%s reason=%q", d.Verdict, d.Route, d.Delete, d.Ban, r, d.Reason)
}
