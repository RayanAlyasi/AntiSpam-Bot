package fake

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// Method names, as recorded in Call.Method and accepted by FailNext and FailAlways.
const (
	MethodSend                     = "Send"
	MethodReact                    = "React"
	MethodRevoke                   = "Revoke"
	MethodEdit                     = "Edit"
	MethodUpdateParticipants       = "UpdateParticipants"
	MethodJoinRequests             = "JoinRequests"
	MethodUpdateJoinRequests       = "UpdateJoinRequests"
	MethodSetAnnounce              = "SetAnnounce"
	MethodSetLocked                = "SetLocked"
	MethodSetJoinApproval          = "SetJoinApproval"
	MethodResetInviteLink          = "ResetInviteLink"
	MethodGroupInfo                = "GroupInfo"
	MethodJoinedGroups             = "JoinedGroups"
	MethodGroupInfoFromLink        = "GroupInfoFromLink"
	MethodSubGroups                = "SubGroups"
	MethodUserInfo                 = "UserInfo"
	MethodProfilePicture           = "ProfilePicture"
	MethodNewsletterInfo           = "NewsletterInfo"
	MethodNewsletterInfoFromInvite = "NewsletterInfoFromInvite"
)

// ErrSuperAdmin is the per-participant result for an attempt to remove a group's superadmin,
// which WhatsApp doesn't allow (DESIGN.md §2.3).
var ErrSuperAdmin = errors.New("fake: can't remove the group's superadmin")

// Call is one recorded WAClient call. Args holds the arguments after ctx, in order.
type Call struct {
	Method string
	Args   []any
	Err    error // what the call returned
}

// Client is an in-memory wa.WAClient for tests. It records every call (including failed ones),
// serves the groups, users, pictures and channels a test sets up, and returns injected errors
// such as wa.ErrRateLimited, a *wa.TemporaryBanError, wa.ErrNotAdmin or wa.ErrTimeout.
// It's safe for concurrent use.
type Client struct {
	mu sync.Mutex
	// Clock stamps sent messages. It defaults to clock.Real.
	Clock clock.Clock

	calls   []Call
	failQ   map[string][]error
	failAll map[string]error
	nextID  int
	links   int

	groups            map[wa.JID]wa.GroupInfo
	inviteLinks       map[string]wa.GroupInfo
	users             map[wa.JID]wa.UserInfo
	pictures          map[wa.JID]wa.ProfilePicture
	newsletters       map[wa.JID]wa.NewsletterInfo
	newsletterInvites map[string]wa.NewsletterInfo
	joinRequests      map[wa.JID][]wa.JoinRequest
	subGroups         map[wa.JID]map[wa.JID]wa.SubGroup // community → groups the bot isn't in
}

var (
	_ wa.WAClient        = (*Client)(nil)
	_ wa.CommunityReader = (*Client)(nil)
)

// New returns an empty fake client.
func New() *Client {
	return &Client{
		failQ:             map[string][]error{},
		failAll:           map[string]error{},
		groups:            map[wa.JID]wa.GroupInfo{},
		inviteLinks:       map[string]wa.GroupInfo{},
		users:             map[wa.JID]wa.UserInfo{},
		pictures:          map[wa.JID]wa.ProfilePicture{},
		newsletters:       map[wa.JID]wa.NewsletterInfo{},
		newsletterInvites: map[string]wa.NewsletterInfo{},
		joinRequests:      map[wa.JID][]wa.JoinRequest{},
		subGroups:         map[wa.JID]map[wa.JID]wa.SubGroup{},
	}
}

// FailNext makes the next len(errs) calls to method return errs, one per call, in order.
func (c *Client) FailNext(method string, errs ...error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failQ[method] = append(c.failQ[method], errs...)
}

// FailAlways makes every call to method return err, after any queued FailNext errors.
// A nil err stops it.
func (c *Client) FailAlways(method string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err == nil {
		delete(c.failAll, method)
		return
	}
	c.failAll[method] = err
}

// Calls returns every recorded call, oldest first.
func (c *Client) Calls() []Call {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.calls)
}

// CallsTo returns the recorded calls to one method, oldest first.
func (c *Client) CallsTo(method string) []Call {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []Call
	for _, call := range c.calls {
		if call.Method == method {
			out = append(out, call)
		}
	}
	return out
}

// SetGroup adds or replaces a group the fake knows about.
func (c *Client) SetGroup(info wa.GroupInfo) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.groups[info.JID] = cloneGroup(info)
}

// SetSubGroup lists g as a group of community that the bot isn't in. Groups set with SetGroup
// whose LinkedParent is community are listed too; a community is a group set with SetGroup and
// IsCommunity.
func (c *Client) SetSubGroup(community wa.JID, g wa.SubGroup) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.subGroups[community] == nil {
		c.subGroups[community] = map[wa.JID]wa.SubGroup{}
	}
	c.subGroups[community][g.JID] = g
}

// SetInviteLink makes GroupInfoFromLink(code) return info.
func (c *Client) SetInviteLink(code string, info wa.GroupInfo) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.inviteLinks[code] = cloneGroup(info)
}

// SetUser makes UserInfo return info for jid.
func (c *Client) SetUser(jid wa.JID, info wa.UserInfo) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.users[jid] = info
}

// SetProfilePicture makes ProfilePicture return pic for jid.
func (c *Client) SetProfilePicture(jid wa.JID, pic wa.ProfilePicture) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pictures[jid] = pic
}

// SetNewsletter makes NewsletterInfo return info for info.JID.
func (c *Client) SetNewsletter(info wa.NewsletterInfo) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.newsletters[info.JID] = info
}

// SetNewsletterInvite makes NewsletterInfoFromInvite(code) return info.
func (c *Client) SetNewsletterInvite(code string, info wa.NewsletterInfo) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.newsletterInvites[code] = info
}

// SetJoinRequests sets the pending join requests of a group.
func (c *Client) SetJoinRequests(group wa.JID, reqs []wa.JoinRequest) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.joinRequests[group] = slices.Clone(reqs)
}

// begin records a call and returns the error it must fail with, if any. It must be called
// with c.mu held. The context is checked first, so a cancelled call fails like a real one.
func (c *Client) begin(ctx context.Context, method string, args ...any) error {
	err := ctx.Err()
	if err == nil {
		if q := c.failQ[method]; len(q) > 0 {
			err, c.failQ[method] = q[0], q[1:]
		} else {
			err = c.failAll[method]
		}
	}
	c.calls = append(c.calls, Call{Method: method, Args: args, Err: err})
	return err
}

// record stores the result of a call that passed begin, so Call.Err reflects behavioural
// errors (such as wa.ErrNotFound) too.
func (c *Client) record(err error) error {
	c.calls[len(c.calls)-1].Err = err
	return err
}

func (c *Client) now() clock.Clock {
	if c.Clock == nil {
		return clock.Real{}
	}
	return c.Clock
}

// Send records the message and returns a new message ID.
func (c *Client) Send(ctx context.Context, chat wa.JID, msg wa.OutgoingMessage) (wa.SendResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.begin(ctx, MethodSend, chat, msg); err != nil {
		return wa.SendResult{}, err
	}
	c.nextID++
	return wa.SendResult{ID: wa.MessageID(fmt.Sprintf("FAKE%06d", c.nextID)), Timestamp: c.now().Now()}, nil
}

// React records the reaction.
func (c *Client) React(ctx context.Context, chat wa.JID, target wa.MessageRef, emoji string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.begin(ctx, MethodReact, chat, target, emoji)
}

// Revoke records the deletion.
func (c *Client) Revoke(ctx context.Context, chat wa.JID, target wa.MessageRef) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.begin(ctx, MethodRevoke, chat, target)
}

// Edit records the edit.
func (c *Client) Edit(ctx context.Context, chat wa.JID, id wa.MessageID, text string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.begin(ctx, MethodEdit, chat, id, text)
}

// UpdateParticipants removes the JIDs from a group set with SetGroup. Someone who isn't a
// participant gets wa.ErrNotFound in their result, and the group's superadmin can't be removed
// (ErrSuperAdmin), like on the real server. An unknown group fails with wa.ErrNotFound.
func (c *Client) UpdateParticipants(ctx context.Context, group wa.JID, jids []wa.JID, action wa.ParticipantAction) ([]wa.ParticipantResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.begin(ctx, MethodUpdateParticipants, group, slices.Clone(jids), action); err != nil {
		return nil, err
	}
	if action != wa.ParticipantRemove {
		return nil, c.record(fmt.Errorf("fake: unsupported participant action %d", action))
	}
	info, ok := c.groups[group]
	if !ok {
		return nil, c.record(fmt.Errorf("group %s: %w", group, wa.ErrNotFound))
	}
	results := make([]wa.ParticipantResult, 0, len(jids))
	for _, jid := range jids {
		res := wa.ParticipantResult{JID: jid}
		i := slices.IndexFunc(info.Participants, func(p wa.Participant) bool { return p.JID == jid })
		switch {
		case i < 0:
			res.Err = wa.ErrNotFound
		case info.Participants[i].IsSuperAdmin:
			res.Err = ErrSuperAdmin
		default:
			info.Participants = slices.Delete(info.Participants, i, i+1)
			info.Size = len(info.Participants)
		}
		results = append(results, res)
	}
	c.groups[group] = info
	return results, nil
}

// JoinRequests returns the pending requests set with SetJoinRequests.
func (c *Client) JoinRequests(ctx context.Context, group wa.JID) ([]wa.JoinRequest, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.begin(ctx, MethodJoinRequests, group); err != nil {
		return nil, err
	}
	return slices.Clone(c.joinRequests[group]), nil
}

// UpdateJoinRequests answers pending requests and removes them from the list. A JID with no
// pending request gets wa.ErrNotFound in its result.
func (c *Client) UpdateJoinRequests(ctx context.Context, group wa.JID, jids []wa.JID, action wa.JoinRequestAction) ([]wa.ParticipantResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.begin(ctx, MethodUpdateJoinRequests, group, slices.Clone(jids), action); err != nil {
		return nil, err
	}
	pending := c.joinRequests[group]
	results := make([]wa.ParticipantResult, 0, len(jids))
	for _, jid := range jids {
		res := wa.ParticipantResult{JID: jid}
		i := slices.IndexFunc(pending, func(r wa.JoinRequest) bool { return r.JID == jid })
		if i < 0 {
			res.Err = wa.ErrNotFound
		} else {
			pending = slices.Delete(pending, i, i+1)
		}
		results = append(results, res)
	}
	c.joinRequests[group] = pending
	return results, nil
}

// SetAnnounce applies the change to a group set with SetGroup (wa.ErrNotFound otherwise).
func (c *Client) SetAnnounce(ctx context.Context, group wa.JID, on bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.begin(ctx, MethodSetAnnounce, group, on); err != nil {
		return err
	}
	return c.updateGroup(group, func(g *wa.GroupInfo) { g.Announce = on })
}

// SetLocked applies the change to a group set with SetGroup (wa.ErrNotFound otherwise).
func (c *Client) SetLocked(ctx context.Context, group wa.JID, on bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.begin(ctx, MethodSetLocked, group, on); err != nil {
		return err
	}
	return c.updateGroup(group, func(g *wa.GroupInfo) { g.Locked = on })
}

// SetJoinApproval applies the change to a group set with SetGroup (wa.ErrNotFound otherwise).
func (c *Client) SetJoinApproval(ctx context.Context, group wa.JID, on bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.begin(ctx, MethodSetJoinApproval, group, on); err != nil {
		return err
	}
	return c.updateGroup(group, func(g *wa.GroupInfo) { g.JoinApproval = on })
}

// updateGroup applies change to a known group, or records wa.ErrNotFound for an unknown one.
func (c *Client) updateGroup(group wa.JID, change func(*wa.GroupInfo)) error {
	g, ok := c.groups[group]
	if !ok {
		return c.record(fmt.Errorf("group %s: %w", group, wa.ErrNotFound))
	}
	change(&g)
	c.groups[group] = g
	return nil
}

// ResetInviteLink returns a new, unique invite link.
func (c *Client) ResetInviteLink(ctx context.Context, group wa.JID) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.begin(ctx, MethodResetInviteLink, group); err != nil {
		return "", err
	}
	c.links++
	return fmt.Sprintf("https://chat.whatsapp.com/FAKE%06d", c.links), nil
}

// GroupInfo returns a group set with SetGroup, or wa.ErrNotFound.
func (c *Client) GroupInfo(ctx context.Context, group wa.JID) (wa.GroupInfo, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.begin(ctx, MethodGroupInfo, group); err != nil {
		return wa.GroupInfo{}, err
	}
	info, ok := c.groups[group]
	if !ok {
		return wa.GroupInfo{}, c.record(fmt.Errorf("group %s: %w", group, wa.ErrNotFound))
	}
	return cloneGroup(info), nil
}

// JoinedGroups returns every group set with SetGroup, sorted by JID.
func (c *Client) JoinedGroups(ctx context.Context) ([]wa.GroupInfo, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.begin(ctx, MethodJoinedGroups); err != nil {
		return nil, err
	}
	out := make([]wa.GroupInfo, 0, len(c.groups))
	for _, g := range c.groups {
		out = append(out, cloneGroup(g))
	}
	slices.SortFunc(out, func(a, b wa.GroupInfo) int { return cmp.Compare(a.JID, b.JID) })
	return out, nil
}

// SubGroups lists a community's groups, sorted by JID: those set with SetGroup and linked to it,
// and those set with SetSubGroup. A community the fake doesn't know returns wa.ErrNotFound.
func (c *Client) SubGroups(ctx context.Context, community wa.JID) ([]wa.SubGroup, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.begin(ctx, MethodSubGroups, community); err != nil {
		return nil, err
	}
	byJID := maps.Clone(c.subGroups[community])
	for _, g := range c.groups {
		if g.LinkedParent == community {
			if byJID == nil {
				byJID = map[wa.JID]wa.SubGroup{}
			}
			byJID[g.JID] = wa.SubGroup{JID: g.JID, Name: g.Name, IsAnnouncement: g.IsAnnouncement}
		}
	}
	if _, ok := c.groups[community]; !ok && byJID == nil {
		return nil, c.record(fmt.Errorf("community %s: %w", community, wa.ErrNotFound))
	}
	out := slices.Collect(maps.Values(byJID))
	slices.SortFunc(out, func(a, b wa.SubGroup) int { return cmp.Compare(a.JID, b.JID) })
	return out, nil
}

// GroupInfoFromLink returns the group set with SetInviteLink, or wa.ErrNotFound.
func (c *Client) GroupInfoFromLink(ctx context.Context, code string) (wa.GroupInfo, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.begin(ctx, MethodGroupInfoFromLink, code); err != nil {
		return wa.GroupInfo{}, err
	}
	info, ok := c.inviteLinks[code]
	if !ok {
		return wa.GroupInfo{}, c.record(fmt.Errorf("invite %s: %w", code, wa.ErrNotFound))
	}
	return cloneGroup(info), nil
}

// UserInfo returns the users set with SetUser; unknown JIDs are left out.
func (c *Client) UserInfo(ctx context.Context, jids []wa.JID) (map[wa.JID]wa.UserInfo, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.begin(ctx, MethodUserInfo, slices.Clone(jids)); err != nil {
		return nil, err
	}
	out := make(map[wa.JID]wa.UserInfo, len(jids))
	for _, jid := range jids {
		if info, ok := c.users[jid]; ok {
			out[jid] = info
		}
	}
	return out, nil
}

// ProfilePicture returns the picture set with SetProfilePicture, nil if its ID equals
// params.ExistingID, or wa.ErrNotFound if there is none.
func (c *Client) ProfilePicture(ctx context.Context, jid wa.JID, params wa.ProfilePictureParams) (*wa.ProfilePicture, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.begin(ctx, MethodProfilePicture, jid, params); err != nil {
		return nil, err
	}
	pic, ok := c.pictures[jid]
	if !ok {
		return nil, c.record(fmt.Errorf("profile picture %s: %w", jid, wa.ErrNotFound))
	}
	if params.ExistingID != "" && params.ExistingID == pic.ID {
		return nil, nil
	}
	return &pic, nil
}

// NewsletterInfo returns the channel set with SetNewsletter, or wa.ErrNotFound.
func (c *Client) NewsletterInfo(ctx context.Context, jid wa.JID) (wa.NewsletterInfo, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.begin(ctx, MethodNewsletterInfo, jid); err != nil {
		return wa.NewsletterInfo{}, err
	}
	info, ok := c.newsletters[jid]
	if !ok {
		return wa.NewsletterInfo{}, c.record(fmt.Errorf("newsletter %s: %w", jid, wa.ErrNotFound))
	}
	return info, nil
}

// NewsletterInfoFromInvite returns the channel set with SetNewsletterInvite, or wa.ErrNotFound.
func (c *Client) NewsletterInfoFromInvite(ctx context.Context, code string) (wa.NewsletterInfo, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.begin(ctx, MethodNewsletterInfoFromInvite, code); err != nil {
		return wa.NewsletterInfo{}, err
	}
	info, ok := c.newsletterInvites[code]
	if !ok {
		return wa.NewsletterInfo{}, c.record(fmt.Errorf("newsletter invite %s: %w", code, wa.ErrNotFound))
	}
	return info, nil
}

func cloneGroup(g wa.GroupInfo) wa.GroupInfo {
	g.Participants = slices.Clone(g.Participants)
	return g
}
