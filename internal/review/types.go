package review

import (
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// Kind is what a review item asks about (DESIGN.md §9.5, §14 reviews.kind).
type Kind string

// Review kinds.
const (
	KindBanDecision     Kind = "ban_decision"     // spam already handled; admins decide on the ban
	KindUncertain       Kind = "uncertain"        // "have a look": suspicious, not deleted
	KindHacked          Kind = "hacked"           // possibly compromised account (§8.7)
	KindAskBefore       Kind = "ask_before"       // ask-admins mode (b): nothing done yet
	KindNetworkIncoming Kind = "network_incoming" // a network or instance-wide entry in an ask-admins group
	KindAppeal          Kind = "appeal"
	KindAlert           Kind = "alert" // notify only, no decision
)

// Decision is what an admin (or the timeout) decided (§14 reviews.decision).
type Decision string

// Decisions. Reply keywords "ban", "kick", "طرد" and "حظر" all mean DecisionBan (§9.5).
const (
	DecisionBan        Decision = "ban"         // spam: delete if still visible, ban, learn
	DecisionNotSpam    Decision = "not_spam"    // overturn: restore trust, retract learning (§7.5)
	DecisionDeleteOnly Decision = "delete_only" // delete if still visible, no ban
	DecisionClear      Decision = "clear"       // hacked account kept: close without a ban (§8.7)
	DecisionMute       Decision = "mute"        // only with the optional ladder (§9.3)
	DecisionIgnore     Decision = "ignore"      // timeout "dismiss"
)

// Status is a review's lifecycle state.
type Status string

// Statuses.
const (
	StatusPending   Status = "pending"
	StatusDecided   Status = "decided"
	StatusTimedOut  Status = "timed_out"
	StatusCancelled Status = "cancelled"
)

// Method is how a decision or vote arrived.
type Method string

// Methods.
const (
	MethodReaction Method = "reaction"
	MethodReply    Method = "reply"
	MethodWebsite  Method = "website"
	MethodCommand  Method = "command"
	MethodTimeout  Method = "timeout"
)

// OnTimeout is the fallback applied when nobody decides in time (§9.5 Timeouts).
type OnTimeout string

// Timeout fallbacks.
const (
	TimeoutKeepDeletedNoBan OnTimeout = "keep_deleted_no_ban"
	TimeoutDeleteNoBan      OnTimeout = "delete_no_ban"
	TimeoutAutoBan          OnTimeout = "auto_ban"
	TimeoutDismiss          OnTimeout = "dismiss"
)

// Destination is where a group's review items go (§9.5). DM reviewers arrive in v0.3.
type Destination string

// Destinations.
const (
	DestinationDashboard Destination = "dashboard"
	DestinationGroup     Destination = "group"
	DestinationDM        Destination = "dm"
)

// EnforcementMode mirrors the enforcement.mode setting (§8.3).
type EnforcementMode string

// Enforcement modes.
const (
	ModeAuto            EnforcementMode = "auto"
	ModeAskDeleteFirst  EnforcementMode = "ask_delete_first"
	ModeAskBeforeAction EnforcementMode = "ask_before_action"
)

// AskMode reports whether m is one of the always-ask-admins modes, which never auto-ban.
func (m EnforcementMode) AskMode() bool {
	return m == ModeAskDeleteFirst || m == ModeAskBeforeAction
}

// Default timeouts (§9.5): 6 h, or 15 min in ask-admins mode (b).
const (
	DefaultTimeout          = 6 * time.Hour
	DefaultAskBeforeTimeout = 15 * time.Minute
	// EditWindow is how long WhatsApp lets the bot edit its own message.
	EditWindow = 20 * time.Minute
	// MaxEvidenceText is the length review text is truncated to.
	MaxEvidenceText = 300
)

// Subject is the person a review is about.
type Subject struct {
	Person          wa.JID // the LID when known
	Phone           wa.JID // phone-number JID when known; shown masked
	Name            string // push name
	MemberFor       time.Duration
	EarlierMessages int
	Established     bool // routing only (§8.1): never auto-banned
	Hacked          bool // possibly compromised (§8.7)
	Admin           bool // the bot never acts on admins (§8.6)
}

// Reason is one line of "why": a signal and its weight.
type Reason struct {
	Text   string
	Weight float64
}

// Evidence is what the review message shows. Text is the raw message text; rendering redacts
// and truncates it.
type Evidence struct {
	Text           string
	Links          []string
	Why            []Reason
	Score          float64
	Source         string // known | rules | ai
	MessageDeleted bool
	WhyAsking      string
	Thumbnail      *wa.Image // inline thumbnail, never full media
}

// NewItem is a request to open a review.
type NewItem struct {
	Group       wa.JID
	GroupName   string
	Kind        Kind
	Subject     Subject
	Message     *wa.MessageRef // the message under review, if any
	Evidence    Evidence
	Proposed    Decision
	DetectionID int64
}

// Item is a stored review.
type Item struct {
	NewItem
	ID        int64
	Code      string // short code such as K7Q2
	Status    Status
	Mode      EnforcementMode // enforcement mode when the item was opened
	Deadline  time.Time
	OnTimeout OnTimeout
	CreatedAt time.Time

	Decision      Decision
	DecidedBy     wa.JID
	DecidedByName string
	Method        Method
	DecidedAt     time.Time
}

// Posted maps one of the bot's review messages to its review (§14 review_messages).
type Posted struct {
	ReviewID      int64
	Chat          wa.JID
	ID            wa.MessageID
	SentAt        time.Time
	OutcomeEdited bool
}

// EditableUntil is the end of WhatsApp's edit window for the message.
func (p Posted) EditableUntil() time.Time { return p.SentAt.Add(EditWindow) }

// VoteRecord is one stored vote, valid or not (§14 review_votes).
type VoteRecord struct {
	ReviewID      int64
	Voter         wa.JID
	Method        Method
	Raw           string // emoji or text
	Decision      Decision
	Valid         bool
	InvalidReason string // not_admin | late | unknown_keyword | withdrawn
	At            time.Time
}

// Invalid vote reasons.
const (
	InvalidNotAdmin       = "not_admin"
	InvalidLate           = "late"
	InvalidUnknownKeyword = "unknown_keyword"
	InvalidWithdrawn      = "withdrawn"
)

// Config is the part of a group's settings that reviews need (§12.1 review.*).
type Config struct {
	Destination Destination
	ReviewGroup wa.JID
	Timeout     time.Duration // zero means the default for the mode
	OnTimeout   OnTimeout     // empty means the default for the mode
	Mode        EnforcementMode
	Language    string
	Reactions   map[Decision][]string // empty means DefaultReactions
	Ladder      bool                  // optional ladder on: mute is a valid decision
	BlurMedia   bool
}
