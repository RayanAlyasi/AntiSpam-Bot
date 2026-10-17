package blacklist

import (
	"context"
	"slices"
	"sync"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// memList is the in-memory List behind MemoryList.
type memList struct {
	mu      sync.Mutex
	entries []Entry // active entries, in insertion order
	nextID  int64
}

func (m *memList) Add(ctx context.Context, e Entry) (Entry, bool, error) {
	if err := ctx.Err(); err != nil {
		return Entry{}, false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, x := range m.entries {
		if x.Scope == e.Scope && x.Group == e.Group && x.E164 == e.E164 {
			return x, false, nil
		}
	}
	// A number already resolved under another scope keeps its resolution.
	for _, x := range m.entries {
		if x.E164 == e.E164 && x.OnWhatsApp != nil {
			e.Person, e.OnWhatsApp = x.Person, x.OnWhatsApp
			break
		}
	}
	m.nextID++
	e.ID = m.nextID
	m.entries = append(m.entries, e)
	return e, true, nil
}

func (m *memList) Remove(ctx context.Context, scope Scope, group wa.JID, e164 string, _ wa.JID) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	n := len(m.entries)
	m.entries = slices.DeleteFunc(m.entries, func(x Entry) bool {
		return x.Scope == scope && x.Group == group && x.E164 == e164
	})
	return len(m.entries) < n, nil
}

func (m *memList) filter(ctx context.Context, keep func(Entry) bool) ([]Entry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Entry
	for _, x := range m.entries {
		if keep(x) {
			out = append(out, x)
		}
	}
	return out, nil
}

func (m *memList) ForGroup(ctx context.Context, group wa.JID) ([]Entry, error) {
	return m.filter(ctx, func(x Entry) bool { return x.Scope == ScopeGroup && x.Group == group })
}

func (m *memList) Instance(ctx context.Context) ([]Entry, error) {
	return m.filter(ctx, func(x Entry) bool { return x.Scope == ScopeInstance })
}

func (m *memList) ByNumber(ctx context.Context, e164 string) ([]Entry, error) {
	return m.filter(ctx, func(x Entry) bool { return x.E164 == e164 })
}

func (m *memList) ByPerson(ctx context.Context, lid wa.JID) ([]Entry, error) {
	if lid == "" {
		return nil, nil
	}
	return m.filter(ctx, func(x Entry) bool { return x.Person == lid })
}

func (m *memList) SetResolved(ctx context.Context, e164 string, lid wa.JID, onWhatsApp bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.entries {
		if m.entries[i].E164 == e164 {
			on := onWhatsApp
			m.entries[i].Person, m.entries[i].OnWhatsApp = lid, &on
		}
	}
	return nil
}
