package domain

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
)

const grp = "120363000000000001@g.us"

func rule(t *testing.T, id int64, scope rules.Scope, typ rules.Type, pattern string, class rules.ActionClass, w float64) rules.Rule {
	t.Helper()
	n, err := rules.NormalizePattern(typ, pattern, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := rules.Rule{ID: id, Scope: scope, Type: typ, Pattern: pattern, NormPattern: n, ActionClass: class, Weight: w, Enabled: true}
	if scope == rules.ScopeGroup {
		r.ScopeID = grp
	}
	return r
}

func TestAnalyzer(t *testing.T) {
	l := rules.StaticLoader{
		Rules: []rules.Rule{
			rule(t, 1, rules.ScopeGlobal, rules.TypeDomain, "evil.com", rules.ActionHard, 0),
			rule(t, 2, rules.ScopeGlobal, rules.TypeDomain, "youtube.com", rules.ActionAllow, 0),
			rule(t, 3, rules.ScopeGroup, rules.TypeDomain, "good.evil.com", rules.ActionAllow, 0),
			rule(t, 4, rules.ScopeGlobal, rules.TypeDomain, "cheap-pills.net", rules.ActionWeighted, 1.5),
			rule(t, 5, rules.ScopeGlobal, rules.TypeURLPrefix, "bit.ly/Scam", rules.ActionHard, 0),
			rule(t, 6, rules.ScopeGlobal, rules.TypeDomain, "bit.ly", rules.ActionAllow, 0),
			rule(t, 7, rules.ScopeGlobal, rules.TypeDomain, "short.io", rules.ActionWeighted, 1),
			rule(t, 8, rules.ScopeGroup, rules.TypeURLPrefix, "short.io/scam", rules.ActionHard, 0),
		},
		Fingerprints: []rules.Fingerprint{{
			Kind: rules.FPURL, Value: "tinyurl.com/x1", Label: rules.LabelSpam,
			Source: rules.SourceAuto, Scope: rules.ScopeGroup, ScopeID: grp,
		}},
	}
	h := rules.NewHolder(l, clock.Real{}, zerolog.Nop())
	if err := h.Reload(t.Context()); err != nil {
		t.Fatal(err)
	}
	allow := []string{}
	a := New[struct{}](ruleview.Source{Rules: h, Settings: func(*envelope.Envelope) rules.ViewOptions {
		return rules.ViewOptions{AllowDomains: allow}
	}})
	tests := []struct {
		name  string
		text  string
		urls  []envelope.URLRef
		allow []string
		codes string
		known bool
	}{
		{"blocklisted domain in text", "visit https://login.evil.com/x now", nil, nil, CodeDomainBlocklisted, true},
		{"obfuscated link", "visit evil[.]com now", nil, nil, CodeDomainBlocklisted, true},
		{"preview url", "", []envelope.URLRef{{URL: "http://EVIL.com:80/", Source: "preview"}}, nil, CodeDomainBlocklisted, true},
		{"group allow beats global deny", "see https://good.evil.com/page", nil, nil, "", false},
		{"settings allow-list never fires", "see https://evil.com", nil, []string{"evil.com"}, "", false},
		{"weighted list", "buy at cheap-pills.net", nil, nil, CodeDomainListed, false},
		{"allowed domain skips its url prefix rules", "https://bit.ly/Scam1", nil, nil, "", false},
		{"url fingerprint", "https://tinyurl.com/x1", nil, nil, CodeURLBlocklisted, true},
		{"weighted domain doesn't hide a hard url prefix", "https://short.io/scam1", nil, nil, CodeDomainListed + "," + CodeURLBlocklisted, false},
		{"global allow-listed domain", "watch https://www.youtube.com/watch?v=1", nil, nil, "", false},
		{"ordinary link", "news at https://example.org/a", nil, nil, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			allow = tt.allow
			e := &envelope.Envelope{Group: grp, URLs: tt.urls}
			if tt.text != "" {
				e.Texts = []envelope.TextSurface{{Source: envelope.SourceBody, Raw: tt.text}}
			}
			textnorm.Prepare(e, "SA")
			if !a.Applies(e, nil) {
				t.Fatal("Applies = false")
			}
			sigs, err := a.Analyze(t.Context(), e, &detect.State{})
			if err != nil {
				t.Fatal(err)
			}
			var codes []string
			for _, s := range sigs {
				codes = append(codes, s.Code)
				wantKnown := tt.known || s.Code == CodeURLBlocklisted || s.Code == CodeDomainBlocklisted
				if s.Known != wantKnown || s.Hard {
					t.Errorf("%s Known = %v, want %v (a hard deny is known evidence, F-048)", s.Code, s.Known, wantKnown)
				}
				if s.Weight <= 0 || s.Evidence == "" {
					t.Errorf("%s weight %v evidence %q", s.Code, s.Weight, s.Evidence)
				}
			}
			if got := strings.Join(codes, ","); got != tt.codes {
				t.Errorf("codes = %q, want %q", got, tt.codes)
			}
		})
	}
}
