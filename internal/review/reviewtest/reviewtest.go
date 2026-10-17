// Package reviewtest holds in-memory fakes of the review package's ports, for tests of the
// review, blacklist and chat packages. Every fake is safe for concurrent use.
package reviewtest

import (
	"context"
	"slices"
	"sync"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// Actions records every enqueued action in order.
type Actions struct {
	mu   sync.Mutex
	list []review.Action
	// Err, when set, is returned by Enqueue (the action is still recorded).
	Err error
}

var _ review.Actions = (*Actions)(nil)

// Enqueue records a.
func (a *Actions) Enqueue(_ context.Context, act review.Action) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.list = append(a.list, act)
	return a.Err
}

// All returns every recorded action in order.
func (a *Actions) All() []review.Action {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.list)
}

// Reset forgets the recorded actions.
func (a *Actions) Reset() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.list = nil
}

// Of returns the recorded actions of type T, in order.
func Of[T review.Action](a *Actions) []T {
	var out []T
	for _, act := range a.All() {
		if t, ok := act.(T); ok {
			out = append(out, t)
		}
	}
	return out
}

// Admins is a settable admin list per group.
type Admins struct {
	mu     sync.Mutex
	admins map[wa.JID]map[wa.JID]bool
	// Err, when set, is returned by IsAdmin.
	Err error
}

var _ review.Admins = (*Admins)(nil)

// Set makes person an admin of group (or not).
func (a *Admins) Set(group, person wa.JID, admin bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.admins == nil {
		a.admins = map[wa.JID]map[wa.JID]bool{}
	}
	if a.admins[group] == nil {
		a.admins[group] = map[wa.JID]bool{}
	}
	a.admins[group][person] = admin
}

// IsAdmin reports whether person is an admin of group.
func (a *Admins) IsAdmin(_ context.Context, group, person wa.JID) (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.Err != nil {
		return false, a.Err
	}
	return a.admins[group][person], nil
}

// AdminsOf lists the admins of group.
func (a *Admins) AdminsOf(group wa.JID) []wa.JID {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []wa.JID
	for p, ok := range a.admins[group] {
		if ok {
			out = append(out, p)
		}
	}
	slices.Sort(out)
	return out
}

// GroupsOf lists the groups where person is an admin.
func (a *Admins) GroupsOf(person wa.JID) []wa.JID {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []wa.JID
	for g, m := range a.admins {
		if m[person] {
			out = append(out, g)
		}
	}
	slices.Sort(out)
	return out
}

// Settings serves a review.Config per group; groups without one get Default.
type Settings struct {
	mu      sync.Mutex
	configs map[wa.JID]review.Config
	// Default is served for groups without their own config.
	Default review.Config
}

var _ review.Settings = (*Settings)(nil)

// Set stores a group's config.
func (s *Settings) Set(group wa.JID, c review.Config) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.configs == nil {
		s.configs = map[wa.JID]review.Config{}
	}
	s.configs[group] = c
}

// ReviewConfig returns the group's config.
func (s *Settings) ReviewConfig(_ context.Context, group wa.JID) (review.Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.configs[group]; ok {
		return c, nil
	}
	return s.Default, nil
}

// CorpusEntry is one entry in the fake corpus.
type CorpusEntry struct {
	ID        int64
	Group     wa.JID
	Message   wa.MessageRef
	Text      string
	Admin     wa.JID
	Source    string
	Retracted bool
}

// Corpus is an in-memory confirmed-spam corpus with a fingerprint allow-list. A retracted
// entry loses its text, as the real corpus deletes it.
type Corpus struct {
	mu      sync.Mutex
	entries []CorpusEntry
	allowed []review.FingerprintAllow
}

var _ review.Corpus = (*Corpus)(nil)

// Confirm adds an active entry.
func (c *Corpus) Confirm(_ context.Context, conf review.Confirmation) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	id := int64(len(c.entries) + 1)
	c.entries = append(c.entries, CorpusEntry{
		ID: id, Group: conf.Group, Message: conf.Message, Text: conf.Text, Admin: conf.Admin, Source: conf.Source,
	})
	return id, nil
}

// EntryFor finds the newest active entry created from a message.
func (c *Corpus) EntryFor(_ context.Context, group wa.JID, msg wa.MessageID) (int64, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := len(c.entries) - 1; i >= 0; i-- {
		e := c.entries[i]
		if e.Group == group && e.Message.ID == msg && !e.Retracted {
			return e.ID, true, nil
		}
	}
	return 0, false, nil
}

// Retract marks an entry retracted and deletes its text.
func (c *Corpus) Retract(_ context.Context, id int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.entries {
		if c.entries[i].ID == id {
			c.entries[i].Retracted = true
			c.entries[i].Text = ""
			return nil
		}
	}
	return review.ErrNotFound
}

// AllowFingerprint records an allow-list entry. Like the real corpus, it keeps no text.
func (c *Corpus) AllowFingerprint(_ context.Context, f review.FingerprintAllow) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	f.Text = ""
	c.allowed = append(c.allowed, f)
	return nil
}

// Entries returns every entry, retracted ones included.
func (c *Corpus) Entries() []CorpusEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.entries)
}

// Active returns the entries that still match.
func (c *Corpus) Active() []CorpusEntry {
	var out []CorpusEntry
	for _, e := range c.Entries() {
		if !e.Retracted {
			out = append(out, e)
		}
	}
	return out
}

// Allowed returns the fingerprint allow-list entries.
func (c *Corpus) Allowed() []review.FingerprintAllow {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.allowed)
}

// StandingEvent is one call recorded by Standing.
type StandingEvent struct {
	Kind    string // remove_strike | restore_trust | false_positive
	Group   wa.JID
	Person  wa.JID
	Message wa.MessageID
	Actor   wa.JID
}

// Standing records trust and strike changes.
type Standing struct {
	mu     sync.Mutex
	events []StandingEvent
}

var _ review.Standing = (*Standing)(nil)

// RemoveStrike records a strike removal.
func (s *Standing) RemoveStrike(_ context.Context, group, person wa.JID, msg wa.MessageID) error {
	s.add(StandingEvent{Kind: "remove_strike", Group: group, Person: person, Message: msg})
	return nil
}

// RestoreTrust records a trust restore.
func (s *Standing) RestoreTrust(_ context.Context, group, person wa.JID) error {
	s.add(StandingEvent{Kind: "restore_trust", Group: group, Person: person})
	return nil
}

// RecordFalsePositive records a false positive.
func (s *Standing) RecordFalsePositive(_ context.Context, group wa.JID, msg wa.MessageRef, actor wa.JID) error {
	s.add(StandingEvent{Kind: "false_positive", Group: group, Person: msg.Sender, Message: msg.ID, Actor: actor})
	return nil
}

func (s *Standing) add(e StandingEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e)
}

// Events returns every recorded event in order.
func (s *Standing) Events() []StandingEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.events)
}

// Audit records audit entries.
type Audit struct {
	mu      sync.Mutex
	entries []review.AuditEntry
}

var _ review.Audit = (*Audit)(nil)

// Record stores e.
func (a *Audit) Record(_ context.Context, e review.AuditEntry) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.entries = append(a.entries, e)
	return nil
}

// Entries returns every entry in order.
func (a *Audit) Entries() []review.AuditEntry {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.entries)
}
