package identity

import (
	"context"
	"fmt"
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
}

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
}

// NewMemoryGroupStore returns an in-memory GroupStore, for tests and tools.
func NewMemoryGroupStore() GroupStore { return &memGroups{m: map[wa.JID]Group{}} }

type memGroups struct {
	mu sync.RWMutex
	m  map[wa.JID]Group
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
