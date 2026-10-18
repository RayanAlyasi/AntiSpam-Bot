// Package chattest holds in-memory fakes of the chat package's ports. Every fake is safe for
// concurrent use.
package chattest

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/api/chat"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review/reviewtest"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// Groups serves protected groups, review groups and participants. Admin lists come from the
// shared reviewtest.Admins, so one fake answers both chat and review.
type Groups struct {
	mu        sync.Mutex
	Admins    *reviewtest.Admins
	protected map[wa.JID]string // JID → name
	names     map[wa.JID]string
	served    map[wa.JID][]wa.JID // review group → protected groups
	members   map[wa.JID][]wa.JID
	community map[wa.JID]chat.GroupRef         // group → its community's JID and name
	listed    map[wa.JID][]chat.CommunityGroup // community → its groups
}

var (
	_ chat.Groups      = (*Groups)(nil)
	_ chat.Communities = (*Groups)(nil)
)

// NewGroups returns an empty Groups that reads admins from admins.
func NewGroups(admins *reviewtest.Admins) *Groups {
	return &Groups{
		Admins:    admins,
		protected: map[wa.JID]string{},
		names:     map[wa.JID]string{},
		served:    map[wa.JID][]wa.JID{},
		members:   map[wa.JID][]wa.JID{},
		community: map[wa.JID]chat.GroupRef{},
		listed:    map[wa.JID][]chat.CommunityGroup{},
	}
}

// InCommunity puts group in the community with the given JID and name.
func (g *Groups) InCommunity(group, community wa.JID, name string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.community[group] = chat.GroupRef{JID: community, Name: name}
}

// SetCommunityGroups sets the groups CommunityGroups lists for a community.
func (g *Groups) SetCommunityGroups(community wa.JID, groups ...chat.CommunityGroup) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.listed[community] = slices.Clone(groups)
}

// CommunityGroups lists the groups set with SetCommunityGroups.
func (g *Groups) CommunityGroups(_ context.Context, community wa.JID) ([]chat.CommunityGroup, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.listed[community]), nil
}

// Protect makes group a protected group called name.
func (g *Groups) Protect(group wa.JID, name string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.protected[group] = name
	g.names[group] = name
}

// SetName sets the name of a group that isn't protected (a review group).
func (g *Groups) SetName(group wa.JID, name string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.names[group] = name
}

// Serve records that reviewGroup is the review group of protected.
func (g *Groups) Serve(reviewGroup, protected wa.JID) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !slices.Contains(g.served[reviewGroup], protected) {
		g.served[reviewGroup] = append(g.served[reviewGroup], protected)
	}
}

// Unserve removes protected from reviewGroup's list.
func (g *Groups) Unserve(reviewGroup, protected wa.JID) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.served[reviewGroup] = slices.DeleteFunc(g.served[reviewGroup], func(j wa.JID) bool { return j == protected })
}

// SetParticipants sets a group's participants.
func (g *Groups) SetParticipants(group wa.JID, people ...wa.JID) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.members[group] = slices.Clone(people)
}

// IsProtected reports whether group is protected.
func (g *Groups) IsProtected(_ context.Context, group wa.JID) (bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	_, ok := g.protected[group]
	return ok, nil
}

// Name returns a group's name.
func (g *Groups) Name(_ context.Context, group wa.JID) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.names[group], nil
}

// AdminGroups lists the protected groups where person is an admin.
func (g *Groups) AdminGroups(_ context.Context, person wa.JID) ([]chat.GroupRef, error) {
	var out []chat.GroupRef
	for _, grp := range g.Admins.GroupsOf(person) {
		g.mu.Lock()
		name, ok := g.protected[grp]
		c := g.community[grp]
		g.mu.Unlock()
		if ok {
			out = append(out, chat.GroupRef{JID: grp, Name: name, Community: c.JID, CommunityName: c.Name})
		}
	}
	return out, nil
}

// ServedBy lists the protected groups chat reviews for.
func (g *Groups) ServedBy(_ context.Context, c wa.JID) ([]wa.JID, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.served[c]), nil
}

// Participants lists a group's participants.
func (g *Groups) Participants(_ context.Context, group wa.JID) ([]wa.JID, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.members[group]), nil
}

// Settings is a map of dotted keys per group. Keys must be registered with Allow before Set
// accepts them; Validate, when set, can reject a value.
type Settings struct {
	mu       sync.Mutex
	values   map[wa.JID]map[string]string
	known    map[string]bool
	changes  []SettingsChange
	Validate func(key, value string) error
	// Presets maps a preset name to the keys it resets.
	Presets map[string]map[string]string
}

// SettingsChange is one recorded Set or preset change.
type SettingsChange struct {
	Group wa.JID
	Key   string
	Value string
	Actor wa.JID
}

var _ chat.Settings = (*Settings)(nil)

// NewSettings returns Settings that know the given keys with their defaults.
func NewSettings(defaults map[string]string) *Settings {
	s := &Settings{values: map[wa.JID]map[string]string{}, known: map[string]bool{}}
	for k, v := range defaults {
		s.known[k] = true
		s.values[""] = mapSet(s.values[""], k, v)
	}
	return s
}

func mapSet(m map[string]string, k, v string) map[string]string {
	if m == nil {
		m = map[string]string{}
	}
	m[k] = v
	return m
}

// Get returns a key's value for a group (or its default).
func (s *Settings) Get(_ context.Context, group wa.JID, key string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.known[key] {
		return "", fmt.Errorf("%q: %w", key, chat.ErrUnknownKey)
	}
	if v, ok := s.values[group][key]; ok {
		return v, nil
	}
	return s.values[""][key], nil
}

// Set validates and stores a value.
func (s *Settings) Set(_ context.Context, group wa.JID, key, value string, actor wa.JID) (chat.Change, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.known[key] {
		return chat.Change{}, fmt.Errorf("%q: %w", key, chat.ErrUnknownKey)
	}
	if s.Validate != nil {
		if err := s.Validate(key, value); err != nil {
			return chat.Change{}, fmt.Errorf("%s=%q: %w: %w", key, value, chat.ErrInvalidValue, err)
		}
	}
	old, ok := s.values[group][key]
	if !ok {
		old = s.values[""][key]
	}
	s.values[group] = mapSet(s.values[group], key, value)
	s.changes = append(s.changes, SettingsChange{Group: group, Key: key, Value: value, Actor: actor})
	return chat.Change{Key: key, Old: old, New: value}, nil
}

// ApplyPreset sets "preset" and the preset's keys.
func (s *Settings) ApplyPreset(ctx context.Context, group wa.JID, preset string, actor wa.JID) ([]chat.Change, error) {
	keys, ok := s.Presets[preset]
	if !ok {
		return nil, fmt.Errorf("preset %q: %w", preset, chat.ErrInvalidValue)
	}
	var out []chat.Change
	c, err := s.Set(ctx, group, "preset", preset, actor)
	if err != nil {
		return nil, err
	}
	out = append(out, c)
	for k, v := range keys {
		c, err := s.Set(ctx, group, k, v, actor)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// Changes returns every recorded change in order.
func (s *Settings) Changes() []SettingsChange {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.changes)
}

// Lists records list edits.
type Lists struct {
	mu    sync.Mutex
	Edits []string // "allow_domain example.com", "block_word free money", …
}

var _ chat.Lists = (*Lists)(nil)

func (l *Lists) add(op, v string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.Edits = append(l.Edits, op+" "+v)
	return nil
}

// AllowDomain records the edit.
func (l *Lists) AllowDomain(_ context.Context, _ wa.JID, d string, _ wa.JID) error {
	return l.add("allow_domain", d)
}

// BlockDomain records the edit.
func (l *Lists) BlockDomain(_ context.Context, _ wa.JID, d string, _ wa.JID) error {
	return l.add("block_domain", d)
}

// BlockWord records the edit.
func (l *Lists) BlockWord(_ context.Context, _ wa.JID, p string, _ wa.JID) error {
	return l.add("block_word", p)
}

// UnblockWord records the edit.
func (l *Lists) UnblockWord(_ context.Context, _ wa.JID, p string, _ wa.JID) error {
	return l.add("unblock_word", p)
}

// All returns the edits in order.
func (l *Lists) All() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.Edits)
}

// Recent returns a fixed count per phrase.
type Recent struct {
	Counts map[string]int
}

var _ chat.Recent = (*Recent)(nil)

// CountMatching returns the configured count.
func (r *Recent) CountMatching(_ context.Context, _ wa.JID, phrase string) (int, error) {
	return r.Counts[phrase], nil
}

// Members is an in-memory member allow-list.
type Members struct {
	mu      sync.Mutex
	allowed map[wa.JID]map[wa.JID]bool
}

var _ chat.Members = (*Members)(nil)

// Allow adds person to group's allow-list.
func (m *Members) Allow(_ context.Context, group, person, _ wa.JID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.allowed == nil {
		m.allowed = map[wa.JID]map[wa.JID]bool{}
	}
	if m.allowed[group] == nil {
		m.allowed[group] = map[wa.JID]bool{}
	}
	m.allowed[group][person] = true
	return nil
}

// Unallow removes person from group's allow-list.
func (m *Members) Unallow(_ context.Context, group, person, _ wa.JID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.allowed[group], person)
	return nil
}

// Allowed reports whether person is allow-listed in group.
func (m *Members) Allowed(group, person wa.JID) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.allowed[group][person]
}

// History is a settable list of the bot's reversible actions per group.
type History struct {
	mu      sync.Mutex
	actions map[wa.JID][]chat.Reversible
	undone  map[int64]wa.JID
}

var _ chat.History = (*History)(nil)

// Add records a reversible action.
func (h *History) Add(group wa.JID, r chat.Reversible) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.actions == nil {
		h.actions = map[wa.JID][]chat.Reversible{}
	}
	h.actions[group] = append(h.actions[group], r)
}

// LastReversible returns the newest action not yet undone.
func (h *History) LastReversible(_ context.Context, group wa.JID) (chat.Reversible, bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	list := h.actions[group]
	for i := len(list) - 1; i >= 0; i-- {
		if _, done := h.undone[list[i].ID]; !done {
			return list[i], true, nil
		}
	}
	return chat.Reversible{}, false, nil
}

// MarkUndone records that an action was undone.
func (h *History) MarkUndone(_ context.Context, id int64, by wa.JID) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.undone == nil {
		h.undone = map[int64]wa.JID{}
	}
	h.undone[id] = by
	return nil
}

// Identity maps JIDs to canonical JIDs and phone numbers to people.
type Identity struct {
	mu      sync.Mutex
	aliases map[wa.JID]wa.JID
	phones  map[string]wa.JID
}

var _ chat.Identity = (*Identity)(nil)

// Link records that phone (a phone JID such as "9665…@s.whatsapp.net") and lid are one person.
func (i *Identity) Link(phone, lid wa.JID) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.aliases == nil {
		i.aliases = map[wa.JID]wa.JID{}
		i.phones = map[string]wa.JID{}
	}
	i.aliases[phone] = lid
	i.phones["+"+phone.User()] = lid
}

// Canonical returns the LID for a linked phone JID, or jid itself.
func (i *Identity) Canonical(_ context.Context, jid wa.JID) (wa.JID, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if lid, ok := i.aliases[jid]; ok {
		return lid, nil
	}
	return jid, nil
}

// ByPhone finds the person with a number.
func (i *Identity) ByPhone(_ context.Context, e164 string) (wa.JID, bool, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	lid, ok := i.phones[e164]
	return lid, ok, nil
}

// Audit records audit entries.
type Audit = reviewtest.Audit

// Track is a settable TrackRecord.
type Track struct{ Ready bool }

// AIReady returns Ready.
func (t Track) AIReady(context.Context, wa.JID) (bool, error) { return t.Ready, nil }

// Stats returns fixed stats.
type Stats struct{ Day chat.DayStats }

// Today returns Day.
func (s Stats) Today(context.Context, wa.JID) (chat.DayStats, error) { return s.Day, nil }

// Start is a convenient start time for tests.
var Start = time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
