package blacklist_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/rs/zerolog"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/blacklist"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/rules"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/ruleview"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/textnorm"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/envelope"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

const (
	bot      wa.JID = "966500000000@s.whatsapp.net"
	groupOne wa.JID = "120363000000000001@g.us"
	groupTwo wa.JID = "120363000000000002@g.us"

	listed      = "+966551234567" // Saudi mobile, written 0551234567 nationally
	listedOther = "+447911123456" // UK mobile
	unlisted    = "+966501234567"
	nearMiss    = "+966551234568" // differs from listed in the last digit
	region      = "SA"
)

var hashKey = []byte("0123456789abcdef0123456789abcdef")

// policy stands in for the per-group policy type the pipeline passes to Applies.
type policy struct{}

func newHasher(t *testing.T) blacklist.Hasher {
	t.Helper()
	h, err := blacklist.NewHasher(hashKey)
	if err != nil {
		t.Fatalf("NewHasher(32-byte key): %v, want nil (keys of at least MinKeyLen bytes are valid)", err)
	}
	return h
}

// fakeLoader serves a mutable blacklist and no rules or fingerprints.
type fakeLoader struct {
	mu      sync.Mutex
	entries []rules.BlacklistEntry
}

func (l *fakeLoader) LoadRules(context.Context) ([]rules.Rule, error) { return nil, nil }

func (l *fakeLoader) LoadFingerprints(context.Context) ([]rules.Fingerprint, error) {
	return nil, nil
}

func (l *fakeLoader) LoadBlacklist(context.Context) ([]rules.BlacklistEntry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]rules.BlacklistEntry(nil), l.entries...), nil
}

func (l *fakeLoader) set(entries ...rules.BlacklistEntry) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = entries
}

// fixture is an analyzer wired to a holder over a fake loader.
type fixture struct {
	loader   *fakeLoader
	holder   *rules.Holder
	hasher   blacklist.Hasher
	analyzer *blacklist.Analyzer[policy]
}

// newFixture builds the analyzer. useInstance is the group's blacklist.use_instance setting;
// when it is nil, the source has no Settings func at all.
func newFixture(t *testing.T, useInstance *bool, entries ...rules.BlacklistEntry) *fixture {
	t.Helper()
	f := &fixture{loader: &fakeLoader{}, hasher: newHasher(t)}
	f.loader.set(entries...)
	f.holder = rules.NewHolder(f.loader, clock.Real{}, zerolog.Nop())
	f.reload(t)
	src := ruleview.Source{Rules: f.holder}
	if useInstance != nil {
		use := *useInstance
		src.Settings = func(e *envelope.Envelope) rules.ViewOptions {
			return rules.ViewOptions{Bot: string(e.Bot), Group: string(e.Group), UseInstanceBlacklist: use}
		}
	}
	f.analyzer = blacklist.New[policy](src, f.hasher.Hash)
	return f
}

func (f *fixture) reload(t *testing.T) {
	t.Helper()
	if err := f.holder.Reload(t.Context()); err != nil {
		t.Fatalf("Holder.Reload: %v, want nil", err)
	}
}

// groupEntry and instanceEntry build entries with hashFor, which uses the same key as every
// fixture's hasher.
func groupEntry(t *testing.T, id int64, group wa.JID, e164 string) rules.BlacklistEntry {
	t.Helper()
	return rules.BlacklistEntry{ID: id, Scope: rules.BlacklistGroup, GroupJID: string(group), PNHash: hashFor(t, e164)}
}

func instanceEntry(t *testing.T, id int64, e164 string) rules.BlacklistEntry {
	t.Helper()
	return rules.BlacklistEntry{ID: id, Scope: rules.BlacklistInstance, PNHash: hashFor(t, e164)}
}

// hashFor returns the keyed hash of e164 under hashKey.
func hashFor(t *testing.T, e164 string) string {
	t.Helper()
	return newHasher(t).Hash(e164)
}

func ptr[T any](v T) *T { return &v }

// bodyEnvelope is a group text message with the given body.
func bodyEnvelope(group wa.JID, body string) *envelope.Envelope {
	return &envelope.Envelope{
		Bot:   bot,
		Group: group,
		Texts: []envelope.TextSurface{{Source: envelope.SourceBody, Raw: body}},
	}
}

// analyze prepares the envelope like the pipeline does and runs the analyzer on it.
func analyze(t *testing.T, a *blacklist.Analyzer[policy], e *envelope.Envelope) []detect.Signal {
	t.Helper()
	textnorm.Prepare(e, region)
	sigs, err := a.Analyze(t.Context(), e, &detect.State{})
	if err != nil {
		t.Fatalf("Analyze: %v, want nil (a blacklist lookup works from memory and can't fail)", err)
	}
	return sigs
}

// blacklistSignals keeps the signals with the blacklist code.
func blacklistSignals(sigs []detect.Signal) []detect.Signal {
	var out []detect.Signal
	for _, s := range sigs {
		if s.Code == blacklist.CodeBlacklisted {
			out = append(out, s)
		}
	}
	return out
}

// asciiDigits returns only the digits in s, with Arabic-Indic and Persian digits folded to ASCII,
// so a number can't hide in evidence behind spacing, punctuation or another script.
func asciiDigits(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r >= '٠' && r <= '٩':
			b.WriteRune('0' + (r - '٠'))
		case r >= '۰' && r <= '۹':
			b.WriteRune('0' + (r - '۰'))
		}
	}
	return b.String()
}
