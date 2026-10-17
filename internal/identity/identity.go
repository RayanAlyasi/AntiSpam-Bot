package identity

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"sync"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// Errors.
var (
	// ErrNotPerson is returned for JIDs that can't identify a person (groups, channels, broadcast, empty).
	ErrNotPerson = errors.New("identity: not a person JID")
	// ErrNoPerson means a person ID doesn't exist (or was merged away).
	ErrNoPerson = errors.New("identity: no such person")
	// ErrConflict means a write would link one identity to two persons.
	ErrConflict = errors.New("identity: identity already linked to another person")
)

// PersonID is the internal persons.id.
type PersonID int64

// Person is a person's current identifiers; LID or PN is empty when unknown.
type Person struct {
	ID  PersonID
	LID wa.JID
	PN  wa.JID
}

// Source is where an identity was learned (identities.source).
type Source string

// Identity sources (DESIGN.md §14).
const (
	SourceSenderAlt   Source = "sender_alt"
	SourceParticipant Source = "participant"
	SourceEvent       Source = "event"
	SourceLIDStore    Source = "lid_store"
	SourceDM          Source = "dm"
	SourceSelfVCard   Source = "self_vcard"
	SourceUsync       Source = "usync"
)

func (s Source) valid() bool {
	switch s {
	case SourceSenderAlt, SourceParticipant, SourceEvent, SourceLIDStore, SourceDM, SourceSelfVCard, SourceUsync:
		return true
	}
	return false
}

// Kind is the kind of an identity row.
type Kind string

// Identity kinds.
const (
	KindLID Kind = "lid"
	KindPN  Kind = "pn"
)

// Identity is one identities row of a person.
type Identity struct {
	Kind        Kind
	Value       wa.JID
	Source      Source
	CountryCode string
	ValidTo     *time.Time // set when the link was closed (a recycled number)
}

// LIDStore resolves LID↔PN pairs; both return "" when unknown. PNForLID reads only what is
// already known locally (whatsmeow's LID store). LIDForPN may also ask WhatsApp (one batched,
// rate-limited usync IQ), which is allowed only in the PN → LID direction (DESIGN.md §7.7).
type LIDStore interface {
	LIDForPN(ctx context.Context, pn wa.JID) (wa.JID, error)
	PNForLID(ctx context.Context, lid wa.JID) (wa.JID, error)
}

// ResolvedEvent is identity.resolved: a PN became linked to a person who has a LID. It triggers
// the back-fill (DESIGN.md §7.7). MergedFrom is the PN-only person merged into Person, or 0.
type ResolvedEvent struct {
	Person     Person
	PN         wa.JID
	MergedFrom PersonID
	Source     Source
}

// RecycledEvent flags a known PN seen with a different LID: the number was probably reassigned.
// The old link is closed and nothing (bans in particular) follows the number to its new owner;
// the operator is told (DESIGN.md §7.7).
type RecycledEvent struct {
	PN        wa.JID
	OldLID    wa.JID
	OldPerson PersonID
	NewLID    wa.JID
	NewPerson PersonID
}

// Events receives identity events after the change that caused them is stored.
type Events interface {
	Resolved(ctx context.Context, ev ResolvedEvent) error
	RecycledNumber(ctx context.Context, ev RecycledEvent) error
}

// Service maps JIDs to persons (DESIGN.md §7.7): the LID is the primary identity, the PN is added
// whenever a free source reveals it, records are merged on resolution and recycled numbers never
// carry history to a new LID. It's safe for concurrent use.
type Service struct {
	store  Store
	lids   LIDStore
	events Events
	clk    clock.Clock
	locks  [64]sync.Mutex // per-JID stripes, so concurrent Ensures of one JID create one person
}

// New returns a Service. lids and events may be nil (no lookups, no events).
func New(store Store, lids LIDStore, events Events, clk clock.Clock) *Service {
	if clk == nil {
		clk = clock.Real{}
	}
	return &Service{store: store, lids: lids, events: events, clk: clk}
}

// MatchPerson looks a JID up by LID or PN alone, among open links, without creating anything or
// asking anyone. Non-person JIDs are simply not found.
func (s *Service) MatchPerson(ctx context.Context, jid wa.JID) (Person, bool, error) {
	norm, kind, ok := normalize(jid)
	if !ok {
		return Person{}, false, nil
	}
	return s.match(ctx, s.store, kind, norm)
}

func (s *Service) match(ctx context.Context, r Reader, kind Kind, jid wa.JID) (Person, bool, error) {
	id, ok, err := r.FindOpen(ctx, kind, jid)
	if err != nil || !ok {
		return Person{}, false, wrap(err, "match %s", jid)
	}
	p, err := r.Person(ctx, id)
	if err != nil {
		return Person{}, false, wrap(err, "match %s", jid)
	}
	return p, true, nil
}

// Person returns a person by ID.
func (s *Service) Person(ctx context.Context, id PersonID) (Person, error) {
	p, err := s.store.Person(ctx, id)
	return p, wrap(err, "person %d", id)
}

// Ensure finds or creates the person for jid; src is how jid was seen. For a new LID it also
// reads the PN from the local LID store. For a new PN it looks the LID up once (LIDForPN), so the
// LID becomes primary right away; a failed or empty lookup still yields a PN-only person.
func (s *Service) Ensure(ctx context.Context, jid wa.JID, src Source) (Person, error) {
	return s.ensure(ctx, jid, src, true)
}

// EnsureKnown is Ensure without the remote PN → LID lookup: it uses only what is already known
// locally. Bulk paths (seeding a group's participants) use it, so joining a large phone-number
// group never fires one usync query per member; those persons gain their LID from SenderAlt or
// participant data later (DESIGN.md §7.7).
func (s *Service) EnsureKnown(ctx context.Context, jid wa.JID, src Source) (Person, error) {
	return s.ensure(ctx, jid, src, false)
}

func (s *Service) ensure(ctx context.Context, jid wa.JID, src Source, remote bool) (Person, error) {
	norm, kind, ok := normalize(jid)
	if !ok {
		return Person{}, fmt.Errorf("ensure %q: %w", jid, ErrNotPerson)
	}
	if !src.valid() {
		return Person{}, fmt.Errorf("ensure %s: unknown source %q", norm, src)
	}
	if p, found, err := s.match(ctx, s.store, kind, norm); err != nil || found {
		return p, err
	}

	mu := s.lock(norm)
	mu.Lock()
	defer mu.Unlock()
	if p, found, err := s.match(ctx, s.store, kind, norm); err != nil || found {
		return p, err // another caller created it while we waited
	}

	// Lookups run outside the store's write, which may hold a database lock.
	var other wa.JID
	if s.lids != nil {
		var err error
		switch kind {
		case KindLID:
			other, err = s.lids.PNForLID(ctx, norm)
		case KindPN:
			if remote {
				other, err = s.lids.LIDForPN(ctx, norm)
			}
		}
		if err != nil {
			other = "" // the record works without the missing identifier (§7.7)
		}
		if o, oKind, ok := normalize(other); ok && oKind != kind {
			other = o
		} else {
			other = ""
		}
	}

	switch {
	case other != "" && kind == KindLID:
		return s.learn(ctx, norm, other, src, SourceLIDStore)
	case other != "" && kind == KindPN:
		return s.learn(ctx, other, norm, SourceUsync, src)
	}
	var p Person
	err := s.store.Write(ctx, func(tx Tx) error {
		if found, ok, err := s.match(ctx, tx, kind, norm); err != nil || ok {
			p = found
			return err
		}
		now := s.clk.Now()
		id, err := tx.CreatePerson(ctx, now)
		if err != nil {
			return err
		}
		p = Person{ID: id}
		if kind == KindLID {
			p.LID = norm
		} else {
			p.PN = norm
		}
		if err := tx.AddIdentity(ctx, id, newIdentity(kind, norm, src), now); err != nil {
			return err
		}
		return tx.SetIdentifiers(ctx, p)
	})
	return p, wrap(err, "ensure %s", norm)
}

// Learn records a LID↔PN pair revealed by one of the free sources (DESIGN.md §7.7: SenderAlt,
// participant lists, group-change senders, GroupInfo refreshes, the LID store, DMs, a confirmed
// self-vCard). It never looks anything up. It returns the LID's person.
func (s *Service) Learn(ctx context.Context, lid, pn wa.JID, src Source) (Person, error) {
	l, lk, ok1 := normalize(lid)
	p, pk, ok2 := normalize(pn)
	if !ok1 || !ok2 {
		return Person{}, fmt.Errorf("learn %q/%q: %w", lid, pn, ErrNotPerson)
	}
	if lk != KindLID || pk != KindPN {
		return Person{}, fmt.Errorf("learn %q/%q: want a LID and a phone number: %w", lid, pn, ErrNotPerson)
	}
	if !src.valid() {
		return Person{}, fmt.Errorf("learn %s: unknown source %q", l, src)
	}
	return s.learn(ctx, l, p, src, src)
}

// learn links lid and pn (both normalized) in one write and emits events after it's stored.
func (s *Service) learn(ctx context.Context, lid, pn wa.JID, lidSrc, pnSrc Source) (Person, error) {
	var (
		out      Person
		resolved *ResolvedEvent
		recycled *RecycledEvent
	)
	err := s.store.Write(ctx, func(tx Tx) error {
		resolved, recycled = nil, nil
		now := s.clk.Now()
		lp, lFound, err := s.match(ctx, tx, KindLID, lid)
		if err != nil {
			return err
		}
		pp, pFound, err := s.match(ctx, tx, KindPN, pn)
		if err != nil {
			return err
		}

		if pFound && lFound && pp.ID == lp.ID {
			out = lp // already linked
			if err := tx.AddIdentity(ctx, lp.ID, newIdentity(KindPN, pn, pnSrc), now); err != nil {
				return err
			}
			return tx.AddIdentity(ctx, lp.ID, newIdentity(KindLID, lid, lidSrc), now)
		}

		if pFound && pp.LID != "" && pp.LID != lid {
			// Recycled number: the PN belongs to another LID. Close that link; never merge.
			if err := tx.CloseIdentity(ctx, KindPN, pn, now); err != nil {
				return err
			}
			pp.PN = ""
			if err := tx.SetIdentifiers(ctx, pp); err != nil {
				return err
			}
			target, err := s.lidPerson(ctx, tx, lp, lFound, lid, lidSrc, now)
			if err != nil {
				return err
			}
			if target, err = s.attachPN(ctx, tx, target, pn, pnSrc, now); err != nil {
				return err
			}
			out = target
			recycled = &RecycledEvent{PN: pn, OldLID: pp.LID, OldPerson: pp.ID, NewLID: lid, NewPerson: target.ID}
			return nil
		}

		var merged PersonID
		var target Person
		switch {
		case pFound && !lFound:
			// PN-only person: it gains its LID, which becomes primary.
			target = pp
			target.LID = lid
			if err := tx.AddIdentity(ctx, target.ID, newIdentity(KindLID, lid, lidSrc), now); err != nil {
				return err
			}
			if err := tx.SetIdentifiers(ctx, target); err != nil {
				return err
			}
		case pFound && lFound:
			// A PN-only record and a LID record are one person: merge into the LID person.
			if err := tx.Merge(ctx, pp.ID, lp.ID, "identity.resolved", now); err != nil {
				return err
			}
			merged = pp.ID
			if target, err = s.attachPN(ctx, tx, lp, pn, pnSrc, now); err != nil {
				return err
			}
		default:
			if target, err = s.lidPerson(ctx, tx, lp, lFound, lid, lidSrc, now); err != nil {
				return err
			}
			if target, err = s.attachPN(ctx, tx, target, pn, pnSrc, now); err != nil {
				return err
			}
		}
		out = target
		resolved = &ResolvedEvent{Person: target, PN: pn, MergedFrom: merged, Source: pnSrc}
		return nil
	})
	if err != nil {
		return Person{}, wrap(err, "learn %s", lid)
	}
	return out, s.emit(ctx, resolved, recycled)
}

// lidPerson returns the LID's person, creating it if needed.
func (s *Service) lidPerson(ctx context.Context, tx Tx, p Person, found bool, lid wa.JID, src Source, now time.Time) (Person, error) {
	if found {
		return p, tx.AddIdentity(ctx, p.ID, newIdentity(KindLID, lid, src), now)
	}
	id, err := tx.CreatePerson(ctx, now)
	if err != nil {
		return Person{}, err
	}
	p = Person{ID: id, LID: lid}
	if err := tx.AddIdentity(ctx, id, newIdentity(KindLID, lid, src), now); err != nil {
		return Person{}, err
	}
	return p, tx.SetIdentifiers(ctx, p)
}

// attachPN makes pn the person's current phone number. A different number the person had before
// is closed: one person has one current number, and the old one must no longer match them.
func (s *Service) attachPN(ctx context.Context, tx Tx, p Person, pn wa.JID, src Source, now time.Time) (Person, error) {
	if p.PN != "" && p.PN != pn {
		if err := tx.CloseIdentity(ctx, KindPN, p.PN, now); err != nil {
			return Person{}, err
		}
	}
	if err := tx.AddIdentity(ctx, p.ID, newIdentity(KindPN, pn, src), now); err != nil {
		return Person{}, err
	}
	p.PN = pn
	return p, tx.SetIdentifiers(ctx, p)
}

func (s *Service) emit(ctx context.Context, resolved *ResolvedEvent, recycled *RecycledEvent) error {
	if s.events == nil {
		return nil
	}
	if resolved != nil {
		if err := s.events.Resolved(ctx, *resolved); err != nil {
			return fmt.Errorf("emit identity.resolved for person %d: %w", resolved.Person.ID, err)
		}
	}
	if recycled != nil {
		if err := s.events.RecycledNumber(ctx, *recycled); err != nil {
			return fmt.Errorf("emit recycled number for person %d: %w", recycled.NewPerson, err)
		}
	}
	return nil
}

// Identities returns a person's identity rows, open and closed.
func (s *Service) Identities(ctx context.Context, id PersonID) ([]Identity, error) {
	ids, err := s.store.Identities(ctx, id)
	return ids, wrap(err, "identities of %d", id)
}

func newIdentity(kind Kind, value wa.JID, src Source) Identity {
	ident := Identity{Kind: kind, Value: value, Source: src}
	if kind == KindPN {
		ident.CountryCode = CountryCode(value)
	}
	return ident
}

func (s *Service) lock(j wa.JID) *sync.Mutex {
	h := fnv.New32a()
	_, _ = h.Write([]byte(j))
	return &s.locks[h.Sum32()%uint32(len(s.locks))]
}

func wrap(err error, format string, args ...any) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf(format+": %w", append(args, err)...)
}
