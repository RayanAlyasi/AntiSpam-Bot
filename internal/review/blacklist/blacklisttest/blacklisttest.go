// Package blacklisttest holds in-memory fakes of the blacklist package's ports. Every fake is
// safe for concurrent use.
package blacklisttest

import (
	"context"
	"slices"
	"sync"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review/blacklist"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// Groups serves protected groups and their members.
type Groups struct {
	mu      sync.Mutex
	members map[wa.JID][]blacklist.Member
	order   []wa.JID
}

var _ blacklist.Groups = (*Groups)(nil)

// SetMembers makes group protected with these members.
func (g *Groups) SetMembers(group wa.JID, members ...blacklist.Member) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.members == nil {
		g.members = map[wa.JID][]blacklist.Member{}
	}
	if _, ok := g.members[group]; !ok {
		g.order = append(g.order, group)
	}
	g.members[group] = slices.Clone(members)
}

// Protected lists the protected groups in the order they were added.
func (g *Groups) Protected(context.Context) ([]wa.JID, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.order), nil
}

// Members lists a group's members.
func (g *Groups) Members(_ context.Context, group wa.JID) ([]blacklist.Member, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.members[group]), nil
}

// Settings serves per-group blacklist settings; groups without their own get Default.
type Settings struct {
	mu      sync.Mutex
	groups  map[wa.JID]blacklist.GroupSettings
	Default blacklist.GroupSettings
}

var _ blacklist.Settings = (*Settings)(nil)

// Set stores a group's settings.
func (s *Settings) Set(group wa.JID, gs blacklist.GroupSettings) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.groups == nil {
		s.groups = map[wa.JID]blacklist.GroupSettings{}
	}
	s.groups[group] = gs
}

// Blacklist returns a group's settings.
func (s *Settings) Blacklist(_ context.Context, group wa.JID) (blacklist.GroupSettings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if gs, ok := s.groups[group]; ok {
		return gs, nil
	}
	return s.Default, nil
}

// Notice is one recorded notification.
type Notice struct {
	To    wa.JID
	Group wa.JID
	Key   string
	Vars  map[string]string
}

// Notifier records notifications.
type Notifier struct {
	mu      sync.Mutex
	notices []Notice
}

var _ blacklist.Notifier = (*Notifier)(nil)

// Notify records the notice.
func (n *Notifier) Notify(_ context.Context, to, group wa.JID, key string, vars map[string]string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.notices = append(n.notices, Notice{To: to, Group: group, Key: key, Vars: vars})
	return nil
}

// All returns the recorded notices.
func (n *Notifier) All() []Notice {
	n.mu.Lock()
	defer n.mu.Unlock()
	return slices.Clone(n.notices)
}

// Budget counts reads and can refuse them.
type Budget struct {
	mu    sync.Mutex
	waits int
	// Err, when set, is returned by Wait (the budget is exhausted or ctx ended).
	Err error
}

var _ blacklist.ReadBudget = (*Budget)(nil)

// Wait records one read.
func (b *Budget) Wait(ctx context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.Err != nil {
		return b.Err
	}
	b.waits++
	return ctx.Err()
}

// Waits returns how many reads were allowed.
func (b *Budget) Waits() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.waits
}

// Link is one recorded identity link.
type Link struct{ Phone, LID wa.JID }

// Linker records identity links.
type Linker struct {
	mu    sync.Mutex
	links []Link
}

var _ blacklist.Linker = (*Linker)(nil)

// Link records the link.
func (l *Linker) Link(_ context.Context, phone, lid wa.JID) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.links = append(l.links, Link{Phone: phone, LID: lid})
	return nil
}

// All returns the recorded links.
func (l *Linker) All() []Link {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.links)
}

// Reviews records opened review items and returns them with sequential IDs and codes.
type Reviews struct {
	mu    sync.Mutex
	items []review.NewItem
}

var _ blacklist.Reviews = (*Reviews)(nil)

// Open records n.
func (r *Reviews) Open(_ context.Context, n review.NewItem) (review.Item, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items = append(r.items, n)
	id := int64(len(r.items))
	return review.Item{NewItem: n, ID: id, Code: "RV" + string(rune('A'+id%26)) + "2", Status: review.StatusPending}, nil
}

// All returns the opened items.
func (r *Reviews) All() []review.NewItem {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.items)
}
