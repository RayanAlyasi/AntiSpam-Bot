package script

import (
	"context"
	"fmt"
	"strings"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/envelope"
)

// Signal codes.
const (
	// CodeMixedScript: words that mix look-alike scripts.
	CodeMixedScript = "text.mixed_script"
	// CodeInvisible: invisible, combining-flood and decorative characters
	// stripped from the text (normalize.Result.Stripped).
	CodeInvisible = "text.invisible_chars"
)

// Weights. Both are obfuscation hints, never enough on their own to reach T_review.
const (
	MixedScriptWeight     = 1.5 // first mixed word
	MixedScriptStep       = 0.5 // each further mixed word
	MixedScriptMax        = 3.0
	InvisibleMinCount     = 3 // fewer stripped characters don't fire (a stray LRM or two)
	InvisibleWeight       = 0.5
	InvisibleHeavyCount   = 10
	InvisibleHeavyWeight  = 1.0
	InvisibleFloodCount   = 30
	InvisibleFloodWeight  = 1.5
	maxEvidenceMixedWords = 3
)

// Analyzer emits the script-anomaly signals (T1). P is the group policy type; it isn't read.
type Analyzer[P any] struct{}

// New returns the analyzer.
func New[P any]() *Analyzer[P] { return &Analyzer[P]{} }

// Name implements detect.Analyzer.
func (*Analyzer[P]) Name() string { return "text.script" }

// Tier implements detect.Analyzer.
func (*Analyzer[P]) Tier() detect.Tier { return detect.T1 }

// Applies implements detect.Analyzer: any message with text.
func (*Analyzer[P]) Applies(e *envelope.Envelope, _ *P) bool { return len(e.Texts) > 0 }

// Analyze implements detect.Analyzer. Mixed words are looked for in Norm, which has NFKC and
// invisibles removed but no confusables skeleton.
func (*Analyzer[P]) Analyze(_ context.Context, e *envelope.Envelope, _ *detect.State) ([]detect.Signal, error) {
	var words []string
	stripped := 0
	for i := range e.Texts {
		t := &e.Texts[i]
		stripped += t.Stripped
		for _, w := range MixedWords(t.Norm) {
			words = append(words, fmt.Sprintf("%q (%s)", w.Word, joinScripts(w.Scripts)))
		}
	}
	var out []detect.Signal
	if n := len(words); n > 0 {
		shown := words[:min(n, maxEvidenceMixedWords)]
		ev := fmt.Sprintf("%d word(s) mixing look-alike scripts: %s", n, strings.Join(shown, ", "))
		if n > len(shown) {
			ev += ", …"
		}
		out = append(out, detect.Signal{
			Code:   CodeMixedScript,
			Weight: min(MixedScriptMax, MixedScriptWeight+MixedScriptStep*float64(n-1)), Evidence: ev,
		})
	}
	if w := invisibleWeight(stripped); w > 0 {
		out = append(out, detect.Signal{
			Code: CodeInvisible, Weight: w,
			Evidence: fmt.Sprintf("%d hidden or decorative characters removed from the text", stripped),
		})
	}
	return out, nil
}

func invisibleWeight(n int) float64 {
	switch {
	case n >= InvisibleFloodCount:
		return InvisibleFloodWeight
	case n >= InvisibleHeavyCount:
		return InvisibleHeavyWeight
	case n >= InvisibleMinCount:
		return InvisibleWeight
	default:
		return 0
	}
}

func joinScripts(s []Script) string {
	parts := make([]string, len(s))
	for i, x := range s {
		parts[i] = string(x)
	}
	return strings.Join(parts, "+")
}
