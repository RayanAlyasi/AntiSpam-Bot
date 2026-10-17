package safety

import (
	"reflect"
	"testing"
	"time"

	"pgregory.net/rapid"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/policy"
)

// Property (DESIGN §18: auto_ban with require_source never bans on other sources): for any
// evidence other than ai, the guard decides exactly as policy.Decide, including cells an admin
// set to auto-ban on ai only.
func TestPropertyNonAIEvidenceDecidesAsPolicy(t *testing.T) {
	aiCells := []policy.Cell{
		{Action: policy.CellAutoBan, RequireSource: []policy.Source{policy.SourceAI}},
		{Action: policy.CellAutoBan, RequireSource: []policy.Source{policy.SourceAI}, Else: policy.CellDeleteOnly},
		{Action: policy.CellAutoBan, RequireSource: []policy.Source{policy.SourceAI}, Else: policy.CellAllow},
		{Action: policy.CellAutoBan, RequireSource: []policy.Source{policy.SourceAI, policy.SourceKnown}, Else: policy.CellDeleteOnly},
	}
	rapid.Check(t, func(rt *rapid.T) {
		s := settings(t, rapid.SampledFrom(presets).Draw(rt, "preset"))
		s.Enforcement.Mode = rapid.SampledFrom(modes).Draw(rt, "mode")
		typ := rapid.SampledFrom(policy.ContentTypes()).Draw(rt, "type")
		s.Enforcement.AutobanMatrix[typ] = rapid.SampledFrom(append(aiCells, cells...)).Draw(rt, "cell")
		src := rapid.SampledFrom([]policy.Source{policy.SourceKnown, policy.SourceRules, policy.SourceNone}).Draw(rt, "source")
		band := rapid.SampledFrom([]policy.Band{policy.BandCertain, policy.BandUncertain, policy.BandStillUncertain, policy.BandClean}).Draw(rt, "band")
		st := policy.Standing{Role: policy.RoleMember, New: rapid.Bool().Draw(rt, "new"), Established: rapid.Bool().Draw(rt, "established")}
		f := policy.Finding{Band: band, Source: src, Score: 0.9, Type: typ, At: time.Unix(0, 0)}
		got, want := Decide(s, f, st, Unmet{}), policy.Decide(s, f, st)
		if !reflect.DeepEqual(got, want) {
			rt.Fatalf("source %s with cell %v: guard decided %+v, policy %+v; the guard must only change ai evidence", src, s.Enforcement.AutobanMatrix[typ], got, want)
		}
	})
}

func TestAIOnlyCellKeepsItsFallback(t *testing.T) {
	tests := []struct {
		cell policy.Cell
		want policy.CellAction
	}{
		{policy.Cell{Action: policy.CellAutoBan, RequireSource: []policy.Source{policy.SourceAI}}, policy.CellAsk},
		{policy.Cell{Action: policy.CellAutoBan, RequireSource: []policy.Source{policy.SourceAI}, Else: policy.CellDeleteOnly}, policy.CellDeleteOnly},
		{policy.Cell{Action: policy.CellAutoBan, RequireSource: []policy.Source{policy.SourceAI}, Else: policy.CellAllow}, policy.CellAllow},
	}
	for _, tt := range tests {
		s := settings(t, policy.PresetStandard)
		s.Enforcement.AutobanMatrix[policy.TypeLinks] = tt.cell
		c := Settings(s, Unmet{}).Enforcement.AutobanMatrix[policy.TypeLinks]
		for _, src := range []policy.Source{policy.SourceKnown, policy.SourceRules, policy.SourceAI} {
			if got := c.Effective(src); got != tt.want {
				t.Errorf("cell %v, source %s: Effective = %s, want %s (the cell's fallback; known never gains an auto-ban)", tt.cell, src, got, tt.want)
			}
		}
	}
}
