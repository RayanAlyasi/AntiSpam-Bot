package review

import (
	"context"
	"errors"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// Errors returned by stores and services.
var (
	ErrNotFound  = errors.New("review: not found")
	ErrCodeTaken = errors.New("review: short code already used")
	// ErrAutoBanInAskMode: a timeout must never become a ban the bot isn't allowed to make (§9.5).
	ErrAutoBanInAskMode = errors.New("review: auto_ban is not allowed in ask-admins modes")
	ErrUnknownFallback  = errors.New("review: unknown timeout fallback")
)

// Priority is an action-queue priority (DESIGN.md §10.2).
type Priority int

// Priorities, highest first.
const (
	P0 Priority = iota // raid response
	P1                 // spam and policy deletions, blacklist deletions
	P2                 // bans, join-request rejections
	P3                 // review messages, edits, DMs, reactions
	P4                 // background
)

// BanReason is why an admin banned or kicked someone. Only spam feeds learning and the network (§13.7).
type BanReason string

// Ban reasons.
const (
	ReasonSpam  BanReason = "spam"
	ReasonOther BanReason = "other"
)

// Action is one WhatsApp write, handed to the paced action queue (§10). Nothing in this
// package or its callers writes to WhatsApp directly.
type Action interface{ isAction() }

// Ban is a local ban: kick plus a rejoin block in Group (§9.1, F-077). It runs at P2.
type Ban struct {
	Group    wa.JID
	Target   wa.JID
	Reason   BanReason
	Actor    wa.JID // the admin who decided; empty for the bot itself
	Via      string // command | review | blacklist | timeout
	ReviewID int64
}

// Kick removes Target from Group without a rejoin block. It runs at P2.
type Kick struct {
	Group  wa.JID
	Target wa.JID
	Reason BanReason
	Actor  wa.JID
	Via    string
}

// Unban lifts a local ban's rejoin block. It never re-adds the person. Via scopes it: "blacklist"
// lifts only a block the blacklist placed; "command" (an admin's .unban or .undo) lifts any.
type Unban struct {
	Group  wa.JID
	Target wa.JID
	Actor  wa.JID
	Via    string
}

// Delete revokes a message for everyone.
type Delete struct {
	Group    wa.JID
	Message  wa.MessageRef
	Priority Priority
	Reason   string // spam | blacklist | command | review | timeout
}

// RejectJoin rejects pending join requests at P2.
type RejectJoin struct {
	Group   wa.JID
	Targets []wa.JID
	Reason  string
}

// Send sends a message at P3: a DM, a review item or a post in a review group. When ReviewID
// is set, the queue reports the sent message back through Service.RecordPosted.
type Send struct {
	Chat     wa.JID
	Message  wa.OutgoingMessage
	ReviewID int64
	Purpose  string // review | outcome | reply | onboarding | notice
}

// Edit replaces the text of one of the bot's messages at P3.
type Edit struct {
	Chat wa.JID
	ID   wa.MessageID
	Text string
}

// React sets the bot's reaction on a message at P3.
type React struct {
	Chat   wa.JID
	Target wa.MessageRef
	Emoji  string
}

func (Ban) isAction()        {}
func (Kick) isAction()       {}
func (Unban) isAction()      {}
func (Delete) isAction()     {}
func (RejectJoin) isAction() {}
func (Send) isAction()       {}
func (Edit) isAction()       {}
func (React) isAction()      {}

// Actions enqueues writes on the paced action queue. The adapter to internal/actions lives
// with the wiring; tests use reviewtest.Actions.
type Actions interface {
	Enqueue(ctx context.Context, a Action) error
}

// Admins answers "is this person a current admin of this group", from the participant cache
// refreshed when older than 10 minutes (§9.5).
type Admins interface {
	IsAdmin(ctx context.Context, group, person wa.JID) (bool, error)
}

// Settings returns the review part of a group's settings.
type Settings interface {
	ReviewConfig(ctx context.Context, group wa.JID) (Config, error)
}

// Decided is the outcome written by Store.Decide.
type Decided struct {
	Status   Status // decided or timed_out
	Decision Decision
	By       wa.JID
	ByName   string
	Method   Method
	At       time.Time
}

// Store persists reviews, their posted messages and votes.
type Store interface {
	// Create stores a new item and assigns its ID. It returns ErrCodeTaken if the code exists.
	Create(ctx context.Context, it Item) (Item, error)
	Get(ctx context.Context, id int64) (Item, error)
	ByCode(ctx context.Context, code string) (Item, error)
	// Decide moves a pending item to d.Status atomically. won is false, and the current item is
	// returned unchanged, when the item was no longer pending: the first decision wins.
	Decide(ctx context.Context, id int64, d Decided) (it Item, won bool, err error)
	AddVote(ctx context.Context, v VoteRecord) error
	Votes(ctx context.Context, id int64) ([]VoteRecord, error)
	AddPosted(ctx context.Context, p Posted) error
	PostedFor(ctx context.Context, id int64) ([]Posted, error)
	// ByMessage finds the item one of the bot's review messages belongs to.
	ByMessage(ctx context.Context, chat wa.JID, id wa.MessageID) (Item, error)
	MarkOutcomeEdited(ctx context.Context, chat wa.JID, id wa.MessageID) error
	// Pending lists pending items of a group, oldest first; an empty group lists all.
	Pending(ctx context.Context, group wa.JID) ([]Item, error)
	// Due lists pending items whose deadline is at or before now.
	Due(ctx context.Context, now time.Time) ([]Item, error)
}

// Learner turns admin decisions into learning (§7.5). Learning implements it.
type Learner interface {
	ConfirmSpam(ctx context.Context, c Confirmation) error
	Overturn(ctx context.Context, o Overturn) error
}

// AuditEntry is one audit-log line (§14 audit_log), written with the same dotted keys the
// website uses.
type AuditEntry struct {
	Actor  wa.JID // empty for the bot itself
	Group  wa.JID
	Action string // e.g. "settings.set", "blacklist.add", "review.decide"
	Diff   map[string]any
	At     time.Time
}

// Audit writes the audit log (F-006).
type Audit interface {
	Record(ctx context.Context, e AuditEntry) error
}

// Localizer returns user-facing text in a language, with {name} placeholders filled from vars.
// The default implementation is Texts; the locales package replaces it once it exists.
type Localizer interface {
	T(lang, key string, vars map[string]string) string
}
