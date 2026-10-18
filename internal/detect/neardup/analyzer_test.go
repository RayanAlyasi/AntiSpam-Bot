package neardup

import (
	"context"
	"strings"
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/envelope"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/scoring"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

const group wa.JID = "1203@g.us"

func TestAnalyzer(t *testing.T) {
	ix := NewIndex(DefaultThresholds())
	ix.Add("c1", FingerprintText(template))
	allowed := NewIndex(DefaultThresholds())
	allowed.Add("c1", FingerprintText(template))
	allowed.Allow(FingerprintText(template))
	words := strings.Fields(template)
	loose := strings.Join(append(words[:len(words)*2/3:len(words)*2/3], "hello", "friends", "please", "read",
		"this", "carefully", "because", "it", "matters", "to", "everyone"), " ")

	tests := []struct {
		name     string
		indexes  []*Index
		texts    []envelope.TextSurface
		wantCode string
		wantBand scoring.Band
	}{
		{"near-copy in the body is known", []*Index{ix}, []envelope.TextSurface{{Source: "body", Norm: template}}, CodeCertain, scoring.BandCertain},
		{"near-copy in a caption is known", []*Index{nil, ix}, []envelope.TextSurface{{Source: "body", Norm: "look"}, {Source: "caption", Norm: template}}, CodeCertain, scoring.BandCertain},
		{"loose match goes to verification only", []*Index{ix}, []envelope.TextSurface{{Source: "body", Norm: loose}}, CodeLoose, scoring.BandUncertain},
		{"allow-listed fingerprint never matches", []*Index{allowed}, []envelope.TextSurface{{Source: "body", Norm: template}}, "", scoring.BandClean},
		{"unnormalized text is not matched", []*Index{ix}, []envelope.TextSurface{{Source: "body", Raw: template}}, "", scoring.BandClean},
		{"homoglyph copy is known", []*Index{ix}, []envelope.TextSurface{{Source: "body", Norm: strings.ReplaceAll(template, "o", "\u043e")}}, CodeCertain, scoring.BandCertain},
		{"short replies are skipped", []*Index{ix}, []envelope.TextSurface{{Source: "body", Norm: "ok"}}, "", scoring.BandClean},
		{"no corpus", nil, []envelope.TextSurface{{Source: "body", Norm: template}}, "", scoring.BandClean},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := NewAnalyzer[struct{}](func(g wa.JID) []*Index {
				if g != group {
					t.Fatalf("asked for indexes of %s, want %s", g, group)
				}
				return tt.indexes
			})
			e := &envelope.Envelope{Group: group, Texts: tt.texts}
			if !a.Applies(e, nil) || a.Tier() != detect.T1 {
				t.Fatal("the analyzer must apply to text messages at T1")
			}
			sigs, err := a.Analyze(t.Context(), e, &detect.State{})
			if err != nil {
				t.Fatal(err)
			}
			var code string
			if len(sigs) > 1 {
				t.Fatalf("got %d signals, want at most one", len(sigs))
			}
			if len(sigs) == 1 {
				code = sigs[0].Code
				if sigs[0].Known != (code == CodeCertain) {
					t.Fatalf("signal %s Known=%v: only a certain match is known evidence (DESIGN §7.3)", code, sigs[0].Known)
				}
			}
			if code != tt.wantCode {
				t.Fatalf("signal %q, want %q", code, tt.wantCode)
			}
			v := scoring.Classify(scoring.Score(scoring.DefaultWeights(), sigs), scoring.DefaultThresholds())
			if v.Band != tt.wantBand {
				t.Fatalf("band %s, want %s (a loose match must reach T_review but never CERTAIN)", v.Band, tt.wantBand)
			}
			if code == CodeLoose && v.DeletesAsSpam() {
				t.Fatal("a loose match alone must never delete (DESIGN §7.0)")
			}
		})
	}
}

func TestAnalyzerRespectsContext(t *testing.T) {
	ix := NewIndex(DefaultThresholds())
	ix.Add("c1", FingerprintText(template))
	a := NewAnalyzer[struct{}](func(wa.JID) []*Index { return []*Index{ix} })
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := a.Analyze(ctx, &envelope.Envelope{Texts: []envelope.TextSurface{{Norm: template}}}, &detect.State{}); err == nil {
		t.Fatal("Analyze with a cancelled context must return its error")
	}
}
