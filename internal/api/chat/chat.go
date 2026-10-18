// Package chat is the in-chat control surface (DESIGN.md §13.6, §13.7, §9.7): DM handling with
// inbound-first thread tracking, the admin command framework and its commands, review votes
// cast by reaction or reply, and onboarding by DM. Nothing is ever posted in a protected group:
// command messages there are deleted and the reply goes by DM; in a review group the reply
// stays in the review group.
package chat

import (
	"context"
	"errors"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review/blacklist"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// DefaultPrefix is the command prefix.
const DefaultPrefix = "."

// ContextTTL is how long a DM's .use context is remembered.
const ContextTTL = time.Hour

// Message is an inbound message or reaction the router hands to chat: every DM, every message
// in a review group, and messages in protected groups that may be commands.
type Message struct {
	Chat        wa.JID // the group, or for a DM the other person's JID
	Sender      wa.JID // LID when known
	SenderPhone wa.JID // phone-number JID when known
	SenderName  string
	ID          wa.MessageID
	Text        string
	Quoted      *Quoted
	Mentions    []wa.JID
	Reaction    *Reaction // set for a reaction instead of a text message
	At          time.Time
}

// IsDM reports whether the message came in a one-to-one chat.
func (m Message) IsDM() bool { return !m.Chat.IsGroup() }

// Ref is the message's reference.
func (m Message) Ref() wa.MessageRef { return wa.MessageRef{Sender: m.Sender, ID: m.ID} }

// Quoted is the message a reply quotes.
type Quoted struct {
	ID          wa.MessageID
	Sender      wa.JID
	SenderPhone wa.JID
	Text        string
}

// Reaction is a reaction to a message; an empty Emoji removes it.
type Reaction struct {
	Target wa.MessageID
	Emoji  string
}

// GroupRef names a group.
type GroupRef struct {
	JID  wa.JID
	Name string
	// Community is the WhatsApp community the group belongs to, if any, and CommunityName its
	// name (DESIGN.md §12.4).
	Community     wa.JID
	CommunityName string
}

// CommunityGroup is one group of a community and whether the bot can protect it.
type CommunityGroup struct {
	JID    wa.JID
	Name   string
	Member bool // the bot is in the group
	Admin  bool // the bot is an admin there, so it protects the group
}

// Communities lists a community's groups (DESIGN.md §12.4).
type Communities interface {
	// CommunityGroups lists every group of a community the bot knows of, including the ones it
	// isn't in, with the bot's standing in each. The announcement group is left out: only
	// admins post there.
	CommunityGroups(ctx context.Context, community wa.JID) ([]CommunityGroup, error)
}

// Groups answers questions about groups.
type Groups interface {
	// IsProtected reports whether the bot protects (moderates) the group.
	IsProtected(ctx context.Context, group wa.JID) (bool, error)
	Name(ctx context.Context, group wa.JID) (string, error)
	// AdminGroups lists the protected groups where person is a current admin.
	AdminGroups(ctx context.Context, person wa.JID) ([]GroupRef, error)
	// ServedBy lists the protected groups whose review group is chat.
	ServedBy(ctx context.Context, chat wa.JID) ([]wa.JID, error)
	Participants(ctx context.Context, group wa.JID) ([]wa.JID, error)
}

// Settings errors. Adapters wrap their own validation errors with these.
var (
	ErrUnknownKey   = errors.New("chat: unknown setting")
	ErrInvalidValue = errors.New("chat: invalid value")
)

// Change is one settings change.
type Change struct {
	Key string
	Old string
	New string
}

// Settings reads and writes a group's settings by dotted key (§12.1), validated with the same
// schema as the website; Set writes history and the audit log (F-054).
type Settings interface {
	Get(ctx context.Context, group wa.JID, key string) (string, error)
	Set(ctx context.Context, group wa.JID, key, value string, actor wa.JID) (Change, error)
	// ApplyPreset switches the preset and resets the auto-ban matrix to it (F-055).
	ApplyPreset(ctx context.Context, group wa.JID, preset string, actor wa.JID) ([]Change, error)
}

// Lists edits a group's domain and word lists (F-042, F-048).
type Lists interface {
	AllowDomain(ctx context.Context, group wa.JID, domain string, actor wa.JID) error
	BlockDomain(ctx context.Context, group wa.JID, domain string, actor wa.JID) error
	BlockWord(ctx context.Context, group wa.JID, phrase string, actor wa.JID) error
	UnblockWord(ctx context.Context, group wa.JID, phrase string, actor wa.JID) error
}

// Recent counts how many of a group's recent messages (the 72 h inbox, matched in memory) a
// phrase would have matched (§8.4 dry run).
type Recent interface {
	CountMatching(ctx context.Context, group wa.JID, phrase string) (int, error)
}

// Members manages the member allow-list.
type Members interface {
	Allow(ctx context.Context, group, person, actor wa.JID) error
	Unallow(ctx context.Context, group, person, actor wa.JID) error
}

// Reversible is the bot's last action in a group that .undo can reverse.
type Reversible struct {
	ID     int64
	Kind   string // ban | kick | strike
	Target wa.JID
	At     time.Time
}

// History finds the bot's last reversible action in a group.
type History interface {
	LastReversible(ctx context.Context, group wa.JID) (Reversible, bool, error)
	MarkUndone(ctx context.Context, id int64, by wa.JID) error
}

// Identity maps JIDs to people (§7.7).
type Identity interface {
	// Canonical returns the person's primary JID (the LID when known), so one person has one
	// DM thread whichever JID they used.
	Canonical(ctx context.Context, jid wa.JID) (wa.JID, error)
	// ByPhone finds the person with a phone number (E.164).
	ByPhone(ctx context.Context, e164 string) (wa.JID, bool, error)
}

// TrackRecord reports whether AI-verified evidence has earned a track record in a group, which
// `.autoban <type> auto ai` requires (§9.2).
type TrackRecord interface {
	AIReady(ctx context.Context, group wa.JID) (bool, error)
}

// DayStats is today's activity in a group, for .status.
type DayStats struct {
	Deleted int
	Banned  int
	Pending int
}

// Stats returns today's activity.
type Stats interface {
	Today(ctx context.Context, group wa.JID) (DayStats, error)
}

// Joiner joins a group from an invite link and leaves it again (onboarding, §13.6).
type Joiner interface {
	GroupFromLink(ctx context.Context, code string) (wa.GroupInfo, error)
	JoinWithLink(ctx context.Context, code string) (wa.JID, error)
	Leave(ctx context.Context, group wa.JID) error
}

// Deps are the Handler's collaborators. Optional ones may be nil; their commands then reply
// that the feature isn't available.
type Deps struct {
	Groups      Groups
	Communities Communities // nil: replies can't name the community groups the bot isn't in
	Admins      review.Admins
	Actions     review.Actions
	Reviews     *review.Service
	Learning    review.Learner
	Evidence    *review.Learning // evidence snapshots for command deletions
	Blacklist   *blacklist.Service
	Normalize   blacklist.Normalizer
	Settings    Settings
	Lists       Lists
	Recent      Recent
	Members     Members
	History     History
	Identity    Identity
	Track       TrackRecord
	Stats       Stats
	Joiner      Joiner
	Threads     ThreadStore
	Audit       review.Audit
	Standing    review.Standing  // .undo of a strike restores trust
	Self        wa.JID           // the bot's own JID; it never acts on itself
	Texts       review.Localizer // defaults to review.Texts
	Clock       clock.Clock      // defaults to clock.Real
	Prefix      string           // defaults to DefaultPrefix
	Language    string           // reply language when a group has none; defaults to "en"
	// Operator reports whether a person is the instance operator.
	Operator func(wa.JID) bool
	// WebsiteURL is linked from onboarding and .settings.
	WebsiteURL string
}
