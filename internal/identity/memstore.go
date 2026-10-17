package identity

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// NewMemoryStore returns a Store that keeps everything in memory. It's for tests and tools; the
// bot uses the Postgres store.
func NewMemoryStore() Store { return &memStore{state: newMemState()} }

type memStore struct {
	writeMu sync.Mutex   // serializes Write calls
	mu      sync.RWMutex // guards state
	state   *memState
}

type memIdentity struct {
	person    PersonID
	ident     Identity
	firstSeen time.Time
	lastSeen  time.Time
}

type memKey struct {
	group  wa.JID
	person PersonID
}

type memState struct {
	nextID     PersonID
	persons    map[PersonID]Person
	mergedInto map[PersonID]PersonID
	identities []memIdentity
	members    map[memKey]Member
	merges     []memMerge
}

type memMerge struct {
	from, into PersonID
	reason     string
	at         time.Time
}

func newMemState() *memState {
	return &memState{
		persons:    map[PersonID]Person{},
		mergedInto: map[PersonID]PersonID{},
		members:    map[memKey]Member{},
	}
}

func (s *memState) clone() *memState {
	c := *s
	c.persons = maps.Clone(s.persons)
	c.mergedInto = maps.Clone(s.mergedInto)
	c.identities = slices.Clone(s.identities)
	c.members = maps.Clone(s.members)
	c.merges = slices.Clone(s.merges)
	return &c
}

func (m *memStore) read() *memState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state
}

func (m *memStore) FindOpen(ctx context.Context, kind Kind, value wa.JID) (PersonID, bool, error) {
	return m.read().findOpen(kind, value)
}

func (m *memStore) Person(ctx context.Context, id PersonID) (Person, error) {
	return m.read().person(id)
}

func (m *memStore) Identities(ctx context.Context, id PersonID) ([]Identity, error) {
	return m.read().identitiesOf(id), nil
}

func (m *memStore) Member(ctx context.Context, group wa.JID, id PersonID) (Member, bool, error) {
	mem, ok := m.read().members[memKey{group, id}]
	return mem, ok, nil
}

func (m *memStore) Members(ctx context.Context, id PersonID) ([]Member, error) {
	return m.read().membersWhere(func(k memKey) bool { return k.person == id }), nil
}

func (m *memStore) GroupMembers(ctx context.Context, group wa.JID) ([]Member, error) {
	return m.read().membersWhere(func(k memKey) bool { return k.group == group }), nil
}

// Write runs fn on a copy of the state and publishes the copy only if fn succeeds, so a failed
// write leaves nothing behind. Readers keep seeing the old state until then.
func (m *memStore) Write(ctx context.Context, fn func(tx Tx) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.writeMu.Lock()
	defer m.writeMu.Unlock()
	tx := &memTx{s: m.read().clone()}
	if err := fn(tx); err != nil {
		return err
	}
	m.mu.Lock()
	m.state = tx.s
	m.mu.Unlock()
	return nil
}

// memState reads, shared by the store and its transactions.

func (s *memState) findOpen(kind Kind, value wa.JID) (PersonID, bool, error) {
	for _, r := range s.identities {
		if r.ident.Kind == kind && r.ident.Value == value && r.ident.ValidTo == nil {
			return r.person, true, nil
		}
	}
	return 0, false, nil
}

func (s *memState) person(id PersonID) (Person, error) {
	p, ok := s.persons[id]
	if !ok {
		return Person{}, fmt.Errorf("person %d: %w", id, ErrNoPerson)
	}
	return p, nil
}

func (s *memState) identitiesOf(id PersonID) []Identity {
	var out []Identity
	for _, r := range s.identities {
		if r.person == id {
			ident := r.ident
			if ident.ValidTo != nil {
				t := *ident.ValidTo
				ident.ValidTo = &t
			}
			out = append(out, ident)
		}
	}
	return out
}

func (s *memState) membersWhere(keep func(memKey) bool) []Member {
	var out []Member
	for k, m := range s.members {
		if keep(k) {
			out = append(out, m)
		}
	}
	slices.SortFunc(out, func(a, b Member) int {
		if c := strings.Compare(string(a.Group), string(b.Group)); c != 0 {
			return c
		}
		return int(a.Person - b.Person)
	})
	return out
}

type memTx struct{ s *memState }

func (t *memTx) FindOpen(ctx context.Context, kind Kind, value wa.JID) (PersonID, bool, error) {
	return t.s.findOpen(kind, value)
}

func (t *memTx) Person(ctx context.Context, id PersonID) (Person, error) { return t.s.person(id) }

func (t *memTx) Identities(ctx context.Context, id PersonID) ([]Identity, error) {
	return t.s.identitiesOf(id), nil
}

func (t *memTx) Member(ctx context.Context, group wa.JID, id PersonID) (Member, bool, error) {
	m, ok := t.s.members[memKey{group, id}]
	return m, ok, nil
}

func (t *memTx) Members(ctx context.Context, id PersonID) ([]Member, error) {
	return t.s.membersWhere(func(k memKey) bool { return k.person == id }), nil
}

func (t *memTx) GroupMembers(ctx context.Context, group wa.JID) ([]Member, error) {
	return t.s.membersWhere(func(k memKey) bool { return k.group == group }), nil
}

func (t *memTx) CreatePerson(ctx context.Context, now time.Time) (PersonID, error) {
	t.s.nextID++
	id := t.s.nextID
	t.s.persons[id] = Person{ID: id}
	return id, nil
}

func (t *memTx) SetIdentifiers(ctx context.Context, p Person) error {
	if _, ok := t.s.persons[p.ID]; !ok {
		return fmt.Errorf("person %d: %w", p.ID, ErrNoPerson)
	}
	t.s.persons[p.ID] = p
	return nil
}

func (t *memTx) AddIdentity(ctx context.Context, id PersonID, ident Identity, now time.Time) error {
	for i, r := range t.s.identities {
		if r.ident.Kind == ident.Kind && r.ident.Value == ident.Value && r.ident.ValidTo == nil {
			if r.person != id {
				return fmt.Errorf("identity %s is linked to person %d: %w", ident.Value, r.person, ErrConflict)
			}
			t.s.identities[i].lastSeen = now
			return nil
		}
	}
	ident.ValidTo = nil
	t.s.identities = append(t.s.identities, memIdentity{person: id, ident: ident, firstSeen: now, lastSeen: now})
	return nil
}

func (t *memTx) CloseIdentity(ctx context.Context, kind Kind, value wa.JID, at time.Time) error {
	for i, r := range t.s.identities {
		if r.ident.Kind == kind && r.ident.Value == value && r.ident.ValidTo == nil {
			closed := at
			t.s.identities[i].ident.ValidTo = &closed
		}
	}
	return nil
}

func (t *memTx) Merge(ctx context.Context, from, into PersonID, reason string, at time.Time) error {
	if _, ok := t.s.persons[from]; !ok {
		return fmt.Errorf("merge from person %d: %w", from, ErrNoPerson)
	}
	if _, ok := t.s.persons[into]; !ok {
		return fmt.Errorf("merge into person %d: %w", into, ErrNoPerson)
	}
	for i := range t.s.identities {
		if t.s.identities[i].person == from {
			t.s.identities[i].person = into
		}
	}
	for k, m := range t.s.members {
		if k.person != from {
			continue
		}
		delete(t.s.members, k)
		dst := memKey{k.group, into}
		if existing, ok := t.s.members[dst]; ok {
			m = mergeMember(existing, m)
		}
		m.Person = into
		t.s.members[dst] = m
	}
	delete(t.s.persons, from)
	t.s.mergedInto[from] = into
	t.s.merges = append(t.s.merges, memMerge{from: from, into: into, reason: reason, at: at})
	return nil
}

func (t *memTx) UpsertMember(ctx context.Context, m Member) error {
	if _, ok := t.s.persons[m.Person]; !ok {
		return fmt.Errorf("member person %d: %w", m.Person, ErrNoPerson)
	}
	t.s.members[memKey{m.Group, m.Person}] = m
	return nil
}

func (t *memTx) DeleteMember(ctx context.Context, group wa.JID, id PersonID) error {
	delete(t.s.members, memKey{group, id})
	return nil
}
