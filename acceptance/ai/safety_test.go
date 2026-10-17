package ai_test

import (
	"reflect"
	"slices"
	"testing"

	"pgregory.net/rapid"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai/safety"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/policy"
)

// genStanding draws any sender standing, including admins, the bot and hacked accounts.
func genStanding(t *rapid.T) policy.Standing {
	return policy.Standing{
		Role:        rapid.SampledFrom(allRoles).Draw(t, "role"),
		IsBot:       rapid.Float64Range(0, 1).Draw(t, "bot") < 0.05,
		Allowlisted: rapid.Bool().Draw(t, "allowlisted"),
		Established: rapid.Bool().Draw(t, "established"),
		Compromised: rapid.Bool().Draw(t, "compromised"),
		New:         rapid.Bool().Draw(t, "new"),
		Trust:       rapid.Float64Range(0, 1).Draw(t, "trust"),
	}
}

// genSettings draws a preset, an enforcement mode, and optionally a matrix cell for ct that
// explicitly lists extra sources (as a group might have saved after a v0.4 track record).
func genSettings(t *rapid.T, ct policy.ContentType, extra ...policy.Source) policy.Settings {
	s := mustPreset(t, rapid.SampledFrom(namedPresets).Draw(t, "preset")).Clone()
	s.Enforcement.Mode = rapid.SampledFrom(allEnforce).Draw(t, "enforcement")
	s.Uncertain.DeleteWhilePending = false
	if len(extra) > 0 && rapid.Bool().Draw(t, "listExtraSource") {
		s.Enforcement.AutobanMatrix[ct] = policy.Cell{
			Action:        policy.CellAutoBan,
			RequireSource: append([]policy.Source{policy.SourceKnown}, extra...),
		}
	}
	return s
}

func genTrack(t *rapid.T) safety.TrackRecord {
	return rapid.SampledFrom([]safety.TrackRecord{safety.Unmet{}, notMet{}}).Draw(t, "trackRecord")
}

// TestPropertyAISourceNeverAutoBansWithoutTrackRecord: AI-only evidence never bans while the
// track record is unmet, whatever the preset, type, mode, cell or standing.
func TestPropertyAISourceNeverAutoBansWithoutTrackRecord(t *testing.T) {
	if (safety.Unmet{}).AIMet() {
		t.Fatalf("Unmet.AIMet() = true; want false (v0.1 never meets the track record, DESIGN §9.2)")
	}
	rapid.Check(t, func(rt *rapid.T) {
		ct := rapid.SampledFrom(policy.ContentTypes()).Draw(rt, "type")
		s := genSettings(rt, ct, policy.SourceAI)
		st := genStanding(rt)
		f := policy.Finding{Band: policy.BandUncertain, Source: policy.SourceAI, Score: 0.9, Type: ct, At: t0}
		tr := genTrack(rt)

		d := safety.Decide(s, f, st, tr)
		if d.Ban || d.Route == policy.RouteAutoBan || hasAction(d, policy.ActionBan) {
			rt.Fatalf("%s/%s/%s cell %v: Ban = %v, route = %s; want no ban (source ai routes to ask admins until the track record is met, DESIGN §9.2)",
				s.Preset, s.Enforcement.Mode, ct, s.Enforcement.AutobanMatrix[ct], d.Ban, d.Route)
		}
		if d.Review != nil && d.Review.OnTimeout == policy.TimeoutAutoBan {
			rt.Fatalf("%s/%s/%s: review on_timeout = auto_ban; want a fallback that can't ban (an AI-only ban needs a human, DESIGN §9.2)", s.Preset, s.Enforcement.Mode, ct)
		}
		// Where plain policy would have banned, the ban becomes a review and the delete stays.
		if pd := policy.Decide(s, f, st); pd.Ban {
			if d.Review == nil || !d.Review.ProposeBan {
				rt.Fatalf("%s/%s/%s: review = %+v; want a review proposing the ban (the ban goes to admins, DESIGN §9.2)", s.Preset, s.Enforcement.Mode, ct, d.Review)
			}
			if d.Delete != pd.Delete {
				rt.Fatalf("%s/%s/%s: Delete = %v; want %v (the safety rule only moves the ban, the message is still deleted, DESIGN §8.3)", s.Preset, s.Enforcement.Mode, ct, d.Delete, pd.Delete)
			}
		}
	})
}

// TestPropertyLooserNearDuplicateNeverActsAlone: source rules (heuristics, near-duplicates
// below the tight CERTAIN thresholds) never deletes or bans, with or without a track record.
func TestPropertyLooserNearDuplicateNeverActsAlone(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		ct := rapid.SampledFrom(policy.ContentTypes()).Draw(rt, "type")
		s := genSettings(rt, ct, policy.SourceRules, policy.SourceAI)
		st := genStanding(rt)
		band := rapid.SampledFrom([]policy.Band{policy.BandUncertain, policy.BandStillUncertain}).Draw(rt, "band")
		f := policy.Finding{
			Band: band, Source: policy.SourceRules, Score: rapid.Float64Range(0.6, 1).Draw(rt, "score"), Type: ct, At: t0,
			Signals: []policy.Signal{{Code: "fingerprint.simhash_near", Weight: 3}},
		}
		tr := rapid.SampledFrom([]safety.TrackRecord{safety.Unmet{}, met{}}).Draw(rt, "trackRecord")

		d := safety.Decide(s, f, st, tr)
		if d.Delete || d.Ban || d.Route == policy.RouteAutoBan {
			rt.Fatalf("%s/%s/%s %s/rules (track record met=%v): Delete = %v, Ban = %v, route = %s; want neither (a looser near-duplicate only feeds the AI, DESIGN §9.2, §18)",
				s.Preset, s.Enforcement.Mode, ct, band, tr.AIMet(), d.Delete, d.Ban, d.Route)
		}
	})
}

// TestAIAutoBanOnlyWithTrackRecordAndExplicitCell: a met track record alone enables nothing;
// the cell must list ai explicitly, and the usual guarantees still hold.
func TestAIAutoBanOnlyWithTrackRecordAndExplicitCell(t *testing.T) {
	aiSpam := policy.Finding{Band: policy.BandUncertain, Source: policy.SourceAI, Score: 0.9, Type: policy.TypeLinks, At: t0}

	if safety.AllowAISource(safety.Unmet{}) || safety.AllowAISource(notMet{}) {
		t.Errorf("AllowAISource(unmet) = true; want false (ai is greyed out of auto-ban until the track record, DESIGN §9.2)")
	}
	if !safety.AllowAISource(met{}) {
		t.Errorf("AllowAISource(met) = false; want true (after the track record ai may be chosen explicitly)")
	}

	// Met, but no preset lists ai: still ask.
	for _, p := range namedPresets {
		d := safety.Decide(mustPreset(t, p), aiSpam, newMember, met{})
		if d.Ban {
			t.Errorf("%s with track record met: Ban = true; want false (ai is never added to a cell automatically, DESIGN §9.2)", p)
		}
	}

	explicit := mustPreset(t, policy.PresetStandard).Clone()
	explicit.Enforcement.AutobanMatrix[policy.TypeLinks] = policy.Cell{
		Action: policy.CellAutoBan, RequireSource: []policy.Source{policy.SourceKnown, policy.SourceAI},
	}

	if d := safety.Decide(explicit, aiSpam, newMember, met{}); !d.Ban || !d.Delete || d.Route != policy.RouteAutoBan {
		t.Errorf("met + cell [known, ai]: Ban = %v, Delete = %v, route = %s; want auto-ban (explicit choice after the track record, DESIGN §9.2)", d.Ban, d.Delete, d.Route)
	}
	if d := safety.Decide(explicit, aiSpam, newMember, safety.Unmet{}); d.Ban {
		t.Errorf("unmet + cell [known, ai]: Ban = true; want false (a saved ai cell is inert until the track record, DESIGN §9.2)")
	}
	est := policy.Standing{Role: policy.RoleMember, Established: true, Trust: 1}
	if d := safety.Decide(explicit, aiSpam, est, met{}); d.Ban {
		t.Errorf("met + cell [known, ai], established sender: Ban = true; want false (established members are never auto-banned, DESIGN §8.3)")
	}
	ask := explicit.Clone()
	ask.Enforcement.Mode = policy.EnforceAskDeleteFirst
	if d := safety.Decide(ask, aiSpam, newMember, met{}); d.Ban {
		t.Errorf("met + cell [known, ai], ask_delete_first: Ban = true; want false (ask-admins mode never auto-bans, DESIGN §8.3)")
	}

	// Settings strips ai while unmet and leaves the input alone.
	before := explicit.Clone()
	stripped := safety.Settings(explicit, safety.Unmet{})
	if slices.Contains(stripped.Enforcement.AutobanMatrix[policy.TypeLinks].RequireSource, policy.SourceAI) {
		t.Errorf("Settings(unmet) links cell = %v; want ai removed (DESIGN §9.2)", stripped.Enforcement.AutobanMatrix[policy.TypeLinks])
	}
	if !reflect.DeepEqual(explicit, before) {
		t.Errorf("Settings modified its input; want the group's stored settings untouched")
	}
	kept := safety.Settings(explicit, met{})
	if !slices.Contains(kept.Enforcement.AutobanMatrix[policy.TypeLinks].RequireSource, policy.SourceAI) {
		t.Errorf("Settings(met) links cell = %v; want ai kept (the admin chose it explicitly)", kept.Enforcement.AutobanMatrix[policy.TypeLinks])
	}
}

// TestKnownEvidenceStillAutoBans is the control for the properties above: known evidence
// follows the matrix unchanged, so they aren't vacuously true.
func TestKnownEvidenceStillAutoBans(t *testing.T) {
	known := policy.Finding{
		Band: policy.BandCertain, Source: policy.SourceKnown, Score: 0.99, Type: policy.TypeLinks, At: t0,
		Signals: []policy.Signal{{Code: "url.blocklisted", Weight: 4, Known: true}},
	}
	d := safety.Decide(mustPreset(t, policy.PresetStandard), known, newMember, safety.Unmet{})
	if !d.Ban || !d.Delete || d.Route != policy.RouteAutoBan {
		t.Fatalf("Standard, known spam link from a new member: Ban = %v, Delete = %v, route = %s; want auto-ban (CERTAIN known evidence follows the matrix, DESIGN §9.2)", d.Ban, d.Delete, d.Route)
	}

	rapid.Check(t, func(rt *rapid.T) {
		ct := rapid.SampledFrom(policy.ContentTypes()).Draw(rt, "type")
		s := genSettings(rt, ct, policy.SourceAI)
		st := genStanding(rt)
		f := known
		f.Type = ct
		tr := rapid.SampledFrom([]safety.TrackRecord{safety.Unmet{}, met{}}).Draw(rt, "trackRecord")
		got, want := safety.Decide(s, f, st, tr), policy.Decide(s, f, st)
		if !reflect.DeepEqual(got, want) {
			rt.Fatalf("%s/%s/%s known: safety.Decide = %+v; want policy.Decide's %+v (known evidence is untouched by the early-AI rule, DESIGN §9.2)", s.Preset, s.Enforcement.Mode, ct, got, want)
		}
	})
}
