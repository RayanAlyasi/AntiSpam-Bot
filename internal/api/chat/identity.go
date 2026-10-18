package chat

import (
	"context"
	"fmt"
	"strings"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/identity"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// IdentityAdapter implements Identity and Members over the identity service (F-020, F-023).
type IdentityAdapter struct {
	Service *identity.Service
}

var (
	_ Identity = IdentityAdapter{}
	_ Members  = IdentityAdapter{}
)

// Canonical returns the person's LID when known (otherwise their phone JID), so one person has
// one DM thread whichever JID they write from. Unknown JIDs come back unchanged.
func (a IdentityAdapter) Canonical(ctx context.Context, jid wa.JID) (wa.JID, error) {
	p, ok, err := a.Service.MatchPerson(ctx, jid)
	if err != nil {
		return "", fmt.Errorf("identity of %s: %w", jid, err)
	}
	switch {
	case !ok:
		return jid, nil
	case p.LID != "":
		return p.LID, nil
	case p.PN != "":
		return p.PN, nil
	default:
		return jid, nil
	}
}

// ByPhone finds the person with an E.164 number, by LID when known.
func (a IdentityAdapter) ByPhone(ctx context.Context, e164 string) (wa.JID, bool, error) {
	pn := wa.JID(strings.TrimPrefix(e164, "+") + "@" + wa.ServerUser)
	p, ok, err := a.Service.MatchPerson(ctx, pn)
	if err != nil || !ok {
		return "", false, err
	}
	if p.LID != "" {
		return p.LID, true, nil
	}
	return pn, true, nil
}

func (a IdentityAdapter) setAllowlisted(ctx context.Context, group, person wa.JID, on bool) error {
	p, err := a.Service.Ensure(ctx, person, identity.SourceEvent)
	if err != nil {
		return fmt.Errorf("person for %s: %w", person, err)
	}
	if err := a.Service.SetAllowlisted(ctx, group, p.ID, on); err != nil {
		return fmt.Errorf("allow-list %s in %s: %w", person, group, err)
	}
	return nil
}

// Allow adds a member to the group's allow-list.
func (a IdentityAdapter) Allow(ctx context.Context, group, person, _ wa.JID) error {
	return a.setAllowlisted(ctx, group, person, true)
}

// Unallow removes a member from the group's allow-list.
func (a IdentityAdapter) Unallow(ctx context.Context, group, person, _ wa.JID) error {
	return a.setAllowlisted(ctx, group, person, false)
}
