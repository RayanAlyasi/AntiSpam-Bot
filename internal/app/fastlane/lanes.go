package fastlane

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/rs/zerolog"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/envelope"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/identity"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/inbox"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/logging"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/pipeline"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/policy"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa/adapter/inboxenv"
)

// ErrNotConnected means a message arrived for a bot number this process has no WhatsApp
// connection for; the inbox retries it later.
var ErrNotConnected = errors.New("fastlane: bot not connected in this process")

// Lanes is the inbox's moderation sink (inbox.Sinks.Moderate): it keeps one fast lane per bot
// number, created on the bot's first message with that bot's WhatsApp client, and runs each
// lane's executor and rules listener until Run's context ends.
type Lanes struct {
	base   Deps
	client func(bot wa.JID) (wa.WAClient, bool)
	log    zerolog.Logger

	mu     sync.Mutex
	ctx    context.Context // Run's context; nil before Run
	closed bool            // Run is stopping: no new lane may start
	wg     sync.WaitGroup
	lanes  map[wa.JID]*Lane
}

// NewLanes returns the registry. base is the Deps every lane shares (Bot and Client are filled in
// per lane); client returns a connected bot's WhatsApp client.
func NewLanes(base Deps, client func(bot wa.JID) (wa.WAClient, bool)) *Lanes {
	return &Lanes{base: base, client: client, log: base.Log, lanes: map[wa.JID]*Lane{}}
}

var _ inbox.Sink = (*Lanes)(nil)

// Run serves until ctx ends, then waits for every lane to stop.
func (l *Lanes) Run(ctx context.Context) error {
	l.mu.Lock()
	l.ctx = ctx
	l.mu.Unlock()
	<-ctx.Done()
	l.mu.Lock()
	l.closed = true // lane() holds mu while it starts one, so none starts after this
	l.mu.Unlock()
	l.wg.Wait()
	return nil
}

// Handle implements inbox.Sink: one routed group message through the bot's fast lane. Every
// message gets a request_id that its log lines carry (and a detection_id once recorded); a panic
// anywhere on its path is logged with the stack and counted, and the message fails permanently
// so the next one is processed.
func (l *Lanes) Handle(ctx context.Context, it inbox.Item, info wa.MessageInfo, _ inbox.Decision) error {
	ctx, log := logging.WithRequestID(ctx, l.log, logging.NewID())
	log = log.With().Int64("inbox", it.ID).Str("group", logging.MaskJID(string(it.Chat))).Str("msg", string(it.MsgID)).Logger()
	err := Recovered(log, l.base.Metrics, "fastlane", func() error { return l.handle(ctx, log, it, info) })
	if err != nil {
		l.base.Metrics.failed("fastlane")
		log.Error().Err(err).Msg("moderating a message failed")
		if errors.Is(err, ErrPanic) {
			return fmt.Errorf("%w: %w", inbox.ErrPermanent, err) // it would panic again
		}
	}
	return err
}

func (l *Lanes) handle(ctx context.Context, log zerolog.Logger, it inbox.Item, info wa.MessageInfo) error {
	res, err := inboxenv.Decode(it.Kind, it.Raw, it.Info, it.Bot)
	if err != nil {
		if errors.Is(err, inboxenv.ErrCorrupt) {
			return fmt.Errorf("%w: %w", inbox.ErrPermanent, err)
		}
		return err
	}
	if len(res.Unknown) > 0 {
		log.Warn().Strs("fields", res.Unknown).Msg("message has fields the envelope builder doesn't know")
	}
	if res.Envelope == nil {
		log.Debug().Str("kind", it.Kind).Msg("nothing to analyze (reaction, vote, protocol message)")
		return nil
	}
	lane, err := l.lane(ctx, it.Bot)
	if err != nil {
		return err
	}
	v, err := lane.Process(ctx, res.Envelope, info.Timestamp)
	if err != nil {
		return err
	}
	if v.Route != pipeline.RouteNone || v.Band != pipeline.BandClean {
		dlog := log
		if v.DetectionID != 0 {
			_, dlog = logging.WithDetectionID(ctx, log, fmt.Sprint(v.DetectionID))
		}
		dlog.Info().Str("mode", string(v.Mode)).Str("band", string(v.Band)).Str("source", string(v.Source)).
			Str("route", string(v.Route)).Interface("actions", v.Actions).Bool("duplicate", v.Duplicate).Msg("detection")
	}
	return nil
}

func (l *Lanes) lane(ctx context.Context, bot wa.JID) (*Lane, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if lane, ok := l.lanes[bot]; ok {
		return lane, nil
	}
	if l.ctx == nil {
		return nil, errors.New("fastlane: lanes aren't running yet")
	}
	if l.closed || l.ctx.Err() != nil {
		return nil, errors.New("fastlane: shutting down")
	}
	if _, ok := l.client(bot); !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotConnected, bot)
	}
	d := l.base
	// The lane resolves the bot's connection per call: the connector replaces it on reconnects.
	d.Bot, d.Client = bot, liveClient{bot: bot, get: l.client}
	lane, err := New(ctx, d)
	if err != nil {
		return nil, err
	}
	l.lanes[bot] = lane
	l.wg.Go(func() {
		if err := lane.Run(l.ctx); err != nil {
			l.log.Error().Err(err).Str("bot", string(bot)).Msg("fast lane stopped")
		}
	})
	return lane, nil
}

// IdentityMembers answers who sent a message from wp-f's identity service and members table
// (DESIGN.md §7.7, §8.1). The inbox dispatcher has already recorded the sender, so a person
// usually exists; a sender with no membership row is new.
type IdentityMembers struct{ IDs *identity.Service }

// Sender implements pipeline.Members.
func (m IdentityMembers) Sender(ctx context.Context, group wa.JID, who envelope.Identity) (pipeline.Sender, int64, error) {
	var p identity.Person
	found := false
	for _, jid := range []wa.JID{who.LID, who.PN} {
		if jid == "" {
			continue
		}
		var err error
		if p, found, err = m.IDs.MatchPerson(ctx, jid); err != nil {
			return pipeline.Sender{}, 0, err
		}
		if found {
			break
		}
	}
	if !found {
		return pipeline.Sender{}, 0, nil
	}
	mem, ok, err := m.IDs.Member(ctx, group, p.ID)
	if err != nil || !ok {
		return pipeline.Sender{}, int64(p.ID), err
	}
	joined := mem.JoinedAt
	if joined.IsZero() {
		joined = mem.FirstSeen
	}
	rec := policy.MemberRecord{
		LID: string(cmpOr(who.LID, p.LID)), Role: policy.Role(mem.Role), JoinedAt: joined, PreExisting: mem.PreExisting,
		CleanMessages: mem.CleanCount, Allowlisted: mem.Allowlisted, Vouched: mem.Allowlisted,
	}
	return pipeline.Sender{Member: rec, Known: true}, int64(p.ID), nil
}

func cmpOr(a, b wa.JID) wa.JID {
	if a != "" {
		return a
	}
	return b
}

// WakeAll nudges every lane's executor, e.g. after a review action was enqueued.
func (l *Lanes) WakeAll() {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, lane := range l.lanes {
		lane.Wake()
	}
}
