package safety

import (
	"slices"
	"testing"
	"time"

	"pgregory.net/rapid"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/policy"
)

type met struct{}

func (met) AIMet() bool { return true }

var (
	presets = []policy.Preset{policy.PresetRelaxed, policy.PresetStandard, policy.PresetStrict, policy.PresetAskAdmins, policy.PresetCustom}
	modes   = []policy.EnforcementMode{policy.EnforceAuto, policy.EnforceAskDeleteFirst, policy.EnforceAskBeforeAction}
	cells   = []policy.Cell{
		{Action: policy.CellAutoBan, RequireSource: []policy.Source{policy.SourceKnown, policy.SourceAI}},
		{Action: policy.CellAutoBan, RequireSource: []policy.Source{policy.SourceAI}, Else: policy.CellDeleteOnly},
		{Action: policy.CellAutoBan, RequireSource: []policy.Source{policy.SourceAI}},
		{Action: policy.CellAutoBan, RequireSource: []policy.Source{policy.SourceAI}, Else: policy.CellAllow},
		{Action: policy.CellAutoBan},
		{Action: policy.CellAsk},
		{Action: policy.CellDeleteOnly},
	}
)

func settings(t testing.TB, p policy.Preset) policy.Settings {
	t.Helper()
	s, err := policy.PresetSettings(p)
	if err != nil {
		t.Fatalf("PresetSettings(%s): %v", p, err)
	}
	return s
}

func newcomer() policy.Standing { return policy.Standing{Role: policy.RoleMember, New: true} }

// Property (DESIGN §9.2, §18): while the track record is unmet, AI-verified spam never bans
// automatically, directly or through a review timing out, in any preset, mode, content type,
// sender standing or matrix cell, even one an admin set to auto-ban on ai.
func TestPropertyAISourceNeverAutoBansWithoutTrackRecord(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		s := settings(t, rapid.SampledFrom(presets).Draw(rt, "preset"))
		s.Enforcement.Mode = rapid.SampledFrom(modes).Draw(rt, "mode")
		typ := rapid.SampledFrom(policy.ContentTypes()).Draw(rt, "type")
		s.Enforcement.AutobanMatrix[typ] = rapid.SampledFrom(cells).Draw(rt, "cell")
		s.Review.OnTimeout = rapid.SampledFrom([]policy.TimeoutAction{policy.TimeoutAutoBan, policy.TimeoutKeepDeletedNoBan}).Draw(rt, "on_timeout")
		st := policy.Standing{
			Role:        rapid.SampledFrom([]policy.Role{policy.RoleMember, policy.RoleAdmin}).Draw(rt, "role"),
			Established: rapid.Bool().Draw(rt, "established"),
			Compromised: rapid.Bool().Draw(rt, "compromised"),
			New:         rapid.Bool().Draw(rt, "new"),
		}
		f := policy.Finding{Band: policy.BandUncertain, Source: policy.SourceAI, Score: 0.7, Type: typ, At: time.Unix(0, 0)}
		d := Decide(s, f, st, Unmet{})
		if d.Ban {
			rt.Fatalf("AI-only spam was auto-banned: %+v (early-AI safety, DESIGN §9.2)", d)
		}
		if d.Review != nil && d.Review.OnTimeout == policy.TimeoutAutoBan {
			rt.Fatalf("AI-only spam review bans on timeout: %+v", d.Review)
		}
		if st.Role == policy.RoleMember && d.Route != policy.RouteAsk && d.Route != policy.RouteDeleteOnly && d.Route != policy.RouteAllow {
			rt.Fatalf("route %s for AI-only spam from a member, want ask (or the cell's delete_only/allow)", d.Route)
		}
	})
}

// Property: a looser near-duplicate (source rules) never deletes or bans on its own.
func TestPropertyLooserNearDuplicateNeverActsAlone(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		s := settings(t, rapid.SampledFrom(presets).Draw(rt, "preset"))
		s.Enforcement.Mode = rapid.SampledFrom(modes).Draw(rt, "mode")
		typ := rapid.SampledFrom(policy.ContentTypes()).Draw(rt, "type")
		s.Enforcement.AutobanMatrix[typ] = rapid.SampledFrom(cells).Draw(rt, "cell")
		band := rapid.SampledFrom([]policy.Band{policy.BandUncertain, policy.BandStillUncertain}).Draw(rt, "band")
		f := policy.Finding{
			Band: band, Source: policy.SourceRules, Score: 0.8, Type: typ, At: time.Unix(0, 0),
			Signals: []policy.Signal{{Code: "near_duplicate", Weight: 3}},
		}
		d := Decide(s, f, newcomer(), met{})
		if d.Ban || (d.Delete && !s.Uncertain.DeleteWhilePending) {
			rt.Fatalf("source rules acted alone: %+v", d)
		}
	})
}

func TestAISpamDeletesAndAsksAdmins(t *testing.T) {
	for _, p := range presets {
		s := settings(t, p)
		for _, typ := range policy.ContentTypes() {
			s.Enforcement.AutobanMatrix[typ] = cells[0] // an admin asked for auto-ban on known and ai
		}
		f := policy.Finding{Band: policy.BandUncertain, Source: policy.SourceAI, Type: policy.TypeText, At: time.Unix(0, 0)}
		d := Decide(s, f, newcomer(), Unmet{})
		if d.Ban || d.Route != policy.RouteAsk || d.Review == nil || !d.Review.ProposeBan {
			t.Errorf("%s: %+v, want ask admins with a proposed ban, no auto-ban", p, d)
		}
		if s.Enforcement.Mode != policy.EnforceAskBeforeAction && !d.Delete {
			t.Errorf("%s: AI-verified spam was not deleted (DESIGN §7.0)", p)
		}
	}
}

func TestAutoBanOnAIOnlyWithTrackRecordAndExplicitCell(t *testing.T) {
	s := settings(t, policy.PresetStrict)
	f := policy.Finding{Band: policy.BandUncertain, Source: policy.SourceAI, Type: policy.TypeText, At: time.Unix(0, 0)}

	s.Enforcement.AutobanMatrix[policy.TypeText] = policy.Cell{Action: policy.CellAutoBan, RequireSource: []policy.Source{policy.SourceKnown}}
	if d := Decide(s, f, newcomer(), met{}); d.Ban {
		t.Fatalf("track record met but the cell lacks ai: %+v, want ask (never enabled automatically)", d)
	}
	s.Enforcement.AutobanMatrix[policy.TypeText] = cells[0]
	if d := Decide(s, f, newcomer(), met{}); !d.Ban || d.Route != policy.RouteAutoBan {
		t.Fatalf("track record met and the cell lists ai: %+v, want an auto-ban", d)
	}
	if d := Decide(s, f, newcomer(), Unmet{}); d.Ban {
		t.Fatalf("same cell, track record unmet: %+v, want no ban", d)
	}
}

func TestKnownEvidenceStillAutoBans(t *testing.T) {
	s := settings(t, policy.PresetStandard)
	s.Enforcement.AutobanMatrix[policy.TypeText] = policy.Cell{Action: policy.CellAutoBan, RequireSource: []policy.Source{policy.SourceKnown}}
	f := policy.Finding{Band: policy.BandCertain, Source: policy.SourceKnown, Type: policy.TypeText, At: time.Unix(0, 0)}
	if d := Decide(s, f, newcomer(), Unmet{}); !d.Ban || !d.Delete {
		t.Fatalf("known spam from a newcomer under Standard: %+v, want delete and ban (the guard only touches ai)", d)
	}
}

func TestSettingsDoesNotModifyInput(t *testing.T) {
	s := settings(t, policy.PresetStandard)
	s.Enforcement.AutobanMatrix[policy.TypeText] = cells[0]
	s.Enforcement.AutobanMatrix[policy.TypeLinks] = cells[1]
	g := Settings(s, Unmet{})
	if !slices.Contains(s.Enforcement.AutobanMatrix[policy.TypeText].RequireSource, policy.SourceAI) {
		t.Fatal("Settings() modified the caller's matrix")
	}
	for typ, c := range g.Enforcement.AutobanMatrix {
		if slices.Contains(c.RequireSource, policy.SourceAI) {
			t.Errorf("%s still lists ai: %v", typ, c)
		}
	}
	// cells[1] is "auto_ban [ai] / delete_only": with ai stripped it must become its fallback,
	// never auto_ban [known] (that would start auto-banning known spam the group set to delete).
	if got := g.Enforcement.AutobanMatrix[policy.TypeLinks]; got.Action != policy.CellDeleteOnly || got.Effective(policy.SourceKnown) != policy.CellDeleteOnly {
		t.Errorf("ai-only cell became %v, want its fallback delete_only for every source", got)
	}
	if AllowAISource(Unmet{}) || AllowAISource(nil) || !AllowAISource(met{}) {
		t.Error("AllowAISource must follow the track record, and a nil one is unmet")
	}
}
