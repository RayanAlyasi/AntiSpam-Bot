package textrules

import (
	"strings"
	"testing"

	"github.com/rs/zerolog"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/rules"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/ruleview"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/textnorm"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/envelope"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/normalize"
)

const grp = "120363000000000001@g.us"

func rule(t *testing.T, id int64, typ rules.Type, pattern string, class rules.ActionClass, w float64) rules.Rule {
	t.Helper()
	norm := func(s string) string { return normalize.Text(s).Norm }
	n, err := rules.NormalizePattern(typ, pattern, norm)
	if err != nil {
		t.Fatal(err)
	}
	return rules.Rule{
		ID: id, Scope: rules.ScopeGroup, ScopeID: grp, Type: typ, Pattern: pattern, NormPattern: n,
		ActionClass: class, Weight: w, Enabled: true,
	}
}

func source(t *testing.T, rs ...rules.Rule) ruleview.Source {
	t.Helper()
	h := rules.NewHolder(rules.StaticLoader{Rules: rs}, clock.Real{}, zerolog.Nop())
	if err := h.Reload(t.Context()); err != nil {
		t.Fatal(err)
	}
	return ruleview.Source{Rules: h}
}

func msg(texts ...string) *envelope.Envelope {
	e := &envelope.Envelope{Group: grp}
	for i, s := range texts {
		src := envelope.SourceBody
		if i > 0 {
			src = envelope.SourceCaption
		}
		e.Texts = append(e.Texts, envelope.TextSurface{Source: src, Raw: s})
	}
	textnorm.Prepare(e, "SA")
	return e
}

func codes(sigs []detect.Signal) string {
	var c []string
	for _, s := range sigs {
		c = append(c, s.Code)
	}
	return strings.Join(c, ",")
}

func TestKeyword(t *testing.T) {
	src := source(t,
		rule(t, 1, rules.TypePhrase, "free money", rules.ActionWeighted, 2),
		rule(t, 2, rules.TypeKeyword, "bitcoin", rules.ActionWeighted, 1.5),
		rule(t, 3, rules.TypeKeyword, "casino", rules.ActionHard, 0),
	)
	a := NewKeyword[struct{}](src)
	tests := []struct {
		name   string
		e      *envelope.Envelope
		codes  string
		weight float64
	}{
		{"weighted sum across surfaces", msg("get FREE money", "and bitcoin"), CodeKeyword, 3.5},
		{"obfuscated with leet and confusables", msg("fr33 m0ney in bitcоin"), CodeKeyword, 3.5},
		{"hard rule is a policy violation", msg("best casino"), CodeKeywordBlocked, 0},
		{"looks spammy, isn't listed", msg("money is tight this month, free for coffee?"), "", 0},
		{"inside a longer word", msg("bitcoins"), "", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !a.Applies(tt.e, nil) {
				t.Fatal("Applies = false for a text message")
			}
			sigs, err := a.Analyze(t.Context(), tt.e, &detect.State{})
			if err != nil {
				t.Fatal(err)
			}
			if got := codes(sigs); got != tt.codes {
				t.Fatalf("codes = %q, want %q (%+v)", got, tt.codes, sigs)
			}
			for _, s := range sigs {
				if s.Known {
					t.Errorf("signal %s is Known, want text rules never known spam (§7.3)", s.Code)
				}
				if s.Hard != (s.Code == CodeKeywordBlocked) {
					t.Errorf("signal %s Hard = %v", s.Code, s.Hard)
				}
				if !s.Hard && s.Weight != tt.weight {
					t.Errorf("weight = %v, want %v", s.Weight, tt.weight)
				}
				if s.Evidence == "" {
					t.Error("empty evidence")
				}
			}
		})
	}
	if a.Applies(&envelope.Envelope{}, nil) {
		t.Error("Applies = true for a message without text")
	}
	if a.Tier() != detect.T0 || a.Name() == "" {
		t.Errorf("tier %v name %q", a.Tier(), a.Name())
	}
}

func TestKeywordWeightIsCapped(t *testing.T) {
	var rs []rules.Rule
	var words []string
	for i, w := range []string{"alpha", "bravo", "charlie", "delta", "echo"} {
		rs = append(rs, rule(t, int64(i+1), rules.TypeKeyword, w, rules.ActionWeighted, 3))
		words = append(words, w)
	}
	sigs, _ := NewKeyword[struct{}](source(t, rs...)).Analyze(t.Context(), msg(strings.Join(words, " ")), nil)
	if len(sigs) != 1 || sigs[0].Weight != MaxWeight {
		t.Errorf("signals = %+v, want one signal capped at %v", sigs, MaxWeight)
	}
}

func TestRegex(t *testing.T) {
	src := source(t,
		rule(t, 1, rules.TypeRegex, `earn \d+ (usd|dollars?) (a|per) day`, rules.ActionWeighted, 2.5),
		rule(t, 2, rules.TypeRegex, `t\.me/\w+`, rules.ActionHard, 0),
	)
	a := NewRegex[struct{}](src)
	sigs, err := a.Analyze(t.Context(), msg("EARN 500 dollars a day from home"), nil)
	if err != nil || codes(sigs) != CodeRegex || sigs[0].Weight != 2.5 {
		t.Errorf("signals = %+v, %v; want %s +2.5", sigs, err, CodeRegex)
	}
	sigs, _ = a.Analyze(t.Context(), msg("join t.me/signals"), nil)
	if codes(sigs) != CodeRegexBlocked || !sigs[0].Hard {
		t.Errorf("signals = %+v, want a hard %s", sigs, CodeRegexBlocked)
	}
	if sigs, _ = a.Analyze(t.Context(), msg("I earned 5 dollars yesterday"), nil); len(sigs) != 0 {
		t.Errorf("signals = %+v, want none for an ordinary sentence", sigs)
	}
}

func TestFuzzy(t *testing.T) {
	src := source(t,
		rule(t, 1, rules.TypePhrase, "free bitcoin giveaway", rules.ActionHard, 0),
		rule(t, 2, rules.TypePhrase, "guaranteed profit", rules.ActionWeighted, 3),
	)
	a := NewFuzzy[struct{}](src)
	sigs, err := a.Analyze(t.Context(), msg("free bitcion giveaway for all"), nil)
	if err != nil || len(sigs) != 1 {
		t.Fatalf("signals = %+v, %v; want one fuzzy signal", sigs, err)
	}
	s := sigs[0]
	if s.Code != CodeFuzzy || s.Known || s.Hard || s.Weight <= 0 || s.Weight > DefaultFuzzyWeight {
		t.Errorf("signal = %+v, want a weighted %s never Known or Hard (fuzzy only sends to the AI, §7.0)", s, CodeFuzzy)
	}
	if sigs, _ := a.Analyze(t.Context(), msg("free bitcoin giveaway"), nil); len(sigs) != 0 {
		t.Errorf("exact match gave fuzzy signals %+v, want none (the keyword analyzer reports it)", sigs)
	}
	if sigs, _ := a.Analyze(t.Context(), msg("we can't guarantee any profit here"), nil); len(sigs) != 0 {
		t.Errorf("signals = %+v, want none for an ordinary sentence", sigs)
	}
	if a.Tier() != detect.T1 {
		t.Errorf("tier = %v, want T1", a.Tier())
	}
}
