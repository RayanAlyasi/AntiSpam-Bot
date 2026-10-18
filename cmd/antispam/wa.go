package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/smtp"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/riverqueue/river"
	"github.com/rs/zerolog"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/alert"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/identity"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/inbox"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/logging"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa/adapter/connector"
)

// leaseOwner identifies this process in bots.lease_owner: host, PID and a random suffix, so a
// restarted process never mistakes an old lease for its own.
func leaseOwner() string {
	host, _ := os.Hostname()
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%s/%d/%s", host, os.Getpid(), hex.EncodeToString(b))
}

// alertSinks builds the operator alert sinks from the environment (DESIGN.md §16). With none
// set, alerts are only logged.
func alertSinks(getenv func(string) string) []alert.Sink {
	var sinks []alert.Sink
	if u := strings.TrimSpace(getenv("ALERT_WEBHOOK_URL")); u != "" {
		sinks = append(sinks, &alert.Webhook{URL: u})
	}
	if tok, chat := strings.TrimSpace(getenv("ALERT_TELEGRAM_TOKEN")), strings.TrimSpace(getenv("ALERT_TELEGRAM_CHAT_ID")); tok != "" && chat != "" {
		sinks = append(sinks, &alert.Telegram{Token: tok, ChatID: chat})
	}
	if addr, to := strings.TrimSpace(getenv("ALERT_SMTP_ADDR")), strings.TrimSpace(getenv("ALERT_EMAIL_TO")); addr != "" && to != "" {
		e := &alert.Email{Addr: addr, From: strings.TrimSpace(getenv("ALERT_EMAIL_FROM")), To: strings.Split(to, ",")}
		if user := getenv("ALERT_SMTP_USER"); user != "" {
			host, _, _ := strings.Cut(addr, ":")
			e.Auth = smtp.PlainAuth("", user, getenv("ALERT_SMTP_PASSWORD"), host)
		}
		sinks = append(sinks, e)
	}
	return sinks
}

// botRegistry adapts the bots table to the connector's Registry.
type botRegistry struct{ b *store.Bots }

func (r botRegistry) UpsertBot(ctx context.Context, b connector.Bot) error {
	return r.b.UpsertBot(ctx, b.JID, b.LID, b.DisplayName, string(b.Status))
}

func (r botRegistry) SetBotStatus(ctx context.Context, bot wa.JID, s connector.BotStatus) error {
	return r.b.SetBotStatus(ctx, bot, string(s))
}

func (r botRegistry) Bot(ctx context.Context, bot wa.JID) (connector.Bot, bool, error) {
	b, ok, err := r.b.Bot(ctx, bot)
	return connector.Bot{JID: b.JID, LID: b.LID, DisplayName: b.DisplayName, Status: connector.BotStatus(b.Status)}, ok, err
}

// recycledAlert tells the operator about a reassigned phone number (DESIGN.md §7.7): bans never
// follow the number to its new owner, and someone may want to look.
type recycledAlert struct{ n *alert.Notifier }

func (recycledAlert) Resolved(context.Context, identity.ResolvedEvent) error { return nil }

func (r recycledAlert) RecycledNumber(_ context.Context, ev identity.RecycledEvent) error {
	r.n.Notify(alert.Alert{
		Key: "recycled:" + string(ev.NewLID), Severity: alert.Warning,
		Title: "Phone number now belongs to a different account",
		Text: fmt.Sprintf("%s moved from person %d to person %d. Nothing (bans included) was carried over to the new owner.",
			alert.MaskJID(ev.PN), ev.OldPerson, ev.NewPerson),
	})
	return nil
}

// waStack is WP-F's part of the process: identity, groups, the connector and the inbox.
type waStack struct {
	pool      *pgxpool.Pool
	clk       clock.Clock
	logs      *logging.Loggers
	notifier  *alert.Notifier
	ids       *identity.Service
	groups    identity.GroupStore
	manager   *connector.Manager // nil without the connector role
	connected *atomic.Int64
	moderate  inbox.Sink // the detection fast lane (F-088); nil: moderated rows are only marked processed
	chat      inbox.Sink // commands, DMs and review votes (wp-k's chat handler); nil: not handled
}

// lidSwitch lets the identity service use the connector's LID resolver once it exists, and
// nothing when this process runs no connector.
type lidSwitch struct {
	m atomic.Pointer[connector.Manager]
}

func (l *lidSwitch) LIDForPN(ctx context.Context, pn wa.JID) (wa.JID, error) {
	if m := l.m.Load(); m != nil {
		return m.LIDForPN(ctx, pn)
	}
	return "", nil
}

func (l *lidSwitch) PNForLID(ctx context.Context, lid wa.JID) (wa.JID, error) {
	if m := l.m.Load(); m != nil {
		return m.PNForLID(ctx, lid)
	}
	return "", nil
}

func newWAStack(pool *pgxpool.Pool, getenv func(string) string, logs *logging.Loggers, connected *atomic.Int64) (*waStack, *lidSwitch) {
	clk := clock.Real{}
	notifier := alert.NewNotifier(alertSinks(getenv), 0, clk, logs.For("alert"))
	lids := &lidSwitch{}
	ids := identity.New(identity.NewPostgresStore(pool), lids, identity.Fanout{recycledAlert{notifier}}, clk)
	return &waStack{
		pool: pool, clk: clk, logs: logs, notifier: notifier, ids: ids,
		groups: identity.NewPostgresGroupStore(pool), connected: connected,
	}, lids
}

// connectorRole runs the WhatsApp connector: whatsmeow's store, one client per leased bot, the
// group tracker with its participant cache, and the 6-hourly group sync.
func (s *waStack) connectorRole(databaseURL string, reg prometheus.Registerer, lids *lidSwitch) role {
	return role{name: roleConnector, run: func(ctx context.Context) error {
		log := s.logs.For("wa")
		container, sqlDB, err := connector.OpenStore(ctx, databaseURL, s.logs.For("whatsmeow"))
		if err != nil {
			return err
		}
		defer func() { _ = sqlDB.Close() }()
		health, err := alert.NewHealth(s.notifier, nil, reg, s.clk)
		if err != nil {
			return err
		}

		var mgr *connector.Manager
		parts, err := identity.NewParticipants(func(ctx context.Context, group wa.JID) (wa.GroupInfo, error) {
			return s.groupInfo(ctx, mgr, group)
		}, s.clk, 10_000, 0)
		if err != nil {
			return err
		}
		defer parts.Close()
		tracker := identity.NewTracker(s.ids, s.groups, parts, func(bot wa.JID) (wa.JID, wa.JID) { return mgr.Self(bot) }, s.clk, log)
		writer := inbox.NewWriter(s.pool, s.clk)
		sink := wa.EventSinkFunc(func(ctx context.Context, evt wa.Event) error {
			switch evt.(type) {
			case *wa.MessageEvent, *wa.UndecryptableEvent:
				return writer.HandleEvent(ctx, evt)
			case *wa.GroupEvent, *wa.JoinedGroupEvent:
				return tracker.HandleEvent(ctx, evt)
			case *wa.ConnectionEvent, *wa.AccountEvent:
				err := health.HandleEvent(ctx, evt)
				s.connected.Store(int64(health.ConnectedBots()))
				return err
			}
			return nil // profile changes: the profile cache comes with enrichment
		})
		mgr, err = connector.NewManager(connector.NewWhatsmeowBackend(container, log), store.NewBots(s.pool),
			botRegistry{store.NewBots(s.pool)}, sink, connector.Options{Owner: leaseOwner(), Log: log})
		if err != nil {
			return err
		}
		s.manager = mgr
		lids.m.Store(mgr)
		defer lids.m.Store(nil)

		var wg sync.WaitGroup
		wg.Go(func() { _ = s.notifier.Run(ctx) })
		wg.Go(func() { _ = health.Run(ctx) })
		wg.Go(func() { s.syncGroups(ctx, mgr, tracker, log) })
		err = mgr.Run(ctx)
		wg.Wait()
		s.connected.Store(0)
		return err
	}}
}

// groupInfo fetches a group's info through the bot that is in it.
func (s *waStack) groupInfo(ctx context.Context, mgr *connector.Manager, group wa.JID) (wa.GroupInfo, error) {
	g, ok, err := s.groups.Group(ctx, group)
	if err != nil {
		return wa.GroupInfo{}, err
	}
	if !ok || mgr == nil {
		return wa.GroupInfo{}, fmt.Errorf("group %s: %w", group, wa.ErrNotFound)
	}
	c, ok := mgr.Client(g.Bot)
	if !ok {
		return wa.GroupInfo{}, fmt.Errorf("group %s: its bot is not connected here", group)
	}
	return c.GroupInfo(ctx, group)
}

// syncGroups runs Tracker.SyncJoined for every connected bot right after it connects and then
// every identity.GroupRefreshEvery (DESIGN.md §11: group info refreshed every 6 h).
func (s *waStack) syncGroups(ctx context.Context, mgr *connector.Manager, tracker *identity.Tracker, log zerolog.Logger) {
	last := map[wa.JID]time.Time{}
	for {
		connected := mgr.Connected()
		for bot := range last {
			if !slices.Contains(connected, bot) {
				delete(last, bot) // sync again as soon as it reconnects
			}
		}
		for _, bot := range connected {
			if t, ok := last[bot]; ok && s.clk.Now().Sub(t) < identity.GroupRefreshEvery {
				continue
			}
			c, ok := mgr.Client(bot)
			if !ok {
				continue
			}
			infos, err := c.JoinedGroups(ctx)
			if err != nil {
				log.Warn().Err(err).Str("bot", alert.MaskJID(bot)).Msg("list joined groups; retrying later")
				continue
			}
			if err := tracker.SyncJoined(ctx, bot, infos); err != nil {
				log.Error().Err(err).Str("bot", alert.MaskJID(bot)).Msg("sync groups")
			}
			last[bot] = s.clk.Now()
		}
		select {
		case <-ctx.Done():
			return
		case <-s.clk.After(time.Minute):
		}
	}
}

// inboxRole runs the inbox workers: claim, route, record the sender, write the message_index
// base row, hand off (F-014–F-016, F-033). Moderated messages go to the detection fast lane
// (s.moderate, F-088).
func (s *waStack) inboxRole() role {
	return role{name: roleWorkers + "/inbox", run: func(ctx context.Context) error {
		log := s.logs.For("inbox")
		// Routing reads roles from the members table, which the connector keeps current, so it
		// works in a process without a WhatsApp connection too.
		parts, err := identity.NewParticipants(s.ids.GroupInfoFromMembers, s.clk, 10_000, time.Minute)
		if err != nil {
			return err
		}
		defer parts.Close()
		tracker := identity.NewTracker(s.ids, s.groups, nil, nil, s.clk, log)
		router := inbox.NewRouter(tracker, adminCheck{parts}, s.ids)
		d := inbox.NewDispatcher(router, senderObserver{s.ids}, inbox.NewIndex(s.pool), inbox.Sinks{Moderate: s.moderate, Commands: s.chat, DM: s.chat}, log)
		w := inbox.NewWorker(s.pool, d, inbox.WorkerOptions{Log: log})
		return w.Run(ctx)
	}}
}

// senderObserver adapts the identity service to the inbox's SenderObserver.
type senderObserver struct{ ids *identity.Service }

func (o senderObserver) ObserveSender(ctx context.Context, info wa.MessageInfo, dm bool) (int64, error) {
	id, err := o.ids.ObserveSender(ctx, info, dm)
	return int64(id), err
}

// adminCheck answers routing's admin question from the members-backed participant cache.
type adminCheck struct{ p *identity.Participants }

func (a adminCheck) IsGroupAdmin(ctx context.Context, group, jid wa.JID) (bool, error) {
	return a.p.IsAdmin(ctx, group, jid)
}

// RetentionArgs is the hourly purge of old inbox and message_index rows (DESIGN.md §14).
type RetentionArgs struct{}

// Kind implements river.JobArgs.
func (RetentionArgs) Kind() string { return "inbox_retention" }

type retentionWorker struct {
	river.WorkerDefaults[RetentionArgs]
	pool *pgxpool.Pool
	log  zerolog.Logger
}

func (w *retentionWorker) Work(ctx context.Context, _ *river.Job[RetentionArgs]) error {
	in, ix, err := inbox.Purge(ctx, w.pool, time.Now())
	if err != nil {
		return err
	}
	w.log.Info().Int64("inbox", in).Int64("message_index", ix).Msg("retention purge")
	return nil
}

// registerWAJobs adds WP-F's periodic jobs to the River client.
func registerWAJobs(pool *pgxpool.Pool, log zerolog.Logger) func(*river.Workers, *[]*river.PeriodicJob) error {
	return func(workers *river.Workers, periodic *[]*river.PeriodicJob) error {
		if err := river.AddWorkerSafely(workers, &retentionWorker{pool: pool, log: log}); err != nil {
			return fmt.Errorf("register retention: %w", err)
		}
		*periodic = append(*periodic, river.NewPeriodicJob(river.PeriodicInterval(time.Hour),
			func() (river.JobArgs, *river.InsertOpts) { return RetentionArgs{}, nil },
			&river.PeriodicJobOpts{ID: RetentionArgs{}.Kind(), RunOnStart: true}))
		return nil
	}
}

// pairLease records the new bot before taking its lease, so pairing can hold the lease while it
// sets the profile.
type pairLease struct{ b *store.Bots }

func (p pairLease) Acquire(ctx context.Context, bot wa.JID, owner string, ttl time.Duration) (bool, error) {
	if err := p.b.UpsertBot(ctx, bot, "", "", string(connector.BotActive)); err != nil {
		return false, err
	}
	return p.b.Acquire(ctx, bot, owner, ttl)
}

func (p pairLease) Renew(ctx context.Context, bot wa.JID, owner string, ttl time.Duration) (bool, error) {
	return p.b.Renew(ctx, bot, owner, ttl)
}

func (p pairLease) Release(ctx context.Context, bot wa.JID, owner string) error {
	return p.b.Release(ctx, bot, owner)
}

// errPairUsage is returned for a bad `antispam pair` command line.
var errPairUsage = errors.New("usage: antispam pair [-phone +<international number>]")
