package policy

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// BanReason is why someone was banned (group_bans.reason). Only spam bans can ever leave the
// group they happened in (DESIGN.md §9.6).
type BanReason string

// Ban reasons.
const (
	BanSpam   BanReason = "spam"
	BanOther  BanReason = "other"
	BanPolicy BanReason = "policy"
)

// BanDecider is who decided a ban (group_bans.decided_by).
type BanDecider string

// Ban deciders.
const (
	DecidedAuto    BanDecider = "auto"
	DecidedAdmin   BanDecider = "admin"
	DecidedNetwork BanDecider = "network"
)

// GroupBan is a local ban: kick from this group and, with RejoinBlock, keep them out of this
// group only (DESIGN.md §9.6 "Local ban").
type GroupBan struct {
	ID            int64
	Group         wa.JID
	PersonID      int64
	Reason        BanReason
	DecidedBy     BanDecider
	DeciderPerson *int64
	DetectionID   *int64
	ReviewID      *int64
	RejoinBlock   bool
	Active        bool
	CreatedAt     time.Time
	LiftedBy      string
	LiftedAt      time.Time
}

// BanStore keeps group_bans. Implementations live in the store package.
type BanStore interface {
	// CreateBan records an active ban. If the person already has an active ban in the group,
	// it returns that one and created=false, so a repeated decision never doubles it.
	CreateBan(ctx context.Context, b GroupBan) (ban GroupBan, created bool, err error)
	// ActiveBan returns the person's active ban in the group, if any.
	ActiveBan(ctx context.Context, group wa.JID, personID int64) (GroupBan, bool, error)
	// LiftBan deactivates the person's active ban in the group and reports whether there was one.
	LiftBan(ctx context.Context, group wa.JID, personID int64, liftedBy string, at time.Time) (bool, error)
}

// Kicker enqueues a participant removal on the action queue (DESIGN.md §10). Removals of the
// same member in the same group share the dedupe key, so they happen once.
type Kicker interface {
	Kick(ctx context.Context, group, member wa.JID, priority int, dedupeKey string) error
}

// Bans carries out local bans: record, kick through the queue, block rejoin, unban.
type Bans struct {
	store BanStore
	kick  Kicker
	clock clock.Clock
}

// NewBans returns a ban service over store and the queue's kicker.
func NewBans(store BanStore, kick Kicker, c clock.Clock) *Bans {
	return &Bans{store: store, kick: kick, clock: c}
}

// BanRequest is one ban decision.
type BanRequest struct {
	Group         wa.JID
	PersonID      int64
	Member        wa.JID // the identity the group addresses the member by
	Reason        BanReason
	DecidedBy     BanDecider
	DeciderPerson *int64
	DetectionID   *int64
	ReviewID      *int64
	RejoinBlock   bool // enforcement.ban.rejoin_block
}

// ErrInvalidBan is returned for a ban request without a group, person, member or a known
// reason and decider.
var ErrInvalidBan = errors.New("invalid ban")

// KickDedupeKey is the queue dedupe key for the kick that carries out ban banID, so a repeated
// decision on the same ban kicks once while a later re-ban (a new ban ID) kicks again.
func KickDedupeKey(group, member wa.JID, banID int64) string {
	return fmt.Sprintf("kick:%s:%s:ban%d", group, member, banID)
}

// Ban records a local ban and enqueues the kick at P2 (DESIGN.md §10.2). Banning someone who
// is already banned in the group enqueues the same deduplicated kick and records nothing new.
// The record and the enqueue are two steps: if the enqueue fails the error is returned with
// the recorded ban, and the caller retries (the retry finds the ban and re-enqueues).
func (b *Bans) Ban(ctx context.Context, r BanRequest) (GroupBan, error) {
	if r.Group == "" || r.PersonID == 0 || r.Member == "" {
		return GroupBan{}, fmt.Errorf("%w: group, person and member are required", ErrInvalidBan)
	}
	if err := oneOf("reason", r.Reason, BanSpam, BanOther, BanPolicy); err != nil {
		return GroupBan{}, fmt.Errorf("%w: %w", ErrInvalidBan, err)
	}
	if err := oneOf("decided_by", r.DecidedBy, DecidedAuto, DecidedAdmin, DecidedNetwork); err != nil {
		return GroupBan{}, fmt.Errorf("%w: %w", ErrInvalidBan, err)
	}
	ban, _, err := b.store.CreateBan(ctx, GroupBan{
		Group: r.Group, PersonID: r.PersonID, Reason: r.Reason, DecidedBy: r.DecidedBy,
		DeciderPerson: r.DeciderPerson, DetectionID: r.DetectionID, ReviewID: r.ReviewID,
		RejoinBlock: r.RejoinBlock, Active: true, CreatedAt: b.clock.Now(),
	})
	if err != nil {
		return GroupBan{}, fmt.Errorf("record ban in %s: %w", r.Group, err)
	}
	if err := b.kick.Kick(ctx, r.Group, r.Member, P2, KickDedupeKey(r.Group, r.Member, ban.ID)); err != nil {
		return ban, fmt.Errorf("enqueue kick in %s: %w", r.Group, err)
	}
	return ban, nil
}

// Join is a person joining a group (events.GroupInfo Join, or a participant refresh).
type Join struct {
	Group        wa.JID
	PersonID     int64
	Member       wa.JID
	AddedByAdmin bool      // a current admin of the group re-added them deliberately (not by invite link)
	At           time.Time // when the join happened (the event's timestamp); zero means now
}

// OnJoin enforces the rejoin block: a person with an active ban that blocks rejoining is
// kicked at P0. If a current admin deliberately re-added them, that's the admins' own
// decision: the ban is lifted instead and nobody is kicked. It reports whether a kick was
// enqueued.
func (b *Bans) OnJoin(ctx context.Context, j Join) (bool, error) {
	ban, ok, err := b.store.ActiveBan(ctx, j.Group, j.PersonID)
	if err != nil {
		return false, fmt.Errorf("look up ban in %s: %w", j.Group, err)
	}
	if !ok || !ban.RejoinBlock {
		return false, nil
	}
	if j.AddedByAdmin {
		if _, err := b.store.LiftBan(ctx, j.Group, j.PersonID, "admin_readd", b.clock.Now()); err != nil {
			return false, fmt.Errorf("lift ban after admin re-add in %s: %w", j.Group, err)
		}
		return false, nil
	}
	// The dedupe key includes the join time, so a second rejoin later is kicked again while a
	// duplicate event for the same join is not.
	at := j.At
	if at.IsZero() {
		at = b.clock.Now()
	}
	key := fmt.Sprintf("%s:rejoin:%d", KickDedupeKey(j.Group, j.Member, ban.ID), at.Unix())
	if err := b.kick.Kick(ctx, j.Group, j.Member, P0, key); err != nil {
		return false, fmt.Errorf("enqueue rejoin kick in %s: %w", j.Group, err)
	}
	return true, nil
}

// Unban lifts a person's ban in a group. It never re-adds them: adding someone is unsolicited
// and depends on their privacy settings (DESIGN.md §9.6). It reports whether a ban was lifted.
func (b *Bans) Unban(ctx context.Context, group wa.JID, personID int64, liftedBy string) (bool, error) {
	ok, err := b.store.LiftBan(ctx, group, personID, liftedBy, b.clock.Now())
	if err != nil {
		return false, fmt.Errorf("lift ban in %s: %w", group, err)
	}
	return ok, nil
}
