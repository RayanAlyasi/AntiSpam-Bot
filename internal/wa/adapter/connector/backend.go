package connector

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/rs/zerolog"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"golang.org/x/time/rate"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa/adapter"
)

// WhatsmeowSchema is the Postgres schema that holds whatsmeow's device store (DESIGN.md §14).
// The goose migrations create it; whatsmeow creates and upgrades its own tables inside it.
const WhatsmeowSchema = "whatsmeow"

// storeTimeout bounds the inbox insert done before a message is acked.
const storeTimeout = 10 * time.Second

// OpenStore opens whatsmeow's sqlstore on the bot's Postgres, inside the whatsmeow schema, and
// brings its tables up to date. The returned *sql.DB belongs to the container; close it on exit.
func OpenStore(ctx context.Context, databaseURL string, log zerolog.Logger) (*sqlstore.Container, *sql.DB, error) {
	cfg, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		return nil, nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	// whatsmeow's table names are unqualified; the search path puts them in their own schema.
	cfg.RuntimeParams["search_path"] = WhatsmeowSchema
	db := stdlib.OpenDB(*cfg)
	container := sqlstore.NewWithDB(db, "postgres", waLog.Zerolog(log.With().Str("module", "whatsmeow").Logger()))
	if err := container.Upgrade(ctx); err != nil {
		_ = db.Close()
		return nil, nil, fmt.Errorf("upgrade whatsmeow store: %w", err)
	}
	return container, db, nil
}

// WhatsmeowBackend is the real Backend: devices from the sqlstore container, one whatsmeow
// client per session.
type WhatsmeowBackend struct {
	container *sqlstore.Container
	log       zerolog.Logger

	mu      sync.Mutex
	devices map[wa.JID]*store.Device
}

// NewWhatsmeowBackend wraps a sqlstore container.
func NewWhatsmeowBackend(c *sqlstore.Container, log zerolog.Logger) *WhatsmeowBackend {
	return &WhatsmeowBackend{container: c, log: log, devices: map[wa.JID]*store.Device{}}
}

// Devices implements Backend: every paired number in the store (DESIGN.md §2.4: one database
// holds many bot numbers).
func (b *WhatsmeowBackend) Devices(ctx context.Context) ([]Device, error) {
	devs, err := b.container.GetAllDevices(ctx)
	if err != nil {
		return nil, fmt.Errorf("list whatsmeow devices: %w", err)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Device, 0, len(devs))
	for _, d := range devs {
		if d.ID == nil {
			continue
		}
		dev := Device{JID: jid(*d.ID), LID: jid(d.LID), PushName: d.PushName}
		b.devices[dev.JID] = d
		out = append(out, dev)
	}
	return out, nil
}

// Open implements Backend.
func (b *WhatsmeowBackend) Open(ctx context.Context, dev Device, sink wa.EventSink) (Session, error) {
	b.mu.Lock()
	d, ok := b.devices[dev.JID]
	b.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("device %s not in the store", mask(dev.JID))
	}
	log := b.log.With().Str("module", "whatsmeow").Str("bot", mask(dev.JID)).Logger()
	cli := whatsmeow.NewClient(d, waLog.Zerolog(log))
	// Ack a message only after every handler returned, i.e. after the inbox insert committed
	// (DESIGN.md §2.4, §5.1).
	cli.SynchronousAck = true
	// Without the decrypted-event buffer, a message left unacked is redelivered with a signal
	// counter whatsmeow has already used, fails to decrypt (ErrOldCounter) and is dropped; the
	// buffer replays the stored plaintext instead, so an unacked message really is retried.
	cli.EnableDecryptedEventBuffer = true
	cli.EnableAutoReconnect = true
	return newSession(dev.JID, cli, d.LIDs, sink, log), nil
}

// session is one whatsmeow client and its event handling.
type session struct {
	bot    wa.JID
	cli    *whatsmeow.Client
	client *adapter.Client
	lids   store.LIDStore
	sink   wa.EventSink
	log    zerolog.Logger

	lidRes  *LIDResolver
	queue   chan wa.Event // group and profile events; may drop when full
	control chan wa.Event // connection and account events; never dropped
	stop    chan struct{}
	stopped sync.Once
	done    chan struct{}
}

func newSession(bot wa.JID, cli *whatsmeow.Client, lids store.LIDStore, sink wa.EventSink, log zerolog.Logger) *session {
	s := &session{
		bot: bot, cli: cli, client: adapter.New(cli), lids: lids, sink: sink, log: log,
		queue: make(chan wa.Event, 4096), control: make(chan wa.Event, 64),
		stop: make(chan struct{}), done: make(chan struct{}),
	}
	s.lidRes = NewLIDResolver(lids, cli)
	cli.AddEventHandlerWithSuccessStatus(s.onEvent)
	go s.drain()
	return s
}

// Connect implements Session.
func (s *session) Connect(ctx context.Context) error {
	if s.cli.Store.ID == nil {
		return errors.New("device is not paired")
	}
	if err := s.cli.ConnectContext(ctx); err != nil {
		return fmt.Errorf("connect to WhatsApp: %w", err)
	}
	return nil
}

// disconnectWait bounds how long Disconnect waits for queued events to be delivered.
const disconnectWait = 10 * time.Second

// Disconnect implements Session. Queued events are delivered first, for at most disconnectWait.
func (s *session) Disconnect() {
	s.cli.Disconnect()
	s.stopped.Do(func() { close(s.stop) })
	select {
	case <-s.done:
	case <-time.After(disconnectWait):
		s.log.Warn().Msg("disconnect: queued events still being delivered")
	}
}

// Client implements Session. It is nil until the adapter implements every WAClient method
// (F-009, a separate work package); the assertion makes that switch-over automatic.
func (s *session) Client() wa.WAClient {
	var c any = s.client
	if wc, ok := c.(wa.WAClient); ok {
		return wc
	}
	return nil
}

// resolver returns this bot's LID↔PN resolver.
func (s *session) resolver() *LIDResolver { return s.lidRes }

// onEvent runs on whatsmeow's receive loop. Message events are stored synchronously and the
// return value decides the ack: false leaves the message unacked so WhatsApp redelivers it.
// Everything else is queued for an ordered background delivery, because the sink may do slow
// work (database writes, IQ lookups) that must never block, or deadlock, the receive loop.
func (s *session) onEvent(raw any) bool {
	ctx, cancel := context.WithTimeout(context.Background(), storeTimeout)
	defer cancel()
	evt, ok, err := convert(ctx, s.bot, raw, s.lids)
	if err != nil {
		// An unconvertible message stays unacked rather than being lost.
		s.log.Error().Err(err).Msg("convert event")
		return !isMessage(raw)
	}
	if !ok {
		return true
	}
	if isMessage(raw) {
		if err := s.sink.HandleEvent(ctx, evt); err != nil {
			s.log.Error().Err(err).Msg("store message; leaving it unacked for redelivery")
			return false
		}
		return true
	}
	if isControl(evt) {
		// Bans, logouts and connection changes open the breaker and drive health: never drop
		// them. They are rare, so waiting for room here is short.
		select {
		case s.control <- evt:
		case <-s.stop:
		}
		return true
	}
	select {
	case s.queue <- evt:
	default:
		// The 6-hourly group refresh repairs any group state lost here.
		s.log.Error().Str("event", fmt.Sprintf("%T", evt)).Msg("event queue full; event dropped")
	}
	return true
}

func isControl(evt wa.Event) bool {
	switch evt.(type) {
	case *wa.ConnectionEvent, *wa.AccountEvent:
		return true
	}
	return false
}

func isMessage(raw any) bool {
	switch raw.(type) {
	case *events.Message, *events.UndecryptableMessage:
		return true
	}
	return false
}

// drain delivers queued events in order, control events first.
func (s *session) drain() {
	defer close(s.done)
	for {
		select {
		case evt := <-s.control:
			s.deliver(evt)
			continue
		default:
		}
		select {
		case evt := <-s.control:
			s.deliver(evt)
		case evt := <-s.queue:
			s.deliver(evt)
		case <-s.stop:
			for _, q := range []chan wa.Event{s.control, s.queue} {
				for len(q) > 0 {
					s.deliver(<-q)
				}
			}
			return
		}
	}
}

func (s *session) deliver(evt wa.Event) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := s.sink.HandleEvent(ctx, evt); err != nil {
		s.log.Error().Err(err).Str("event", fmt.Sprintf("%T", evt)).Msg("handle event")
	}
}

// LIDResolver implements the identity layer's LIDStore for one bot: PNForLID reads whatsmeow's
// local LID store only; LIDForPN reads it first and then asks WhatsApp with one usync query
// (GetUserInfo), the only lookup DESIGN.md §7.7 allows, and only in the PN → LID direction.
type LIDResolver struct {
	lids  pairLookup
	users userInfoer
	limit *rate.Limiter
}

type userInfoer interface {
	GetUserInfo(ctx context.Context, jids []types.JID) (map[types.JID]types.UserInfo, error)
}

// usyncRate caps PN → LID usync queries. Past the cap a lookup is skipped (the person stays
// PN-only until a free source reveals the LID) rather than queued, so lookups never build up.
var usyncRate = rate.Every(3 * time.Second)

// NewLIDResolver builds a resolver over a whatsmeow LID store; users may be nil (local only).
func NewLIDResolver(lids pairLookup, users userInfoer) *LIDResolver {
	return &LIDResolver{lids: lids, users: users, limit: rate.NewLimiter(usyncRate, 5)}
}

// PNForLID returns the phone number whatsmeow has learned for a LID, or "".
func (r *LIDResolver) PNForLID(ctx context.Context, lid wa.JID) (wa.JID, error) {
	j, err := types.ParseJID(string(lid))
	if err != nil || j.Server != types.HiddenUserServer {
		return "", nil
	}
	pn, err := r.lids.GetPNForLID(ctx, j)
	if err != nil {
		return "", fmt.Errorf("lid store: %w", err)
	}
	return jid(pn), nil
}

// LIDForPN returns the LID of a phone number, or "" when WhatsApp doesn't say.
func (r *LIDResolver) LIDForPN(ctx context.Context, pn wa.JID) (wa.JID, error) {
	j, err := types.ParseJID(string(pn))
	if err != nil || j.Server != types.DefaultUserServer {
		return "", nil
	}
	if lid, err := r.lids.GetLIDForPN(ctx, j); err == nil && !lid.IsEmpty() {
		return jid(lid), nil
	}
	if r.users == nil || !r.limit.Allow() {
		return "", nil
	}
	infos, err := r.users.GetUserInfo(ctx, []types.JID{j})
	if err != nil {
		return "", fmt.Errorf("usync %s: %w", mask(pn), err)
	}
	if info, ok := infos[j]; ok && !info.LID.IsEmpty() {
		return jid(info.LID), nil
	}
	return "", nil
}
