package policy

import (
	"slices"
	"testing"
)

// grid is DESIGN.md §9.2's preset table, column by column, in ContentTypes order.
var grid = map[Preset][]string{
	PresetRelaxed: {
		"delete_only", "ask", "delete_only", "delete_only", "delete_only", "delete_only", "ask",
		"ask", "ask", "ask", "delete_only", "delete_only", "delete_only", "delete_only",
	},
	PresetStandard: {
		"auto_ban [known] / ask", "auto_ban [known] / ask", "auto_ban [known] / ask", "auto_ban [known] / ask", "ask",
		"auto_ban [known] / ask", "auto_ban [known] / ask", "auto_ban [known] / ask", "auto_ban [known] / ask", "auto_ban [known] / ask",
		"ask", "ask", "auto_ban [known] / ask", "ask",
	},
	PresetStrict: {
		"auto_ban [known] / ask", "auto_ban [known] / ask", "auto_ban [known] / ask", "auto_ban [known] / ask",
		"auto_ban [known] / ask", "auto_ban [known] / ask", "auto_ban [known] / ask", "auto_ban [known] / ask",
		"auto_ban [known] / ask", "auto_ban [known] / ask", "auto_ban [known] / ask", "auto_ban [known] / ask",
		"auto_ban [known] / ask", "ask",
	},
	PresetAskAdmins: {"ask", "ask", "ask", "ask", "ask", "ask", "ask", "ask", "ask", "ask", "ask", "ask", "ask", "ask"},
}

func TestPresetGrid(t *testing.T) {
	for p, want := range grid {
		s, err := PresetSettings(p)
		if err != nil {
			t.Fatal(err)
		}
		for i, ct := range ContentTypes() {
			if got := s.Enforcement.AutobanMatrix[ct].String(); got != want[i] {
				t.Errorf("%s/%s = %q, want %q (DESIGN §9.2 grid)", p, ct, got, want[i])
			}
		}
	}
}

func TestPresetExtras(t *testing.T) {
	tests := []struct {
		p      Preset
		mode   EnforcementMode
		delete float64
	}{
		{PresetRelaxed, EnforceAuto, 0.85},
		{PresetStandard, EnforceAuto, 0.85},
		{PresetStrict, EnforceAuto, 0.80},
		{PresetAskAdmins, EnforceAskDeleteFirst, 0.85},
	}
	for _, tt := range tests {
		s, _ := PresetSettings(tt.p)
		if s.Enforcement.Mode != tt.mode || s.Detection.Thresholds.Delete != tt.delete {
			t.Errorf("%s: mode %s, delete %v; want %s, %v (§12.2)", tt.p, s.Enforcement.Mode, s.Detection.Thresholds.Delete, tt.mode, tt.delete)
		}
		if !s.NewMembers.JustJoined.Enabled || s.Policy.Action != ViolationDeleteOnly || s.Ladder.Enabled || !s.Silent {
			t.Errorf("%s changed a shared default (silent, just-joined on, silent violation delete, ladder off; §12.2)", tt.p)
		}
	}
}

func TestSwitchingPresetResetsMatrixAndKeepsOverrides(t *testing.T) {
	s := Defaults()
	s.Enforcement.AutobanMatrix[TypeVideos] = Cell{Action: CellAllow}
	s.Review.Timeout = Duration(3600e9)
	if err := s.ApplyPreset(PresetStrict); err != nil {
		t.Fatal(err)
	}
	if got := s.Enforcement.AutobanMatrix[TypeVideos].Action; got != CellAutoBan {
		t.Errorf("videos after switching to strict = %s, want auto_ban (changing the preset resets the grid, §9.2)", got)
	}
	if s.Review.Timeout != Duration(3600e9) {
		t.Errorf("review.timeout = %v, want the override kept", s.Review.Timeout)
	}
	if err := s.ApplyPreset(PresetStandard); err != nil {
		t.Fatal(err)
	}
	if s.Detection.Thresholds.Delete != 0.85 {
		t.Errorf("delete line after strict → standard = %v, want 0.85", s.Detection.Thresholds.Delete)
	}
	if err := s.ApplyPreset("chaos"); err == nil {
		t.Error("ApplyPreset(chaos) = nil, want an error")
	}
}

func TestDiffFromPreset(t *testing.T) {
	s := Defaults()
	if d, err := s.DiffFromPreset(); err != nil || len(d) != 0 {
		t.Fatalf("DiffFromPreset of the defaults = %v, %v; want none", d, err)
	}
	s.Enforcement.AutobanMatrix[TypeVideos] = Cell{Action: CellAsk}
	s.Limits.MaxBansPerHour = 4
	d, err := s.DiffFromPreset()
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, c := range d {
		keys = append(keys, c.Key)
	}
	want := []string{
		"enforcement.autoban_matrix.videos.action", "enforcement.autoban_matrix.videos.else",
		"enforcement.autoban_matrix.videos.require_source", "limits.max_bans_per_hour",
	}
	if !slices.Equal(keys, want) {
		t.Errorf("DiffFromPreset keys = %v, want %v", keys, want)
	}
	if d[0].String() != `enforcement.autoban_matrix.videos.action: "auto_ban" → "ask"` {
		t.Errorf("change renders as %q", d[0])
	}
}

func TestCellEffective(t *testing.T) {
	tests := []struct {
		c    Cell
		src  Source
		want CellAction
	}{
		{autoKnown(), SourceKnown, CellAutoBan},
		{autoKnown(), SourceAI, CellAsk},
		{autoKnown(), SourceRules, CellAsk},
		{autoKnown(), SourceNone, CellAsk},
		{Cell{Action: CellAutoBan}, SourceKnown, CellAutoBan}, // empty require_source means [known]
		{Cell{Action: CellAutoBan}, SourceAI, CellAsk},        // empty else means ask
		{Cell{Action: CellAutoBan, RequireSource: []Source{SourceKnown, SourceAI}}, SourceAI, CellAutoBan},
		{Cell{Action: CellAutoBan, Else: CellDeleteOnly}, SourceAI, CellDeleteOnly},
		{Cell{Action: CellAutoBan, Else: CellAutoBan}, SourceAI, CellAsk},
		{cell(CellAsk), SourceKnown, CellAsk},
		{cell(CellDeleteOnly), SourceKnown, CellDeleteOnly},
		{cell(CellAllow), SourceKnown, CellAllow},
	}
	for _, tt := range tests {
		if got := tt.c.Effective(tt.src); got != tt.want {
			t.Errorf("%s.Effective(%q) = %s, want %s", tt.c, tt.src, got, tt.want)
		}
	}
}
