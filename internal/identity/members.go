package identity

import (
	"context"
	"fmt"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// Role is a member's role in a group.
type Role string

// Roles (members.role).
const (
	RoleMember     Role = "member"
	RoleAdmin      Role = "admin"
	RoleSuperAdmin Role = "superadmin"
)

func (r Role) rank() int {
	switch r {
	case RoleSuperAdmin:
		return 2
	case RoleAdmin:
		return 1
	}
	return 0
}

// IsAdmin reports whether the role is admin or superadmin.
func (r Role) IsAdmin() bool { return r.rank() > 0 }

// RoleOf maps a participant's flags to a Role.
func RoleOf(isAdmin, isSuperAdmin bool) Role {
	switch {
	case isSuperAdmin:
		return RoleSuperAdmin
	case isAdmin:
		return RoleAdmin
	}
	return RoleMember
}

// Member is one person's membership of one group (DESIGN.md §8.1, §14 members).
type Member struct {
	Group  wa.JID
	Person PersonID
	Role   Role
	// JoinedAt is when the join event was seen; zero for members who were already in the group
	// when the bot arrived, or whose join the bot never saw.
	JoinedAt time.Time
	// FirstSeen is when the bot first knew of this membership. Tenure counts from JoinedAt, or
	// from FirstSeen for pre-existing members (§8.1).
	FirstSeen  time.Time
	MsgCount   int
	CleanCount int
	// PreExisting is set for members present when the bot joined: they start established, so
	// the bot never punishes history (§8.1).
	PreExisting bool
	// Established routes the ban decision only; it never changes detection (§8.1).
	Established bool
	// Allowlisted members skip moderation (§5.1); admins set it by command or on the website.
	Allowlisted bool
}

// mergeMember combines two memberships of the same group when two person records turn out to be
// one person: the longest tenure and the highest role win, and counts add up.
func mergeMember(a, b Member) Member {
	out := a
	if b.Role.rank() > out.Role.rank() {
		out.Role = b.Role
	}
	if out.FirstSeen.IsZero() || (!b.FirstSeen.IsZero() && b.FirstSeen.Before(out.FirstSeen)) {
		out.FirstSeen = b.FirstSeen
	}
	if out.JoinedAt.IsZero() || (!b.JoinedAt.IsZero() && b.JoinedAt.Before(out.JoinedAt)) {
		out.JoinedAt = b.JoinedAt
	}
	out.MsgCount += b.MsgCount
	out.CleanCount += b.CleanCount
	out.PreExisting = out.PreExisting || b.PreExisting
	out.Established = out.Established || b.Established
	out.Allowlisted = out.Allowlisted || b.Allowlisted
	return out
}

// AddMember records that a person is a member of a group, first seen now. It's idempotent: an
// existing membership is left as it is.
func (s *Service) AddMember(ctx context.Context, group wa.JID, id PersonID) error {
	return s.updateMember(ctx, group, id, func(m *Member, existed bool) {})
}

// GroupsOf returns the groups a person is a member of.
func (s *Service) GroupsOf(ctx context.Context, id PersonID) ([]wa.JID, error) {
	ms, err := s.store.Members(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("groups of person %d: %w", id, err)
	}
	groups := make([]wa.JID, 0, len(ms))
	for _, m := range ms {
		groups = append(groups, m.Group)
	}
	return groups, nil
}

// Member returns one membership.
func (s *Service) Member(ctx context.Context, group wa.JID, id PersonID) (Member, bool, error) {
	m, ok, err := s.store.Member(ctx, group, id)
	if err != nil {
		return Member{}, false, fmt.Errorf("member %d of %s: %w", id, group, err)
	}
	return m, ok, nil
}

// GroupMembers returns every known member of a group.
func (s *Service) GroupMembers(ctx context.Context, group wa.JID) ([]Member, error) {
	ms, err := s.store.GroupMembers(ctx, group)
	if err != nil {
		return nil, fmt.Errorf("members of %s: %w", group, err)
	}
	return ms, nil
}

// MemberJoined records a join seen as it happened. A rejoin restarts tenure: the bot treats the
// person as new again (DESIGN.md §8.2), but keeps the first-seen time.
func (s *Service) MemberJoined(ctx context.Context, group wa.JID, id PersonID, at time.Time) error {
	return s.updateMember(ctx, group, id, func(m *Member, existed bool) {
		m.JoinedAt = at
		if !existed {
			m.FirstSeen = at
		}
		m.PreExisting, m.Established = false, false
	})
}

// MemberLeft removes a membership (left, removed or kicked).
func (s *Service) MemberLeft(ctx context.Context, group wa.JID, id PersonID) error {
	err := s.store.Write(ctx, func(tx Tx) error { return tx.DeleteMember(ctx, group, id) })
	if err != nil {
		return fmt.Errorf("member %d left %s: %w", id, group, err)
	}
	return nil
}

// SetRole records a promotion or demotion.
func (s *Service) SetRole(ctx context.Context, group wa.JID, id PersonID, role Role) error {
	return s.updateMember(ctx, group, id, func(m *Member, existed bool) { m.Role = role })
}

// SeedMember records a member who was already in the group when the bot joined, or whom a group
// refresh found without a join event. With preExisting set (the bot's own join), the member is
// marked pre-existing and established (§8.1). Existing memberships keep their history; only the
// role is refreshed.
func (s *Service) SeedMember(ctx context.Context, group wa.JID, id PersonID, role Role, preExisting bool) error {
	return s.updateMember(ctx, group, id, func(m *Member, existed bool) {
		m.Role = role
		if !existed && preExisting {
			m.PreExisting, m.Established = true, true
		}
	})
}

// SetAllowlisted adds a member to, or removes them from, the group's allow-list.
func (s *Service) SetAllowlisted(ctx context.Context, group wa.JID, id PersonID, on bool) error {
	return s.updateMember(ctx, group, id, func(m *Member, existed bool) { m.Allowlisted = on })
}

// IsAllowListed reports whether jid (LID or phone number) is an allow-listed member of group.
func (s *Service) IsAllowListed(ctx context.Context, group, jid wa.JID) (bool, error) {
	p, ok, err := s.MatchPerson(ctx, jid)
	if err != nil || !ok {
		return false, err
	}
	m, ok, err := s.Member(ctx, group, p.ID)
	return ok && m.Allowlisted, err
}

// CountMessage counts one message from a member; clean says the pipeline found nothing wrong.
// A member the bot didn't know yet is recorded, first seen now.
func (s *Service) CountMessage(ctx context.Context, group wa.JID, id PersonID, clean bool) error {
	return s.updateMember(ctx, group, id, func(m *Member, existed bool) {
		m.MsgCount++
		if clean {
			m.CleanCount++
		}
	})
}

func (s *Service) updateMember(ctx context.Context, group wa.JID, id PersonID, change func(m *Member, existed bool)) error {
	if !group.IsGroup() {
		return fmt.Errorf("member of %q: not a group JID", group)
	}
	now := s.clk.Now()
	err := s.store.Write(ctx, func(tx Tx) error {
		m, existed, err := tx.Member(ctx, group, id)
		if err != nil {
			return err
		}
		if !existed {
			m = Member{Group: group, Person: id, Role: RoleMember, FirstSeen: now}
		}
		change(&m, existed)
		return tx.UpsertMember(ctx, m)
	})
	if err != nil {
		return fmt.Errorf("update member %d of %s: %w", id, group, err)
	}
	return nil
}
