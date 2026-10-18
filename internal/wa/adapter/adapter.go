package adapter

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// api is the part of *whatsmeow.Client the adapter calls. Tests substitute a stub, so the
// adapter is tested without a WhatsApp connection.
type api interface {
	SendMessage(ctx context.Context, to types.JID, message *waE2E.Message, extra ...whatsmeow.SendRequestExtra) (whatsmeow.SendResponse, error)
	Upload(ctx context.Context, plaintext []byte, appInfo whatsmeow.MediaType) (whatsmeow.UploadResponse, error)
	BuildRevoke(chat, sender types.JID, id types.MessageID) *waE2E.Message
	BuildEdit(chat types.JID, id types.MessageID, newContent *waE2E.Message) *waE2E.Message
	BuildReaction(chat, sender types.JID, id types.MessageID, reaction string) *waE2E.Message

	UpdateGroupParticipants(ctx context.Context, jid types.JID, participantChanges []types.JID, action whatsmeow.ParticipantChange) ([]types.GroupParticipant, error)
	GetGroupRequestParticipants(ctx context.Context, jid types.JID) ([]types.GroupParticipantRequest, error)
	UpdateGroupRequestParticipants(ctx context.Context, jid types.JID, participantChanges []types.JID, action whatsmeow.ParticipantRequestChange) ([]types.GroupParticipant, error)
	SetGroupAnnounce(ctx context.Context, jid types.JID, announce bool) error
	SetGroupLocked(ctx context.Context, jid types.JID, locked bool) error
	SetGroupJoinApprovalMode(ctx context.Context, jid types.JID, mode bool) error
	GetGroupInviteLink(ctx context.Context, jid types.JID, reset bool) (string, error)

	GetGroupInfo(ctx context.Context, jid types.JID) (*types.GroupInfo, error)
	GetJoinedGroups(ctx context.Context) ([]*types.GroupInfo, error)
	GetGroupInfoFromLink(ctx context.Context, code string) (*types.GroupInfo, error)
	GetUserInfo(ctx context.Context, jids []types.JID) (map[types.JID]types.UserInfo, error)
	GetProfilePictureInfo(ctx context.Context, jid types.JID, params *whatsmeow.GetProfilePictureParams) (*types.ProfilePictureInfo, error)
	GetNewsletterInfo(ctx context.Context, jid types.JID) (*types.NewsletterMetadata, error)
	GetNewsletterInfoWithInvite(ctx context.Context, key string) (*types.NewsletterMetadata, error)
}

var _ api = (*whatsmeow.Client)(nil)

// subGroupLister is the whatsmeow call that lists a community's groups. It's kept out of api so
// the adapter's test stub doesn't have to implement it.
type subGroupLister interface {
	GetSubGroups(ctx context.Context, community types.JID) ([]*types.GroupLinkTarget, error)
}

var _ subGroupLister = (*whatsmeow.Client)(nil)

// Client implements wa.WAClient on top of a whatsmeow client. Every error it returns matches
// one of the wa error values with errors.Is when it's one of the cases the action executor
// handles (DESIGN.md §10.5), and keeps the whatsmeow error in its chain.
type Client struct {
	cli   api
	wm    *whatsmeow.Client
	clock clock.Clock

	mu  sync.Mutex
	ban *wa.TemporaryBanError
	// banUntil is when the current temporary ban lifts.
	banUntil time.Time
}

var (
	_ wa.WAClient        = (*Client)(nil)
	_ wa.CommunityReader = (*Client)(nil)
)

// New wraps a whatsmeow client.
func New(cli *whatsmeow.Client) *Client {
	return &Client{cli: cli, wm: cli, clock: clock.Real{}}
}

// Whatsmeow returns the wrapped client.
func (c *Client) Whatsmeow() *whatsmeow.Client { return c.wm }

// HandleEvent records the connection events that change how calls fail. After an
// *events.TemporaryBan every call returns a *wa.TemporaryBanError until the ban lifts, so the
// executor opens its breaker instead of retrying a disconnected client (DESIGN.md §10.5). A
// later *events.Connected clears it.
// Register it with the whatsmeow client's event handlers; it ignores every other event.
func (c *Client) HandleEvent(evt any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch e := evt.(type) {
	case *events.TemporaryBan:
		c.ban = TemporaryBan(e)
		c.banUntil = c.clock.Now().Add(e.Expire)
	case *events.Connected:
		c.ban = nil // the server let us back in
	}
}

// TemporaryBan converts whatsmeow's temporary-ban event to the wa error.
func TemporaryBan(evt *events.TemporaryBan) *wa.TemporaryBanError {
	return &wa.TemporaryBanError{Code: int(evt.Code), Expire: evt.Expire}
}

// banned returns the current temporary ban, with the time left, or nil.
func (c *Client) banned() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ban == nil {
		return nil
	}
	left := c.banUntil.Sub(c.clock.Now())
	if left <= 0 {
		c.ban = nil
		return nil
	}
	return &wa.TemporaryBanError{Code: c.ban.Code, Expire: left}
}

// call runs fn unless a temporary ban is in force, and maps its error.
func (c *Client) call(op string, fn func() error) error {
	if err := c.banned(); err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	return mapError(op, fn())
}

// Send sends a text message, or an image with Text as its caption, optionally quoting ReplyTo.
func (c *Client) Send(ctx context.Context, chat wa.JID, msg wa.OutgoingMessage) (wa.SendResult, error) {
	var res wa.SendResult
	err := c.call("send", func() error {
		to, err := toWM(chat)
		if err != nil {
			return err
		}
		content, err := c.outgoing(ctx, chat, msg)
		if err != nil {
			return err
		}
		resp, err := c.cli.SendMessage(ctx, to, content)
		if err != nil {
			return err
		}
		res = wa.SendResult{ID: wa.MessageID(resp.ID), Timestamp: resp.Timestamp}
		return nil
	})
	return res, err
}

// outgoing builds the protobuf for msg, uploading its image first.
func (c *Client) outgoing(ctx context.Context, chat wa.JID, msg wa.OutgoingMessage) (*waE2E.Message, error) {
	var ci *waE2E.ContextInfo
	if msg.ReplyTo != nil {
		ci = &waE2E.ContextInfo{
			StanzaID:    proto.String(string(msg.ReplyTo.ID)),
			Participant: proto.String(string(msg.ReplyTo.Sender)),
			RemoteJID:   proto.String(string(chat)),
		}
	}
	if msg.Image != nil {
		up, err := c.cli.Upload(ctx, msg.Image.Data, whatsmeow.MediaImage)
		if err != nil {
			return nil, fmt.Errorf("upload image: %w", err)
		}
		return &waE2E.Message{ImageMessage: &waE2E.ImageMessage{
			Caption:       optString(msg.Text),
			Mimetype:      proto.String(msg.Image.Mimetype),
			URL:           proto.String(up.URL),
			DirectPath:    proto.String(up.DirectPath),
			MediaKey:      up.MediaKey,
			FileEncSHA256: up.FileEncSHA256,
			FileSHA256:    up.FileSHA256,
			FileLength:    proto.Uint64(up.FileLength),
			ContextInfo:   ci,
		}}, nil
	}
	if ci == nil {
		return &waE2E.Message{Conversation: proto.String(msg.Text)}, nil
	}
	return &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{
		Text:        proto.String(msg.Text),
		ContextInfo: ci,
	}}, nil
}

// React sets or, with an empty emoji, removes the bot's reaction on target.
func (c *Client) React(ctx context.Context, chat wa.JID, target wa.MessageRef, emoji string) error {
	return c.call("react", func() error {
		to, sender, err := toWM2(chat, target.Sender)
		if err != nil {
			return err
		}
		_, err = c.cli.SendMessage(ctx, to, c.cli.BuildReaction(to, sender, string(target.ID), emoji))
		return err
	})
}

// Revoke deletes target for everyone, with SendMessage(BuildRevoke) as DESIGN.md §2.3 requires.
func (c *Client) Revoke(ctx context.Context, chat wa.JID, target wa.MessageRef) error {
	return c.call("revoke", func() error {
		to, sender, err := toWM2(chat, target.Sender)
		if err != nil {
			return err
		}
		_, err = c.cli.SendMessage(ctx, to, c.cli.BuildRevoke(to, sender, string(target.ID)))
		return err
	})
}

// Edit replaces the text of one of the bot's own messages.
func (c *Client) Edit(ctx context.Context, chat wa.JID, id wa.MessageID, text string) error {
	return c.call("edit", func() error {
		to, err := toWM(chat)
		if err != nil {
			return err
		}
		edit := c.cli.BuildEdit(to, string(id), &waE2E.Message{Conversation: proto.String(text)})
		_, err = c.cli.SendMessage(ctx, to, edit)
		return err
	})
}

// UpdateParticipants removes many participants in one call. Each result carries the
// participant's own error, mapped like call errors.
func (c *Client) UpdateParticipants(ctx context.Context, group wa.JID, jids []wa.JID, action wa.ParticipantAction) ([]wa.ParticipantResult, error) {
	var res []wa.ParticipantResult
	err := c.call("update participants", func() error {
		if action != wa.ParticipantRemove {
			return fmt.Errorf("unsupported participant action %d", action)
		}
		g, err := toWM(group)
		if err != nil {
			return err
		}
		targets, err := toWMs(jids)
		if err != nil {
			return err
		}
		ps, err := c.cli.UpdateGroupParticipants(ctx, g, targets, whatsmeow.ParticipantChangeRemove)
		if err != nil {
			return err
		}
		res = participantResults(ps)
		return nil
	})
	return res, err
}

// JoinRequests lists a group's pending join requests.
func (c *Client) JoinRequests(ctx context.Context, group wa.JID) ([]wa.JoinRequest, error) {
	var res []wa.JoinRequest
	err := c.call("join requests", func() error {
		g, err := toWM(group)
		if err != nil {
			return err
		}
		reqs, err := c.cli.GetGroupRequestParticipants(ctx, g)
		if err != nil {
			return err
		}
		res = make([]wa.JoinRequest, len(reqs))
		for i, r := range reqs {
			res[i] = wa.JoinRequest{JID: FromWM(r.JID), RequestedAt: r.RequestedAt}
		}
		return nil
	})
	return res, err
}

// UpdateJoinRequests approves or rejects many join requests in one call.
func (c *Client) UpdateJoinRequests(ctx context.Context, group wa.JID, jids []wa.JID, action wa.JoinRequestAction) ([]wa.ParticipantResult, error) {
	var res []wa.ParticipantResult
	err := c.call("update join requests", func() error {
		var change whatsmeow.ParticipantRequestChange
		switch action {
		case wa.JoinRequestApprove:
			change = whatsmeow.ParticipantChangeApprove
		case wa.JoinRequestReject:
			change = whatsmeow.ParticipantChangeReject
		default:
			return fmt.Errorf("unsupported join request action %d", action)
		}
		g, err := toWM(group)
		if err != nil {
			return err
		}
		targets, err := toWMs(jids)
		if err != nil {
			return err
		}
		ps, err := c.cli.UpdateGroupRequestParticipants(ctx, g, targets, change)
		if err != nil {
			return err
		}
		res = participantResults(ps)
		return nil
	})
	return res, err
}

// SetAnnounce switches "only admins can send messages".
func (c *Client) SetAnnounce(ctx context.Context, group wa.JID, on bool) error {
	return c.groupSetting(ctx, "set announce", group, on, c.cli.SetGroupAnnounce)
}

// SetLocked switches "only admins can edit group info".
func (c *Client) SetLocked(ctx context.Context, group wa.JID, on bool) error {
	return c.groupSetting(ctx, "set locked", group, on, c.cli.SetGroupLocked)
}

// SetJoinApproval switches admin approval for new members.
func (c *Client) SetJoinApproval(ctx context.Context, group wa.JID, on bool) error {
	return c.groupSetting(ctx, "set join approval", group, on, c.cli.SetGroupJoinApprovalMode)
}

func (c *Client) groupSetting(ctx context.Context, op string, group wa.JID, on bool, set func(context.Context, types.JID, bool) error) error {
	return c.call(op, func() error {
		g, err := toWM(group)
		if err != nil {
			return err
		}
		return set(ctx, g, on)
	})
}

// ResetInviteLink revokes the group's invite link and returns the new one.
func (c *Client) ResetInviteLink(ctx context.Context, group wa.JID) (string, error) {
	var link string
	err := c.call("reset invite link", func() error {
		g, err := toWM(group)
		if err != nil {
			return err
		}
		link, err = c.cli.GetGroupInviteLink(ctx, g, true)
		return err
	})
	return link, err
}

// GroupInfo returns a group's metadata and participants.
func (c *Client) GroupInfo(ctx context.Context, group wa.JID) (wa.GroupInfo, error) {
	var res wa.GroupInfo
	err := c.call("group info", func() error {
		g, err := toWM(group)
		if err != nil {
			return err
		}
		info, err := c.cli.GetGroupInfo(ctx, g)
		if err != nil {
			return err
		}
		if info == nil {
			return wa.ErrNotFound
		}
		res = groupInfo(info)
		return nil
	})
	return res, err
}

// JoinedGroups returns every group the bot is in.
func (c *Client) JoinedGroups(ctx context.Context) ([]wa.GroupInfo, error) {
	var res []wa.GroupInfo
	err := c.call("joined groups", func() error {
		infos, err := c.cli.GetJoinedGroups(ctx)
		if err != nil {
			return err
		}
		res = make([]wa.GroupInfo, 0, len(infos))
		for _, info := range infos {
			if info != nil {
				res = append(res, groupInfo(info))
			}
		}
		return nil
	})
	return res, err
}

// GroupInfoFromLink resolves an invite code without joining the group.
func (c *Client) GroupInfoFromLink(ctx context.Context, code string) (wa.GroupInfo, error) {
	var res wa.GroupInfo
	err := c.call("group info from link", func() error {
		info, err := c.cli.GetGroupInfoFromLink(ctx, code)
		if err != nil {
			return err
		}
		if info == nil {
			return wa.ErrNotFound
		}
		res = groupInfo(info)
		return nil
	})
	return res, err
}

// SubGroups lists a community's groups, including the ones the bot isn't in.
func (c *Client) SubGroups(ctx context.Context, community wa.JID) ([]wa.SubGroup, error) {
	var res []wa.SubGroup
	err := c.call("sub groups", func() error {
		g, err := toWM(community)
		if err != nil {
			return err
		}
		lister, ok := c.cli.(subGroupLister)
		if !ok {
			return fmt.Errorf("listing a community's groups: %w", errors.ErrUnsupported)
		}
		targets, err := lister.GetSubGroups(ctx, g)
		if err != nil {
			return err
		}
		res = make([]wa.SubGroup, 0, len(targets))
		for _, t := range targets {
			if t != nil {
				res = append(res, wa.SubGroup{JID: FromWM(t.JID), Name: t.Name, IsAnnouncement: t.IsDefaultSubGroup})
			}
		}
		return nil
	})
	return res, err
}

// UserInfo looks up many users in one call.
func (c *Client) UserInfo(ctx context.Context, jids []wa.JID) (map[wa.JID]wa.UserInfo, error) {
	var res map[wa.JID]wa.UserInfo
	err := c.call("user info", func() error {
		targets, err := toWMs(jids)
		if err != nil {
			return err
		}
		infos, err := c.cli.GetUserInfo(ctx, targets)
		if err != nil {
			return err
		}
		res = make(map[wa.JID]wa.UserInfo, len(infos))
		for jid, info := range infos {
			res[FromWM(jid)] = userInfo(info)
		}
		return nil
	})
	return res, err
}

// ProfilePicture returns a picture, or nil when it still matches params.ExistingID.
func (c *Client) ProfilePicture(ctx context.Context, jid wa.JID, params wa.ProfilePictureParams) (*wa.ProfilePicture, error) {
	var res *wa.ProfilePicture
	err := c.call("profile picture", func() error {
		target, err := toWM(jid)
		if err != nil {
			return err
		}
		p := &whatsmeow.GetProfilePictureParams{Preview: params.Preview, ExistingID: params.ExistingID}
		if params.CommonGroup != "" {
			if p.CommonGID, err = toWM(params.CommonGroup); err != nil {
				return err
			}
		}
		info, err := c.cli.GetProfilePictureInfo(ctx, target, p)
		if err != nil || info == nil {
			return err
		}
		res = &wa.ProfilePicture{ID: info.ID, URL: info.URL}
		return nil
	})
	return res, err
}

// NewsletterInfo returns a channel's metadata.
func (c *Client) NewsletterInfo(ctx context.Context, jid wa.JID) (wa.NewsletterInfo, error) {
	var res wa.NewsletterInfo
	err := c.call("newsletter info", func() error {
		target, err := toWM(jid)
		if err != nil {
			return err
		}
		meta, err := c.cli.GetNewsletterInfo(ctx, target)
		if err != nil {
			return err
		}
		if meta == nil {
			return wa.ErrNotFound
		}
		res = newsletterInfo(meta)
		return nil
	})
	return res, err
}

// NewsletterInfoFromInvite resolves a channel invite code.
func (c *Client) NewsletterInfoFromInvite(ctx context.Context, code string) (wa.NewsletterInfo, error) {
	var res wa.NewsletterInfo
	err := c.call("newsletter info from invite", func() error {
		meta, err := c.cli.GetNewsletterInfoWithInvite(ctx, code)
		if err != nil {
			return err
		}
		if meta == nil {
			return wa.ErrNotFound
		}
		res = newsletterInfo(meta)
		return nil
	})
	return res, err
}

func optString(s string) *string {
	if s == "" {
		return nil
	}
	return proto.String(s)
}
