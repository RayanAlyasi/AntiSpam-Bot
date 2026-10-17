package identity

// Stub of the public surface in .agent/contracts/F-020.md. Replace with the implementation.

import (
	"context"
	"errors"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// ErrNotPerson is returned for JIDs that can't identify a person (groups, channels, broadcast, empty).
var ErrNotPerson = errors.New("identity: not a person JID")

// PersonID is the internal persons.id.
type PersonID int64

// Person is a person's current identifiers; LID or PN is empty when unknown.
type Person struct {
	ID  PersonID
	LID wa.JID
	PN  wa.JID
}

// Source is where an identity was learned (identities.source).
type Source string

// Identity sources (DESIGN.md §14).
const (
	SourceSenderAlt   Source = "sender_alt"
	SourceParticipant Source = "participant"
	SourceEvent       Source = "event"
	SourceLIDStore    Source = "lid_store"
	SourceDM          Source = "dm"
	SourceSelfVCard   Source = "self_vcard"
	SourceUsync       Source = "usync"
)

// Kind is the kind of an identity row.
type Kind string

// Identity kinds.
const (
	KindLID Kind = "lid"
	KindPN  Kind = "pn"
)

// Identity is one identities row of a person.
type Identity struct {
	Kind        Kind
	Value       wa.JID
	Source      Source
	CountryCode string
	ValidTo     *time.Time
}

// LIDStore resolves LID<->PN pairs; both return "" when unknown.
type LIDStore interface {
	LIDForPN(ctx context.Context, pn wa.JID) (wa.JID, error)
	PNForLID(ctx context.Context, lid wa.JID) (wa.JID, error)
}

// ResolvedEvent is identity.resolved: a PN became linked to a LID person.
type ResolvedEvent struct {
	Person     Person
	PN         wa.JID
	MergedFrom PersonID
	Source     Source
}

// RecycledEvent flags a known PN seen with a different LID.
type RecycledEvent struct {
	PN        wa.JID
	OldLID    wa.JID
	OldPerson PersonID
	NewLID    wa.JID
	NewPerson PersonID
}

// Events receives identity events.
type Events interface {
	Resolved(ctx context.Context, ev ResolvedEvent) error
	RecycledNumber(ctx context.Context, ev RecycledEvent) error
}

// Store persists persons, identities, merges and memberships.
type Store interface{}

// NewMemoryStore returns an in-memory Store.
func NewMemoryStore() Store { panic("not implemented") }

// Service maps JIDs to persons.
type Service struct{}

// New returns a Service.
func New(store Store, lids LIDStore, events Events, clk clock.Clock) *Service {
	panic("not implemented")
}

// MatchPerson looks a JID up by LID or PN without creating anything.
func (s *Service) MatchPerson(ctx context.Context, jid wa.JID) (Person, bool, error) {
	panic("not implemented")
}

// Ensure finds or creates the person for jid.
func (s *Service) Ensure(ctx context.Context, jid wa.JID, src Source) (Person, error) {
	panic("not implemented")
}

// Learn records a LID<->PN pair from a free source.
func (s *Service) Learn(ctx context.Context, lid, pn wa.JID, src Source) (Person, error) {
	panic("not implemented")
}

// Identities returns a person's identity rows, open and closed.
func (s *Service) Identities(ctx context.Context, id PersonID) ([]Identity, error) {
	panic("not implemented")
}

// AddMember records that a person is a member of a group.
func (s *Service) AddMember(ctx context.Context, group wa.JID, id PersonID) error {
	panic("not implemented")
}

// GroupsOf returns the groups a person is a member of.
func (s *Service) GroupsOf(ctx context.Context, id PersonID) ([]wa.JID, error) {
	panic("not implemented")
}
