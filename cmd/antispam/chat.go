package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/api/chat"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/app/fastlane"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/identity"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/inbox"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/logging"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/policy"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review"
	reviewpg "github.com/RayanAlyasi/AntiSpam-Bot/internal/review/pgstore"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa/adapter/inboxenv"
)

// chatSink hands DMs, review-group messages and possible commands from the inbox to wp-k's chat
// handler (commands, review votes by reaction or reply, onboarding; DESIGN.md §13.7, §9.5). It
// keeps one handler per bot number, since the handler must know its own JID.
type chatSink struct {
	deps    chat.Deps // shared; Self is set per bot
	log     zerolog.Logger
	metrics *fastlane.Metrics

	mu       sync.Mutex
	handlers map[wa.JID]*chat.Handler
}

var _ inbox.Sink = (*chatSink)(nil)

// newChatSink wires the chat handler's ports. Optional ones left nil make their commands reply
// that the feature isn't available yet: lists, the blacklist, history, stats, joining by link.
func newChatSink(pool *pgxpool.Pool, ws *waStack, settings *policy.SettingsService, reviews *review.Service, learning *review.Learning,
	actions review.Actions, operators []string,
) *chatSink {
	ops := map[string]bool{}
	for _, p := range operators {
		ops[strings.TrimPrefix(strings.ReplaceAll(p, " ", ""), "+")] = true
	}
	return &chatSink{handlers: map[wa.JID]*chat.Handler{}, deps: chat.Deps{
		Groups:      chatGroups{ws: ws, settings: settings},
		Communities: chatGroups{ws: ws, settings: settings},
		Admins:      identityAdmins{ws.ids},
		Actions:     actions,
		Reviews:     reviews,
		Learning:    learning,
		Evidence:    learning,
		Settings:    chat.DocSettings{Docs: settings},
		Identity:    chatIdentity{ws.ids},
		Threads:     reviewpg.Threads{DB: pool},
		Audit:       reviewpg.Audit{DB: pool},
		Standing:    noStanding{},
		Clock:       clock.Real{},
		Operator:    func(j wa.JID) bool { return j.Server() == wa.ServerUser && ops[j.User()] },
	}}
}

// Handle implements inbox.Sink. Each message gets a request_id; a panic is logged with the stack,
// counted, and fails only that message.
func (s *chatSink) Handle(ctx context.Context, it inbox.Item, info wa.MessageInfo, d inbox.Decision) error {
	ctx, log := logging.WithRequestID(ctx, s.log, logging.NewID())
	log = log.With().Int64("inbox", it.ID).Str("chat", logging.MaskJID(string(it.Chat))).Str("route", string(d.Route)).Logger()
	err := fastlane.Recovered(log, s.metrics, "chat", func() error { return s.handle(ctx, it, info) })
	switch {
	case err == nil:
	case errors.Is(err, fastlane.ErrPanic):
		s.metrics.Failed("chat")
		return fmt.Errorf("%w: %w", inbox.ErrPermanent, err) // Recovered logged it
	case errors.Is(err, inbox.ErrPermanent):
		s.metrics.Failed("chat")
		log.Error().Err(err).Msg("handling a command or DM failed permanently")
	default:
		s.metrics.Failed("chat") // per failed attempt, like the fast lane
		log.Warn().Err(err).Msg("handling a command or DM failed; the inbox will retry it")
	}
	return err
}

func (s *chatSink) handle(ctx context.Context, it inbox.Item, info wa.MessageInfo) error {
	c, err := inboxenv.DecodeChat(it.Kind, it.Raw, it.Info, it.Bot)
	if err != nil {
		if errors.Is(err, inboxenv.ErrCorrupt) {
			return fmt.Errorf("%w: %w", inbox.ErrPermanent, err)
		}
		return err
	}
	if strings.TrimSpace(c.Text) == "" && c.Reaction == nil {
		return nil
	}
	m := chat.Message{Chat: info.Chat, SenderName: info.PushName, ID: info.ID, Text: c.Text, Mentions: c.Mentions, At: info.Timestamp}
	m.Sender, m.SenderPhone = info.Sender, info.SenderAlt
	if !info.Sender.IsLID() && info.SenderAlt.IsLID() {
		m.Sender, m.SenderPhone = info.SenderAlt, info.Sender
	}
	if q := c.Quoted; q != nil {
		m.Quoted = &chat.Quoted{ID: q.ID, Sender: q.Sender, Text: q.Text}
	}
	if r := c.Reaction; r != nil {
		m.Reaction = &chat.Reaction{Target: r.Target, Emoji: r.Emoji}
	}
	if err := s.handler(it.Bot).Handle(ctx, m); err != nil {
		return fmt.Errorf("chat handler: %w", err)
	}
	return nil
}

func (s *chatSink) handler(bot wa.JID) *chat.Handler {
	s.mu.Lock()
	defer s.mu.Unlock()
	h, ok := s.handlers[bot]
	if !ok {
		d := s.deps
		d.Self = bot
		h = chat.New(d)
		s.handlers[bot] = h
	}
	return h
}

// chatGroups answers the chat handler's group questions from wp-f's groups and members tables
// and the settings documents.
type chatGroups struct {
	ws       *waStack
	settings *policy.SettingsService
}

func (g chatGroups) IsProtected(ctx context.Context, group wa.JID) (bool, error) {
	r, ok, err := g.ws.groups.Group(ctx, group)
	return ok && r.Status == identity.GroupActive, err
}

func (g chatGroups) Name(ctx context.Context, group wa.JID) (string, error) {
	r, ok, err := g.ws.groups.Group(ctx, group)
	if err != nil || !ok {
		return string(group), err
	}
	return r.Name, nil
}

// AdminGroups lists the tracked groups where person is an admin, including ones still waiting
// for the bot to be made admin: an admin can switch a group to shadow mode before the bot gets
// the rights to act there (wiki "Protect your first group"). A community's own entry and
// its announcement group are left out: nobody but admins posts there.
func (g chatGroups) AdminGroups(ctx context.Context, person wa.JID) ([]chat.GroupRef, error) {
	p, ok, err := g.ws.ids.MatchPerson(ctx, person)
	if err != nil || !ok {
		return nil, err
	}
	groups, err := g.ws.ids.GroupsOf(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	var out []chat.GroupRef
	for _, j := range groups {
		r, ok, err := g.ws.groups.Group(ctx, j)
		if err != nil {
			return nil, err
		}
		if !ok || !r.IsChat() || (r.Status != identity.GroupActive && r.Status != identity.GroupPendingAdmin) {
			continue
		}
		m, ok, err := g.ws.ids.Member(ctx, j, p.ID)
		if err != nil {
			return nil, err
		}
		if ok && (m.Role == identity.RoleAdmin || m.Role == identity.RoleSuperAdmin) {
			ref := chat.GroupRef{JID: j, Name: r.Name, Community: r.Community, CommunityName: r.CommunityName}
			if ref.Community != "" {
				if ref.CommunityName, err = g.communityName(ctx, r); err != nil {
					return nil, err
				}
			}
			out = append(out, ref)
		}
	}
	return out, nil
}

// communityName is the name of a group's community: the community's own row when the bot tracks
// it (renames reach it first), otherwise the name stored with the group.
func (g chatGroups) communityName(ctx context.Context, r identity.Group) (string, error) {
	c, ok, err := g.ws.groups.Group(ctx, r.Community)
	if err != nil {
		return "", err
	}
	if ok && c.Kind == identity.KindCommunity && c.Name != "" {
		return c.Name, nil
	}
	return r.CommunityName, nil
}

// CommunityGroups lists a community's groups from its stored list and the groups the bot tracks
// in it, with the bot's standing in each; the announcement group is left out (DESIGN.md §12.4).
func (g chatGroups) CommunityGroups(ctx context.Context, community wa.JID) ([]chat.CommunityGroup, error) {
	listed, err := g.ws.groups.CommunityGroups(ctx, community)
	if err != nil {
		return nil, err
	}
	tracked, err := g.ws.groups.Groups(ctx)
	if err != nil {
		return nil, err
	}
	byJID := map[wa.JID]chat.CommunityGroup{}
	var order []wa.JID
	add := func(cg chat.CommunityGroup) {
		if _, ok := byJID[cg.JID]; !ok {
			order = append(order, cg.JID)
		}
		byJID[cg.JID] = cg
	}
	announcement := map[wa.JID]bool{}
	for _, s := range listed {
		if s.IsAnnouncement {
			announcement[s.JID] = true
			continue
		}
		add(chat.CommunityGroup{JID: s.JID, Name: s.Name})
	}
	for _, r := range tracked {
		if r.Community != community || !r.IsChat() || announcement[r.JID] {
			continue
		}
		member := r.Status != identity.GroupLeft
		add(chat.CommunityGroup{JID: r.JID, Name: cmpName(r.Name, byJID[r.JID].Name), Member: member, Admin: member && r.Status == identity.GroupActive})
	}
	out := make([]chat.CommunityGroup, 0, len(order))
	for _, j := range order {
		out = append(out, byJID[j])
	}
	slices.SortStableFunc(out, func(a, b chat.CommunityGroup) int {
		return cmp.Or(cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)), cmp.Compare(a.JID, b.JID))
	})
	return out, nil
}

func cmpName(a, b string) string { return cmp.Or(a, b) }

func (g chatGroups) ServedBy(ctx context.Context, reviewGroup wa.JID) ([]wa.JID, error) {
	groups, err := g.ws.groups.Groups(ctx, identity.GroupActive, identity.GroupPendingAdmin)
	if err != nil {
		return nil, err
	}
	var out []wa.JID
	for _, r := range groups {
		s, _, err := g.settings.Get(ctx, r.JID)
		if err != nil {
			return nil, err
		}
		if wa.JID(s.Review.ReviewGroup) == reviewGroup {
			out = append(out, r.JID)
		}
	}
	return out, nil
}

func (g chatGroups) Participants(ctx context.Context, group wa.JID) ([]wa.JID, error) {
	ms, err := g.ws.ids.GroupMembers(ctx, group)
	if err != nil {
		return nil, err
	}
	out := make([]wa.JID, 0, len(ms))
	for _, m := range ms {
		p, err := g.ws.ids.Person(ctx, m.Person)
		if err != nil {
			return nil, err
		}
		if j := cmpJID(p.LID, p.PN); j != "" && !slices.Contains(out, j) {
			out = append(out, j)
		}
	}
	return out, nil
}

// chatIdentity maps JIDs to people for the chat handler.
type chatIdentity struct{ ids *identity.Service }

func (c chatIdentity) Canonical(ctx context.Context, jid wa.JID) (wa.JID, error) {
	p, ok, err := c.ids.MatchPerson(ctx, jid)
	if err != nil || !ok {
		return jid, err
	}
	return cmpJID(p.LID, cmpJID(p.PN, jid)), nil
}

func (c chatIdentity) ByPhone(ctx context.Context, e164 string) (wa.JID, bool, error) {
	pn := wa.JID(strings.TrimPrefix(e164, "+") + "@" + wa.ServerUser)
	p, ok, err := c.ids.MatchPerson(ctx, pn)
	if err != nil || !ok {
		return "", false, err
	}
	return cmpJID(p.LID, p.PN), true, nil
}

func cmpJID(a, b wa.JID) wa.JID {
	if a != "" {
		return a
	}
	return b
}
