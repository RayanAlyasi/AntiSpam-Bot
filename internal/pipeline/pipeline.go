package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/actions"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/envelope"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/policy"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/scoring"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// Groups gives the moderation settings of a group. ok is false for a chat the bot doesn't
// protect: its messages are never moderated (DESIGN.md §5.1).
type Groups interface {
	Settings(ctx context.Context, group wa.JID) (s policy.Settings, ok bool, err error)
}

// Members says who sent a message: their member record in the group and their person ID
// (DESIGN.md §7.7, §8.1). A sender the bot has never seen is not Known.
type Members interface {
	Sender(ctx context.Context, group wa.JID, who envelope.Identity) (snd Sender, personID int64, err error)
}

// Store runs fn in one database transaction: everything written through the Tx commits or rolls
// back together, so a detection never exists without its actions (DESIGN.md §4.1).
type Store interface {
	InTx(ctx context.Context, fn func(ctx context.Context, tx Tx) error) error
	// Processed reports whether a message version was already handled. Process checks it before
	// analysing, so a redelivered event spends no AI call and no safety-rail budget (the hourly
	// ban cap, the deletion circuit); Tx.Claim stays the authoritative guard inside the
	// transaction.
	Processed(ctx context.Context, group wa.JID, msg wa.MessageID, version string) (bool, error)
}

// Tx is what the fast lane writes in its transaction.
type Tx interface {
	// Claim marks a message version as processed, and reports false when it already was (a
	// redelivered event), so nothing is recorded or enqueued twice. It holds a lock on the key
	// until the transaction ends.
	Claim(ctx context.Context, group wa.JID, msg wa.MessageID, version string) (bool, error)
	// InsertDetection stores a detection row and returns its ID.
	InsertDetection(ctx context.Context, d policy.DetectionRecord) (int64, error)
	// CreateBan records an active local ban, or returns the existing one (created false).
	CreateBan(ctx context.Context, b policy.GroupBan) (policy.GroupBan, bool, error)
	// Enqueue adds a WhatsApp write to the action queue; a repeated dedupe key is a no-op.
	Enqueue(ctx context.Context, a actions.Action) error
	// OpenReview opens an admin review item (DESIGN.md §9.5), whose message is enqueued in the
	// same transaction, so a review is never lost between the detection and its post.
	OpenReview(ctx context.Context, n review.NewItem) error
}

// Reviews opens review items (DESIGN.md §9.5). Opening one enqueues its message on the queue.
// review.Service implements it; stores bind one to their transaction for Tx.OpenReview.
type Reviews interface {
	Open(ctx context.Context, n review.NewItem) (review.Item, error)
}

// Config configures New. Engine, Groups, Members, Store, Bot and Clock are required.
type Config struct {
	Engine      *Engine
	Groups      Groups
	Members     Members
	Store       Store
	Bot         wa.JID // the bot number that acts
	Clock       clock.Clock
	RuleVersion string // recorded on every detection, e.g. the weights version
	// Committed is called after a transaction that enqueued actions commits, e.g. to wake the
	// executor (actions.Executor.Wake). Optional.
	Committed func()
	// Evidence builds the evidence snapshot kept for a deleted message (F-114); keep false skips
	// it. Saved through the Tx when it is an EvidenceTx. Optional.
	Evidence func(e *envelope.Envelope, detectionID int64, at time.Time) (s review.Snapshot, keep bool)
}

// EvidenceTx is a Tx that keeps evidence snapshots of deleted messages, in the detection's
// transaction, so a failure retries the whole message and no snapshot is silently lost.
type EvidenceTx interface {
	SaveEvidence(ctx context.Context, s review.Snapshot) error
}

// Pipeline is the fast lane from an envelope to recorded detections and enqueued actions.
type Pipeline struct {
	c Config
}

// New builds a pipeline.
func New(c Config) (*Pipeline, error) {
	if c.Engine == nil || c.Groups == nil || c.Members == nil || c.Store == nil || c.Bot == "" || c.Clock == nil {
		return nil, errors.New("pipeline: engine, groups, members, store, bot and clock are required")
	}
	return &Pipeline{c: c}, nil
}

// Process runs one group message through the fast lane (DESIGN.md §4, §7.1). at is when the
// message was sent. Messages in unprotected chats come back with route none and nothing done.
// Process is idempotent: a redelivered message returns Duplicate and writes nothing.
func (p *Pipeline) Process(ctx context.Context, e *envelope.Envelope, at time.Time) (Verdict, error) {
	s, ok, err := p.c.Groups.Settings(ctx, e.Group)
	if err != nil {
		return Verdict{}, fmt.Errorf("pipeline: settings of %s: %w", e.Group, err)
	}
	if !ok {
		return Verdict{Group: e.Group, Message: e.MsgID, Sender: senderJID(e), Band: BandClean, Route: RouteNone}, nil
	}
	if done, err := p.c.Store.Processed(ctx, e.Group, e.MsgID, version(e)); err != nil {
		return Verdict{}, fmt.Errorf("pipeline: check %s/%s: %w", e.Group, e.MsgID, err)
	} else if done {
		return Verdict{Group: e.Group, Message: e.MsgID, Sender: senderJID(e), Mode: Mode(s.Mode), Route: RouteNone, Duplicate: true}, nil
	}
	snd, personID, err := p.c.Members.Sender(ctx, e.Group, e.Sender)
	if err != nil {
		return Verdict{}, fmt.Errorf("pipeline: sender of %s/%s: %w", e.Group, e.MsgID, err)
	}
	snd.IsBot = snd.IsBot || isBot(e, p.c.Bot)
	d, err := p.c.Engine.Evaluate(ctx, e, at, s, snd)
	if err != nil {
		return Verdict{}, err
	}
	if at.IsZero() {
		at = p.c.Clock.Now()
	}

	var reviews []review.NewItem
	var detection int64
	enqueued, duplicate := false, false
	err = p.c.Store.InTx(ctx, func(ctx context.Context, tx Tx) error {
		reviews, enqueued, duplicate = nil, false, false // a retried transaction starts over
		first, err := tx.Claim(ctx, e.Group, e.MsgID, version(e))
		if err != nil {
			return fmt.Errorf("claim: %w", err)
		}
		if !first {
			duplicate = true
			return nil
		}
		var pid *int64
		if personID != 0 {
			pid = &personID
		}
		var detID int64
		ref := policy.DetectionRef{Group: e.Group, MsgID: e.MsgID, PersonID: pid, TierReached: tierReached(d), RuleVersion: p.c.RuleVersion}
		if rec, ok := policy.NewDetection(ref, d.Finding, d.Decision, d.Plan); ok {
			if detID, err = tx.InsertDetection(ctx, rec); err != nil {
				return fmt.Errorf("record detection: %w", err)
			}
			detection = detID
		}
		for _, a := range d.Plan.Enqueue {
			switch a.Kind {
			case policy.ActionDelete:
				err = tx.Enqueue(ctx, actions.Action{
					Bot: p.c.Bot, Chat: e.Group, Kind: actions.KindRevoke, Priority: actions.Priority(a.Priority),
					Payload:   actions.Payload{Target: wa.MessageRef{Sender: senderJID(e), ID: e.MsgID}},
					DedupeKey: actions.RevokeKey(e.Group, e.MsgID), MessageTime: at, ReasonDetectionID: detID,
				})
				if err == nil && p.c.Evidence != nil {
					// Every deletion keeps an evidence snapshot, in the same transaction (F-114).
					if ev, ok := tx.(EvidenceTx); ok {
						if snap, keep := p.c.Evidence(e, detID, at); keep {
							if err = ev.SaveEvidence(ctx, snap); err != nil {
								err = fmt.Errorf("save evidence: %w", err)
							}
						}
					}
				}
				enqueued = true
			case policy.ActionBan:
				if personID == 0 {
					// No person record to ban (identity not resolved yet): the bot never bans
					// someone it can't keep track of, so the admins decide instead.
					reviews = append(reviews, reviewItem(e, s, snd, d, policy.Action{
						Kind:   policy.ActionReview,
						Review: &policy.ReviewRequest{Kind: policy.ReviewBanDecision, ProposeBan: true},
					}, detID))
					break
				}
				err = p.ban(ctx, tx, e, s, personID, detID, a.Priority)
				enqueued = true
			case policy.ActionReview:
				reviews = append(reviews, reviewItem(e, s, snd, d, a, detID))
			}
			if err != nil {
				return fmt.Errorf("enqueue %s: %w", a.Kind, err)
			}
		}
		if emoji := shadowReaction(s, d); emoji != "" {
			// Shadow mode changes nothing but shows its judgement (DESIGN.md §8.4): one reaction
			// per message, so an edit (same ID) never gets a second one. It stands in for the
			// action it reports, so it runs at that action's priority.
			if err := tx.Enqueue(ctx, actions.Action{
				Bot: p.c.Bot, Chat: e.Group, Kind: actions.KindReact, Priority: shadowPriority(d),
				Payload:   actions.Payload{Target: wa.MessageRef{Sender: senderJID(e), ID: e.MsgID}, Emoji: emoji, Purpose: actions.PurposeShadow},
				DedupeKey: actions.ShadowReactKey(e.Group, e.MsgID), ReasonDetectionID: detID,
			}); err != nil {
				return fmt.Errorf("enqueue shadow reaction: %w", err)
			}
			enqueued = true
		}
		for _, n := range reviews {
			if err := tx.OpenReview(ctx, n); err != nil {
				return fmt.Errorf("open review: %w", err)
			}
			enqueued = true
		}
		return nil
	})
	if err != nil {
		return Verdict{}, fmt.Errorf("pipeline: write %s/%s: %w", e.Group, e.MsgID, err)
	}
	v := d.Verdict
	v.DetectionID = detection
	if duplicate {
		v.Duplicate, v.Actions = true, nil
		return v, nil
	}
	if enqueued && p.c.Committed != nil {
		p.c.Committed()
	}
	return v, nil
}

// ban records the local ban and enqueues the kick, with the same dedupe key policy.Bans uses, so
// a repeated decision never kicks twice (DESIGN.md §9.1, §9.6 "Local ban").
func (p *Pipeline) ban(ctx context.Context, tx Tx, e *envelope.Envelope, s policy.Settings, personID, detID int64, prio int) error {
	member := senderJID(e)
	if personID == 0 {
		return fmt.Errorf("ban %s in %s: no person ID", member, e.Group)
	}
	det := detID
	b, _, err := tx.CreateBan(ctx, policy.GroupBan{
		Group: e.Group, PersonID: personID, Reason: policy.BanSpam, DecidedBy: policy.DecidedAuto,
		DetectionID: &det, RejoinBlock: s.Enforcement.Ban.RejoinBlock, Active: true, CreatedAt: p.c.Clock.Now(),
	})
	if err != nil {
		return err
	}
	return tx.Enqueue(ctx, actions.Action{
		Bot: p.c.Bot, Chat: e.Group, Kind: actions.KindKick, Priority: actions.Priority(prio),
		Payload:   actions.Payload{JIDs: []wa.JID{member}},
		DedupeKey: policy.KickDedupeKey(e.Group, member, b.ID), ReasonDetectionID: detID,
	})
}

func reviewItem(e *envelope.Envelope, s policy.Settings, snd Sender, d Decision, a policy.Action, detID int64) review.NewItem {
	kind, proposed := review.KindUncertain, review.DecisionDeleteOnly
	if a.Review != nil {
		kind = review.Kind(a.Review.Kind)
		if a.Review.ProposeBan {
			proposed = review.DecisionBan
		}
	}
	var text string
	for _, t := range e.Texts {
		if text != "" {
			text += "\n"
		}
		text += t.Raw
	}
	ev := review.Evidence{
		Text:           text,
		Score:          d.Score.P,
		Source:         string(d.Finding.Source),
		MessageDeleted: d.Decision.Delete,
		WhyAsking:      d.Decision.Reason,
	}
	for _, u := range e.URLs {
		ev.Links = append(ev.Links, u.URL)
	}
	for _, sc := range d.Score.Signals {
		if sc.Counted && !sc.Hard {
			ev.Why = append(ev.Why, review.Reason{Text: reasonText(sc), Weight: sc.Applied})
		}
	}
	return review.NewItem{
		Group:   e.Group,
		Kind:    kind,
		Message: &wa.MessageRef{Sender: senderJID(e), ID: e.MsgID},
		Subject: review.Subject{
			Person: e.Sender.LID, Phone: e.Sender.PN, Name: e.Sender.PushName,
			Established: snd.Known && policy.StandingOf(snd.Member, false, s, d.Finding.At).Established,
			Hacked:      snd.Member.Compromised,
			Admin:       snd.Member.Role == policy.RoleAdmin || snd.Member.Role == policy.RoleSuperAdmin,
		},
		Evidence:    ev,
		Proposed:    proposed,
		DetectionID: detID,
	}
}

// version identifies one version of a message: the same ID with the same content is a
// redelivery; an edit (same ID, new content) is analyzed again (DESIGN.md §5.1).
func version(e *envelope.Envelope) string {
	h := sha256.New() // hash.Hash writes never fail
	_, _ = fmt.Fprintf(h, "%s\x00%t\x00", e.Kind, e.IsEdit)
	for _, t := range e.Texts {
		_, _ = fmt.Fprintf(h, "%s\x00%s\x00", t.Source, t.Raw)
	}
	for _, m := range e.Media {
		h.Write(m.SHA256)
	}
	for _, c := range e.Contacts {
		_, _ = fmt.Fprintf(h, "%v\x00", c.WAIDs)
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// Shadow-mode reactions (DESIGN.md §8.4).
const (
	ShadowCertain = "❌" // the bot would act without asking: auto_ban, delete_only, policy
	ShadowAsk     = "❓" // the bot would ask the admins
)

// shadowReaction is the reaction shadow mode shows on a message, or "" for none: only in a group
// in shadow mode with shadow.reactions on (not an enforce group the safety rails demoted to
// shadow), and never for routes allow or none (a clean message). It goes by the route alone: a
// still-uncertain message has verdict clean but route ask.
func shadowReaction(s policy.Settings, d Decision) string {
	if s.Mode != policy.ModeShadow || d.Plan.Mode != policy.DetectionShadow || !s.Shadow.Reactions {
		return ""
	}
	switch d.Decision.Route {
	case policy.RouteAutoBan, policy.RouteDeleteOnly, policy.RoutePolicy:
		return ShadowCertain
	case policy.RouteAsk:
		return ShadowAsk
	default:
		return ""
	}
}

// shadowPriority is the queue priority of a shadow reaction: that of the most urgent action the
// decision would have taken in enforce mode (the delete, P1, for a certain catch), so the
// reaction shows up when that action would have run. P4 when it would take none.
func shadowPriority(d Decision) actions.Priority {
	prio := actions.P4
	for _, a := range d.Plan.Would {
		if p := actions.Priority(a.Priority); p.Valid() && p < prio {
			prio = p
		}
	}
	return prio
}

func tierReached(d Decision) string {
	if d.Score.Signals == nil {
		return "t0"
	}
	return "t" + fmt.Sprint(int(detect.FastLane))
}

func isBot(e *envelope.Envelope, bot wa.JID) bool {
	return bot != "" && (e.Sender.LID == bot || e.Sender.PN == bot)
}

func reasonText(sc scoring.Scored) string {
	if sc.Evidence != "" {
		return sc.Evidence
	}
	return sc.Code
}
