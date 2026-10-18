package queue

import (
	"context"
	"fmt"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/identity"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// IdentityPersons is Persons over the identity service (F-020): the person for a JID, created
// if the bot hasn't met them, so a ban can always be recorded.
type IdentityPersons struct {
	Service *identity.Service
}

var _ Persons = IdentityPersons{}

// PersonID returns the person behind jid.
func (p IdentityPersons) PersonID(ctx context.Context, jid wa.JID) (int64, error) {
	person, err := p.Service.Ensure(ctx, jid, identity.SourceEvent)
	if err != nil {
		return 0, fmt.Errorf("person for %s: %w", jid, err)
	}
	return int64(person.ID), nil
}

// JID returns the person's LID, or their phone JID when no LID is known.
func (p IdentityPersons) JID(ctx context.Context, id int64) (wa.JID, error) {
	person, err := p.Service.Person(ctx, identity.PersonID(id))
	if err != nil {
		return "", fmt.Errorf("person %d: %w", id, err)
	}
	if person.LID != "" {
		return person.LID, nil
	}
	return person.PN, nil
}

// MatchPersonID finds the person behind jid without creating one (pgstore.PersonMatcher).
func (p IdentityPersons) MatchPersonID(ctx context.Context, jid wa.JID) (int64, bool, error) {
	person, ok, err := p.Service.MatchPerson(ctx, jid)
	if err != nil || !ok {
		return 0, false, err
	}
	return int64(person.ID), true, nil
}

// IdentityLinker links a blacklisted number to the LID GetUserInfo returned (§7.7, F-125); it
// implements blacklist.Linker.
type IdentityLinker struct {
	Service *identity.Service
}

// Link records the pair, learned through a usync lookup.
func (l IdentityLinker) Link(ctx context.Context, phone, lid wa.JID) error {
	if _, err := l.Service.Learn(ctx, lid, phone, identity.SourceUsync); err != nil {
		return fmt.Errorf("link %s to its LID: %w", phone, err)
	}
	return nil
}
