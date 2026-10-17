package wa

import (
	"context"
	"strings"
	"time"
)

// JID is a WhatsApp address in its string form ("<user>@<server>"), for example a person's
// LID ("123@lid"), phone-number JID ("9665...@s.whatsapp.net") or a group ("1203...@g.us").
// A JID never carries a device suffix ("123:5@lid"): the adapter applies ToNonAD when converting
// from whatsmeow's types.JID, so one person has one JID per identifier (DESIGN.md §7.7).
// Nothing outside internal/wa needs whatsmeow for it.
type JID string

// Well-known JID servers.
const (
	ServerUser       = "s.whatsapp.net"
	ServerLID        = "lid"
	ServerGroup      = "g.us"
	ServerNewsletter = "newsletter"
	ServerBroadcast  = "broadcast"
)

// User returns the part before the "@" (the whole JID if there is none).
func (j JID) User() string {
	user, _, _ := strings.Cut(string(j), "@")
	return user
}

// Server returns the part after the "@" (empty if there is none).
func (j JID) Server() string {
	_, server, _ := strings.Cut(string(j), "@")
	return server
}

// IsGroup reports whether j is a group chat.
func (j JID) IsGroup() bool { return j.Server() == ServerGroup }

// IsLID reports whether j is a LID (privacy ID), the primary identity of a person (DESIGN.md §7.7).
func (j JID) IsLID() bool { return j.Server() == ServerLID }

// MessageID is a WhatsApp message ID. Edits refer to the original message's ID.
type MessageID string

// OutgoingMessage is a message the bot sends: a review item, a DM reply or a notification.
type OutgoingMessage struct {
	Text    string
	ReplyTo *MessageRef // quote this message (a follow-up to a review, a reply to a command)
	Image   *Image      // optional image; Text becomes its caption
}

// MessageRef identifies a message in a chat.
type MessageRef struct {
	Sender JID
	ID     MessageID
}

// Image is an inline image to send, such as a re-uploaded thumbnail in a review (DESIGN.md §9.5).
type Image struct {
	Data     []byte
	Mimetype string
}

// SendResult is what the server returned for a sent message.
type SendResult struct {
	ID        MessageID
	Timestamp time.Time
}

// ParticipantAction is a change to a group's participants. Only removal exists: the bot never
// adds people, because adding someone is unsolicited and depends on their privacy settings
// (DESIGN.md §9.6), and it never changes admin roles.
type ParticipantAction int

// Participant actions.
const (
	ParticipantRemove ParticipantAction = iota + 1
)

// JoinRequestAction answers pending join requests in approval-mode groups.
type JoinRequestAction int

// Join request actions.
const (
	JoinRequestApprove JoinRequestAction = iota + 1
	JoinRequestReject
)

// ParticipantResult is the per-JID outcome of a batched participant or join-request update.
// Err is nil on success, or an error such as ErrNotFound for someone who isn't in the group.
type ParticipantResult struct {
	JID JID
	Err error
}

// GroupInfo is a group's metadata and participant list.
type GroupInfo struct {
	JID            JID
	Name           string
	Topic          string
	Owner          JID
	OwnerPN        JID
	LinkedParent   JID // the community this group belongs to, if any
	AddressingMode string
	Announce       bool // only admins can send
	Locked         bool // only admins can edit group info
	JoinApproval   bool
	Size           int
	Participants   []Participant
}

// Participant is one member of a group. PhoneNumber and LID are set when the server shares them.
type Participant struct {
	JID          JID
	PhoneNumber  JID
	LID          JID
	IsAdmin      bool
	IsSuperAdmin bool
}

// JoinRequest is a pending request to join an approval-mode group.
type JoinRequest struct {
	JID         JID
	RequestedAt time.Time
}

// UserInfo is what the server returns about a user (one batched IQ covers many users).
type UserInfo struct {
	VerifiedName string
	Status       string // the "about" text
	PictureID    string
	Devices      int
	LID          JID
}

// ProfilePictureParams controls a profile-picture lookup.
type ProfilePictureParams struct {
	Preview     bool
	ExistingID  string // if it still matches, the lookup returns nil: a cheap "unchanged" check
	CommonGroup JID    // a group shared with the user, which helps with privacy-restricted pictures
}

// ProfilePicture is a profile picture's ID and download URL.
type ProfilePicture struct {
	ID  string
	URL string
}

// NewsletterInfo is a channel's public metadata (DESIGN.md §6.8).
type NewsletterInfo struct {
	JID         JID
	Name        string
	Description string
	Subscribers int
	Verified    bool
	PictureURL  string
}

// WAClient is every WhatsApp operation the bot performs. Writes (Send, React, Revoke, Edit,
// UpdateParticipants, UpdateJoinRequests, ResetInviteLink and the Set* calls) are only ever
// called by the action executor in internal/actions, which paces them (DESIGN.md §10).
type WAClient interface {
	// Send sends a message to a chat (a DM, a review group or, only with silent off, a protected group).
	Send(ctx context.Context, chat JID, msg OutgoingMessage) (SendResult, error)
	// React sets the bot's reaction on a message; an empty emoji removes it.
	React(ctx context.Context, chat JID, target MessageRef, emoji string) error
	// Revoke deletes a message for everyone, as an admin when it isn't the bot's own (DESIGN.md §2.3).
	Revoke(ctx context.Context, chat JID, target MessageRef) error
	// Edit replaces the text of one of the bot's own messages (allowed for about 20 minutes).
	Edit(ctx context.Context, chat JID, id MessageID, text string) error

	// UpdateParticipants applies one action to many participants in a single call.
	UpdateParticipants(ctx context.Context, group JID, jids []JID, action ParticipantAction) ([]ParticipantResult, error)
	// JoinRequests lists the pending join requests of an approval-mode group.
	JoinRequests(ctx context.Context, group JID) ([]JoinRequest, error)
	// UpdateJoinRequests approves or rejects pending join requests in a single call.
	UpdateJoinRequests(ctx context.Context, group JID, jids []JID, action JoinRequestAction) ([]ParticipantResult, error)
	// SetAnnounce switches "only admins can send messages" on or off (lockdown, DESIGN.md §9.4).
	SetAnnounce(ctx context.Context, group JID, on bool) error
	// SetLocked switches "only admins can edit group info" on or off.
	SetLocked(ctx context.Context, group JID, on bool) error
	// SetJoinApproval switches admin approval for new members on or off.
	SetJoinApproval(ctx context.Context, group JID, on bool) error
	// ResetInviteLink revokes the group's invite link and returns the new one.
	ResetInviteLink(ctx context.Context, group JID) (string, error)

	// GroupInfo returns a group's metadata and participants.
	GroupInfo(ctx context.Context, group JID) (GroupInfo, error)
	// JoinedGroups returns every group the bot is in.
	JoinedGroups(ctx context.Context) ([]GroupInfo, error)
	// GroupInfoFromLink resolves an invite code to the target group without joining it.
	GroupInfoFromLink(ctx context.Context, code string) (GroupInfo, error)
	// UserInfo looks up many users in one call. Unknown users are missing from the map.
	UserInfo(ctx context.Context, jids []JID) (map[JID]UserInfo, error)
	// ProfilePicture returns a user's or group's picture, or nil if it still matches params.ExistingID.
	ProfilePicture(ctx context.Context, jid JID, params ProfilePictureParams) (*ProfilePicture, error)
	// NewsletterInfo returns a channel's metadata.
	NewsletterInfo(ctx context.Context, jid JID) (NewsletterInfo, error)
	// NewsletterInfoFromInvite resolves a channel invite code to the channel's metadata.
	NewsletterInfoFromInvite(ctx context.Context, code string) (NewsletterInfo, error)
}

// Revoke is a "delete for everyone" the bot received: a sender deleting their own message or an
// admin deleting someone's (DESIGN.md §2.1). It cancels pending actions on the target and is
// logged, since "post, then delete fast" is an evasion pattern. The sending client sets both
// the revoke flag and the key, so a sender revoke always has Target.Sender == By; whether the
// server checks ByAdmin is still to be confirmed (spike).
type Revoke struct {
	Chat    JID
	Target  MessageRef // the deleted message and its sender
	By      JID        // who deleted it
	ByAdmin bool
}
