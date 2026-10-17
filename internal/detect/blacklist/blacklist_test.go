package blacklist

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/rs/zerolog"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/rules"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/ruleview"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/textnorm"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/envelope"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/normalize/redact"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

func TestParseArgs(t *testing.T) {
	tests := []struct {
		name, args, region string
		numbers, invalid   string // fmt of the slices
		reason             string
	}{
		{"spaced international", "+966 55 123 4567", "SA", "[+966551234567]", "[]", ""},
		{"00 prefix", "00966551234567", "SA", "[+966551234567]", "[]", ""},
		{"national with region", "0551234567", "SA", "[+966551234567]", "[]", ""},
		{"arabic-indic digits", "٠٥٥١٢٣٤٥٦٧", "SA", "[+966551234567]", "[]", ""},
		{"wa.me link", "https://wa.me/966551234567", "", "[+966551234567]", "[]", ""},
		{"several with reason", "+966 55 123 4567 0561234567 sells fake visas", "SA", "[+966551234567 +966561234567]", "[]", "sells fake visas"},
		{"duplicates once", "0551234567 +966551234567", "SA", "[+966551234567]", "[]", ""},
		{"invalid reported", "0551234567 12345 spam", "SA", "[+966551234567]", "[12345]", "spam"},
		{"short number reported whole", "+966 55 123 456", "SA", "[]", "[+966 55 123 456]", ""},
		{"national without region", "0551234567", "", "[]", "[0551234567]", ""},
		{"dash before reason", "0551234567 - scam ring", "SA", "[+966551234567]", "[]", "- scam ring"},
		{"reason keeps its spacing", "0551234567   fake   jobs ", "SA", "[+966551234567]", "[]", "fake   jobs"},
		{"no numbers", "spam", "SA", "[]", "[]", "spam"},
		{"empty", "", "SA", "[]", "[]", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := ParseArgs(tt.args, tt.region)
			if got := fmt.Sprint(p.Numbers); got != tt.numbers {
				t.Errorf("Numbers = %s, want %s", got, tt.numbers)
			}
			if got := fmt.Sprint(p.Invalid); got != tt.invalid {
				t.Errorf("Invalid = %s, want %s (invalid numbers must be reported, §9.8)", got, tt.invalid)
			}
			if p.Reason != tt.reason {
				t.Errorf("Reason = %q, want %q", p.Reason, tt.reason)
			}
		})
	}
}

func FuzzParseArgs(f *testing.F) {
	for _, s := range []string{"+966 55 123 4567 spam", "", "٠٥٥ 1", "wa.me/1", "+ + ( )", "\xff 0551234567"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		p := ParseArgs(s, "SA")
		for _, n := range p.Numbers {
			if !strings.HasPrefix(n, "+") {
				t.Fatalf("number %q is not E.164", n)
			}
		}
		for _, bad := range p.Invalid {
			if bad == "" {
				t.Fatal("empty invalid entry")
			}
		}
	})
}

var key = []byte("0123456789abcdef-test-key")

func TestHasher(t *testing.T) {
	if _, err := NewHasher([]byte("short")); !errors.Is(err, ErrShortKey) {
		t.Errorf("NewHasher(short) err = %v, want ErrShortKey (a short key makes the hashes reversible)", err)
	}
	h, err := NewHasher(key)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := h.Hash("+966551234567"), redact.PhoneHash(key, "+966551234567"); got != want {
		t.Errorf("Hash = %s, want redact.PhoneHash %s (stores and redaction must agree)", got, want)
	}
	other, _ := NewHasher(append([]byte("x"), key...))
	if other.Hash("+966551234567") == h.Hash("+966551234567") {
		t.Error("two keys gave the same hash")
	}
}

func TestMask(t *testing.T) {
	for in, want := range map[string]string{
		"+966551234567": "+966•••••••67",
		"+12025550123":  "+120••••••23",
		"+123":          "••••",
	} {
		if got := Mask(in); got != want {
			t.Errorf("Mask(%s) = %s, want %s", in, got, want)
		}
	}
}

const (
	grp    = "120363000000000001@g.us"
	other  = "120363000000000002@g.us"
	listed = "+966551234567"
)

func newAnalyzer(t *testing.T, entries []rules.BlacklistEntry, useInstance bool) (*Analyzer[struct{}], *rules.Holder, Hasher) {
	t.Helper()
	h, _ := NewHasher(key)
	hold := rules.NewHolder(rules.StaticLoader{Blacklist: entries}, clock.Real{}, zerolog.Nop())
	if err := hold.Reload(t.Context()); err != nil {
		t.Fatal(err)
	}
	src := ruleview.Source{Rules: hold, Settings: func(*envelope.Envelope) rules.ViewOptions {
		return rules.ViewOptions{UseInstanceBlacklist: useInstance}
	}}
	return New[struct{}](src, h.Hash), hold, h
}

func TestAnalyzer(t *testing.T) {
	h, _ := NewHasher(key)
	groupEntry := rules.BlacklistEntry{ID: 1, Scope: rules.BlacklistGroup, GroupJID: grp, PNHash: h.Hash(listed)}
	instEntry := rules.BlacklistEntry{ID: 2, Scope: rules.BlacklistInstance, PNHash: h.Hash(listed)}
	text := func(s string) envelope.Envelope {
		return envelope.Envelope{Group: grp, Texts: []envelope.TextSurface{{Source: envelope.SourceBody, Raw: s}}}
	}
	tests := []struct {
		name        string
		entries     []rules.BlacklistEntry
		useInstance bool
		env         envelope.Envelope
		group       string
		fire        bool
	}{
		{"number in text", []rules.BlacklistEntry{groupEntry}, false, text("call me on 055 123 4567 now"), grp, true},
		{"vcard waid", []rules.BlacklistEntry{groupEntry}, false, envelope.Envelope{Contacts: []envelope.ContactRef{{WAIDs: []string{"966551234567"}}}}, grp, true},
		{"vcard tel", []rules.BlacklistEntry{groupEntry}, false, envelope.Envelope{Contacts: []envelope.ContactRef{{Phones: []string{"+966 55 123 4567"}}}}, grp, true},
		{"wa.me link", []rules.BlacklistEntry{groupEntry}, false, envelope.Envelope{URLs: []envelope.URLRef{{URL: "https://wa.me/966551234567", Source: "text"}}}, grp, true},
		{"unlisted number", []rules.BlacklistEntry{groupEntry}, false, text("call 0561234567"), grp, false},
		{"another group's entry", []rules.BlacklistEntry{groupEntry}, true, text("call 0551234567"), other, false},
		{"instance entry used", []rules.BlacklistEntry{instEntry}, true, text("call 0551234567"), other, true},
		{"instance entry opted out", []rules.BlacklistEntry{instEntry}, false, text("call 0551234567"), other, false},
		{"price is not a number", []rules.BlacklistEntry{groupEntry}, false, text("only 3000 ريال today"), grp, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, _, _ := newAnalyzer(t, tt.entries, tt.useInstance)
			e := tt.env
			e.Group = wa.JID(tt.group)
			textnorm.Prepare(&e, "SA")
			if !a.Applies(&e, nil) && tt.fire {
				t.Fatal("Applies = false for a message with numbers")
			}
			sigs, err := a.Analyze(t.Context(), &e, nil)
			if err != nil {
				t.Fatal(err)
			}
			if got := len(sigs) == 1; got != tt.fire {
				t.Fatalf("signals = %+v, want fire=%v", sigs, tt.fire)
			}
			if !tt.fire {
				return
			}
			s := sigs[0]
			if s.Code != CodeBlacklisted || !s.Known || s.Weight <= 0 || s.Hard {
				t.Errorf("signal = %+v, want known %s with positive weight (§9.8 step 4)", s, CodeBlacklisted)
			}
			if strings.Contains(s.Evidence, "551234567") || !strings.Contains(s.Evidence, "+966•") {
				t.Errorf("evidence %q must show the number masked", s.Evidence)
			}
		})
	}
}

func TestRemovalLiftsSignal(t *testing.T) {
	h, _ := NewHasher(key)
	ld := &switchLoader{entries: []rules.BlacklistEntry{{ID: 1, Scope: rules.BlacklistGroup, GroupJID: grp, PNHash: h.Hash(listed)}}}
	hold := rules.NewHolder(ld, clock.Real{}, zerolog.Nop())
	if err := hold.Reload(t.Context()); err != nil {
		t.Fatal(err)
	}
	a := New[struct{}](ruleview.Source{Rules: hold}, h.Hash)
	e := envelope.Envelope{Group: grp, Texts: []envelope.TextSurface{{Source: "body", Raw: "+966551234567"}}}
	textnorm.Prepare(&e, "")
	if s, _ := a.Analyze(t.Context(), &e, nil); len(s) != 1 {
		t.Fatalf("before removal: %v, want one signal", s)
	}
	ld.entries = nil
	if err := hold.Reload(t.Context()); err != nil {
		t.Fatal(err)
	}
	if s, _ := a.Analyze(t.Context(), &e, nil); len(s) != 0 {
		t.Errorf("after removal: %v, want none (remove is the undo, §9.8 step 5)", s)
	}
}

type switchLoader struct{ entries []rules.BlacklistEntry }

func (s *switchLoader) LoadRules(context.Context) ([]rules.Rule, error) { return nil, nil }
func (s *switchLoader) LoadFingerprints(context.Context) ([]rules.Fingerprint, error) {
	return nil, nil
}

func (s *switchLoader) LoadBlacklist(context.Context) ([]rules.BlacklistEntry, error) {
	return s.entries, nil
}
