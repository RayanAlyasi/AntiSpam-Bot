package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/identity"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/policy"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review/blacklist"
	reviewpg "github.com/RayanAlyasi/AntiSpam-Bot/internal/review/pgstore"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review/queue"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// blacklistReadInterval paces the blacklist's GetUserInfo lookups (one batched IQ of up to 50
// numbers each), well inside the read budget (DESIGN.md §10.3); they only run on `.blacklist add`.
const blacklistReadInterval = 2 * time.Second

// newBlacklist wires wp-k's phone-number blacklist (DESIGN.md §9.8) onto wp-g's number_blacklist
// table, wp-f's identity and groups, the shared action queue and the review service.
func newBlacklist(pool *pgxpool.Pool, key []byte, ws *waStack, lids *lidSwitch, settings *policy.SettingsService,
	reviews *review.Service, actions review.Actions, notify blacklist.Notifier, clk clock.Clock,
) *blacklist.Service {
	persons := queue.IdentityPersons{Service: ws.ids}
	list := reviewpg.BlacklistList{DB: pool, HashKey: key, Persons: persons, Clock: clk}
	return blacklist.NewService(blacklist.Deps{
		List:      list,
		Normalize: reviewpg.PhoneNormalizer{},
		Resolver: blacklist.NewResolver(blacklist.ResolverDeps{
			Users:  connectedUsers{ws: ws, lids: lids},
			Budget: &intervalBudget{clock: clk, every: blacklistReadInterval},
			Linker: queue.IdentityLinker{Service: ws.ids},
			List:   list,
		}),
		Groups:   blacklistGroups{ws: ws, settings: settings},
		Settings: blacklist.PolicySettings{Docs: settings},
		Actions:  actions,
		Reviews:  reviews,
		Notifier: notify,
		Audit:    reviewpg.Audit{DB: pool},
		Clock:    clk,
	})
}

// blacklistGroups lists the protected groups and their members with roles, so the blacklist bans
// listed members but never admins. A group is protected the way the fast lane sees it: the bot
// is an admin there (wp-f's groups table) and the group has a settings document.
type blacklistGroups struct {
	ws       *waStack
	settings *policy.SettingsService
}

func (g blacklistGroups) Protected(ctx context.Context) ([]wa.JID, error) {
	groups, err := g.ws.groups.Groups(ctx, identity.GroupActive)
	if err != nil {
		return nil, fmt.Errorf("protected groups: %w", err)
	}
	out := make([]wa.JID, 0, len(groups))
	for _, gr := range groups {
		if _, version, err := g.settings.Get(ctx, gr.JID); err != nil {
			return nil, fmt.Errorf("settings of %s: %w", gr.JID, err)
		} else if version > 0 {
			out = append(out, gr.JID)
		}
	}
	return out, nil
}

func (g blacklistGroups) Members(ctx context.Context, group wa.JID) ([]blacklist.Member, error) {
	members, err := g.ws.ids.GroupMembers(ctx, group)
	if err != nil {
		return nil, fmt.Errorf("members of %s: %w", group, err)
	}
	out := make([]blacklist.Member, 0, len(members))
	for _, m := range members {
		p, err := g.ws.ids.Person(ctx, m.Person)
		if errors.Is(err, identity.ErrNoPerson) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("person %d: %w", m.Person, err)
		}
		jid := p.LID
		if jid == "" {
			jid = p.PN
		}
		out = append(out, blacklist.Member{JID: jid, LID: p.LID, Phone: p.PN, Admin: m.Role.IsAdmin()})
	}
	return out, nil
}

// connectedUsers looks numbers up through a connected bot number (only the connector role has
// one; elsewhere resolution is skipped and the entry still works as a content signal).
type connectedUsers struct {
	ws   *waStack
	lids *lidSwitch
}

func (u connectedUsers) UserInfo(ctx context.Context, jids []wa.JID) (map[wa.JID]wa.UserInfo, error) {
	m := u.lids.m.Load()
	if m == nil {
		return nil, errors.New("no WhatsApp connection in this process")
	}
	bot, err := botFor(ctx, u.ws, u.lids, "")
	if err != nil {
		return nil, err
	}
	c, ok := m.Client(bot)
	if !ok {
		return nil, fmt.Errorf("bot %s is not connected", bot)
	}
	return c.UserInfo(ctx, jids)
}

// intervalBudget lets one read through per interval.
type intervalBudget struct {
	clock clock.Clock
	every time.Duration
	mu    sync.Mutex
	next  time.Time
}

func (b *intervalBudget) Wait(ctx context.Context) error {
	b.mu.Lock()
	now := b.clock.Now()
	at := b.next
	if at.Before(now) {
		at = now
	}
	b.next = at.Add(b.every)
	b.mu.Unlock()
	return b.clock.Sleep(ctx, at.Sub(now))
}

// chatNotifier tells an admin something through the chat handler of the bot that serves the
// group: by DM when they have an open thread, otherwise held until they write (inbound-first).
type chatNotifier struct {
	sink *chatSink
	ws   *waStack
	lids *lidSwitch
}

func (n chatNotifier) Notify(ctx context.Context, to, group wa.JID, key string, vars map[string]string) error {
	bot, err := botFor(ctx, n.ws, n.lids, group)
	if err != nil {
		return err
	}
	return n.sink.handler(bot).Notify(ctx, to, group, key, vars)
}
