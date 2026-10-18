package main

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/identity"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/inbox"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/policy"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review/blacklist"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review/queue"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

const (
	// joinRequestPoll is how often approval-mode groups' pending join requests are checked
	// against the blacklist; WhatsApp sends no event for a new request.
	joinRequestPoll = 5 * time.Minute
	// hookBacklog is how many group events wait for the hooks before the receive loop blocks.
	hookBacklog = 256
)

// groupHooks are wp-k's reactions to group events (DESIGN.md §9.6, §9.8, §13.6): local bans'
// rejoin block and the blacklist on joins, the blacklist on join requests, and onboarding DMs
// when the bot is promoted or added without admin rights. The workers wiring sets them; a
// connector running without the workers role in the same process has none, and then only the
// next `.ban` removes a returning person.
//
// Events reach them through a queue that one goroutine drains in order, never on whatsmeow's
// receive loop (§2 rule 7), and they only log their errors: a failed reaction never holds up
// WhatsApp's events.
type groupHooks struct {
	bl *blacklist.Service
	// onboard sends the onboarding DMs through the chat handler of the bot that got the event.
	onboard onboarding
	// bans enforces local bans' rejoin block on every join (policy.Bans.OnJoin, F-077), with
	// persons to find the joiner and admins to tell a deliberate re-add by an admin apart.
	bans    *policy.Bans
	persons queue.Persons
	admins  review.Admins
	// groups tells which groups the bot protects and acts in: joins elsewhere are left alone.
	groups enforcingGroups
	// reads paces the join-request polls, one group per read (§10.3); nil: unpaced.
	reads interface {
		Wait(ctx context.Context) error
	}
	events chan any
	log    zerolog.Logger
}

// onboarding is the part of the chat handler the group hooks call.
type onboarding interface {
	OnPromoted(ctx context.Context, bot, group, by wa.JID) error
	OnNotAdmin(ctx context.Context, bot, group, addedBy wa.JID) error
}

// chatOnboarding sends onboarding DMs through the chat handler of the bot concerned.
type chatOnboarding struct{ sink *chatSink }

func (c chatOnboarding) OnPromoted(ctx context.Context, bot, group, by wa.JID) error {
	return c.sink.handler(bot).OnPromoted(ctx, group, by)
}

func (c chatOnboarding) OnNotAdmin(ctx context.Context, bot, group, addedBy wa.JID) error {
	return c.sink.handler(bot).OnNotAdmin(ctx, group, addedBy)
}

// enforcingGroups reports whether the bot acts in a group.
type enforcingGroups interface {
	Enforcing(ctx context.Context, group wa.JID) (bool, error)
}

// groupModes is enforcingGroups over wp-f's groups table and the settings documents: the bot is
// an admin there and the group's mode is enforce. Shadow and paused groups are never acted on
// (§8.4).
type groupModes struct {
	groups   identity.GroupStore
	settings *policy.SettingsService
}

func (m groupModes) Enforcing(ctx context.Context, group wa.JID) (bool, error) {
	g, ok, err := m.groups.Group(ctx, group)
	if err != nil {
		return false, fmt.Errorf("group %s: %w", group, err)
	}
	if !ok || g.Status != identity.GroupActive {
		return false, nil
	}
	s, _, err := m.settings.Get(ctx, group)
	if err != nil {
		return false, fmt.Errorf("settings of %s: %w", group, err)
	}
	return s.Mode == policy.ModeEnforce, nil
}

// selfFunc returns a bot number's own phone-number JID and LID.
type selfFunc func(bot wa.JID) (pn, lid wa.JID)

// person is a group participant as the blacklist matches it: LID and phone number when the
// event paired them.
func person(j wa.JID, pairs []wa.JIDPair) blacklist.Person {
	p := blacklist.Person{JID: j}
	if j.IsLID() {
		p.LID = j
	} else {
		p.Phone = j
	}
	for _, pr := range pairs {
		switch j {
		case pr.LID:
			p.Phone = pr.PN
		case pr.PN:
			p.LID = pr.LID
		}
	}
	return p
}

// isSelf reports whether one of jids is the bot itself.
func isSelf(self selfFunc, bot wa.JID, jids []wa.JID) bool {
	pn, lid := bot, wa.JID("")
	if self != nil {
		pn, lid = self(bot)
	}
	for _, j := range jids {
		if j == bot || (pn != "" && j == pn) || (lid != "" && j == lid) {
			return true
		}
	}
	return false
}

// enqueue hands a group event to the hooks, after the tracker has stored it. It blocks only when
// hookBacklog events are already waiting, so a raid slows the receive loop instead of losing
// joins.
func (h *groupHooks) enqueue(ctx context.Context, evt any) {
	select {
	case h.events <- evt:
	case <-ctx.Done():
	}
}

// run handles queued group events in order until ctx ends.
func (h *groupHooks) run(ctx context.Context, self selfFunc) {
	for {
		select {
		case <-ctx.Done():
			return
		case evt := <-h.events:
			switch e := evt.(type) {
			case *wa.GroupEvent:
				h.groupEvent(ctx, self, e)
			case *wa.JoinedGroupEvent:
				h.joinedGroup(ctx, self, e)
			}
		}
	}
}

// groupEvent handles one participant change.
func (h *groupHooks) groupEvent(ctx context.Context, self selfFunc, e *wa.GroupEvent) {
	if len(e.Join) > 0 {
		h.joins(ctx, self, e)
	}
	if h.onboard != nil && e.Sender != "" && isSelf(self, e.Bot, e.Promote) {
		// The promoting admin hears that protection is on, by DM once they have a thread (§13.6 step 3).
		if err := h.onboard.OnPromoted(ctx, e.Bot, e.Group, e.Sender); err != nil {
			h.log.Warn().Err(err).Msg("promotion notice")
		}
	}
}

// joins checks every joiner against the blacklist and local bans, in groups the bot acts in.
func (h *groupHooks) joins(ctx context.Context, self selfFunc, e *wa.GroupEvent) {
	if h.groups == nil {
		return
	}
	on, err := h.groups.Enforcing(ctx, e.Group)
	if err != nil {
		h.log.Warn().Err(err).Msg("mode of a group with joiners")
		return
	}
	if !on {
		return
	}
	for _, j := range e.Join {
		if isSelf(self, e.Bot, []wa.JID{j}) {
			continue
		}
		listed := false
		if h.bl != nil {
			// A listed joiner is banned (kick + rejoin block), silently, at once (§9.8 step 3).
			var err error
			if listed, err = h.bl.OnJoin(ctx, e.Group, person(j, e.Pairs)); err != nil {
				h.log.Warn().Err(err).Msg("blacklist check of a joiner")
			}
		}
		// The group's own rejoin block always applies (§9.6), also for listed people, whose list
		// entry may only open a review. For them an admin's re-add doesn't lift it: the list wins,
		// and removing the number is how admins let them back.
		h.rejoin(ctx, e, j, listed)
	}
}

// rejoin kicks a locally banned person who joined again (P0), unless a current admin re-added
// them deliberately rather than through an invite link (§9.6 "Keeping them out"). A listed person
// is never treated as re-added by an admin.
func (h *groupHooks) rejoin(ctx context.Context, e *wa.GroupEvent, j wa.JID, listed bool) {
	if h.bans == nil || h.persons == nil {
		return
	}
	pid, err := h.persons.PersonID(ctx, j)
	if err != nil {
		h.log.Warn().Err(err).Msg("person of a joiner")
		return
	}
	byAdmin := false
	if !listed && e.Sender != "" && e.Sender != j && e.JoinReason != "invite" && h.admins != nil {
		if byAdmin, err = h.admins.IsAdmin(ctx, e.Group, e.Sender); err != nil {
			h.log.Warn().Err(err).Msg("admin check of whoever added a joiner")
			byAdmin = false
		}
	}
	if _, err := h.bans.OnJoin(ctx, policy.Join{Group: e.Group, PersonID: pid, Member: j, AddedByAdmin: byAdmin, At: e.Timestamp}); err != nil {
		h.log.Warn().Err(err).Msg("rejoin check")
	}
}

// joinedGroup handles the bot joining a group: without admin rights, the admin who added it gets
// at most one reminder a day, in an existing DM thread only (§13.6 step 4).
func (h *groupHooks) joinedGroup(ctx context.Context, self selfFunc, e *wa.JoinedGroupEvent) {
	if h.onboard == nil || e.Sender == "" {
		return
	}
	for _, p := range e.Info.Participants {
		if isSelf(self, e.Bot, []wa.JID{p.JID, p.LID, p.PhoneNumber}) && (p.IsAdmin || p.IsSuperAdmin) {
			return
		}
	}
	if err := h.onboard.OnNotAdmin(ctx, e.Bot, e.Info.JID, e.Sender); err != nil {
		h.log.Warn().Err(err).Msg("not-admin reminder")
	}
}

// clientFunc returns a connected bot's client.
type clientFunc func(bot wa.JID) (wa.WAClient, bool)

// pollJoinRequests checks pending join requests every joinRequestPoll until ctx ends.
func (h *groupHooks) pollJoinRequests(ctx context.Context, groups identity.GroupStore, clients clientFunc) {
	if h.bl == nil {
		return
	}
	t := time.NewTicker(joinRequestPoll)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		h.joinRequests(ctx, groups, clients)
	}
}

// joinRequests rejects pending join requests from listed people in approval-mode groups
// (§9.8 step 3). The list is a read per group, so it runs only for protected groups that use
// join approval; the blacklist leaves shadow and paused groups alone.
func (h *groupHooks) joinRequests(ctx context.Context, groups identity.GroupStore, clients clientFunc) {
	active, err := groups.Groups(ctx, identity.GroupActive)
	if err != nil {
		if ctx.Err() == nil {
			h.log.Warn().Err(err).Msg("groups for join requests")
		}
		return
	}
	for _, g := range active {
		if !g.JoinApproval {
			continue
		}
		c, ok := clients(g.Bot)
		if !ok {
			continue
		}
		if h.reads != nil {
			if err := h.reads.Wait(ctx); err != nil {
				return
			}
		}
		reqs, err := c.JoinRequests(ctx, g.JID)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			h.log.Warn().Err(err).Msg("join requests")
			continue
		}
		people := make([]blacklist.Person, len(reqs))
		for i, r := range reqs {
			people[i] = person(r.JID, nil)
		}
		if len(people) > 0 {
			if _, err := h.bl.OnJoinRequests(ctx, g.JID, people); err != nil {
				h.log.Warn().Err(err).Msg("blacklist check of join requests")
			}
		}
	}
}

// blacklistSink checks every moderated message's sender against the blacklist before the fast
// lane sees it: a listed sender's message is deleted at P1 and they're banned while still a
// member (§9.8 step 3). The fast lane still runs, so content rules apply as usual; the shared
// revoke dedupe key keeps the deletion single. Edits and revokes are left to the fast lane: their
// IDs aren't the message on screen, which was checked when it arrived.
type blacklistSink struct {
	next inbox.Sink
	bl   *blacklist.Service
}

var _ inbox.Sink = blacklistSink{}

func (s blacklistSink) Handle(ctx context.Context, it inbox.Item, info wa.MessageInfo, d inbox.Decision) error {
	if !info.IsFromMe && info.IsGroup && info.Sender != "" && info.Edit == "" {
		p := blacklist.Person{JID: info.Sender}
		for _, j := range []wa.JID{info.Sender, info.SenderAlt} {
			switch {
			case j.IsLID():
				p.LID = j
			case j.Server() == wa.ServerUser:
				p.Phone = j
			}
		}
		if _, err := s.bl.OnMessage(ctx, info.Chat, wa.MessageRef{Sender: info.Sender, ID: info.ID}, p); err != nil {
			// The inbox retries the item, fast lane included; the dedupe keys keep its actions single.
			return fmt.Errorf("blacklist check of a sender: %w", err)
		}
	}
	if s.next == nil {
		return nil
	}
	return s.next.Handle(ctx, it, info, d)
}
