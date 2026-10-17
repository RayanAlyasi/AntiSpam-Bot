// Package connector owns the bot's WhatsApp connections (DESIGN.md §4, §5.1, §15): one whatsmeow
// client per bot number, connected only while this process holds that bot's lease. It turns
// whatsmeow events into wa.Event values for an EventSink, stores message events before they are
// acked, and keeps the bots table in step with each connection.
package connector

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// Lease timings (DESIGN.md §15: renewed every 10 s).
const (
	DefaultRenewEvery = 10 * time.Second
	DefaultLeaseTTL   = 30 * time.Second
	// replacedCooldown keeps a bot offline after StreamReplaced, so two processes that both
	// think they own a session don't kick each other off in a loop.
	replacedCooldown = 5 * time.Minute
)

// BotStatus is a bot number's state (bots.status).
type BotStatus string

// Bot statuses.
const (
	BotActive   BotStatus = "active"
	BotPaused   BotStatus = "paused" // the operator paused it: never connected
	BotBanned   BotStatus = "banned" // temporarily banned by WhatsApp
	BotUnpaired BotStatus = "unpaired"
)

// Bot is one bots row.
type Bot struct {
	JID         wa.JID
	LID         wa.JID
	DisplayName string
	Status      BotStatus
}

// Lease is the per-bot connector lease (DESIGN.md §15). A process connects a bot only while it
// holds the bot's lease; Acquire succeeds when the lease is free, expired or already ours.
type Lease interface {
	Acquire(ctx context.Context, bot wa.JID, owner string, ttl time.Duration) (bool, error)
	// Renew extends a lease we hold; false means we lost it.
	Renew(ctx context.Context, bot wa.JID, owner string, ttl time.Duration) (bool, error)
	Release(ctx context.Context, bot wa.JID, owner string) error
}

// Registry is the bots table.
type Registry interface {
	// UpsertBot records a paired device; it keeps an existing row's status.
	UpsertBot(ctx context.Context, b Bot) error
	SetBotStatus(ctx context.Context, bot wa.JID, status BotStatus) error
	Bot(ctx context.Context, bot wa.JID) (Bot, bool, error)
}

// Session is one bot's WhatsApp connection.
type Session interface {
	Connect(ctx context.Context) error
	Disconnect()
	// Client is the WAClient for this bot, for the action executor and lookups.
	Client() wa.WAClient
}

// Device is a paired bot number in the whatsmeow store.
type Device struct {
	JID      wa.JID
	LID      wa.JID
	PushName string
}

// Backend lists paired devices and opens sessions. The whatsmeow backend is the real one; tests
// use a fake, so nothing here ever talks to WhatsApp in a test.
type Backend interface {
	Devices(ctx context.Context) ([]Device, error)
	// Open prepares a session for a device. Events go to sink; message events must be stored
	// by sink before they are acked.
	Open(ctx context.Context, dev Device, sink wa.EventSink) (Session, error)
}

// Options configures a Manager.
type Options struct {
	Owner      string // this process's lease owner ID; must be unique per process
	RenewEvery time.Duration
	LeaseTTL   time.Duration
	Clock      clock.Clock
	Log        zerolog.Logger
}

// Manager connects every paired, active bot whose lease it can take, renews the leases and
// disconnects a bot the moment its lease is lost.
type Manager struct {
	backend  Backend
	lease    Lease
	registry Registry
	sink     wa.EventSink
	opts     Options

	mu       sync.Mutex
	sessions map[wa.JID]Session
	cooldown map[wa.JID]time.Time
	devices  map[wa.JID]Device // last listing, for Self
}

// NewManager builds a manager. sink receives every event of every bot.
func NewManager(backend Backend, lease Lease, registry Registry, sink wa.EventSink, opts Options) (*Manager, error) {
	if opts.Owner == "" {
		return nil, errors.New("connector: lease owner must be set")
	}
	if opts.RenewEvery <= 0 {
		opts.RenewEvery = DefaultRenewEvery
	}
	if opts.LeaseTTL <= 0 {
		opts.LeaseTTL = DefaultLeaseTTL
	}
	if opts.LeaseTTL <= opts.RenewEvery {
		return nil, fmt.Errorf("connector: lease TTL %s must exceed the renew interval %s", opts.LeaseTTL, opts.RenewEvery)
	}
	if opts.Clock == nil {
		opts.Clock = clock.Real{}
	}
	opts.Log = opts.Log.With().Str("module", "connector").Logger()
	return &Manager{
		backend: backend, lease: lease, registry: registry, sink: sink, opts: opts,
		sessions: map[wa.JID]Session{}, cooldown: map[wa.JID]time.Time{}, devices: map[wa.JID]Device{},
	}, nil
}

// Run keeps bots connected until ctx ends, then disconnects them and releases their leases.
func (m *Manager) Run(ctx context.Context) error {
	defer m.shutdown()
	for {
		m.Tick(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-m.opts.Clock.After(m.opts.RenewEvery):
		}
	}
}

// Client returns the WAClient of a connected bot.
func (m *Manager) Client(bot wa.JID) (wa.WAClient, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[bot]
	if !ok {
		return nil, false
	}
	c := s.Client()
	return c, c != nil
}

// Self returns a bot number's own phone-number JID and LID, so group tracking can tell the bot
// apart from members.
func (m *Manager) Self(bot wa.JID) (pn, lid wa.JID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return bot, m.devices[bot].LID
}

// Connected lists the bots this process holds sessions for.
func (m *Manager) Connected() []wa.JID {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]wa.JID, 0, len(m.sessions))
	for b := range m.sessions {
		out = append(out, b)
	}
	return out
}

// Tick runs one round: renew held leases (dropping sessions whose lease is lost), then try to
// take and connect every other paired, active bot.
func (m *Manager) Tick(ctx context.Context) {
	dropped := map[wa.JID]bool{}
	for _, bot := range m.Connected() {
		ok, err := m.lease.Renew(ctx, bot, m.opts.Owner, m.opts.LeaseTTL)
		if err != nil {
			// We can't prove we still own it: stop before the lease expires under us.
			m.opts.Log.Error().Err(err).Str("bot", mask(bot)).Msg("renew lease; disconnecting")
		}
		if err != nil || !ok {
			if !ok && err == nil {
				m.opts.Log.Warn().Str("bot", mask(bot)).Msg("lease lost; disconnecting")
			}
			state := wa.Released // another process owns it now
			if err != nil {
				state = wa.Disconnected // we just can't tell: the bot is down as far as we know
			}
			m.drop(ctx, bot, false, state)
			dropped[bot] = true // don't reconnect in the same round
		}
	}

	devices, err := m.backend.Devices(ctx)
	if err != nil {
		m.opts.Log.Error().Err(err).Msg("list paired devices")
		return
	}
	m.mu.Lock()
	for _, d := range devices {
		m.devices[d.JID] = d
	}
	m.mu.Unlock()
	for _, dev := range devices {
		if ctx.Err() != nil {
			return
		}
		if dropped[dev.JID] {
			continue
		}
		if err := m.connect(ctx, dev); err != nil {
			m.opts.Log.Error().Err(err).Str("bot", mask(dev.JID)).Msg("connect bot")
		}
	}
}

func (m *Manager) connect(ctx context.Context, dev Device) error {
	m.mu.Lock()
	_, running := m.sessions[dev.JID]
	until, cooling := m.cooldown[dev.JID]
	m.mu.Unlock()
	if running {
		return nil
	}
	if cooling && m.opts.Clock.Now().Before(until) {
		return nil
	}

	b, known, err := m.registry.Bot(ctx, dev.JID)
	if err != nil {
		return fmt.Errorf("read bot: %w", err)
	}
	if !known || b.LID != dev.LID || b.DisplayName != dev.PushName {
		if err := m.registry.UpsertBot(ctx, Bot{JID: dev.JID, LID: dev.LID, DisplayName: dev.PushName, Status: BotActive}); err != nil {
			return fmt.Errorf("record bot: %w", err)
		}
	}
	if known && b.Status == BotPaused {
		return nil
	}

	ok, err := m.lease.Acquire(ctx, dev.JID, m.opts.Owner, m.opts.LeaseTTL)
	if err != nil {
		return fmt.Errorf("acquire lease: %w", err)
	}
	if !ok {
		return nil // another process owns this bot
	}
	s, err := m.backend.Open(ctx, dev, wa.EventSinkFunc(m.handle))
	if err != nil {
		m.release(ctx, dev.JID)
		return fmt.Errorf("open session: %w", err)
	}
	m.mu.Lock()
	m.sessions[dev.JID] = s
	m.mu.Unlock()
	if err := s.Connect(ctx); err != nil {
		m.drop(ctx, dev.JID, true, wa.Disconnected)
		return fmt.Errorf("connect: %w", err)
	}
	m.opts.Log.Info().Str("bot", mask(dev.JID)).Msg("bot connected")
	return nil
}

// handle sees every event first: it keeps bots.status current and reacts to account problems,
// then passes the event on.
func (m *Manager) handle(ctx context.Context, evt wa.Event) error {
	switch e := evt.(type) {
	case *wa.ConnectionEvent:
		if e.State == wa.Connected {
			m.setStatus(ctx, e.Bot, BotActive)
		}
	case *wa.AccountEvent:
		switch e.Problem {
		case wa.ProblemTemporaryBan:
			m.setStatus(ctx, e.Bot, BotBanned)
		case wa.ProblemLoggedOut:
			m.setStatus(ctx, e.Bot, BotUnpaired)
			go m.drop(context.WithoutCancel(ctx), e.Bot, true, wa.Released)
		case wa.ProblemStreamReplaced:
			m.mu.Lock()
			m.cooldown[e.Bot] = m.opts.Clock.Now().Add(replacedCooldown)
			m.mu.Unlock()
			go m.drop(context.WithoutCancel(ctx), e.Bot, true, wa.Released)
		}
	}
	return m.sink.HandleEvent(ctx, evt)
}

func (m *Manager) setStatus(ctx context.Context, bot wa.JID, s BotStatus) {
	if b, ok, err := m.registry.Bot(ctx, bot); err == nil && ok && (b.Status == s || b.Status == BotPaused) {
		return
	}
	if err := m.registry.SetBotStatus(ctx, bot, s); err != nil {
		m.opts.Log.Error().Err(err).Str("bot", mask(bot)).Str("status", string(s)).Msg("set bot status")
	}
}

// drop disconnects a bot's session; with release it also gives up the lease. whatsmeow emits
// no event for a disconnect we ask for, so drop tells the sink itself: state is Disconnected
// when the bot is down as far as we know, or Released when this process simply no longer owns
// it (shutdown, lease taken over, logged out), which health tracking shouldn't alert on.
func (m *Manager) drop(ctx context.Context, bot wa.JID, release bool, state wa.ConnectionState) {
	m.mu.Lock()
	s, ok := m.sessions[bot]
	delete(m.sessions, bot)
	m.mu.Unlock()
	if ok {
		s.Disconnect()
		if err := m.sink.HandleEvent(ctx, &wa.ConnectionEvent{Bot: bot, State: state}); err != nil {
			m.opts.Log.Error().Err(err).Str("bot", mask(bot)).Msg("report disconnect")
		}
	}
	if release {
		m.release(ctx, bot)
	}
}

func (m *Manager) release(ctx context.Context, bot wa.JID) {
	if err := m.lease.Release(ctx, bot, m.opts.Owner); err != nil {
		m.opts.Log.Error().Err(err).Str("bot", mask(bot)).Msg("release lease")
	}
}

func (m *Manager) shutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, bot := range m.Connected() {
		m.drop(ctx, bot, true, wa.Released)
	}
}

// mask hides most digits of a phone-number JID for logs (GO-003).
func mask(j wa.JID) string {
	u := j.User()
	if j.Server() != wa.ServerUser || len(u) < 6 {
		return string(j)
	}
	b := []byte(u)
	for i := 3; i < len(b)-2; i++ {
		b[i] = '*'
	}
	return string(b) + "@" + j.Server()
}

// resolverSession is a session that can resolve LID↔PN pairs (the whatsmeow one can).
type resolverSession interface{ resolver() *LIDResolver }

func (m *Manager) anyResolver() *LIDResolver {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.sessions {
		if rs, ok := s.(resolverSession); ok {
			return rs.resolver()
		}
	}
	return nil
}

// PNForLID resolves a LID through any connected bot (whatsmeow's LID store is shared by every
// device in the database). It returns "" when no bot is connected or nothing is known.
func (m *Manager) PNForLID(ctx context.Context, lid wa.JID) (wa.JID, error) {
	if r := m.anyResolver(); r != nil {
		return r.PNForLID(ctx, lid)
	}
	return "", nil
}

// LIDForPN resolves a phone number through any connected bot; see LIDResolver.LIDForPN.
func (m *Manager) LIDForPN(ctx context.Context, pn wa.JID) (wa.JID, error) {
	if r := m.anyResolver(); r != nil {
		return r.LIDForPN(ctx, pn)
	}
	return "", nil
}
