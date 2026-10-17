package policy_test

import (
	"slices"
	"testing"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/policy"
)

var (
	abKnown = policy.Cell{Action: policy.CellAutoBan, RequireSource: []policy.Source{policy.SourceKnown}, Else: policy.CellAsk}
	ask     = policy.Cell{Action: policy.CellAsk}
	delOnly = policy.Cell{Action: policy.CellDeleteOnly}
)

// designGrid is the §9.2 preset table, row by row in gridTypes order.
var designGrid = map[policy.Preset][]policy.Cell{
	//                      text     links  images   videos   audio    stickers docs   contacts channels invites polls    locations status   view_once
	policy.PresetRelaxed:   {delOnly, ask, delOnly, delOnly, delOnly, delOnly, ask, ask, ask, ask, delOnly, delOnly, delOnly, delOnly},
	policy.PresetStandard:  {abKnown, abKnown, abKnown, abKnown, ask, abKnown, abKnown, abKnown, abKnown, abKnown, ask, ask, abKnown, ask},
	policy.PresetStrict:    {abKnown, abKnown, abKnown, abKnown, abKnown, abKnown, abKnown, abKnown, abKnown, abKnown, abKnown, abKnown, abKnown, ask},
	policy.PresetAskAdmins: {ask, ask, ask, ask, ask, ask, ask, ask, ask, ask, ask, ask, ask, ask},
}

// sameCell compares cells by meaning: the same action and the same effective action for every
// source, so an empty require_source equals [known] and an empty else equals ask (§12.1).
func sameCell(got, want policy.Cell) bool {
	if got.Action != want.Action {
		return false
	}
	for _, src := range allSources {
		if wantEffective(got, src) != wantEffective(want, src) {
			return false
		}
	}
	return true
}

func checkGrid(t *testing.T, p policy.Preset, m map[policy.ContentType]policy.Cell) {
	t.Helper()
	if len(m) != len(gridTypes) {
		t.Errorf("%s: matrix has %d cells, want %d (one per §9.2 content type)", p, len(m), len(gridTypes))
	}
	for i, ct := range gridTypes {
		got, ok := m[ct]
		want := designGrid[p][i]
		if !ok || !sameCell(got, want) {
			t.Errorf("%s/%s: got cell %+v (present=%v), want %+v (preset grid, DESIGN §9.2)", p, ct, got, ok, want)
		}
	}
}

func TestContentTypesInGridOrder(t *testing.T) {
	if got := policy.ContentTypes(); !slices.Equal(got, gridTypes) {
		t.Fatalf("ContentTypes() = %v, want %v (all 14 matrix rows in DESIGN §9.2 order)", got, gridTypes)
	}
}

func TestPresetMatrixMatchesDesignGrid(t *testing.T) {
	for _, p := range namedPresets {
		t.Run(string(p), func(t *testing.T) {
			s := mustPreset(t, p)
			if s.Preset != p {
				t.Errorf("Preset = %q, want %q (the document records its preset, DESIGN §12.1)", s.Preset, p)
			}
			checkGrid(t, p, s.Enforcement.AutobanMatrix)
			wantMode := policy.EnforceAuto
			if p == policy.PresetAskAdmins {
				wantMode = policy.EnforceAskDeleteFirst
			}
			if s.Enforcement.Mode != wantMode {
				t.Errorf("enforcement.mode = %q, want %q (Ask admins sets ask_delete_first, others auto, DESIGN §12.2)", s.Enforcement.Mode, wantMode)
			}
			if err := s.Validate(); err != nil {
				t.Errorf("Validate() = %v, want nil (presets are valid documents, DESIGN §12.2)", err)
			}
		})
	}
}

func TestDefaultsAreStandardAndConservative(t *testing.T) {
	s := policy.Defaults()
	checkGrid(t, policy.PresetStandard, s.Enforcement.AutobanMatrix)
	checks := []struct {
		name      string
		got, want any
		why       string
	}{
		{"preset", s.Preset, policy.PresetStandard, "Standard is the default preset, §12.2"},
		{"mode", s.Mode, policy.ModeEnforce, "§12.1"},
		{"silent", s.Silent, true, "silent by default, §9.7"},
		{"enforcement.mode", s.Enforcement.Mode, policy.EnforceAuto, "§8.3"},
		{"enforcement.established.min_days", s.Enforcement.Established.MinDays, 7, "§8.1"},
		{"enforcement.established.min_clean_messages", s.Enforcement.Established.MinCleanMessages, 20, "§8.1"},
		{"enforcement.ban.rejoin_block", s.Enforcement.Ban.RejoinBlock, true, "ban = kick + rejoin block, §9.1"},
		{"uncertain.delete_while_pending", s.Uncertain.DeleteWhilePending, false, "still-uncertain stays visible, §7.0"},
		{"policy.action", s.Policy.Action, policy.ViolationDeleteOnly, "silent delete, §9.3"},
		{"policy.repeat set", s.Policy.Repeat != nil, false, "repeat is opt-in, §9.3"},
		{"review.timeout", s.Review.Timeout, policy.Duration(6 * time.Hour), "§9.5"},
		{"review.on_timeout", s.Review.OnTimeout, policy.TimeoutKeepDeletedNoBan, "§9.5"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("Defaults().%s = %v, want %v (%s)", c.name, c.got, c.want, c.why)
		}
	}
	if err := s.Validate(); err != nil {
		t.Errorf("Defaults().Validate() = %v, want nil", err)
	}
}

func TestApplyPresetResetsMatrix(t *testing.T) {
	s := policy.Defaults()
	s.Enforcement.AutobanMatrix[policy.TypeVideos] = delOnly
	s.Enforcement.AutobanMatrix[policy.TypeText] = policy.Cell{Action: policy.CellAllow}
	if err := s.ApplyPreset(policy.PresetStrict); err != nil {
		t.Fatalf("ApplyPreset(strict) = %v, want nil", err)
	}
	if s.Preset != policy.PresetStrict {
		t.Errorf("Preset = %q after ApplyPreset(strict), want strict", s.Preset)
	}
	checkGrid(t, policy.PresetStrict, s.Enforcement.AutobanMatrix) // changing the preset resets the grid, §9.2

	if err := s.ApplyPreset(policy.PresetAskAdmins); err != nil {
		t.Fatalf("ApplyPreset(ask_admins) = %v, want nil", err)
	}
	checkGrid(t, policy.PresetAskAdmins, s.Enforcement.AutobanMatrix)
	if s.Enforcement.Mode != policy.EnforceAskDeleteFirst {
		t.Errorf("enforcement.mode = %q after ApplyPreset(ask_admins), want ask_delete_first (DESIGN §12.2)", s.Enforcement.Mode)
	}
}

func TestPresetMatricesAreIndependent(t *testing.T) {
	a := mustPreset(t, policy.PresetStandard)
	a.Enforcement.AutobanMatrix[policy.TypeLinks] = policy.Cell{Action: policy.CellAllow}
	b := mustPreset(t, policy.PresetStandard)
	c := policy.Defaults()
	if !sameCell(b.Enforcement.AutobanMatrix[policy.TypeLinks], abKnown) || !sameCell(c.Enforcement.AutobanMatrix[policy.TypeLinks], abKnown) {
		t.Fatalf("editing one group's matrix changed another's (links now %+v / %+v), want independent copies (each group has its own settings document, DESIGN §12.1)",
			b.Enforcement.AutobanMatrix[policy.TypeLinks], c.Enforcement.AutobanMatrix[policy.TypeLinks])
	}
}

func TestUnknownPresetRejected(t *testing.T) {
	if _, err := policy.PresetSettings("lenient"); err == nil {
		t.Errorf("PresetSettings(\"lenient\") error = nil, want an error (only the §12.2 presets exist)")
	}
	s := policy.Defaults()
	if err := s.ApplyPreset("lenient"); err == nil {
		t.Errorf("ApplyPreset(\"lenient\") error = nil, want an error (only the §12.2 presets exist)")
	}
}

func TestValidateRejectsAutoBanTimeoutInAskModes(t *testing.T) {
	for _, m := range askModes {
		s := mustPreset(t, policy.PresetAskAdmins)
		s.Enforcement.Mode = m
		s.Review.OnTimeout = policy.TimeoutAutoBan
		if err := s.Validate(); err == nil {
			t.Errorf("%s with review.on_timeout=auto_ban: Validate() = nil, want an error (a timeout must never ban in ask-admins mode, DESIGN §9.5)", m)
		}
	}
}

func TestCellEffective(t *testing.T) {
	known, rules, ai, none := policy.SourceKnown, policy.SourceRules, policy.SourceAI, policy.SourceNone
	cases := []struct {
		name string
		cell policy.Cell
		want map[policy.Source]policy.CellAction
	}{
		{
			"auto_ban default require_source is known, default else is ask",
			policy.Cell{Action: policy.CellAutoBan},
			map[policy.Source]policy.CellAction{known: policy.CellAutoBan, rules: policy.CellAsk, ai: policy.CellAsk, none: policy.CellAsk},
		},
		{
			"auto_ban [known] else ask",
			abKnown,
			map[policy.Source]policy.CellAction{known: policy.CellAutoBan, rules: policy.CellAsk, ai: policy.CellAsk, none: policy.CellAsk},
		},
		{
			"auto_ban [known,ai] else delete_only",
			policy.Cell{Action: policy.CellAutoBan, RequireSource: []policy.Source{known, ai}, Else: policy.CellDeleteOnly},
			map[policy.Source]policy.CellAction{known: policy.CellAutoBan, ai: policy.CellAutoBan, rules: policy.CellDeleteOnly, none: policy.CellDeleteOnly},
		},
		{
			"auto_ban [ai] does not ban on known",
			policy.Cell{Action: policy.CellAutoBan, RequireSource: []policy.Source{ai}},
			map[policy.Source]policy.CellAction{known: policy.CellAsk, ai: policy.CellAutoBan, rules: policy.CellAsk, none: policy.CellAsk},
		},
		{
			"auto_ban else auto_ban is treated as ask",
			policy.Cell{Action: policy.CellAutoBan, RequireSource: []policy.Source{known}, Else: policy.CellAutoBan},
			map[policy.Source]policy.CellAction{known: policy.CellAutoBan, ai: policy.CellAsk, rules: policy.CellAsk, none: policy.CellAsk},
		},
		{"ask", ask, map[policy.Source]policy.CellAction{known: policy.CellAsk, ai: policy.CellAsk, rules: policy.CellAsk, none: policy.CellAsk}},
		{"delete_only", delOnly, map[policy.Source]policy.CellAction{known: policy.CellDeleteOnly, ai: policy.CellDeleteOnly, rules: policy.CellDeleteOnly, none: policy.CellDeleteOnly}},
		{"allow", policy.Cell{Action: policy.CellAllow}, map[policy.Source]policy.CellAction{known: policy.CellAllow, ai: policy.CellAllow, rules: policy.CellAllow, none: policy.CellAllow}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for src, want := range c.want {
				if got := c.cell.Effective(src); got != want {
					t.Errorf("Effective(%q) = %q, want %q (auto_ban only for a listed source, else the fallback, never a ban on other evidence, DESIGN §9.2)", src, got, want)
				}
			}
		})
	}
}
