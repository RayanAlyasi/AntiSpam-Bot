package inbox

import (
	"context"
	"encoding/json"
	"fmt"

	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/zerolog"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// SenderObserver records what a message reveals about its sender: the LID↔PN pair in SenderAlt
// (source sender_alt) or, for a DM, in the DM's addresses (source dm) (DESIGN.md §7.7). It
// returns the sender's persons.id (0 if the sender isn't a person).
type SenderObserver interface {
	ObserveSender(ctx context.Context, info wa.MessageInfo, dm bool) (int64, error)
}

// IndexWriter records message_index rows.
type IndexWriter interface {
	Put(ctx context.Context, e IndexEntry) error
}

// Sink receives routed messages. Each route has its own sink; a nil sink drops that route.
type Sink interface {
	Handle(ctx context.Context, it Item, info wa.MessageInfo, d Decision) error
}

// Sinks are the downstream handlers per route.
type Sinks struct {
	Moderate Sink // protected-group members' messages: detection and actions
	Commands Sink // messages only parsed for commands (also gets RouteModerate items)
	Status   Sink // status posts: person reputation only
	DM       Sink // the command/auth/review handler
}

// Dispatcher is the inbox Handler that routes each message (F-016), records its sender's
// identifiers, writes the message_index row of a moderated group message (F-033), then hands it
// to the route's sinks. No analysis happens here.
type Dispatcher struct {
	router  *Router
	senders SenderObserver
	index   IndexWriter
	sinks   Sinks
	log     zerolog.Logger
}

// NewDispatcher builds the handler. senders and index may be nil.
func NewDispatcher(router *Router, senders SenderObserver, index IndexWriter, sinks Sinks, log zerolog.Logger) *Dispatcher {
	return &Dispatcher{router: router, senders: senders, index: index, sinks: sinks, log: log.With().Str("module", "inbox").Logger()}
}

// infoJSON is the part of whatsmeow's MessageInfo JSON the dispatcher reads. whatsmeow's JIDs
// marshal as strings.
type infoJSON struct {
	Chat           string
	Sender         string
	SenderAlt      string
	IsFromMe       bool
	IsGroup        bool
	AddressingMode string
	ID             string
	PushName       string
	Timestamp      time.Time
	Edit           string
}

// DecodeInfo reads the routing fields of an inbox row's MessageInfo, with JIDs in the same
// canonical form the connector gives live events (wa.CanonicalJID).
func DecodeInfo(raw json.RawMessage) (wa.MessageInfo, error) {
	var j infoJSON
	if err := json.Unmarshal(raw, &j); err != nil {
		return wa.MessageInfo{}, fmt.Errorf("%w: decode message info: %v", ErrPermanent, err)
	}
	jid := func(s string) wa.JID {
		if s == "" {
			return ""
		}
		return wa.CanonicalJID(s)
	}
	return wa.MessageInfo{
		Chat: jid(j.Chat), Sender: jid(j.Sender), SenderAlt: jid(j.SenderAlt), IsFromMe: j.IsFromMe,
		IsGroup: j.IsGroup, AddressingMode: j.AddressingMode, ID: wa.MessageID(j.ID), PushName: j.PushName,
		Timestamp: j.Timestamp, Edit: j.Edit,
	}, nil
}

// HandleInbox implements Handler.
func (d *Dispatcher) HandleInbox(ctx context.Context, it Item) error {
	info, err := DecodeInfo(it.Info)
	if err != nil {
		return err
	}
	dec, err := d.router.Route(ctx, info)
	if err != nil {
		return err // transient: retried, never guessed
	}
	var person int64
	if d.senders != nil && dec.Route != RouteIgnore {
		if person, err = d.senders.ObserveSender(ctx, info, dec.Route == RouteDM); err != nil {
			return fmt.Errorf("observe sender: %w", err)
		}
	}
	// The base index row: sender and time, verdict clean. Detection later fills in the
	// fingerprints and raises the verdict; the upsert never erases either. Edits and revokes are
	// protocol messages about another ID, so they get no row of their own.
	if d.index != nil && dec.Route == RouteModerate && info.Edit == "" {
		at := info.Timestamp
		if at.IsZero() {
			at = it.ReceivedAt
		}
		if err := d.index.Put(ctx, IndexEntry{Group: info.Chat, MsgID: info.ID, SenderPerson: person, At: at, Verdict: VerdictClean}); err != nil {
			return fmt.Errorf("index message: %w", err)
		}
	}
	var sinks []Sink
	switch dec.Route {
	case RouteModerate:
		sinks = []Sink{d.sinks.Moderate, d.sinks.Commands}
	case RouteCommands:
		sinks = []Sink{d.sinks.Commands}
	case RouteStatus:
		sinks = []Sink{d.sinks.Status}
	case RouteDM:
		sinks = []Sink{d.sinks.DM}
	}
	for _, s := range sinks {
		if s == nil {
			continue
		}
		if err := s.Handle(ctx, it, info, dec); err != nil {
			return fmt.Errorf("%s: %w", dec.Route, err)
		}
	}
	d.log.Debug().Str("route", string(dec.Route)).Str("reason", dec.Reason).Str("msg_id", string(info.ID)).Msg("routed")
	return nil
}

func textOf(s string) pgtype.Text { return pgtype.Text{String: s, Valid: s != ""} }
