package blacklist_test

import (
	"strings"
	"sync"
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/blacklist"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/rules"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/ruleview"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/textnorm"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/envelope"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// contentCase is a message that carries the listed number in one place.
type contentCase struct {
	name string
	env  func(group wa.JID) *envelope.Envelope
}

func vcardEnvelope(group wa.JID, c envelope.ContactRef) *envelope.Envelope {
	return &envelope.Envelope{Bot: bot, Group: group, Contacts: []envelope.ContactRef{c}}
}

func urlEnvelope(group wa.JID, body, url string) *envelope.Envelope {
	e := bodyEnvelope(group, body)
	e.URLs = []envelope.URLRef{{URL: url, Source: envelope.SourceBody}}
	return e
}

// listedContent covers every place §9.8 step 4 names: a vCard waid or TEL, a wa.me link and a
// number advertised in text (with the §7.2 obfuscations).
var listedContent = []contentCase{
	{"vcard waid", func(g wa.JID) *envelope.Envelope {
		return vcardEnvelope(g, envelope.ContactRef{DisplayName: "Cheap Followers", FullName: "Cheap Followers", WAIDs: []string{"966551234567"}})
	}},
	{"vcard TEL international", func(g wa.JID) *envelope.Envelope {
		return vcardEnvelope(g, envelope.ContactRef{DisplayName: "Offers", Phones: []string{"+966 55 123 4567"}})
	}},
	{"vcard TEL national", func(g wa.JID) *envelope.Envelope {
		return vcardEnvelope(g, envelope.ContactRef{DisplayName: "Offers", Phones: []string{"055 123 4567"}})
	}},
	{"wa.me link in URLs", func(g wa.JID) *envelope.Envelope {
		return urlEnvelope(g, "chat with us https://wa.me/966551234567", "https://wa.me/966551234567")
	}},
	{"api.whatsapp.com link in URLs", func(g wa.JID) *envelope.Envelope {
		return urlEnvelope(g, "click here", "https://api.whatsapp.com/send?phone=966551234567&text=hi")
	}},
	{"wa.me link only in text", func(g wa.JID) *envelope.Envelope {
		return bodyEnvelope(g, "Join now: wa.me/966551234567?text=hi")
	}},
	{"text international", func(g wa.JID) *envelope.Envelope {
		return bodyEnvelope(g, "Call +966 55 123 4567 for cheap followers")
	}},
	{"text national", func(g wa.JID) *envelope.Envelope {
		return bodyEnvelope(g, "اتصل على 0551234567 للاستثمار")
	}},
	{"text Arabic-Indic digits", func(g wa.JID) *envelope.Envelope {
		return bodyEnvelope(g, "واتساب ٠٥٥١٢٣٤٥٦٧ ارباح يومية")
	}},
	{"text spaced digits", func(g wa.JID) *envelope.Envelope {
		return bodyEnvelope(g, "whatsapp 0 5 5 1 2 3 4 5 6 7 now")
	}},
	{"text zero-width inside", func(g wa.JID) *envelope.Envelope {
		return bodyEnvelope(g, "contact +966​55‌1234‍567")
	}},
	{"caption", func(g wa.JID) *envelope.Envelope {
		return &envelope.Envelope{Bot: bot, Group: g, Texts: []envelope.TextSurface{{Source: "caption", Raw: "Order on 0551234567"}}}
	}},
}

// checkKnown asserts one blacklist signal with known evidence and a masked number.
func checkKnown(t *testing.T, name, e164 string, sigs []detect.Signal) {
	t.Helper()
	hits := blacklistSignals(sigs)
	if len(hits) != 1 {
		t.Errorf("%s: got %d %s signals (all signals %+v), want exactly 1 (a blacklisted number in content is "+
			"known spam, DESIGN §9.8 step 4)", name, len(hits), blacklist.CodeBlacklisted, sigs)
		return
	}
	s := hits[0]
	if !s.Known {
		t.Errorf("%s: signal Known = false, want true (the manual blacklist is CERTAIN evidence with source known, DESIGN §7.3)", name)
	}
	if s.Hard {
		t.Errorf("%s: signal Hard = true, want false (a blacklist hit is spam, not a policy violation, DESIGN §9.3)", name)
	}
	if s.Weight <= 0 {
		t.Errorf("%s: signal Weight = %v, want > 0 (it is spam evidence)", name, s.Weight)
	}
	checkEvidence(t, name, e164, s.Evidence)
}

func checkEvidence(t *testing.T, name, e164, evidence string) {
	t.Helper()
	mask := blacklist.Mask(e164)
	if !strings.Contains(evidence, mask) {
		t.Errorf("%s: evidence %q lacks the masked number %q (admins must see which listed number matched)", name, evidence, mask)
	}
	if len(evidence) <= len(mask) {
		t.Errorf("%s: evidence %q only has the number, want it to say where the number was found too", name, evidence)
	}
	if strings.Contains(asciiDigits(evidence), asciiDigits(e164)[3:]) {
		t.Errorf("%s: evidence %q contains the full number, want it masked (numbers are personal data and "+
			"group-admin views show them masked, DESIGN §9.8, §17.1)", name, evidence)
	}
}

func TestBlacklistAnalyzerIdentity(t *testing.T) {
	f := newFixture(t, nil)
	if got := f.analyzer.Name(); got != "number.blacklist" {
		t.Errorf("Name() = %q, want %q", got, "number.blacklist")
	}
	if got := f.analyzer.Tier(); got != detect.T0 {
		t.Errorf("Tier() = %v, want T0 (exact number lists run in the policy-and-exact tier, DESIGN §7.1)", got)
	}
	if blacklist.CodeBlacklisted != "number.blacklisted" {
		t.Errorf("CodeBlacklisted = %q, want %q", blacklist.CodeBlacklisted, "number.blacklisted")
	}
}

func TestBlacklistedNumberInContentIsKnown(t *testing.T) {
	scopes := []struct {
		name        string
		useInstance *bool
		entry       func(t *testing.T) rules.BlacklistEntry
	}{
		{"group entry", nil, func(t *testing.T) rules.BlacklistEntry { return groupEntry(t, 1, groupOne, listed) }},
		{"instance entry", ptr(true), func(t *testing.T) rules.BlacklistEntry { return instanceEntry(t, 1, listed) }},
	}
	for _, sc := range scopes {
		for _, c := range listedContent {
			t.Run(sc.name+"/"+c.name, func(t *testing.T) {
				f := newFixture(t, sc.useInstance, sc.entry(t))
				e := c.env(groupOne)
				if !f.analyzer.Applies(e, &policy{}) {
					t.Errorf("Applies = false for a message carrying a number, want true (otherwise the pipeline skips " +
						"the blacklist check)")
				}
				checkKnown(t, c.name, listed, analyze(t, f.analyzer, e))
			})
		}
	}
}

func TestBlacklistOneSignalPerMessage(t *testing.T) {
	f := newFixture(t, ptr(true), groupEntry(t, 1, groupOne, listed), instanceEntry(t, 2, listed))
	e := &envelope.Envelope{
		Bot:   bot,
		Group: groupOne,
		Texts: []envelope.TextSurface{
			{Source: envelope.SourceBody, Raw: "call 0551234567 or +966 55 123 4567 or wa.me/966551234567"},
			{Source: "caption", Raw: "٠٥٥١٢٣٤٥٦٧"},
		},
		URLs:     []envelope.URLRef{{URL: "https://wa.me/966551234567", Source: envelope.SourceBody}},
		Contacts: []envelope.ContactRef{{DisplayName: "x", Phones: []string{"+966551234567"}, WAIDs: []string{"966551234567"}}},
	}
	sigs := blacklistSignals(analyze(t, f.analyzer, e))
	if len(sigs) != 1 {
		t.Errorf("got %d %s signals for one number seen in many places and on both lists, want 1 "+
			"(repeats must not stack the weight)", len(sigs), blacklist.CodeBlacklisted)
	}
}

func TestBlacklistEvidenceMasksNumber(t *testing.T) {
	f := newFixture(t, nil, groupEntry(t, 1, groupOne, listed))
	for _, c := range listedContent {
		sigs := blacklistSignals(analyze(t, f.analyzer, c.env(groupOne)))
		for _, s := range sigs {
			checkEvidence(t, c.name, listed, s.Evidence)
		}
	}
}

func TestBlacklistNoSignal(t *testing.T) {
	tests := []struct {
		name        string
		useInstance *bool
		entries     func(t *testing.T) []rules.BlacklistEntry
		env         *envelope.Envelope
		why         string
	}{
		{
			"unlisted number in text", nil,
			func(t *testing.T) []rules.BlacklistEntry {
				return []rules.BlacklistEntry{groupEntry(t, 1, groupOne, listed)}
			},
			bodyEnvelope(groupOne, "call 0501234567 for the meeting"), "only listed numbers count",
		},
		{
			"unlisted number in vcard", nil,
			func(t *testing.T) []rules.BlacklistEntry {
				return []rules.BlacklistEntry{groupEntry(t, 1, groupOne, listed)}
			},
			vcardEnvelope(groupOne, envelope.ContactRef{DisplayName: "Mum", Phones: []string{"+966501234567"}, WAIDs: []string{"966501234567"}}),
			"sharing an ordinary contact is normal",
		},
		{
			"near-miss number", nil,
			func(t *testing.T) []rules.BlacklistEntry {
				return []rules.BlacklistEntry{groupEntry(t, 1, groupOne, listed)}
			},
			bodyEnvelope(groupOne, "my number is 0551234568"), "matching is exact on E.164",
		},
		{
			"no number at all", nil,
			func(t *testing.T) []rules.BlacklistEntry {
				return []rules.BlacklistEntry{groupEntry(t, 1, groupOne, listed)}
			},
			bodyEnvelope(groupOne, "صباح الخير، see you at 5 pm"), "ordinary chat must not fire",
		},
		{
			"partial digits of the listed number", nil,
			func(t *testing.T) []rules.BlacklistEntry {
				return []rules.BlacklistEntry{groupEntry(t, 1, groupOne, listed)}
			},
			bodyEnvelope(groupOne, "the code is 1234567"), "a fragment is not the number",
		},
		{
			"another group's entry", nil,
			func(t *testing.T) []rules.BlacklistEntry {
				return []rules.BlacklistEntry{groupEntry(t, 1, groupTwo, listed)}
			},
			bodyEnvelope(groupOne, "call 0551234567"), "group entries are that group's admins' decision, DESIGN §9.8",
		},
		{
			"instance entry, group opted out", ptr(false),
			func(t *testing.T) []rules.BlacklistEntry { return []rules.BlacklistEntry{instanceEntry(t, 1, listed)} },
			bodyEnvelope(groupOne, "call 0551234567"), "blacklist.use_instance: false opts out, DESIGN §9.8",
		},
		{
			"instance entry, no settings", nil,
			func(t *testing.T) []rules.BlacklistEntry { return []rules.BlacklistEntry{instanceEntry(t, 1, listed)} },
			vcardEnvelope(groupOne, envelope.ContactRef{DisplayName: "x", WAIDs: []string{"966551234567"}}),
			"without group settings the instance list is not used (conservative default)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t, tt.useInstance, tt.entries(t)...)
			if sigs := blacklistSignals(analyze(t, f.analyzer, tt.env)); len(sigs) != 0 {
				t.Errorf("got %+v, want no %s signal (%s)", sigs, blacklist.CodeBlacklisted, tt.why)
			}
		})
	}
}

func TestBlacklistGroupComesFromEnvelope(t *testing.T) {
	f := newFixture(t, nil, groupEntry(t, 1, groupOne, listed), groupEntry(t, 2, groupTwo, listedOther))
	// Settings that name the wrong group must not redirect the lookup.
	a := blacklist.New[policy](ruleview.Source{
		Rules: f.holder,
		Settings: func(*envelope.Envelope) rules.ViewOptions {
			return rules.ViewOptions{Bot: "someone@s.whatsapp.net", Group: string(groupTwo)}
		},
	}, f.hasher.Hash)

	if sigs := blacklistSignals(analyze(t, a, bodyEnvelope(groupOne, "call 0551234567"))); len(sigs) != 1 {
		t.Errorf("listed in groupOne, message in groupOne, settings say groupTwo: got %d signals, want 1 "+
			"(the group always comes from the envelope)", len(sigs))
	}
	if sigs := blacklistSignals(analyze(t, a, bodyEnvelope(groupOne, "call +44 7911 123456"))); len(sigs) != 0 {
		t.Errorf("listed only in groupTwo, message in groupOne, settings say groupTwo: got %d signals, want 0 "+
			"(another group's list must never apply)", len(sigs))
	}
}

func TestBlacklistRemoveLiftsSignal(t *testing.T) {
	f := newFixture(t, nil)
	msg := func() *envelope.Envelope {
		return vcardEnvelope(groupOne, envelope.ContactRef{DisplayName: "Offers", WAIDs: []string{"966551234567"}})
	}

	if sigs := blacklistSignals(analyze(t, f.analyzer, msg())); len(sigs) != 0 {
		t.Fatalf("before the number is listed: got %d signals, want 0", len(sigs))
	}

	f.loader.set(groupEntry(t, 7, groupOne, listed))
	f.reload(t)
	if sigs := blacklistSignals(analyze(t, f.analyzer, msg())); len(sigs) != 1 {
		t.Fatalf("after .blacklist add and a reload: got %d signals, want 1 (new entries apply without a restart)", len(sigs))
	}

	f.loader.set()
	f.reload(t)
	if sigs := blacklistSignals(analyze(t, f.analyzer, msg())); len(sigs) != 0 {
		t.Errorf("after .blacklist remove and a reload: got %d signals, want 0 (remove is the undo and lifts the "+
			"content signal, DESIGN §9.8 step 5)", len(sigs))
	}
}

func TestBlacklistAnalyzeDuringReload(t *testing.T) {
	f := newFixture(t, nil, groupEntry(t, 1, groupOne, listed))
	entry := groupEntry(t, 1, groupOne, listed)
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range 50 {
				e := bodyEnvelope(groupOne, "call 0551234567")
				textnorm.Prepare(e, region)
				sigs, err := f.analyzer.Analyze(t.Context(), e, &detect.State{})
				if err != nil {
					t.Errorf("Analyze during a reload: %v, want nil", err)
					return
				}
				if n := len(blacklistSignals(sigs)); n > 1 {
					t.Errorf("got %d signals during a reload, want 0 or 1 (a reader sees the old or the new list, never a mix)", n)
				}
			}
		})
	}
	for i := range 50 {
		if i%2 == 0 {
			f.loader.set()
		} else {
			f.loader.set(entry)
		}
		f.reload(t)
	}
	wg.Wait()
}
