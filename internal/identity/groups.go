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

// GroupStatus is a group's lifecycle state (groups.status).
type GroupStatus string

// Group statuses.
const (
	// GroupPendingAdmin: the bot is in the group but not an admin, so it can't protect it yet.
	GroupPendingAdmin GroupStatus = "pending_admin"
	// GroupActive: the bot is an admin; the group is moderated.
	GroupActive GroupStatus = "active"
	// GroupLeft: the bot left or was removed, or the group was deleted.
	GroupLeft GroupStatus = "left"
)

// GroupKind tells an ordinary group from a community's own entry and its announcement group
// (groups.kind, DESIGN.md §12.4).
type GroupKind string

// Group kinds.
const (
	// KindGroup is a group members chat in, standalone or in a community.
	KindGroup GroupKind = "group"
	// KindCommunity is a community itself: it holds its groups but no chat.
	KindCommunity GroupKind = "community"
	// KindAnnouncement is a community's announcement group, where only admins post.
	KindAnnouncement GroupKind = "announcement"
)

// KindOf returns the kind of group a GroupInfo describes.
func KindOf(info wa.GroupInfo) GroupKind {
	switch {
	case info.IsCommunity:
		return KindCommunity
	case info.IsAnnouncement:
		return KindAnnouncement
	}
	return KindGroup
}

// Group is the bot's view of one group (DESIGN.md §14 groups).
type Group struct {
	JID            wa.JID
	Bot            wa.JID // the bot number that is in this group
	Name           string
	Topic          string
	Size           int
	AddressingMode string // "pn" or "lid"
	BotIsAdmin     bool
	Announce       bool
	Locked         bool
	JoinApproval   bool
	JoinedAt       time.Time
	Status         GroupStatus
	// LastInfoRefresh is when the full group info was last fetched (join or periodic refresh).
	LastInfoRefresh time.Time
	// Kind is empty for a row written before communities were tracked; it means KindGroup.
	Kind GroupKind
	// Community is the community the group belongs to, if any, and CommunityName its name.
	Community     wa.JID
	CommunityName string
}

// IsChat reports whether members chat in the group: it isn't a community's own entry or its
// announcement group.
func (g Group) IsChat() bool { return g.Kind == "" || g.Kind == KindGroup }

// statusFor derives the status from admin rights.
func statusFor(botIsAdmin bool) GroupStatus {
	if botIsAdmin {
		return GroupActive
	}
	return GroupPendingAdmin
}

// GroupStore persists groups rows.
type GroupStore interface {
	Group(ctx context.Context, jid wa.JID) (Group, bool, error)
	UpsertGroup(ctx context.Context, g Group) error
	// Groups returns the groups with one of the given statuses (all groups when none is given),
	// ordered by JID.
	Groups(ctx context.Context, statuses ...GroupStatus) ([]Group, error)
	// StaleGroups returns groups that aren't left and were last refreshed at or before the given time.
	StaleGroups(ctx context.Context, refreshedBy time.Time) ([]Group, error)

	// CommunityGroups returns a community's own list of its groups, ordered by JID, including
	// the ones the bot isn't in.
	CommunityGroups(ctx context.Context, community wa.JID) ([]wa.SubGroup, error)
	// SetCommunityGroups replaces a community's list.
	SetCommunityGroups(ctx context.Context, community wa.JID, groups []wa.SubGroup) error
	// LinkCommunityGroup adds one group to a community's list (an empty name keeps the known one);
	// UnlinkCommunityGroup removes it.
	LinkCommunityGroup(ctx context.Context, community wa.JID, g wa.SubGroup) error
	UnlinkCommunityGroup(ctx context.Context, community, group wa.JID) error
}

// NewMemoryGroupStore returns an in-memory GroupStore, for tests and tools.
func NewMemoryGroupStore() GroupStore {
	return &memGroups{m: map[wa.JID]Group{}, subs: map[wa.JID]map[wa.JID]wa.SubGroup{}}
}

type memGroups struct {
	mu   sync.RWMutex
	m    map[wa.JID]Group
	subs map[wa.JID]map[wa.JID]wa.SubGroup // community → its groups
}

func (s *memGroups) Group(_ context.Context, jid wa.JID) (Group, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	g, ok := s.m[jid]
	return g, ok, nil
}

func (s *memGroups) UpsertGroup(_ context.Context, g Group) error {
	if !g.JID.IsGroup() {
		return fmt.Errorf("upsert group %q: not a group JID", g.JID)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[g.JID] = g
	return nil
}

func (s *memGroups) Groups(_ context.Context, statuses ...GroupStatus) ([]Group, error) {
	return s.filter(func(g Group) bool { return len(statuses) == 0 || slices.Contains(statuses, g.Status) }), nil
}

func (s *memGroups) StaleGroups(_ context.Context, by time.Time) ([]Group, error) {
	return s.filter(func(g Group) bool { return g.Status != GroupLeft && !g.LastInfoRefresh.After(by) }), nil
}

func (s *memGroups) filter(keep func(Group) bool) []Group {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Group
	for _, g := range s.m {
		if keep(g) {
			out = append(out, g)
		}
	}
	slices.SortFunc(out, func(a, b Group) int { return strings.Compare(string(a.JID), string(b.JID)) })
	return out
}

func (s *memGroups) CommunityGroups(_ context.Context, community wa.JID) ([]wa.SubGroup, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := slices.Collect(maps.Values(s.subs[community]))
	slices.SortFunc(out, func(a, b wa.SubGroup) int { return strings.Compare(string(a.JID), string(b.JID)) })
	return out, nil
}

func (s *memGroups) SetCommunityGroups(_ context.Context, community wa.JID, groups []wa.SubGroup) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := make(map[wa.JID]wa.SubGroup, len(groups))
	for _, g := range groups {
		m[g.JID] = g
	}
	s.subs[community] = m
	return nil
}

func (s *memGroups) LinkCommunityGroup(_ context.Context, community wa.JID, g wa.SubGroup) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.subs[community] == nil {
		s.subs[community] = map[wa.JID]wa.SubGroup{}
	}
	if g.Name == "" {
		g.Name = s.subs[community][g.JID].Name
	}
	s.subs[community][g.JID] = g
	return nil
}

func (s *memGroups) UnlinkCommunityGroup(_ context.Context, community, group wa.JID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.subs[community], group)
	return nil
}
