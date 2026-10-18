package identity

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// GroupRefreshEvery is how often a group's full info is refetched (DESIGN.md §11).
const GroupRefreshEvery = 6 * time.Hour

// BotSelfFunc returns a bot number's own PN and LID JIDs, so the tracker can tell the bot apart
// from the people in its groups.
type BotSelfFunc func(bot wa.JID) (pn, lid wa.JID)

// CommunitySource reads communities through a bot's WAClient: a community's own info (for its
// name) and its list of groups, including the ones the bot isn't in.
type CommunitySource interface {
	GroupInfo(ctx context.Context, bot, group wa.JID) (wa.GroupInfo, error)
	SubGroups(ctx context.Context, bot, community wa.JID) ([]wa.SubGroup, error)
}

// StartFunc is called when the bot joins a community's group or becomes an admin there, after
// the group is stored (DESIGN.md §12.4: a new community group starts from the community's
// settings).
type StartFunc func(ctx context.Context, g Group) error

// Tracker keeps groups, members and the participant cache current from the connector's group
// events and from full group-info fetches, and records every LID↔PN pair those reveal
// (DESIGN.md §5.1, §7.7, §8.1, §11). It must not run on the receive loop: it writes to the
// database and may look identities up.
type Tracker struct {
	ids    *Service
	groups GroupStore
	parts  *Participants
	self   BotSelfFunc
	clk    clock.Clock
	log    zerolog.Logger

	src     CommunitySource // nil: communities are named only from events and their own rows
	onStart StartFunc       // nil: nothing happens when the bot joins a community group

	mu     sync.Mutex
	listed map[wa.JID]time.Time // community → when its info and group list were last fetched
}

// NewTracker builds a tracker. parts may be nil (no cache to maintain).
func NewTracker(ids *Service, groups GroupStore, parts *Participants, self BotSelfFunc, clk clock.Clock, log zerolog.Logger) *Tracker {
	if clk == nil {
		clk = clock.Real{}
	}
	if self == nil {
		self = func(bot wa.JID) (wa.JID, wa.JID) { return bot, "" }
	}
	return &Tracker{
		ids: ids, groups: groups, parts: parts, self: self, clk: clk, log: log.With().Str("module", "groups").Logger(),
		listed: map[wa.JID]time.Time{},
	}
}

// WithCommunities makes the tracker fetch each community's name and group list through src, at
// most once per GroupRefreshEvery per community, and call onStart (which may be nil) when the
// bot joins a community's group or becomes an admin there. It returns t.
func (t *Tracker) WithCommunities(src CommunitySource, onStart StartFunc) *Tracker {
	t.src, t.onStart = src, onStart
	return t
}

// GroupState reports whether the bot tracks a group (it is in it) and whether the group is
// protected (the bot is an admin there), for message routing.
func (t *Tracker) GroupState(ctx context.Context, group wa.JID) (tracked, moderated bool, err error) {
	g, ok, err := t.groups.Group(ctx, group)
	if err != nil {
		return false, false, fmt.Errorf("group state of %s: %w", group, err)
	}
	if !ok || g.Status == GroupLeft {
		return false, false, nil
	}
	return true, g.Status == GroupActive, nil
}

// HandleEvent applies JoinedGroup and GroupInfo events; other events are ignored.
func (t *Tracker) HandleEvent(ctx context.Context, evt wa.Event) error {
	switch e := evt.(type) {
	case *wa.JoinedGroupEvent:
		return t.joined(ctx, e)
	case *wa.GroupEvent:
		return t.changed(ctx, e)
	}
	return nil
}

func (t *Tracker) isBot(bot, j wa.JID) bool {
	k, _, ok := normalize(j)
	if !ok {
		return false
	}
	pn, lid := t.self(bot)
	for _, b := range []wa.JID{bot, pn, lid} {
		if kb, _, ok := normalize(b); ok && kb == k {
			return true
		}
	}
	return false
}

// joined handles the bot joining (or being added to) a group: everyone already there is
// pre-existing and established (§8.1).
func (t *Tracker) joined(ctx context.Context, e *wa.JoinedGroupEvent) error {
	var errs []error
	if e.Sender != "" && e.SenderPN != "" {
		if _, err := t.learn(ctx, e.Sender, e.SenderPN, SourceEvent); err != nil {
			errs = append(errs, err)
		}
	}
	old, existed, err := t.groups.Group(ctx, e.Info.JID)
	if err != nil {
		return fmt.Errorf("joined %s: %w", e.Info.JID, err)
	}
	now := t.clk.Now()
	// A rejoin after leaving starts fresh history: members seen now are pre-existing again.
	preExisting := !existed || old.Status == GroupLeft
	joinedAt := now
	if existed && old.Status != GroupLeft && !old.JoinedAt.IsZero() {
		joinedAt = old.JoinedAt
	}
	g, err := t.apply(ctx, e.Bot, e.Info, old, joinedAt, preExisting)
	errs = append(errs, err, t.started(ctx, g))
	return errors.Join(errs...)
}

// Refresh applies a full group-info fetch (the 6-hourly refresh, §11): metadata, admin status,
// roles and any membership changes the bot missed. Members found without a join event are not
// treated as pre-existing.
func (t *Tracker) Refresh(ctx context.Context, bot wa.JID, info wa.GroupInfo) error {
	old, existed, err := t.groups.Group(ctx, info.JID)
	if err != nil {
		return fmt.Errorf("refresh %s: %w", info.JID, err)
	}
	joinedAt := t.clk.Now()
	if existed && !old.JoinedAt.IsZero() {
		joinedAt = old.JoinedAt
	}
	g, err := t.apply(ctx, bot, info, old, joinedAt, false)
	if g.Status == GroupActive && old.Status != GroupActive {
		// Made an admin while the bot wasn't listening.
		err = errors.Join(err, t.started(ctx, g))
	}
	return err
}

// RefreshStale refetches every group not refreshed within GroupRefreshEvery and returns how many
// were refreshed. A group the bot can no longer see (ErrNotFound) is marked left.
func (t *Tracker) RefreshStale(ctx context.Context, fetch func(ctx context.Context, bot, group wa.JID) (wa.GroupInfo, error)) (int, error) {
	stale, err := t.groups.StaleGroups(ctx, t.clk.Now().Add(-GroupRefreshEvery))
	if err != nil {
		return 0, fmt.Errorf("list stale groups: %w", err)
	}
	var errs []error
	n := 0
	for _, g := range stale {
		if err := ctx.Err(); err != nil {
			return n, err
		}
		info, err := fetch(ctx, g.Bot, g.JID)
		if errors.Is(err, wa.ErrNotFound) {
			g.Status, g.BotIsAdmin = GroupLeft, false
			if err := t.groups.UpsertGroup(ctx, g); err != nil {
				errs = append(errs, err)
			}
			continue
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("fetch %s: %w", g.JID, err))
			continue
		}
		if err := t.Refresh(ctx, g.Bot, info); err != nil {
			errs = append(errs, err)
			continue
		}
		n++
	}
	return n, errors.Join(errs...)
}

// SyncJoined applies one bot's full group list (GetJoinedGroups, one IQ for all its groups; run
// every GroupRefreshEvery). Known groups are refreshed. A group the bot is in but has no row for
// (it was added while offline) is treated like a join: its members are pre-existing (§8.1). The
// bot's groups missing from the list are marked left.
func (t *Tracker) SyncJoined(ctx context.Context, bot wa.JID, infos []wa.GroupInfo) error {
	var errs []error
	seen := map[wa.JID]bool{}
	for _, info := range infos {
		seen[info.JID] = true
		g, existed, err := t.groups.Group(ctx, info.JID)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if existed && g.Status != GroupLeft {
			errs = append(errs, t.Refresh(ctx, bot, info))
			continue
		}
		errs = append(errs, t.joined(ctx, &wa.JoinedGroupEvent{Bot: bot, Info: info}))
	}
	all, err := t.groups.Groups(ctx, GroupActive, GroupPendingAdmin)
	if err != nil {
		return errors.Join(append(errs, err)...)
	}
	for _, g := range all {
		if g.Bot == bot && !seen[g.JID] {
			g.Status, g.BotIsAdmin = GroupLeft, false
			if err := t.groups.UpsertGroup(ctx, g); err != nil {
				errs = append(errs, err)
			}
			if t.parts != nil {
				t.parts.Invalidate(g.JID)
			}
		}
	}
	return errors.Join(errs...)
}

// apply writes a full snapshot: the group row, one person and membership per participant, and
// the participant cache. Memberships the snapshot no longer contains are removed. old is the
// stored row, if any. It returns the row it wrote.
func (t *Tracker) apply(ctx context.Context, bot wa.JID, info wa.GroupInfo, old Group, joinedAt time.Time, preExisting bool) (Group, error) {
	now := t.clk.Now()
	g := Group{
		JID: info.JID, Bot: bot, Name: info.Name, Topic: info.Topic, Size: info.Size,
		AddressingMode: info.AddressingMode, Announce: info.Announce, Locked: info.Locked,
		JoinApproval: info.JoinApproval, JoinedAt: joinedAt, LastInfoRefresh: now,
		Kind: KindOf(info),
	}
	if g.Kind == KindCommunity {
		t.refreshCommunity(ctx, bot, info.JID)
	} else if info.LinkedParent != "" {
		g.Community = info.LinkedParent
		g.CommunityName = t.communityName(ctx, bot, info.LinkedParent, old)
	}
	if g.Size == 0 {
		g.Size = len(info.Participants)
	}
	var errs []error
	present := map[PersonID]bool{}
	for _, pt := range info.Participants {
		if t.isBot(bot, pt.JID) || t.isBot(bot, pt.LID) || t.isBot(bot, pt.PhoneNumber) {
			g.BotIsAdmin = pt.IsAdmin || pt.IsSuperAdmin
			continue
		}
		lid, pn := participantIDs(pt)
		p, err := t.person(ctx, lid, pn, SourceParticipant)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		present[p.ID] = true
		if err := t.ids.SeedMember(ctx, info.JID, p.ID, RoleOf(pt.IsAdmin, pt.IsSuperAdmin), preExisting); err != nil {
			errs = append(errs, err)
		}
	}
	g.Status = statusFor(g.BotIsAdmin)
	if err := t.groups.UpsertGroup(ctx, g); err != nil {
		errs = append(errs, fmt.Errorf("upsert group %s: %w", g.JID, err))
	}
	if len(errs) == 0 { // only prune against a complete snapshot
		members, err := t.ids.GroupMembers(ctx, info.JID)
		if err != nil {
			errs = append(errs, err)
		}
		for _, m := range members {
			if !present[m.Person] {
				if err := t.ids.MemberLeft(ctx, info.JID, m.Person); err != nil {
					errs = append(errs, err)
				}
			}
		}
	}
	if t.parts != nil {
		t.parts.Set(info)
	}
	return g, errors.Join(errs...)
}

// changed applies a GroupInfo event.
func (t *Tracker) changed(ctx context.Context, e *wa.GroupEvent) error {
	var errs []error
	if e.Sender != "" && e.SenderPN != "" {
		if _, err := t.learn(ctx, e.Sender, e.SenderPN, SourceEvent); err != nil {
			errs = append(errs, err)
		}
	}
	pairs := map[wa.JID]wa.JID{}
	for _, pr := range e.Pairs {
		if _, err := t.learn(ctx, pr.LID, pr.PN, SourceParticipant); err != nil {
			errs = append(errs, err)
		}
		if l, _, ok := normalize(pr.LID); ok {
			pairs[l] = pr.PN
		}
		if p, _, ok := normalize(pr.PN); ok {
			pairs[p] = pr.LID
		}
	}

	g, existed, err := t.groups.Group(ctx, e.Group)
	if err != nil {
		return fmt.Errorf("group event %s: %w", e.Group, err)
	}
	if !existed {
		g = Group{JID: e.Group, Bot: e.Bot, Status: GroupPendingAdmin}
	}
	if e.Name != nil {
		g.Name = *e.Name
	}
	if e.Topic != nil {
		g.Topic = *e.Topic
	}
	if e.Announce != nil {
		g.Announce = *e.Announce
	}
	if e.Locked != nil {
		g.Locked = *e.Locked
	}
	if e.JoinApproval != nil {
		g.JoinApproval = *e.JoinApproval
	}
	wasActive := existed && g.Status == GroupActive
	errs = append(errs, t.linked(ctx, &g, e.Link, false), t.linked(ctx, &g, e.Unlink, true))

	at := e.Timestamp
	if at.IsZero() {
		at = t.clk.Now()
	}
	member := func(j wa.JID, fn func(PersonID) error) {
		lid, pn := j, pairs[mustNorm(j)]
		if _, kind, _ := normalize(j); kind == KindPN {
			lid, pn = pairs[mustNorm(j)], j
		}
		p, err := t.person(ctx, lid, pn, SourceParticipant)
		if err != nil {
			errs = append(errs, err)
			return
		}
		if err := fn(p.ID); err != nil {
			errs = append(errs, err)
		}
	}
	for _, j := range e.Join {
		if t.isBot(e.Bot, j) {
			continue
		}
		g.Size++
		member(j, func(id PersonID) error { return t.ids.MemberJoined(ctx, e.Group, id, at) })
	}
	for _, j := range e.Leave {
		if t.isBot(e.Bot, j) {
			g.Status, g.BotIsAdmin = GroupLeft, false
			continue
		}
		g.Size = max(g.Size-1, 0)
		if p, ok, err := t.ids.MatchPerson(ctx, j); err != nil {
			errs = append(errs, err)
		} else if ok {
			if err := t.ids.MemberLeft(ctx, e.Group, p.ID); err != nil {
				errs = append(errs, err)
			}
		}
	}
	for _, j := range e.Promote {
		if t.isBot(e.Bot, j) {
			g.BotIsAdmin, g.Status = true, GroupActive
			continue
		}
		member(j, func(id PersonID) error { return t.ids.SetRole(ctx, e.Group, id, RoleAdmin) })
	}
	for _, j := range e.Demote {
		if t.isBot(e.Bot, j) {
			g.BotIsAdmin, g.Status = false, GroupPendingAdmin
			continue
		}
		member(j, func(id PersonID) error { return t.ids.SetRole(ctx, e.Group, id, RoleMember) })
	}
	if e.Deleted {
		g.Status, g.BotIsAdmin = GroupLeft, false
	}
	if err := t.groups.UpsertGroup(ctx, g); err != nil {
		errs = append(errs, fmt.Errorf("upsert group %s: %w", g.JID, err))
	} else if g.Status == GroupActive && !wasActive {
		errs = append(errs, t.started(ctx, g))
	}
	if t.parts != nil {
		t.parts.Apply(e)
	}
	return errors.Join(errs...)
}

// linked applies a community link change to g, the event's group, and to the community's group
// list. When the event is about the community, the linked group's own row is updated too.
func (t *Tracker) linked(ctx context.Context, g *Group, l *wa.CommunityLink, unlink bool) error {
	if l == nil || l.Community == "" || l.Group == "" {
		return nil
	}
	var errs []error
	if unlink {
		errs = append(errs, t.groups.UnlinkCommunityGroup(ctx, l.Community, l.Group))
	} else {
		sub := wa.SubGroup{JID: l.Group, IsAnnouncement: l.IsAnnouncement}
		if l.Group != g.JID {
			sub.Name = l.Name
		} else {
			sub.Name = g.Name
		}
		errs = append(errs, t.groups.LinkCommunityGroup(ctx, l.Community, sub))
	}
	set := func(grp *Group, communityName string) {
		switch {
		case unlink && grp.Community == l.Community:
			grp.Community, grp.CommunityName = "", ""
		case !unlink:
			if grp.Community != l.Community {
				grp.CommunityName = ""
			}
			grp.Community, grp.CommunityName = l.Community, cmp.Or(communityName, grp.CommunityName)
			if l.IsAnnouncement {
				grp.Kind = KindAnnouncement
			}
		}
	}
	if l.Group == g.JID {
		set(g, l.Name)
		return errors.Join(errs...)
	}
	// The event is about the community: g is it, and the linked group may be one of ours.
	sub, ok, err := t.groups.Group(ctx, l.Group)
	if err != nil || !ok {
		return errors.Join(append(errs, err)...)
	}
	set(&sub, g.Name)
	return errors.Join(append(errs, t.groups.UpsertGroup(ctx, sub))...)
}

// started tells onStart that the bot joined, or became an admin in, a community's group.
func (t *Tracker) started(ctx context.Context, g Group) error {
	if t.onStart == nil || g.JID == "" || g.Community == "" || !g.IsChat() || g.Status == GroupLeft {
		return nil
	}
	if err := t.onStart(ctx, g); err != nil {
		return fmt.Errorf("start %s from community %s: %w", g.JID, g.Community, err)
	}
	return nil
}

// communityName names a group's community: the community's own row when the bot tracks it, a
// fresh fetch when one is due, or the name stored with the group before.
func (t *Tracker) communityName(ctx context.Context, bot, community wa.JID, old Group) string {
	var name string
	if old.Community == community {
		name = old.CommunityName
	}
	if c, ok, err := t.groups.Group(ctx, community); err == nil && ok && c.Kind == KindCommunity && c.Name != "" {
		name = c.Name
	}
	if fetched := t.refreshCommunity(ctx, bot, community); fetched != "" {
		name = fetched
	}
	return name
}

// refreshCommunity fetches a community's name and group list through the bot when the last
// fetch is older than GroupRefreshEvery, stores the list, and returns the name ("" when nothing
// was fetched). A failed fetch is logged and tried again on the next snapshot.
func (t *Tracker) refreshCommunity(ctx context.Context, bot, community wa.JID) string {
	if t.src == nil {
		return ""
	}
	now := t.clk.Now()
	t.mu.Lock()
	last, ok := t.listed[community]
	t.mu.Unlock()
	if ok && now.Sub(last) < GroupRefreshEvery {
		return ""
	}
	log := t.log.With().Str("community", string(community)).Logger()
	subs, err := t.src.SubGroups(ctx, bot, community)
	if err != nil {
		log.Warn().Err(err).Msg("list a community's groups")
		return ""
	}
	if err := t.groups.SetCommunityGroups(ctx, community, subs); err != nil {
		log.Warn().Err(err).Msg("store a community's groups")
		return ""
	}
	info, err := t.src.GroupInfo(ctx, bot, community)
	if err != nil {
		log.Warn().Err(err).Msg("community info")
		return ""
	}
	t.mu.Lock()
	t.listed[community] = now
	t.mu.Unlock()
	return info.Name
}

// person finds or creates a participant's person from whatever identifiers are known, recording
// the pair when both are. It never asks WhatsApp.
func (t *Tracker) person(ctx context.Context, lid, pn wa.JID, src Source) (Person, error) {
	_, lk, lok := normalize(lid)
	_, pk, pok := normalize(pn)
	switch {
	case lok && pok && lk == KindLID && pk == KindPN:
		return t.learn(ctx, lid, pn, src)
	case lok:
		return t.ids.EnsureKnown(ctx, lid, src)
	case pok:
		return t.ids.EnsureKnown(ctx, pn, src)
	}
	return Person{}, fmt.Errorf("participant %q/%q: %w", lid, pn, ErrNotPerson)
}

func (t *Tracker) learn(ctx context.Context, lid, pn wa.JID, src Source) (Person, error) {
	// Event data can be in either order or carry the same kind twice; only a LID+PN pair counts.
	if _, k, _ := normalize(lid); k == KindPN {
		lid, pn = pn, lid
	}
	p, err := t.ids.Learn(ctx, lid, pn, src)
	if errors.Is(err, ErrNotPerson) {
		return t.ids.EnsureKnown(ctx, lid, src)
	}
	return p, err
}

func mustNorm(j wa.JID) wa.JID {
	k, _, _ := normalize(j)
	return k
}
