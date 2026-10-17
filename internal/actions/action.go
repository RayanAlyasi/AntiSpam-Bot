package actions

import (
	"errors"
	"fmt"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// Priority orders actions: P0 runs first (DESIGN.md §10.2).
type Priority int

// Priorities, highest first.
const (
	P0 Priority = iota // raid response (lockdown, batch kick), delete of a high-confidence scam
	P1                 // spam and policy deletions
	P2                 // bans (kicks), join-request rejections
	P3                 // review messages, edits, reactions, DMs, OTPs, notifications
	P4                 // background work, retro-cleanup
)

// Valid reports whether p is one of P0–P4.
func (p Priority) Valid() bool { return p >= P0 && p <= P4 }

func (p Priority) String() string { return fmt.Sprintf("P%d", int(p)) }

// Kind is what an action makes WhatsApp do.
type Kind string

// Action kinds. Every one is a WhatsApp write.
const (
	KindRevoke       Kind = "revoke"        // delete a message for everyone (Payload.Target)
	KindKick         Kind = "kick"          // remove participants from a group (Payload.JIDs); coalesced
	KindRejectJoin   Kind = "reject_join"   // reject pending join requests (Payload.JIDs); coalesced
	KindSend         Kind = "send"          // send a message (Payload.Text, Variants, ReplyTo, Image)
	KindEdit         Kind = "edit"          // edit one of the bot's messages (Payload.Target.ID, Text)
	KindReact        Kind = "react"         // react to a message (Payload.Target, Emoji)
	KindAnnounce     Kind = "announce"      // lockdown: only admins can send (Payload.On)
	KindJoinApproval Kind = "join_approval" // lockdown: admins approve new members (Payload.On)
	KindResetInvite  Kind = "reset_invite"  // lockdown: revoke the invite link
)

// Kinds lists every action kind.
var Kinds = []Kind{
	KindRevoke, KindKick, KindRejectJoin, KindSend, KindEdit, KindReact,
	KindAnnounce, KindJoinApproval, KindResetInvite,
}

// Valid reports whether k is a known kind.
func (k Kind) Valid() bool {
	for _, known := range Kinds {
		if k == known {
			return true
		}
	}
	return false
}

// Coalesced reports whether actions of this kind for one group merge into batched calls.
func (k Kind) Coalesced() bool { return k == KindKick || k == KindRejectJoin }

// Posts reports whether the action makes something visible in the chat (a message, an edit or
// a reaction). Posts in protected groups are refused while the group is silent (DESIGN.md §9.7).
func (k Kind) Posts() bool { return k == KindSend || k == KindEdit || k == KindReact }

// NeedsAdmin reports whether the bot must be a group admin for the action.
func (k Kind) NeedsAdmin() bool {
	switch k {
	case KindRevoke, KindKick, KindRejectJoin, KindAnnounce, KindJoinApproval, KindResetInvite:
		return true
	default:
		return false
	}
}

// Lockdown reports whether the kind is part of a lockdown (DESIGN.md §9.4).
func (k Kind) Lockdown() bool {
	return k == KindAnnounce || k == KindJoinApproval || k == KindResetInvite
}

// Purpose says what a send is for. OTPs and reviews have reserved sub-budgets (DESIGN.md §10.3).
type Purpose string

// Send purposes.
const (
	PurposeDM      Purpose = "dm"      // command replies, onboarding
	PurposeOTP     Purpose = "otp"     // website login codes
	PurposeReview  Purpose = "review"  // review items and their follow-ups (DESIGN.md §9.5)
	PurposeNotice  Purpose = "notice"  // admin and operator notifications
	PurposeWarning Purpose = "warning" // optional warnings (ladder, silent off)
)

// Status is where an action is in its life.
type Status string

// Action statuses. Pending and running are live; the rest are final.
const (
	StatusPending   Status = "pending"
	StatusRunning   Status = "running"
	StatusDone      Status = "done"
	StatusFailed    Status = "failed"    // permanent error, or out of attempts
	StatusExpired   Status = "expired"   // its deadline passed before it could run
	StatusCancelled Status = "cancelled" // cancelled, e.g. the sender deleted the message first
	StatusRefused   Status = "refused"   // refused by the queue: a post in a silent protected group
)

// Final reports whether the status can't change any more.
func (s Status) Final() bool { return s != StatusPending && s != StatusRunning }

// Action is one queued WhatsApp write (DESIGN.md §10.1).
type Action struct {
	ID       int64
	Bot      wa.JID // the bot number that performs it
	Chat     wa.JID // the group, or the user's chat for a DM
	Kind     Kind
	Priority Priority
	Payload  Payload
	// DedupeKey is unique across all actions ever enqueued: a second enqueue with the same key
	// is a no-op. It must name the cause when a repeat is legitimate (e.g. KickKey).
	DedupeKey string
	// NotBefore is the earliest time the action may run; zero means at once.
	NotBefore time.Time
	// Deadline is when the action stops mattering; it is dropped as expired after it. Zero picks
	// the kind's default (see DefaultDeadline).
	Deadline time.Time
	// MessageTime is when the targeted message was sent (revoke). Deletes run newest
	// first, and a delete's default deadline is MessageTime plus WhatsApp's ~48 h window.
	MessageTime time.Time
	// ReasonDetectionID links the action to the detection that caused it (0 for none).
	ReasonDetectionID int64

	// Set by the queue.
	Status    Status
	Attempts  int
	CreatedAt time.Time
	UpdatedAt time.Time
	LastError string
	Result    Result
}

// Payload holds the arguments of an action; which fields apply depends on the kind.
type Payload struct {
	Target   wa.MessageRef  `json:",omitzero"`  // revoke, react: the message; edit: Target.ID is the bot's own message
	JIDs     []wa.JID       `json:",omitempty"` // kick, reject_join
	Text     string         `json:",omitempty"` // send, edit
	Variants []string       `json:",omitempty"` // send: alternative wordings of Text
	ReplyTo  *wa.MessageRef `json:",omitempty"` // send: quote this message
	Image    *wa.Image      `json:",omitempty"` // send: optional image; the text is its caption
	Emoji    string         `json:",omitempty"` // react: empty removes the reaction
	On       bool           `json:",omitempty"` // announce, join_approval
	Purpose  Purpose        `json:",omitempty"` // send
}

// Result is what an executed action returned.
type Result struct {
	MessageID    wa.MessageID         `json:",omitempty"` // send: the sent message
	SentText     string               `json:",omitempty"` // send: the variant that went out
	InviteLink   string               `json:",omitempty"` // reset_invite: the new link
	Participants []ParticipantOutcome `json:",omitempty"` // kick, reject_join: per-JID outcome
}

// ParticipantOutcome is the result of a kick or join-request rejection for one JID.
type ParticipantOutcome struct {
	JID   wa.JID
	Error string `json:",omitempty"` // empty on success
	Gone  bool   `json:",omitempty"` // wasn't in the group (or had no request): counts as done
}

// EnqueueResult says what Enqueue did.
type EnqueueResult struct {
	ID       int64
	Inserted bool   // false: an action with the same dedupe key exists (ID is that action)
	Dropped  string // non-empty: the action wasn't stored, and why (e.g. its deadline already passed)
}

// Errors returned by the queue.
var (
	// ErrInvalid wraps every validation failure of an enqueued action.
	ErrInvalid = errors.New("actions: invalid action")
	// ErrNotFound is returned for an unknown action ID.
	ErrNotFound = errors.New("actions: action not found")
	// ErrExecutorRunning is returned by Executor.Run when another executor already serves the bot.
	ErrExecutorRunning = errors.New("actions: an executor is already running for this bot")
)

// WhatsApp's delete-for-everyone window, and the default deadlines per kind (DESIGN.md §10.4).
const (
	RevokeWindow         = 48 * time.Hour
	DefaultEditWindow    = 15 * time.Minute // WhatsApp allows edits for about 20 minutes
	DefaultWarningWindow = 10 * time.Minute // an optional warning older than this is pointless
	DefaultLockdownTTL   = time.Hour
	DefaultTTL           = 24 * time.Hour
)

// DefaultDeadline is the deadline an action gets when it's enqueued without one, at now:
//   - revoke: MessageTime (or now, if unset) plus RevokeWindow;
//   - edit: now plus DefaultEditWindow;
//   - a send with PurposeWarning: now plus DefaultWarningWindow;
//   - lockdown kinds: now plus DefaultLockdownTTL;
//   - everything else: now plus DefaultTTL.
func DefaultDeadline(a Action, now time.Time) time.Time {
	switch {
	case a.Kind == KindRevoke:
		base := a.MessageTime
		if base.IsZero() {
			base = now
		}
		return base.Add(RevokeWindow)
	case a.Kind == KindEdit:
		return now.Add(DefaultEditWindow)
	case a.Kind == KindSend && a.Payload.Purpose == PurposeWarning:
		return now.Add(DefaultWarningWindow)
	case a.Kind.Lockdown():
		return now.Add(DefaultLockdownTTL)
	default:
		return now.Add(DefaultTTL)
	}
}

// RevokeKey is the dedupe key of the delete of message id in chat (DESIGN.md §10.4). A REVOKE
// event from the sender cancels the action with this key.
func RevokeKey(chat wa.JID, id wa.MessageID) string { return fmt.Sprintf("revoke:%s:%s", chat, id) }

// KickKey is the dedupe key of the removal of jid from group because of cause (a ban or
// detection ID), so a later, separate ban of the same person is not mistaken for a duplicate.
func KickKey(group, jid wa.JID, cause string) string {
	return fmt.Sprintf("kick:%s:%s:%s", group, jid, cause)
}

// RejectJoinKey is the dedupe key of the rejection of jid's join request to group because of cause.
func RejectJoinKey(group, jid wa.JID, cause string) string {
	return fmt.Sprintf("reject_join:%s:%s:%s", group, jid, cause)
}
