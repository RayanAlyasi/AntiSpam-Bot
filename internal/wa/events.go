package wa

import (
	"context"
	"time"
)

// Event is one thing the connector observed on a bot's WhatsApp connection, converted from a
// whatsmeow event into a type the rest of the bot can use without importing whatsmeow
// (DESIGN.md §5.1). Every event carries the bot it arrived on.
type Event interface {
	// BotJID is the bot number the event arrived on.
	BotJID() JID
	isEvent()
}

// EventSink receives the connector's events. It runs on the receive loop, so it must return
// fast and must not analyse anything (DESIGN.md §3.7). For a MessageEvent or an
// UndecryptableEvent, a nil error means the event is durably stored and may be acked; an error
// leaves the message unacked so WhatsApp redelivers it.
type EventSink interface {
	HandleEvent(ctx context.Context, evt Event) error
}

// EventSinkFunc adapts a function to EventSink.
type EventSinkFunc func(ctx context.Context, evt Event) error

// HandleEvent calls f.
func (f EventSinkFunc) HandleEvent(ctx context.Context, evt Event) error { return f(ctx, evt) }

// MessageInfo is the metadata of a received message (whatsmeow's types.MessageInfo, reduced to
// what the bot uses). JIDs are non-AD.
type MessageInfo struct {
	Chat           JID
	Sender         JID
	SenderAlt      JID // the sender's other identifier (often the phone number in LID groups), if sent
	IsFromMe       bool
	IsGroup        bool
	AddressingMode string // "pn" or "lid"
	ID             MessageID
	Type           string
	PushName       string
	Timestamp      time.Time
	Edit           string // whatsmeow's edit attribute: "" normal, "1" edit, "7" sender revoke, "8" admin revoke
	MediaType      string
	VerifiedName   string
}

// MessageEvent is a received message, kept raw: Raw is the marshalled waE2E.Message proto (still
// wrapped, as received) and InfoJSON is whatsmeow's MessageInfo as JSON, so the inbox can store
// and replay it exactly (DESIGN.md §5.1, §14).
type MessageEvent struct {
	Bot      JID
	Info     MessageInfo
	Raw      []byte
	InfoJSON []byte
}

// UndecryptableEvent is a message that could not be decrypted, such as view-once media, which
// linked devices never receive (DESIGN.md §2.1). Only the sender and ID are known.
type UndecryptableEvent struct {
	Bot             JID
	Info            MessageInfo
	InfoJSON        []byte
	IsUnavailable   bool
	UnavailableType string // e.g. "view_once"
}

// JIDPair links one person's LID and phone-number JID, as learned from an event.
type JIDPair struct {
	LID JID
	PN  JID
}

// GroupEvent is a change to a group: metadata or participants (whatsmeow's events.GroupInfo).
// Pointer fields are nil when that part didn't change.
type GroupEvent struct {
	Bot          JID
	Group        JID
	Sender       JID // who made the change, if known
	SenderPN     JID // the sender's phone number, when Sender is a LID
	Timestamp    time.Time
	Name         *string
	Topic        *string
	Announce     *bool
	Locked       *bool
	JoinApproval *bool
	Deleted      bool
	JoinReason   string // "invite" when someone joined through an invite link
	Join         []JID
	Leave        []JID
	Promote      []JID
	Demote       []JID
	// Pairs are LID↔PN links for the participants above, as whatsmeow's LID store knows them.
	Pairs []JIDPair
	// Link and Unlink report a group joining or leaving a community. The event may be about
	// either side: Group is the event's group or Community is.
	Link   *CommunityLink
	Unlink *CommunityLink
}

// CommunityLink is a group linked to (or unlinked from) a community. Name is the other side's
// name as the event gave it: the community's when the event is about the group, the group's when
// it is about the community.
type CommunityLink struct {
	Community      JID
	Group          JID
	Name           string
	IsAnnouncement bool
}

// JoinedGroupEvent means the bot joined or was added to a group. Info is the group's full
// metadata, including the participants present at that moment.
type JoinedGroupEvent struct {
	Bot      JID
	Info     GroupInfo
	Reason   string // "invite" if the bot joined through a link
	Sender   JID    // who added the bot, if known
	SenderPN JID
}

// ProfileKind says which part of a profile changed.
type ProfileKind string

// Profile change kinds.
const (
	ProfileKindPicture  ProfileKind = "picture"
	ProfileKindAbout    ProfileKind = "about"
	ProfileKindPushName ProfileKind = "push_name"
)

// ProfileEvent is a change to a user's (or group's) picture, about text or push name. Consumers
// invalidate cached profile data (DESIGN.md §11).
type ProfileEvent struct {
	Bot       JID
	Kind      ProfileKind
	JID       JID
	JIDAlt    JID // the other identifier, when whatsmeow knows it (push names)
	Timestamp time.Time
	PictureID string // new picture ID; empty when removed
	Removed   bool
	About     string
	PushName  string
}

// ConnectionState is the bot's connection status.
type ConnectionState string

// Connection states.
const (
	Connected         ConnectionState = "connected"
	Disconnected      ConnectionState = "disconnected"
	KeepAliveTimeout  ConnectionState = "keepalive_timeout"
	KeepAliveRestored ConnectionState = "keepalive_restored"
	// Released: this process stopped owning the bot (shutdown, lease taken over, logged out).
	// The bot isn't connected here, but that alone is no reason to alert.
	Released ConnectionState = "released"
)

// ConnectionEvent is a connection status change, used for health metrics (DESIGN.md §16).
type ConnectionEvent struct {
	Bot   JID
	State ConnectionState
}

// AccountProblem is a signal that the bot number can't (or mustn't) keep writing.
type AccountProblem string

// Account problems (DESIGN.md §2.4, §10.5).
const (
	ProblemTemporaryBan   AccountProblem = "temporary_ban"
	ProblemLoggedOut      AccountProblem = "logged_out"
	ProblemStreamReplaced AccountProblem = "stream_replaced"
)

// AccountEvent is a temporary ban, a logout or a replaced stream. Each one opens the action
// executor's breaker and alerts the operator (DESIGN.md §10.5).
type AccountEvent struct {
	Bot     JID
	Problem AccountProblem
	Code    int           // temporary-ban reason code (e.g. 101), or the logout reason
	Expire  time.Duration // how long a temporary ban lasts
}

// BotJID implements Event.
func (e *MessageEvent) BotJID() JID { return e.Bot }

// BotJID implements Event.
func (e *UndecryptableEvent) BotJID() JID { return e.Bot }

// BotJID implements Event.
func (e *GroupEvent) BotJID() JID { return e.Bot }

// BotJID implements Event.
func (e *JoinedGroupEvent) BotJID() JID { return e.Bot }

// BotJID implements Event.
func (e *ProfileEvent) BotJID() JID { return e.Bot }

// BotJID implements Event.
func (e *ConnectionEvent) BotJID() JID { return e.Bot }

// BotJID implements Event.
func (e *AccountEvent) BotJID() JID { return e.Bot }

func (*MessageEvent) isEvent()       {}
func (*UndecryptableEvent) isEvent() {}
func (*GroupEvent) isEvent()         {}
func (*JoinedGroupEvent) isEvent()   {}
func (*ProfileEvent) isEvent()       {}
func (*ConnectionEvent) isEvent()    {}
func (*AccountEvent) isEvent()       {}
