package fastlane

import (
	"context"
	"fmt"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// liveClient is a bot number's WAClient that looks the connection up on every call. The connector
// replaces a bot's whatsmeow client after a reconnect, a re-pair or a lease change, so a lane must
// never keep the one it was created with. While the bot isn't connected, calls fail with
// wa.ErrTimeout, which the executor treats as transient and retries.
type liveClient struct {
	bot wa.JID
	get func(bot wa.JID) (wa.WAClient, bool)
}

var _ wa.WAClient = liveClient{}

func (l liveClient) c() (wa.WAClient, error) {
	if c, ok := l.get(l.bot); ok {
		return c, nil
	}
	return nil, fmt.Errorf("%w: bot %s is not connected", wa.ErrTimeout, l.bot)
}

func (l liveClient) Send(ctx context.Context, chat wa.JID, msg wa.OutgoingMessage) (wa.SendResult, error) {
	c, err := l.c()
	if err != nil {
		return wa.SendResult{}, err
	}
	return c.Send(ctx, chat, msg)
}

func (l liveClient) React(ctx context.Context, chat wa.JID, target wa.MessageRef, emoji string) error {
	c, err := l.c()
	if err != nil {
		return err
	}
	return c.React(ctx, chat, target, emoji)
}

func (l liveClient) Revoke(ctx context.Context, chat wa.JID, target wa.MessageRef) error {
	c, err := l.c()
	if err != nil {
		return err
	}
	return c.Revoke(ctx, chat, target)
}

func (l liveClient) Edit(ctx context.Context, chat wa.JID, id wa.MessageID, text string) error {
	c, err := l.c()
	if err != nil {
		return err
	}
	return c.Edit(ctx, chat, id, text)
}

func (l liveClient) UpdateParticipants(ctx context.Context, group wa.JID, jids []wa.JID, action wa.ParticipantAction) ([]wa.ParticipantResult, error) {
	c, err := l.c()
	if err != nil {
		return nil, err
	}
	return c.UpdateParticipants(ctx, group, jids, action)
}

func (l liveClient) JoinRequests(ctx context.Context, group wa.JID) ([]wa.JoinRequest, error) {
	c, err := l.c()
	if err != nil {
		return nil, err
	}
	return c.JoinRequests(ctx, group)
}

func (l liveClient) UpdateJoinRequests(ctx context.Context, group wa.JID, jids []wa.JID, action wa.JoinRequestAction) ([]wa.ParticipantResult, error) {
	c, err := l.c()
	if err != nil {
		return nil, err
	}
	return c.UpdateJoinRequests(ctx, group, jids, action)
}

func (l liveClient) SetAnnounce(ctx context.Context, group wa.JID, on bool) error {
	c, err := l.c()
	if err != nil {
		return err
	}
	return c.SetAnnounce(ctx, group, on)
}

func (l liveClient) SetLocked(ctx context.Context, group wa.JID, on bool) error {
	c, err := l.c()
	if err != nil {
		return err
	}
	return c.SetLocked(ctx, group, on)
}

func (l liveClient) SetJoinApproval(ctx context.Context, group wa.JID, on bool) error {
	c, err := l.c()
	if err != nil {
		return err
	}
	return c.SetJoinApproval(ctx, group, on)
}

func (l liveClient) ResetInviteLink(ctx context.Context, group wa.JID) (string, error) {
	c, err := l.c()
	if err != nil {
		return "", err
	}
	return c.ResetInviteLink(ctx, group)
}

func (l liveClient) GroupInfo(ctx context.Context, group wa.JID) (wa.GroupInfo, error) {
	c, err := l.c()
	if err != nil {
		return wa.GroupInfo{}, err
	}
	return c.GroupInfo(ctx, group)
}

func (l liveClient) JoinedGroups(ctx context.Context) ([]wa.GroupInfo, error) {
	c, err := l.c()
	if err != nil {
		return nil, err
	}
	return c.JoinedGroups(ctx)
}

func (l liveClient) GroupInfoFromLink(ctx context.Context, code string) (wa.GroupInfo, error) {
	c, err := l.c()
	if err != nil {
		return wa.GroupInfo{}, err
	}
	return c.GroupInfoFromLink(ctx, code)
}

func (l liveClient) UserInfo(ctx context.Context, jids []wa.JID) (map[wa.JID]wa.UserInfo, error) {
	c, err := l.c()
	if err != nil {
		return nil, err
	}
	return c.UserInfo(ctx, jids)
}

func (l liveClient) ProfilePicture(ctx context.Context, jid wa.JID, params wa.ProfilePictureParams) (*wa.ProfilePicture, error) {
	c, err := l.c()
	if err != nil {
		return nil, err
	}
	return c.ProfilePicture(ctx, jid, params)
}

func (l liveClient) NewsletterInfo(ctx context.Context, jid wa.JID) (wa.NewsletterInfo, error) {
	c, err := l.c()
	if err != nil {
		return wa.NewsletterInfo{}, err
	}
	return c.NewsletterInfo(ctx, jid)
}

func (l liveClient) NewsletterInfoFromInvite(ctx context.Context, code string) (wa.NewsletterInfo, error) {
	c, err := l.c()
	if err != nil {
		return wa.NewsletterInfo{}, err
	}
	return c.NewsletterInfoFromInvite(ctx, code)
}
