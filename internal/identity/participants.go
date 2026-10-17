package identity

import (
	"context"
	"fmt"
	"maps"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/cache"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// AdminMaxAge is how old a group's participant list may be before an admin check refreshes it
// from WhatsApp (DESIGN.md §13.3: every write re-checks admin status, with a forced refresh when
// the cache is older than 10 minutes).
const AdminMaxAge = 10 * time.Minute

// GroupInfoFunc fetches a group's metadata and participants, through the WAClient of the bot that
// is in the group.
type GroupInfoFunc func(ctx context.Context, group wa.JID) (wa.GroupInfo, error)

// Participants caches each group's participant roles, answering "is X an admin of G" by LID or
// phone number (DESIGN.md §11). Group events keep it current in place; an entry older than
// AdminMaxAge is refetched before it's trusted for an admin decision.
type Participants struct {
	c      *cache.Cache[wa.JID, *roster]
	fetch  GroupInfoFunc
	maxAge time.Duration
}

// roster is an immutable participant snapshot; updates copy it.
type roster struct {
	roles map[wa.JID]Role   // every known JID form (LID and PN) → role
	alias map[wa.JID]wa.JID // each JID form → the other form of the same participant, if known
}

// NewParticipants builds the cache. maxSize bounds the number of groups held; maxAge ≤ 0 means
// AdminMaxAge.
func NewParticipants(fetch GroupInfoFunc, clk clock.Clock, maxSize int, maxAge time.Duration) (*Participants, error) {
	if maxAge <= 0 {
		maxAge = AdminMaxAge
	}
	// The TTL only reclaims memory for groups nobody asks about; freshness is maxAge.
	c, err := cache.New[wa.JID, *roster](cache.Options{MaxSize: maxSize, TTL: 6 * time.Hour, Clock: clk})
	if err != nil {
		return nil, fmt.Errorf("participant cache: %w", err)
	}
	return &Participants{c: c, fetch: fetch, maxAge: maxAge}, nil
}

// Close releases the cache.
func (p *Participants) Close() { p.c.Close() }

// Role returns a participant's role in a group, refreshing the list first if it is missing or
// older than the max age. found is false for someone who isn't a participant.
func (p *Participants) Role(ctx context.Context, group, jid wa.JID) (role Role, found bool, err error) {
	key, _, ok := normalize(jid)
	if !ok {
		return "", false, nil
	}
	r, age, cached := p.c.GetWithAge(group)
	if !cached || age >= p.maxAge {
		if r, err = p.refresh(ctx, group); err != nil {
			return "", false, err
		}
	}
	role, found = r.roles[key]
	return role, found, nil
}

// IsAdmin reports whether jid is an admin or superadmin of group (fresh to within the max age).
func (p *Participants) IsAdmin(ctx context.Context, group, jid wa.JID) (bool, error) {
	role, found, err := p.Role(ctx, group, jid)
	return found && role.IsAdmin(), err
}

// CachedRole answers from the cache only, whatever its age; ok is false if the group isn't cached.
// Message routing uses it: group events keep the cache current between the 6-hourly refreshes
// (DESIGN.md §11), and the 10-minute forced refresh is for permission checks on writes (§13.3),
// which use Role or IsAdmin.
func (p *Participants) CachedRole(group, jid wa.JID) (role Role, found, ok bool) {
	r, cached := p.c.Get(group)
	if !cached {
		return "", false, false
	}
	key, _, valid := normalize(jid)
	if !valid {
		return "", false, true
	}
	role, found = r.roles[key]
	return role, found, true
}

// IsGroupAdmin is the routing check: it answers from the cache whatever its age (events keep it
// current) and fetches only a group that isn't cached.
func (p *Participants) IsGroupAdmin(ctx context.Context, group, jid wa.JID) (bool, error) {
	if role, found, ok := p.CachedRole(group, jid); ok {
		return found && role.IsAdmin(), nil
	}
	r, err := p.refresh(ctx, group)
	if err != nil {
		return false, err
	}
	key, _, valid := normalize(jid)
	return valid && r.roles[key].IsAdmin(), nil
}

// Refresh fetches a group's participants from WhatsApp and caches them.
func (p *Participants) Refresh(ctx context.Context, group wa.JID) error {
	_, err := p.refresh(ctx, group)
	return err
}

func (p *Participants) refresh(ctx context.Context, group wa.JID) (*roster, error) {
	if p.fetch == nil {
		return nil, fmt.Errorf("refresh participants of %s: no fetcher", group)
	}
	p.c.Invalidate(group)
	r, err := p.c.Load(ctx, group, func(ctx context.Context, group wa.JID) (*roster, error) {
		info, err := p.fetch(ctx, group)
		if err != nil {
			return nil, err
		}
		return rosterOf(info), nil
	})
	if err != nil {
		return nil, fmt.Errorf("refresh participants of %s: %w", group, err)
	}
	return r, nil
}

// Set replaces a group's snapshot with full group info (on join or a periodic refresh).
func (p *Participants) Set(info wa.GroupInfo) { p.c.Set(info.JID, rosterOf(info)) }

// Invalidate drops a group, so the next admin check refetches it.
func (p *Participants) Invalidate(group wa.JID) { p.c.Invalidate(group) }

// Apply updates a cached group in place from a participant change; groups not cached are left
// alone (the next check fetches them).
func (p *Participants) Apply(evt *wa.GroupEvent) {
	if len(evt.Join)+len(evt.Leave)+len(evt.Promote)+len(evt.Demote) == 0 && !evt.Deleted {
		return
	}
	if evt.Deleted {
		p.c.Invalidate(evt.Group)
		return
	}
	p.c.Update(evt.Group, func(old *roster, found bool) (*roster, bool) {
		if !found {
			return nil, false
		}
		r := &roster{roles: maps.Clone(old.roles), alias: maps.Clone(old.alias)}
		for _, pair := range evt.Pairs {
			r.link(pair.LID, pair.PN)
		}
		for _, j := range evt.Join {
			r.set(j, RoleMember)
		}
		for _, j := range evt.Promote {
			r.set(j, RoleAdmin)
		}
		for _, j := range evt.Demote {
			r.set(j, RoleMember)
		}
		for _, j := range evt.Leave {
			r.remove(j)
		}
		return r, true
	})
}

func rosterOf(info wa.GroupInfo) *roster {
	r := &roster{roles: map[wa.JID]Role{}, alias: map[wa.JID]wa.JID{}}
	for _, pt := range info.Participants {
		role := RoleOf(pt.IsAdmin, pt.IsSuperAdmin)
		lid, pn := participantIDs(pt)
		r.link(lid, pn)
		for _, j := range []wa.JID{pt.JID, lid, pn} {
			if k, _, ok := normalize(j); ok {
				r.roles[k] = role
			}
		}
	}
	return r
}

// participantIDs returns a participant's LID and PN, as far as they are known.
func participantIDs(pt wa.Participant) (lid, pn wa.JID) {
	for _, j := range []wa.JID{pt.JID, pt.LID, pt.PhoneNumber} {
		k, kind, ok := normalize(j)
		switch {
		case !ok:
		case kind == KindLID && lid == "":
			lid = k
		case kind == KindPN && pn == "":
			pn = k
		}
	}
	return lid, pn
}

func (r *roster) link(a, b wa.JID) {
	ka, _, okA := normalize(a)
	kb, _, okB := normalize(b)
	if okA && okB && ka != kb {
		r.alias[ka], r.alias[kb] = kb, ka
	}
}

func (r *roster) set(j wa.JID, role Role) {
	k, _, ok := normalize(j)
	if !ok {
		return
	}
	if old, isOwner := r.roles[k]; isOwner && old == RoleSuperAdmin && role == RoleAdmin {
		role = RoleSuperAdmin // a promote event never downgrades the owner
	}
	r.roles[k] = role
	if other, ok := r.alias[k]; ok {
		r.roles[other] = role
	}
}

func (r *roster) remove(j wa.JID) {
	k, _, ok := normalize(j)
	if !ok {
		return
	}
	delete(r.roles, k)
	if other, ok := r.alias[k]; ok {
		delete(r.roles, other)
	}
}
