package identity

import (
	"context"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// Store persists persons, identities, merges and memberships (DESIGN.md §14). Reads may run
// concurrently with writes; Write runs fn as one atomic, serialized unit: either everything it
// did is kept or nothing is, and no two Write calls interleave (across processes too, for the
// Postgres store). NewMemoryStore is the in-process implementation; the bot uses a Postgres one.
type Store interface {
	Reader
	Write(ctx context.Context, fn func(tx Tx) error) error
}

// Reader is the read side of a Store.
type Reader interface {
	// FindOpen returns the person whose open (valid_to IS NULL) identity has this kind and value.
	FindOpen(ctx context.Context, kind Kind, value wa.JID) (PersonID, bool, error)
	// Person returns a person's current LID and PN.
	Person(ctx context.Context, id PersonID) (Person, error)
	// Identities returns all of a person's identity rows, open and closed, oldest first.
	Identities(ctx context.Context, id PersonID) ([]Identity, error)
	// Member returns one membership.
	Member(ctx context.Context, group wa.JID, id PersonID) (Member, bool, error)
	// Members returns a person's memberships, ordered by group.
	Members(ctx context.Context, id PersonID) ([]Member, error)
	// GroupMembers returns a group's memberships, ordered by person.
	GroupMembers(ctx context.Context, group wa.JID) ([]Member, error)
}

// Tx is what a Write may do. Implementations need not validate arguments; the Service does.
type Tx interface {
	Reader
	// CreatePerson inserts an empty person first seen at now.
	CreatePerson(ctx context.Context, now time.Time) (PersonID, error)
	// SetIdentifiers sets a person's current LID and PN (persons.lid, persons.pn).
	SetIdentifiers(ctx context.Context, p Person) error
	// AddIdentity links an identity to a person, or refreshes last_seen if that exact open link
	// already exists.
	AddIdentity(ctx context.Context, id PersonID, ident Identity, now time.Time) error
	// CloseIdentity ends the open link of kind/value at the given time (valid_to).
	CloseIdentity(ctx context.Context, kind Kind, value wa.JID, at time.Time) error
	// Merge moves everything attached to from (identities, memberships and, in Postgres, every
	// table that references persons) to into, records the merge (person_merges) and retires from.
	Merge(ctx context.Context, from, into PersonID, reason string, at time.Time) error
	// UpsertMember creates or replaces one membership.
	UpsertMember(ctx context.Context, m Member) error
	// DeleteMember removes one membership.
	DeleteMember(ctx context.Context, group wa.JID, id PersonID) error
}
