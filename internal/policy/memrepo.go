package policy

import (
	"context"
	"encoding/json"
	"slices"
	"sync"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// MemSettingsRepo is an in-memory SettingsRepo for tests in any package. It keeps every
// version, the audit diffs and the notifications, like the Postgres store does.
type MemSettingsRepo struct {
	clock clock.Clock

	mu       sync.Mutex
	current  map[wa.JID]StoredSettings
	history  map[wa.JID][]StoredSettings
	audit    []MemAudit
	notified []wa.JID
	// OnSave, when set, runs inside SaveSettings before the version check (to simulate a
	// concurrent change in tests).
	OnSave func(group wa.JID)
}

// MemAudit is one audit entry MemSettingsRepo recorded.
type MemAudit struct {
	Actor string
	Group wa.JID
	Diff  json.RawMessage
}

// NewMemSettingsRepo returns an empty repo reading time from c.
func NewMemSettingsRepo(c clock.Clock) *MemSettingsRepo {
	return &MemSettingsRepo{clock: c, current: map[wa.JID]StoredSettings{}, history: map[wa.JID][]StoredSettings{}}
}

// LoadSettings implements SettingsRepo.
func (m *MemSettingsRepo) LoadSettings(_ context.Context, group wa.JID) (StoredSettings, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.current[group]
	return s, ok, nil
}

// SaveSettings implements SettingsRepo.
func (m *MemSettingsRepo) SaveSettings(_ context.Context, c SettingsChange) (int, error) {
	if m.OnSave != nil {
		m.OnSave(c.Group)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current[c.Group].Version != c.PrevVersion {
		return 0, ErrSettingsConflict
	}
	s := StoredSettings{Doc: slices.Clone(c.Doc), Version: c.PrevVersion + 1, UpdatedBy: c.Actor, UpdatedAt: m.clock.Now()}
	m.current[c.Group] = s
	m.history[c.Group] = append(m.history[c.Group], s)
	m.audit = append(m.audit, MemAudit{Actor: c.Actor, Group: c.Group, Diff: c.AuditDiff(s.Version)})
	m.notified = append(m.notified, c.Group)
	return s.Version, nil
}

// Put stores a document directly, as another process would, without audit or notification.
func (m *MemSettingsRepo) Put(group wa.JID, doc json.RawMessage, actor string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := StoredSettings{Doc: doc, Version: m.current[group].Version + 1, UpdatedBy: actor, UpdatedAt: m.clock.Now()}
	m.current[group] = s
	m.history[group] = append(m.history[group], s)
	return s.Version
}

// History returns every stored version of a group's document, oldest first.
func (m *MemSettingsRepo) History(group wa.JID) []StoredSettings {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.history[group])
}

// Audit returns the audit entries written so far.
func (m *MemSettingsRepo) Audit() []MemAudit {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.audit)
}

// Notified returns the groups notified so far, in order.
func (m *MemSettingsRepo) Notified() []wa.JID {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.notified)
}

var _ SettingsRepo = (*MemSettingsRepo)(nil)
