package rules

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/rs/zerolog"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/textmatch"
)

const (
	grp   = "120363000000000001@g.us"
	other = "120363000000000002@g.us"
	bot   = "15550000001@s.whatsapp.net"
	hashA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	hashB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

// mk builds an enabled rule with its pattern normalized.
func mk(t *testing.T, id int64, scope Scope, scopeID string, typ Type, pattern string, class ActionClass) Rule {
	t.Helper()
	r := Rule{ID: id, Scope: scope, ScopeID: scopeID, Type: typ, Pattern: pattern, ActionClass: class, Weight: 2, Enabled: true}
	if typ == TypePhone {
		r.NormPattern = pattern
		return r
	}
	n, err := NormalizePattern(typ, pattern, nil)
	if err != nil {
		t.Fatalf("NormalizePattern(%s, %q): %v", typ, pattern, err)
	}
	r.NormPattern = n
	return r
}

func TestNormalizePattern(t *testing.T) {
	tests := []struct {
		typ     Type
		in      string
		want    string
		wantErr error
	}{
		{TypeKeyword, "  Bitcoin! ", "bitcoin", nil},
		{TypeKeyword, "free bitcoin", "", ErrInvalidPattern},
		{TypePhrase, "Free-Bitcoin  NOW", "free bitcoin now", nil},
		{TypePhrase, "💰💰", "", ErrInvalidPattern},
		{TypeRegex, `earn \d+`, `earn \d+`, nil},
		{TypeRegex, `a*`, "", ErrInvalidPattern},
		{TypeDomain, "HTTPS://Www.Example.COM.:443/path?x=1", "www.example.com", nil},
		{TypeDomain, "*.bit.ly", "bit.ly", nil},
		{TypeDomain, "пример.рф", "xn--e1afmkfd.xn--p1ai", nil},
		{TypeDomain, "localhost", "", ErrInvalidPattern},
		{TypeDomain, "", "", ErrInvalidPattern},
		{TypeURLPrefix, "http://Bit.LY/Abc", "bit.ly/Abc", nil},
		{TypeURLPrefix, "example.com", "example.com/", nil},
		{TypeSHA256, strings.ToUpper(hashA), hashA, nil},
		{TypeSHA256, "abc", "", ErrInvalidPattern},
		{TypeSimHash, "00FF00FF00FF00FF", "00ff00ff00ff00ff", nil},
		{TypeChannel, "120363000000000099@newsletter", "120363000000000099@newsletter", nil},
		{TypeInvite, "AbC dEf", "", ErrInvalidPattern},
		{TypePhone, "+966551234567", "", ErrInvalidPattern},
		{"nonsense", "x", "", ErrInvalidType},
	}
	for _, tt := range tests {
		got, err := NormalizePattern(tt.typ, tt.in, nil)
		if tt.wantErr != nil {
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("NormalizePattern(%s, %q) err = %v, want %v", tt.typ, tt.in, err, tt.wantErr)
			}
			continue
		}
		if err != nil || got != tt.want {
			t.Errorf("NormalizePattern(%s, %q) = %q, %v; want %q", tt.typ, tt.in, got, err, tt.want)
		}
	}
}

func TestNormalizePatternUsesTextNormalizer(t *testing.T) {
	fold := func(s string) string { return strings.ReplaceAll(s, "е", "e") } // Cyrillic е
	got, err := NormalizePattern(TypeKeyword, "frее", fold)
	if err != nil || got != "free" {
		t.Errorf("NormalizePattern with normalizer = %q, %v; want free", got, err)
	}
}

func TestRuleValidate(t *testing.T) {
	ok := mk(t, 1, ScopeGroup, grp, TypeKeyword, "bitcoin", ActionWeighted)
	tests := []struct {
		name string
		edit func(*Rule)
		want error
	}{
		{"valid", func(*Rule) {}, nil},
		{"global with scope id", func(r *Rule) { r.Scope = ScopeGlobal }, ErrInvalidScope},
		{"group without scope id", func(r *Rule) { r.ScopeID = "" }, ErrInvalidScope},
		{"unknown scope", func(r *Rule) { r.Scope = "account" }, ErrInvalidScope},
		{"unknown action", func(r *Rule) { r.ActionClass = "ban" }, ErrInvalidAction},
		{"allow keyword", func(r *Rule) { r.ActionClass = ActionAllow }, ErrInvalidAction},
		{"NaN weight", func(r *Rule) { r.Weight = nanWeight() }, ErrInvalidWeight},
		{"huge weight", func(r *Rule) { r.Weight = 1e6 }, ErrInvalidWeight},
		{"norm pattern not in words form", func(r *Rule) { r.NormPattern = "Bitcoin!" }, ErrInvalidPattern},
		{"regex over the limit stored by hand", func(r *Rule) {
			r.Type, r.Pattern, r.NormPattern = TypeRegex, `((abc|def|ghi){30}){30}`, `((abc|def|ghi){30}){30}`
		}, ErrInvalidPattern},
		{"domain norm mismatch", func(r *Rule) { r.Type, r.Pattern, r.NormPattern = TypeDomain, "Example.com", "Example.com" }, ErrInvalidPattern},
		{"phone without hash", func(r *Rule) { r.Type, r.Pattern, r.NormPattern = TypePhone, "+966551234567", "+966551234567" }, ErrInvalidPattern},
		{"phone with hash", func(r *Rule) { r.Type, r.Pattern, r.NormPattern = TypePhone, "+966551234567", hashA }, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := ok
			tt.edit(&r)
			if err := r.Validate(); !errors.Is(err, tt.want) || (tt.want == nil && err != nil) {
				t.Errorf("Validate = %v, want %v", err, tt.want)
			}
		})
	}
}

func nanWeight() float64 { z := 0.0; return z / z }

func ruleIDs(hits []TextHit) string {
	var ids []string
	for _, h := range hits {
		ids = append(ids, fmt.Sprintf("%d:%s", h.Rule.ID, h.Scope))
	}
	return strings.Join(ids, ",")
}

func TestKeywordsAcrossVariantsAndScopes(t *testing.T) {
	crypto := mk(t, 4, ScopeGlobal, "", TypeKeyword, "forex", ActionWeighted)
	crypto.Pack = "crypto"
	disabled := mk(t, 5, ScopeGlobal, "", TypeKeyword, "hello", ActionHard)
	disabled.Enabled = false
	rs := []Rule{
		mk(t, 1, ScopeGlobal, "", TypeKeyword, "bitcoin", ActionWeighted),
		mk(t, 2, ScopeGroup, grp, TypePhrase, "free money", ActionHard),
		mk(t, 3, ScopeGroup, other, TypeKeyword, "bitcoin", ActionHard),
		crypto,
		disabled,
		mk(t, 6, ScopeBot, bot, TypeKeyword, "casino", ActionWeighted),
	}
	s, errs := Compile(rs, nil, nil)
	if len(errs) != 0 {
		t.Fatalf("Compile errs = %v", errs)
	}
	tests := []struct {
		name string
		o    ViewOptions
		text Text
		want string
	}{
		{"group rule before global", ViewOptions{Group: grp, Bot: bot}, Text{Norm: "Free  MONEY and bitcoin"}, "2:group,1:global"},
		{"leet variant", ViewOptions{Group: grp}, Text{Norm: "fr33 m0ney", Leet: "free money"}, "2:group"},
		{"skeleton variant", ViewOptions{Group: grp}, Text{Norm: "bitcоin", Skeleton: "bitcoin"}, "1:global"},
		{"other group's rule doesn't apply", ViewOptions{Group: grp}, Text{Norm: "nothing here"}, ""},
		{"other group sees its own rule first", ViewOptions{Group: other}, Text{Norm: "bitcoin"}, "3:group,1:global"},
		{"pack only when subscribed", ViewOptions{Group: grp}, Text{Norm: "forex signals"}, ""},
		{"subscribed pack", ViewOptions{Group: grp, Packs: []string{"crypto", "crypto"}}, Text{Norm: "forex signals"}, "4:global"},
		{"bot scope", ViewOptions{Group: grp, Bot: bot}, Text{Norm: "online casino"}, "6:bot"},
		{"disabled rule", ViewOptions{Group: grp}, Text{Norm: "hello"}, ""},
		{"whole word only", ViewOptions{Group: grp}, Text{Norm: "bitcoins"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ruleIDs(s.View(tt.o).Keywords(tt.text)); got != tt.want {
				t.Errorf("Keywords = %q, want %q", got, tt.want)
			}
		})
	}
	if r, f, b := s.Counts(); r != 5 || f != 0 || b != 0 {
		t.Errorf("Counts = %d, %d, %d; want 5 enabled rules", r, f, b)
	}
}

func TestRegexesAndFuzzy(t *testing.T) {
	rs := []Rule{
		mk(t, 1, ScopeGroup, grp, TypeRegex, `earn \d+ (usd|dollars?)`, ActionWeighted),
		mk(t, 2, ScopeGlobal, "", TypePhrase, "free bitcoin giveaway", ActionHard),
		mk(t, 3, ScopeGlobal, "", TypePhrase, "win big", ActionWeighted),
		mk(t, 4, ScopeGlobal, "", TypePhrase, "win", ActionWeighted), // too short for fuzzy
	}
	s, _ := Compile(rs, nil, nil)
	v := s.View(ViewOptions{Group: grp})
	if got := ruleIDs(v.Regexes(Text{Norm: "EARN  500 Dollars today"})); got != "1:group" {
		t.Errorf("Regexes = %q, want 1:group", got)
	}
	if got := ruleIDs(v.Regexes(Text{Norm: "eаrn 500 usd", Skeleton: "earn 500 usd"})); got != "1:group" {
		t.Errorf("Regexes on skeleton = %q, want 1:group", got)
	}
	hits := fuzzy(t, v, Text{Norm: "free bitcion giveaway for everyone"})
	if got := ruleIDs(hits); got != "2:global" || hits[0].Similarity >= 1 {
		t.Errorf("Fuzzy = %q %+v, want 2:global with similarity < 1", got, hits)
	}
	if got := ruleIDs(fuzzy(t, v, Text{Norm: "free bitcoin giveaway"})); got != "" {
		t.Errorf("Fuzzy on an exact match = %q, want none (exact matches belong to Keywords)", got)
	}
	if got := ruleIDs(fuzzy(t, v, Text{Norm: "wun"})); got != "" {
		t.Errorf("Fuzzy on a short phrase = %q, want none", got)
	}
}

func TestDomainPrecedence(t *testing.T) {
	rs := []Rule{
		mk(t, 1, ScopeGlobal, "", TypeDomain, "evil.com", ActionHard),
		mk(t, 2, ScopeGlobal, "", TypeDomain, "youtube.com", ActionAllow),
		mk(t, 3, ScopeGroup, grp, TypeDomain, "youtube.com", ActionHard), // group deny beats global allow
		mk(t, 4, ScopeGroup, grp, TypeDomain, "good.evil.com", ActionAllow),
		mk(t, 5, ScopeGlobal, "", TypeDomain, "blogspot.com", ActionWeighted),
		mk(t, 6, ScopeGlobal, "", TypeDomain, "blogspot.com", ActionHard), // stronger deny wins
		mk(t, 7, ScopeGlobal, "", TypeDomain, "both.com", ActionHard),
		mk(t, 8, ScopeGlobal, "", TypeDomain, "both.com", ActionAllow), // allow beats deny in one layer
	}
	s, _ := Compile(rs, nil, nil)
	tests := []struct {
		o            ViewOptions
		host         string
		allowed      bool
		rule         int64
		matched      string
		wantNoResult bool
	}{
		{o: ViewOptions{Group: grp}, host: "evil.com", rule: 1, matched: "evil.com"},
		{o: ViewOptions{Group: grp}, host: "a.b.evil.com", rule: 1, matched: "evil.com"},
		{o: ViewOptions{Group: grp}, host: "good.evil.com", allowed: true, matched: "good.evil.com"},
		{o: ViewOptions{Group: grp}, host: "x.good.evil.com", allowed: true, matched: "good.evil.com"},
		{o: ViewOptions{Group: other}, host: "good.evil.com", rule: 1, matched: "evil.com"},
		{o: ViewOptions{Group: grp}, host: "m.youtube.com", rule: 3, matched: "youtube.com"},
		{o: ViewOptions{Group: other}, host: "youtube.com", allowed: true, matched: "youtube.com"},
		{o: ViewOptions{Group: grp, AllowDomains: []string{"https://YouTube.com/"}}, host: "youtube.com", allowed: true, matched: "youtube.com"},
		{o: ViewOptions{Group: grp, AllowDomains: []string{"evil.com"}}, host: "x.evil.com", allowed: true, matched: "evil.com"},
		{o: ViewOptions{Group: grp}, host: "news.blogspot.com", rule: 6, matched: "blogspot.com"},
		{o: ViewOptions{Group: grp}, host: "both.com", allowed: true, matched: "both.com"},
		{o: ViewOptions{Group: grp}, host: "notevil.com", wantNoResult: true},
		{o: ViewOptions{Group: grp}, host: "com", wantNoResult: true},
		{o: ViewOptions{Group: grp}, host: "", wantNoResult: true},
	}
	for _, tt := range tests {
		got := s.View(tt.o).Domain(tt.host)
		if tt.wantNoResult {
			if got != (ListResult{}) {
				t.Errorf("Domain(%q) = %+v, want no result", tt.host, got)
			}
			continue
		}
		var id int64
		if got.Rule != nil {
			id = got.Rule.ID
		}
		if got.Allowed != tt.allowed || id != tt.rule || got.Matched != tt.matched || got.Denied() == tt.allowed {
			t.Errorf("Domain(%q) in %v = allowed %v rule %d matched %q; want %v %d %q",
				tt.host, tt.o.Group, got.Allowed, id, got.Matched, tt.allowed, tt.rule, tt.matched)
		}
	}
}

func TestURLPrefix(t *testing.T) {
	rs := []Rule{
		mk(t, 1, ScopeGlobal, "", TypeURLPrefix, "bit.ly/", ActionWeighted),
		mk(t, 2, ScopeGlobal, "", TypeURLPrefix, "bit.ly/Scam", ActionHard),
		mk(t, 3, ScopeGroup, grp, TypeURLPrefix, "bit.ly/ScamOK", ActionAllow),
	}
	s, _ := Compile(rs, nil, nil)
	v := s.View(ViewOptions{Group: grp})
	if r := v.URL("bit.ly/Scam123"); r.Rule == nil || r.Rule.ID != 2 {
		t.Errorf("URL longest prefix = %+v, want rule 2", r)
	}
	if r := v.URL("bit.ly/ScamOK1"); !r.Allowed {
		t.Errorf("URL with a group allow prefix = %+v, want allowed", r)
	}
	if r := v.URL("bit.ly/scam"); r.Rule == nil || r.Rule.ID != 1 {
		t.Errorf("URL paths are case-sensitive: %+v, want rule 1", r)
	}
	if r := v.URL("example.com/"); r.Denied() || r.Allowed {
		t.Errorf("URL unlisted = %+v, want nothing", r)
	}
}

func TestExactRulesAndFingerprints(t *testing.T) {
	rs := []Rule{
		mk(t, 1, ScopeGlobal, "", TypeSHA256, hashA, ActionHard),
		mk(t, 2, ScopeGroup, grp, TypeChannel, "1@newsletter", ActionHard),
		mk(t, 3, ScopeGlobal, "", TypePhone, hashB, ActionHard),
	}
	fps := []Fingerprint{
		{Kind: FPSHA256, Value: hashB, Label: LabelSpam, Source: SourceAuto, Scope: ScopeGroup, ScopeID: grp},
		{Kind: FPSHA256, Value: hashA, Label: LabelAllow, Source: SourceAdmin, Scope: ScopeGroup, ScopeID: other}, // overturned there
		{Kind: FPInvite, Value: "Code1", Label: LabelSpam, Source: SourceFeed, Scope: ScopeGlobal},
		{Kind: FPInvite, Value: "Code1", Label: LabelAllow, Source: SourceAdmin, Scope: ScopeGlobal},
		{Kind: "bogus", Value: "x", Label: LabelSpam, Source: SourceAuto, Scope: ScopeGlobal},
	}
	s, errs := Compile(rs, fps, nil)
	if len(errs) != 1 {
		t.Fatalf("Compile errs = %v, want exactly the bogus fingerprint", errs)
	}
	v := s.View(ViewOptions{Group: grp})
	if r := v.Exact(FPSHA256, hashA); r.Rule == nil || r.Rule.ID != 1 || r.Scope != ScopeGlobal {
		t.Errorf("Exact(sha256 A) = %+v, want global rule 1", r)
	}
	if r := v.Exact(FPSHA256, hashB); r.Fingerprint == nil || r.Scope != ScopeGroup {
		t.Errorf("Exact(sha256 B) = %+v, want the group's spam fingerprint", r)
	}
	if r := s.View(ViewOptions{Group: other}).Exact(FPSHA256, hashA); !r.Allowed || r.Denied() {
		t.Errorf("Exact in a group that overturned the hash = %+v, want allowed (fingerprint allow-list)", r)
	}
	if r := v.Exact(FPInvite, "Code1"); !r.Allowed {
		t.Errorf("Exact(invite) with allow and spam fingerprints in one scope = %+v, want allowed", r)
	}
	if r := v.Exact(FPInvite, "code1"); r.Denied() || r.Allowed {
		t.Errorf("Exact(invite) is case-sensitive: %+v", r)
	}
	if r := v.Exact(FPChannel, "1@newsletter"); r.Rule == nil || r.Rule.ID != 2 {
		t.Errorf("Exact(channel) = %+v, want rule 2", r)
	}
	if r := v.Exact(FPNumber, hashB); r.Rule == nil || r.Rule.ID != 3 {
		t.Errorf("Exact(number) = %+v, want rule 3", r)
	}
	if r := v.Exact(FPNumber, ""); r.Denied() {
		t.Errorf("Exact(empty) = %+v, want nothing", r)
	}
}

func TestBlacklisted(t *testing.T) {
	bl := []BlacklistEntry{
		{ID: 1, Scope: BlacklistGroup, GroupJID: grp, PNHash: hashA},
		{ID: 2, Scope: BlacklistInstance, PNHash: hashB},
		{ID: 3, Scope: BlacklistInstance, GroupJID: grp, PNHash: hashA}, // invalid: instance with group
		{ID: 4, Scope: BlacklistGroup, PNHash: hashA},                   // invalid: group without JID
		{ID: 5, Scope: BlacklistGroup, GroupJID: grp, PNHash: "not hex"},
	}
	s, errs := Compile(nil, nil, bl)
	if len(errs) != 3 {
		t.Fatalf("Compile errs = %v, want 3 invalid entries", errs)
	}
	tests := []struct {
		o    ViewOptions
		hash string
		want int64
	}{
		{ViewOptions{Group: grp}, hashA, 1},
		{ViewOptions{Group: other, UseInstanceBlacklist: true}, hashA, 0}, // another group's entry
		{ViewOptions{Group: grp, UseInstanceBlacklist: true}, hashB, 2},
		{ViewOptions{Group: grp, UseInstanceBlacklist: false}, hashB, 0}, // opted out of the instance list
		{ViewOptions{}, hashA, 0},
	}
	for _, tt := range tests {
		var got int64
		if e := s.View(tt.o).Blacklisted(tt.hash); e != nil {
			got = e.ID
		}
		if got != tt.want {
			t.Errorf("Blacklisted(%s…) in %+v = entry %d, want %d", tt.hash[:4], tt.o, got, tt.want)
		}
	}
}

func TestCompileSkipsInvalidRules(t *testing.T) {
	bad := mk(t, 2, ScopeGroup, grp, TypeKeyword, "x", ActionHard)
	bad.Scope = "nowhere"
	s, errs := Compile([]Rule{mk(t, 1, ScopeGroup, grp, TypeKeyword, "bitcoin", ActionHard), bad}, nil, nil)
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "rule 2") {
		t.Fatalf("errs = %v, want one error naming rule 2", errs)
	}
	if got := ruleIDs(s.View(ViewOptions{Group: grp}).Keywords(Text{Norm: "bitcoin"})); got != "1:group" {
		t.Errorf("valid rule still matches: %q", got)
	}
}

func TestEmptySnapshotMatchesNothing(t *testing.T) {
	v := Empty().View(ViewOptions{Group: grp, Bot: bot, UseInstanceBlacklist: true})
	if len(v.Keywords(Text{Norm: "anything"})) != 0 || v.Domain("evil.com").Denied() || v.Blacklisted(hashA) != nil {
		t.Error("empty snapshot matched something")
	}
}

type fakeLoader struct {
	mu    sync.Mutex
	rules []Rule
	err   error
	loads int
}

func (f *fakeLoader) LoadRules(context.Context) ([]Rule, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.loads++
	return f.rules, f.err
}
func (f *fakeLoader) LoadFingerprints(context.Context) ([]Fingerprint, error) { return nil, nil }
func (f *fakeLoader) LoadBlacklist(context.Context) ([]BlacklistEntry, error) { return nil, nil }

func (f *fakeLoader) set(rs []Rule, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rules, f.err = rs, err
}

func (f *fakeLoader) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.loads
}

func matches(h *Holder, text string) bool {
	return len(h.Current().View(ViewOptions{Group: grp}).Keywords(Text{Norm: text})) > 0
}

func TestHolderRunReloadsOnNotify(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ld := &fakeLoader{}
		h := NewHolder(ld, clock.Real{}, zerolog.Nop())
		if h.Current() == nil {
			t.Fatal("Current before load is nil, want the empty snapshot")
		}
		ctx, cancel := context.WithCancel(t.Context())
		changes := make(chan struct{}, 8)
		done := make(chan error, 1)
		go func() { done <- h.Run(ctx, changes) }()
		synctest.Wait()
		if ld.count() != 1 || matches(h, "bitcoin") {
			t.Fatalf("after start: loads %d, want 1 and no rules", ld.count())
		}

		ld.set([]Rule{mk(t, 1, ScopeGroup, grp, TypeKeyword, "bitcoin", ActionHard)}, nil)
		for range 5 { // a burst costs one reload
			changes <- struct{}{}
		}
		synctest.Wait()
		if !matches(h, "bitcoin") {
			t.Fatal("rule not active after NOTIFY, want it live within a second (F-041)")
		}
		if n := ld.count(); n > 3 {
			t.Errorf("loads after a burst of 5 = %d, want the burst coalesced", n)
		}

		// A failed reload keeps the old rules and retries.
		ld.set(nil, errors.New("db down"))
		changes <- struct{}{}
		synctest.Wait()
		if !matches(h, "bitcoin") {
			t.Fatal("rules lost after a failed reload, want the previous snapshot kept")
		}
		ld.set(nil, nil)
		time.Sleep(retryDelay)
		synctest.Wait()
		if matches(h, "bitcoin") {
			t.Error("rule still active after the retry loaded an empty table")
		}

		// The periodic resync picks up changes without a notification.
		ld.set([]Rule{mk(t, 1, ScopeGroup, grp, TypeKeyword, "casino", ActionHard)}, nil)
		time.Sleep(ResyncInterval)
		synctest.Wait()
		if !matches(h, "casino") {
			t.Error("resync didn't reload the rules")
		}

		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Errorf("Run = %v, want context.Canceled", err)
		}
	})
}

func TestHolderRunStopsWhenChannelCloses(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := NewHolder(&fakeLoader{}, clock.Real{}, zerolog.Nop())
		changes := make(chan struct{})
		close(changes)
		if err := h.Run(t.Context(), changes); err == nil {
			t.Error("Run with a closed channel = nil, want an error so the caller restarts the listener")
		}
	})
}

func TestHolderReloadError(t *testing.T) {
	ld := &fakeLoader{err: errors.New("boom")}
	h := NewHolder(ld, clock.Real{}, zerolog.Nop())
	if err := h.Reload(t.Context()); err == nil || !strings.Contains(err.Error(), "load rules") {
		t.Errorf("Reload = %v, want a wrapped load error", err)
	}
}

func fuzzy(t *testing.T, v View, text Text) []TextHit {
	t.Helper()
	hits, err := v.Fuzzy(t.Context(), text)
	if err != nil {
		t.Fatal(err)
	}
	return hits
}

func TestWeightedEntriesDoNotHideKnownEvidence(t *testing.T) {
	hard := func(r Rule) Rule { r.ActionClass = ActionHard; return r }
	rs := []Rule{
		mk(t, 1, ScopeGroup, grp, TypeDomain, "bit.ly", ActionWeighted),
		mk(t, 2, ScopeGlobal, "", TypeDomain, "bit.ly", ActionHard),
		mk(t, 3, ScopeGroup, grp, TypeURLPrefix, "short.io/", ActionWeighted),
		hard(mk(t, 4, ScopeGlobal, "", TypeURLPrefix, "short.io/scam", ActionHard)),
		mk(t, 5, ScopeGroup, grp, TypeSHA256, hashA, ActionWeighted),
		mk(t, 6, ScopeGroup, grp, TypeDomain, "watch.me", ActionWeighted),
		mk(t, 7, ScopeGlobal, "", TypeDomain, "watch.me", ActionAllow),
	}
	fps := []Fingerprint{{Kind: FPSHA256, Value: hashA, Label: LabelSpam, Source: SourceAuto, Scope: ScopeGroup, ScopeID: grp}}
	s, errs := Compile(rs, fps, nil)
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	v := s.View(ViewOptions{Group: grp})
	if r := v.Domain("x.bit.ly"); r.Rule == nil || r.Rule.ID != 2 {
		t.Errorf("Domain = %+v, want the global hard rule 2 (a narrower weighted rule must not hide known evidence)", r)
	}
	if r := v.URL("short.io/scam1"); r.Rule == nil || r.Rule.ID != 4 {
		t.Errorf("URL = %+v, want the hard prefix 4", r)
	}
	if r := v.URL("short.io/other"); r.Rule == nil || r.Rule.ID != 3 {
		t.Errorf("URL = %+v, want the weighted prefix 3 when nothing stronger matches", r)
	}
	if r := v.Exact(FPSHA256, hashA); r.Fingerprint == nil {
		t.Errorf("Exact = %+v, want the spam fingerprint over the weighted rule in the same scope", r)
	}
	if r := v.Domain("watch.me"); !r.Allowed {
		t.Errorf("Domain = %+v, want a wider allow to beat a narrower weighted rule", r)
	}
}

func TestSkeletonOnlyMatchesCrossScriptText(t *testing.T) {
	s, _ := Compile([]Rule{
		mk(t, 1, ScopeGlobal, "", TypeKeyword, "corn", ActionHard),
		mk(t, 2, ScopeGlobal, "", TypeKeyword, "lol", ActionHard),
		mk(t, 3, ScopeGlobal, "", TypeKeyword, "free", ActionHard),
	}, nil, nil)
	v := s.View(ViewOptions{Group: grp})
	for _, tt := range []struct {
		text Text
		want string
	}{
		{Text{Norm: "visit example com", Skeleton: "visit example corn"}, ""},
		{Text{Norm: "room 101", Skeleton: "roorn lol"}, ""},
		{Text{Norm: "frее stuff", Skeleton: "free stuff"}, "3:global"},
	} {
		if got := ruleIDs(v.Keywords(tt.text)); got != tt.want {
			t.Errorf("Keywords(%q) = %q, want %q (skeleton only for cross-script look-alikes)", tt.text.Norm, got, tt.want)
		}
	}
}

func TestRegexScansABoundedPrefix(t *testing.T) {
	s, _ := Compile([]Rule{mk(t, 1, ScopeGlobal, "", TypeRegex, `spam\d+`, ActionHard)}, nil, nil)
	v := s.View(ViewOptions{Group: grp})
	pad := strings.Repeat("a ", MaxRegexText)
	if got := ruleIDs(v.Regexes(Text{Norm: "spam1 " + pad})); got != "1:global" {
		t.Errorf("early match = %q", got)
	}
	if got := ruleIDs(v.Regexes(Text{Norm: pad + "spam1"})); got != "" {
		t.Errorf("match past MaxRegexText = %q, want none (bounded T0 cost)", got)
	}
}

func TestFuzzyIsBoundedAndCancellable(t *testing.T) {
	s, _ := Compile([]Rule{mk(t, 1, ScopeGlobal, "", TypePhrase, "free bitcoin giveaway", ActionWeighted)}, nil, nil)
	v := s.View(ViewOptions{Group: grp})
	pad := strings.Repeat("hello ", textmatch.MaxFuzzyTokens)
	if got := ruleIDs(fuzzy(t, v, Text{Norm: "free bitcion giveaway " + pad})); got != "1:global" {
		t.Errorf("near-miss at the start = %q, want found", got)
	}
	if got := ruleIDs(fuzzy(t, v, Text{Norm: pad + "free bitcion giveaway"})); got != "" {
		t.Errorf("near-miss past MaxFuzzyTokens words = %q, want none (the work per message is bounded, T1 budget)", got)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := v.Fuzzy(ctx, Text{Norm: "free bitcion giveaway"}); !errors.Is(err, context.Canceled) {
		t.Errorf("Fuzzy with a cancelled context = %v, want context.Canceled", err)
	}
}

func TestFingerprintNormalizeAndValidate(t *testing.T) {
	f := Fingerprint{Kind: FPSHA256, Value: " " + strings.ToUpper(hashA) + " ", Label: LabelSpam, Source: SourceAdmin, Scope: ScopeGlobal}
	f.Normalize()
	if f.Value != hashA || f.Validate() != nil {
		t.Errorf("normalized = %q, %v; want lower-case hex that validates", f.Value, f.Validate())
	}
	inv := Fingerprint{Kind: FPInvite, Value: " AbC ", Label: LabelSpam, Source: SourceAdmin, Scope: ScopeGlobal}
	inv.Normalize()
	if inv.Value != "AbC" {
		t.Errorf("invite = %q, want case kept", inv.Value)
	}
	bad := Fingerprint{Kind: FPNumber, Value: "+966551234567", Label: LabelSpam, Source: SourceAdmin, Scope: ScopeGlobal}
	if err := bad.Validate(); !errors.Is(err, ErrInvalidPattern) {
		t.Errorf("number fingerprint in the clear: %v, want rejected (keyed hashes only)", err)
	}
}
