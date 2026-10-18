// Package queue carries the review, command and blacklist actions (review.Action) out on the
// paced action queue (internal/actions, DESIGN.md §10) and through local bans (policy.Bans,
// F-077). It is the only bridge from those packages to WhatsApp writes.
package queue

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/actions"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/policy"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// Enqueuer is the part of the action queue the adapter uses; actions.Store implements it.
type Enqueuer interface {
	Enqueue(ctx context.Context, a actions.Action) (actions.EnqueueResult, error)
	Get(ctx context.Context, id int64) (actions.Action, error)
}

// Persons maps a JID to its person (identity, F-020): the LID person, created if needed.
type Persons interface {
	PersonID(ctx context.Context, jid wa.JID) (int64, error)
}

// BlacklistBans remembers which local ban the blacklist placed for a person in a group, by ban
// ID, so removing a number lifts only that ban and never an admin's own (§9.8 Undo), even if
// the blacklist's ban was lifted some other way meanwhile.
type BlacklistBans interface {
	Mark(ctx context.Context, group wa.JID, personID, banID int64) error
	BanID(ctx context.Context, group wa.JID, personID int64) (banID int64, ok bool, err error)
	Forget(ctx context.Context, group wa.JID, personID int64) error
}

// Errors.
var (
	ErrNoPersons = errors.New("queue: bans need the identity port")
	ErrUnknown   = errors.New("queue: unknown action")
)

// Queue implements review.Actions.
type Queue struct {
	Queue Enqueuer
	// BotFor returns the bot number that serves a chat (a group, or the bot a DM goes out from).
	BotFor   func(ctx context.Context, chat wa.JID) (wa.JID, error)
	Bans     *policy.Bans
	BanStore policy.BanStore
	Persons  Persons
	Marks    BlacklistBans
	// RejoinBlock reads enforcement.ban.rejoin_block for a group; nil means always block.
	RejoinBlock func(ctx context.Context, group wa.JID) (bool, error)
	// Posted is told about each review message once the queue has sent it
	// (review.Service.RecordPosted).
	Posted func(ctx context.Context, p review.Posted) error
	Clock  clock.Clock
	// Wake, when set, nudges the executor after an enqueue.
	Wake func()

	mu      sync.Mutex
	posting map[int64]int64 // action ID → review ID, until the send finishes
	seq     atomic.Int64    // makes per-occurrence dedupe causes unique within a process
}

// occurrence names one occurrence of a repeatable action (a kick, a join rejection) for its
// dedupe key: the time plus a sequence number, so two at the same instant stay distinct.
func (q *Queue) occurrence() string {
	return fmt.Sprintf("%d.%d", q.now().UnixNano(), q.seq.Add(1))
}

var _ review.Actions = (*Queue)(nil)

func (q *Queue) now() time.Time {
	if q.Clock == nil {
		return clock.Real{}.Now()
	}
	return q.Clock.Now()
}

func priority(p review.Priority) actions.Priority {
	return actions.Priority(p)
}

func short(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:8])
}

func purpose(p string) actions.Purpose {
	switch p {
	case "review", "outcome":
		return actions.PurposeReview
	case "notice":
		return actions.PurposeNotice
	default:
		return actions.PurposeDM
	}
}

// Enqueue carries out one action.
func (q *Queue) Enqueue(ctx context.Context, a review.Action) error {
	switch a := a.(type) {
	case review.Ban:
		return q.ban(ctx, a)
	case review.Unban:
		return q.unban(ctx, a)
	case review.Kick:
		return q.put(ctx, a.Group, actions.KindKick, actions.P2, actions.Payload{JIDs: []wa.JID{a.Target}},
			actions.KickKey(a.Group, a.Target, "kick:"+q.occurrence()), 0)
	case review.Delete:
		return q.put(ctx, a.Group, actions.KindRevoke, priority(a.Priority), actions.Payload{Target: a.Message},
			actions.RevokeKey(a.Group, a.Message.ID), 0)
	case review.RejectJoin:
		var errs []error
		for _, t := range a.Targets {
			// Each request gets its own cause: a person can ask again after a rejection.
			errs = append(errs, q.put(ctx, a.Group, actions.KindRejectJoin, actions.P2, actions.Payload{JIDs: []wa.JID{t}},
				actions.RejectJoinKey(a.Group, t, a.Reason+":"+q.occurrence()), 0))
		}
		return errors.Join(errs...)
	case review.Send:
		key := fmt.Sprintf("send:%s:%s:%s", a.Chat, a.Purpose, short(a.Message.Text))
		if a.ReviewID != 0 {
			// The review ID leads the key, so OnFinished can map the sent message to its review
			// from the action alone, even after a restart.
			key = reviewKeyPrefix + strconv.FormatInt(a.ReviewID, 10) + ":" + key
		} else {
			key += ":" + q.occurrence()
		}
		return q.put(ctx, a.Chat, actions.KindSend, actions.P3, actions.Payload{
			Text: a.Message.Text, ReplyTo: a.Message.ReplyTo, Image: a.Message.Image, Purpose: purpose(a.Purpose),
		}, key, a.ReviewID)
	case review.Edit:
		return q.put(ctx, a.Chat, actions.KindEdit, actions.P3, actions.Payload{Target: wa.MessageRef{ID: a.ID}, Text: a.Text},
			fmt.Sprintf("edit:%s:%s:%s", a.Chat, a.ID, short(a.Text)), 0)
	case review.React:
		return q.put(ctx, a.Chat, actions.KindReact, actions.P3, actions.Payload{Target: a.Target, Emoji: a.Emoji},
			fmt.Sprintf("react:%s:%s:%s", a.Chat, a.Target.ID, a.Emoji), 0)
	default:
		return fmt.Errorf("%w: %T", ErrUnknown, a)
	}
}

// maxRetries bounds how often put re-enqueues past an action that died (failed, expired or
// refused) under the same dedupe key.
const maxRetries = 5

func (q *Queue) put(ctx context.Context, chat wa.JID, kind actions.Kind, p actions.Priority, pl actions.Payload, key string, reviewID int64) error {
	bot, err := q.BotFor(ctx, chat)
	if err != nil {
		return fmt.Errorf("bot for %s: %w", chat, err)
	}
	k := key
	var res actions.EnqueueResult
	for attempt := 0; ; attempt++ {
		res, err = q.Queue.Enqueue(ctx, actions.Action{Bot: bot, Chat: chat, Kind: kind, Priority: p, Payload: pl, DedupeKey: k})
		if err != nil {
			return fmt.Errorf("enqueue %s in %s: %w", kind, chat, err)
		}
		if res.Dropped != "" {
			return fmt.Errorf("enqueue %s in %s: dropped: %s", kind, chat, res.Dropped)
		}
		if res.Inserted || attempt >= maxRetries {
			break
		}
		// The key exists. A live or done action is the same work; one that died without being
		// cancelled (a sender's own delete cancels a revoke) is tried again under a new key.
		prev, err := q.Queue.Get(ctx, res.ID)
		if err != nil {
			return fmt.Errorf("existing %s action %d: %w", kind, res.ID, err)
		}
		if !prev.Status.Final() || prev.Status == actions.StatusDone || prev.Status == actions.StatusCancelled {
			break
		}
		if prev.Status == actions.StatusExpired && (kind == actions.KindRevoke || kind == actions.KindEdit) {
			// Past WhatsApp's delete or edit window: another try would only waste a write.
			break
		}
		k = fmt.Sprintf("%s:retry%d", key, attempt+1)
	}
	if reviewID != 0 && res.Inserted {
		q.mu.Lock()
		if q.posting == nil {
			q.posting = map[int64]int64{}
		}
		q.posting[res.ID] = reviewID
		q.mu.Unlock()
	}
	if q.Wake != nil {
		q.Wake()
	}
	return nil
}

func banReason(r review.BanReason) policy.BanReason {
	if r == review.ReasonSpam {
		return policy.BanSpam
	}
	return policy.BanOther
}

// ErrNoBanStore is returned for blacklist bans and unbans without the stores that tell the
// blacklist's own bans apart.
var ErrNoBanStore = errors.New("queue: blacklist bans need the ban store and the marks")

func (q *Queue) ban(ctx context.Context, b review.Ban) error {
	if q.Persons == nil || q.Bans == nil {
		return ErrNoPersons
	}
	if q.BanStore == nil || (b.Via == "blacklist" && q.Marks == nil) {
		return ErrNoBanStore
	}
	pid, err := q.Persons.PersonID(ctx, b.Target)
	if err != nil {
		return fmt.Errorf("person for %s: %w", b.Target, err)
	}
	block := true
	if q.RejoinBlock != nil && b.Via != "blacklist" {
		// A blacklist ban always keeps the person out (§9.8): only other bans follow the setting.
		if block, err = q.RejoinBlock(ctx, b.Group); err != nil {
			return fmt.Errorf("rejoin block setting: %w", err)
		}
	}
	decided := policy.DecidedAdmin
	if b.Actor == "" {
		decided = policy.DecidedAuto
	}
	req := policy.BanRequest{Group: b.Group, PersonID: pid, Member: b.Target, Reason: banReason(b.Reason), DecidedBy: decided, RejoinBlock: block}
	var errs []error
	if b.Actor != "" {
		dp, err := q.Persons.PersonID(ctx, b.Actor)
		if err != nil {
			errs = append(errs, fmt.Errorf("decider for ban of %s: %w", b.Target, err))
		} else {
			req.DeciderPerson = &dp
		}
	}
	if b.ReviewID != 0 {
		id := b.ReviewID
		req.ReviewID = &id
	}
	prev, had, err := q.BanStore.ActiveBan(ctx, b.Group, pid)
	if err != nil {
		return fmt.Errorf("active ban: %w", err)
	}
	ban, err := q.Bans.Ban(ctx, req)
	if err != nil {
		return errors.Join(append(errs, fmt.Errorf("ban %s in %s: %w", b.Target, b.Group, err))...)
	}
	if had && ban.ID == prev.ID {
		// Already banned and back in the group (a rejoin, or a message while still a member):
		// Bans reuses the first kick's dedupe key, so this occurrence needs its own kick.
		errs = append(errs, q.put(ctx, b.Group, actions.KindKick, actions.P2, actions.Payload{JIDs: []wa.JID{b.Target}},
			actions.KickKey(b.Group, b.Target, fmt.Sprintf("reban%d:%s", ban.ID, q.occurrence())), 0))
	} else if b.Via == "blacklist" {
		// Only a ban the blacklist created is the blacklist's to lift later.
		errs = append(errs, q.Marks.Mark(ctx, b.Group, pid, ban.ID))
	}
	return errors.Join(errs...)
}

// ownedByBlacklist reports whether the person's active ban in group is one the blacklist made.
func (q *Queue) ownedByBlacklist(ctx context.Context, group wa.JID, pid int64) (bool, error) {
	active, ok, err := q.BanStore.ActiveBan(ctx, group, pid)
	if err != nil || !ok {
		return false, err
	}
	banID, marked, err := q.Marks.BanID(ctx, group, pid)
	if err != nil {
		return false, fmt.Errorf("blacklist ban lookup: %w", err)
	}
	return marked && banID == active.ID, nil
}

func (q *Queue) unban(ctx context.Context, u review.Unban) error {
	if q.Persons == nil || q.Bans == nil {
		return ErrNoPersons
	}
	pid, err := q.Persons.PersonID(ctx, u.Target)
	if err != nil {
		return fmt.Errorf("person for %s: %w", u.Target, err)
	}
	liftedBy := "command"
	if u.Via == "blacklist" {
		if q.BanStore == nil || q.Marks == nil {
			return ErrNoBanStore
		}
		owns, err := q.ownedByBlacklist(ctx, u.Group, pid)
		if err != nil || !owns {
			// Not the blacklist's ban: an admin's own ban stays (§9.8 Undo).
			return err
		}
		liftedBy = "blacklist"
	}
	if _, err := q.Bans.Unban(ctx, u.Group, pid, liftedBy); err != nil {
		return fmt.Errorf("unban %s in %s: %w", u.Target, u.Group, err)
	}
	if q.Marks != nil {
		if err := q.Marks.Forget(ctx, u.Group, pid); err != nil {
			return fmt.Errorf("forget blacklist ban: %w", err)
		}
	}
	return nil
}

// reviewKeyPrefix starts the dedupe key of a review post: "review:<id>:send:…".
const reviewKeyPrefix = "review:"

// reviewOf returns the review a send action posted, from its dedupe key.
func reviewOf(a actions.Action) (int64, bool) {
	rest, ok := strings.CutPrefix(a.DedupeKey, reviewKeyPrefix)
	if !ok || a.Kind != actions.KindSend {
		return 0, false
	}
	idText, _, _ := strings.Cut(rest, ":")
	id, err := strconv.ParseInt(idText, 10, 64)
	return id, err == nil && id > 0
}

// OnFinished is the action queue's completion hook (actions.Deps.OnFinished): a review post
// that went out is mapped to its review, so reactions and replies on it find the review
// (§9.5 review_messages). It runs on the executor goroutine and is quick; a repeat call is
// harmless because RecordPosted ignores a message it already knows.
func (q *Queue) OnFinished(ctx context.Context, a actions.Action) error {
	id, ok := reviewOf(a)
	if !ok || a.Status != actions.StatusDone || a.Result.MessageID == "" || q.Posted == nil {
		return nil
	}
	if err := q.Posted(ctx, review.Posted{ReviewID: id, Chat: a.Chat, ID: a.Result.MessageID, SentAt: a.UpdatedAt}); err != nil {
		return err // Sweep still has it and tries again
	}
	q.mu.Lock()
	delete(q.posting, a.ID)
	q.mu.Unlock()
	return nil
}

// Sweep reports finished review posts to Posted, so reactions and replies on them find their
// review. Call it periodically; it forgets sends that failed, expired or were cancelled.
func (q *Queue) Sweep(ctx context.Context) error {
	q.mu.Lock()
	pending := make(map[int64]int64, len(q.posting))
	for a, r := range q.posting {
		pending[a] = r
	}
	q.mu.Unlock()
	var errs []error
	for actionID, reviewID := range pending {
		a, err := q.Queue.Get(ctx, actionID)
		if errors.Is(err, actions.ErrNotFound) {
			q.mu.Lock()
			delete(q.posting, actionID)
			q.mu.Unlock()
			continue
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("action %d: %w", actionID, err))
			continue
		}
		switch a.Status {
		case actions.StatusPending, actions.StatusRunning:
			continue
		case actions.StatusDone:
			if a.Result.MessageID != "" && q.Posted != nil {
				if err := q.Posted(ctx, review.Posted{ReviewID: reviewID, Chat: a.Chat, ID: a.Result.MessageID, SentAt: a.UpdatedAt}); err != nil {
					errs = append(errs, err)
					continue
				}
			}
		case actions.StatusFailed, actions.StatusExpired, actions.StatusCancelled, actions.StatusRefused:
			// Never sent: there is nothing to map.
		}
		q.mu.Lock()
		delete(q.posting, actionID)
		q.mu.Unlock()
	}
	return errors.Join(errs...)
}

// MemoryMarks is an in-memory BlacklistBans, safe for concurrent use.
type MemoryMarks struct {
	mu sync.Mutex
	m  map[string]int64
}

var _ BlacklistBans = (*MemoryMarks)(nil)

func markKey(group wa.JID, personID int64) string {
	return string(group) + "|" + strconv.FormatInt(personID, 10)
}

// Mark records the blacklist's ban.
func (m *MemoryMarks) Mark(_ context.Context, group wa.JID, personID, banID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.m == nil {
		m.m = map[string]int64{}
	}
	m.m[markKey(group, personID)] = banID
	return nil
}

// BanID returns the ban the blacklist placed, if any.
func (m *MemoryMarks) BanID(_ context.Context, group wa.JID, personID int64) (int64, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.m[markKey(group, personID)]
	return id, ok, nil
}

// Forget drops the record.
func (m *MemoryMarks) Forget(_ context.Context, group wa.JID, personID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.m, markKey(group, personID))
	return nil
}

// ProtectedFrom adapts the queue's group state to review.Deps.Protected, so the review service
// refuses to post into a protected group even if settings point there (§9.7).
func ProtectedFrom(g actions.Groups) func(ctx context.Context, chat wa.JID) (bool, error) {
	return func(ctx context.Context, chat wa.JID) (bool, error) {
		p, err := g.Posting(ctx, chat)
		if err != nil {
			return false, fmt.Errorf("posting state of %s: %w", chat, err)
		}
		return p.Protected, nil
	}
}
