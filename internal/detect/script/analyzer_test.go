package script

import (
	"strings"
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/envelope"
)

func TestAnalyzer(t *testing.T) {
	tests := []struct {
		name   string
		texts  []envelope.TextSurface
		codes  string
		weight map[string]float64
	}{
		{
			"bilingual arabic english stays quiet",
			[]envelope.TextSurface{{Norm: "مرحبا everyone, meeting tomorrow في المكتب ❤️"}},
			"", nil,
		},
		{
			"one mixed word",
			[]envelope.TextSurface{{Norm: "get frее crypto"}},
			CodeMixedScript,
			map[string]float64{CodeMixedScript: MixedScriptWeight},
		},
		{
			"several mixed words across surfaces are capped",
			[]envelope.TextSurface{{Norm: "frее bοnus"}, {Norm: "pаypal аccount vеrify lоgin"}},
			CodeMixedScript,
			map[string]float64{CodeMixedScript: MixedScriptMax},
		},
		{
			"a couple of stray invisibles stay quiet",
			[]envelope.TextSurface{{Norm: "hello", Stripped: 2}},
			"", nil,
		},
		{
			"invisible padding",
			[]envelope.TextSurface{{Norm: "hello", Stripped: 4}, {Norm: "x", Stripped: 8}},
			CodeInvisible,
			map[string]float64{CodeInvisible: InvisibleHeavyWeight},
		},
		{
			"invisible flood",
			[]envelope.TextSurface{{Norm: "hi", Stripped: 400}},
			CodeInvisible,
			map[string]float64{CodeInvisible: InvisibleFloodWeight},
		},
	}
	a := New[struct{}]()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &envelope.Envelope{Texts: tt.texts}
			sigs, err := a.Analyze(t.Context(), e, &detect.State{})
			if err != nil {
				t.Fatal(err)
			}
			var codes []string
			for _, s := range sigs {
				codes = append(codes, s.Code)
				if s.Known || s.Hard {
					t.Errorf("%s is Known/Hard, want a weighted signal only (new heuristics never decide alone, DET-002)", s.Code)
				}
				if w := tt.weight[s.Code]; s.Weight != w {
					t.Errorf("%s weight = %v, want %v", s.Code, s.Weight, w)
				}
				if s.Evidence == "" {
					t.Errorf("%s has no evidence", s.Code)
				}
			}
			if got := strings.Join(codes, ","); got != tt.codes {
				t.Errorf("codes = %q, want %q", got, tt.codes)
			}
		})
	}
	if a.Applies(&envelope.Envelope{}, nil) || a.Tier() != detect.T1 {
		t.Error("Applies on an empty message or wrong tier")
	}
}
