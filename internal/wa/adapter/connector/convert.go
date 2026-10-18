package connector

import (
	"context"
	"encoding/json"
	"fmt"

	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// jid converts a whatsmeow JID to the bot's non-AD string form (DESIGN.md §7.7: ToNonAD).
func jid(j types.JID) wa.JID {
	if j.IsEmpty() {
		return ""
	}
	return wa.CanonicalJID(j.ToNonAD().String())
}

func jidPtr(j *types.JID) wa.JID {
	if j == nil {
		return ""
	}
	return jid(*j)
}

func jids(js []types.JID) []wa.JID {
	if len(js) == 0 {
		return nil
	}
	out := make([]wa.JID, 0, len(js))
	for _, j := range js {
		out = append(out, jid(j))
	}
	return out
}

func messageInfo(i types.MessageInfo) wa.MessageInfo {
	out := wa.MessageInfo{
		Chat:           jid(i.Chat),
		Sender:         jid(i.Sender),
		SenderAlt:      jid(i.SenderAlt),
		IsFromMe:       i.IsFromMe,
		IsGroup:        i.IsGroup,
		AddressingMode: string(i.AddressingMode),
		ID:             wa.MessageID(i.ID),
		Type:           i.Type,
		PushName:       i.PushName,
		Timestamp:      i.Timestamp,
		Edit:           string(i.Edit),
		MediaType:      i.MediaType,
	}
	if i.VerifiedName != nil && i.VerifiedName.Details != nil {
		out.VerifiedName = i.VerifiedName.Details.GetVerifiedName()
	}
	return out
}

func groupInfo(g *types.GroupInfo) wa.GroupInfo {
	out := wa.GroupInfo{
		JID:            jid(g.JID),
		Name:           g.Name,
		Topic:          g.Topic,
		Owner:          jid(g.OwnerJID),
		OwnerPN:        jid(g.OwnerPN),
		LinkedParent:   jid(g.LinkedParentJID),
		IsCommunity:    g.IsParent,
		IsAnnouncement: g.IsDefaultSubGroup,
		AddressingMode: string(g.AddressingMode),
		Announce:       g.IsAnnounce,
		Locked:         g.IsLocked,
		JoinApproval:   g.IsJoinApprovalRequired,
		Size:           g.ParticipantCount,
	}
	if out.Size == 0 {
		out.Size = len(g.Participants)
	}
	for _, p := range g.Participants {
		out.Participants = append(out.Participants, wa.Participant{
			JID: jid(p.JID), PhoneNumber: jid(p.PhoneNumber), LID: jid(p.LID),
			IsAdmin: p.IsAdmin, IsSuperAdmin: p.IsSuperAdmin,
		})
	}
	return out
}

// pairLookup reads LID↔PN pairs that whatsmeow already knows (its local LID store).
type pairLookup interface {
	GetPNForLID(ctx context.Context, lid types.JID) (types.JID, error)
	GetLIDForPN(ctx context.Context, pn types.JID) (types.JID, error)
}

// convert turns a whatsmeow event into the bot's event type. ok is false for events the bot
// doesn't use. Only the local LID store is read (no network), so it is safe on the receive loop.
func convert(ctx context.Context, bot wa.JID, evt any, lids pairLookup) (wa.Event, bool, error) {
	switch e := evt.(type) {
	case *events.Message:
		raw := e.RawMessage
		if raw == nil {
			raw = e.Message
		}
		data, err := proto.Marshal(raw)
		if err != nil {
			return nil, false, fmt.Errorf("marshal message %s: %w", e.Info.ID, err)
		}
		info, err := json.Marshal(e.Info)
		if err != nil {
			return nil, false, fmt.Errorf("marshal message info %s: %w", e.Info.ID, err)
		}
		return &wa.MessageEvent{Bot: bot, Info: messageInfo(e.Info), Raw: data, InfoJSON: info}, true, nil
	case *events.UndecryptableMessage:
		info, err := json.Marshal(e.Info)
		if err != nil {
			return nil, false, fmt.Errorf("marshal message info %s: %w", e.Info.ID, err)
		}
		return &wa.UndecryptableEvent{
			Bot: bot, Info: messageInfo(e.Info), InfoJSON: info,
			IsUnavailable: e.IsUnavailable, UnavailableType: string(e.UnavailableType),
		}, true, nil
	case *events.GroupInfo:
		return groupEvent(ctx, bot, e, lids), true, nil
	case *events.JoinedGroup:
		return &wa.JoinedGroupEvent{
			Bot: bot, Info: groupInfo(&e.GroupInfo), Reason: e.Reason,
			Sender: jidPtr(e.Sender), SenderPN: jidPtr(e.SenderPN),
		}, true, nil
	case *events.Picture:
		return &wa.ProfileEvent{
			Bot: bot, Kind: wa.ProfileKindPicture, JID: jid(e.JID), Timestamp: e.Timestamp,
			PictureID: e.PictureID, Removed: e.Remove,
		}, true, nil
	case *events.UserAbout:
		return &wa.ProfileEvent{
			Bot: bot, Kind: wa.ProfileKindAbout, JID: jid(e.JID), Timestamp: e.Timestamp,
			About: e.Status,
		}, true, nil
	case *events.PushName:
		pe := &wa.ProfileEvent{
			Bot: bot, Kind: wa.ProfileKindPushName, JID: jid(e.JID), JIDAlt: jid(e.JIDAlt),
			PushName: e.NewPushName,
		}
		if e.Message != nil {
			pe.Timestamp = e.Message.Timestamp
		}
		return pe, true, nil
	case *events.Connected:
		return &wa.ConnectionEvent{Bot: bot, State: wa.Connected}, true, nil
	case *events.Disconnected:
		return &wa.ConnectionEvent{Bot: bot, State: wa.Disconnected}, true, nil
	case *events.KeepAliveTimeout:
		return &wa.ConnectionEvent{Bot: bot, State: wa.KeepAliveTimeout}, true, nil
	case *events.KeepAliveRestored:
		return &wa.ConnectionEvent{Bot: bot, State: wa.KeepAliveRestored}, true, nil
	case *events.TemporaryBan:
		return &wa.AccountEvent{Bot: bot, Problem: wa.ProblemTemporaryBan, Code: int(e.Code), Expire: e.Expire}, true, nil
	case *events.LoggedOut:
		return &wa.AccountEvent{Bot: bot, Problem: wa.ProblemLoggedOut, Code: int(e.Reason)}, true, nil
	case *events.StreamReplaced:
		return &wa.AccountEvent{Bot: bot, Problem: wa.ProblemStreamReplaced}, true, nil
	}
	return nil, false, nil
}

// communityLink turns whatsmeow's link change into a CommunityLink. The change names the other
// side: the parent when the event is about a group, a subgroup when it is about a community.
func communityLink(group wa.JID, c *types.GroupLinkChange) *wa.CommunityLink {
	if c == nil {
		return nil
	}
	other := jid(c.Group.JID)
	switch c.Type {
	case types.GroupLinkChangeTypeParent:
		return &wa.CommunityLink{Community: other, Group: group, Name: c.Group.Name}
	case types.GroupLinkChangeTypeSub:
		return &wa.CommunityLink{Community: group, Group: other, Name: c.Group.Name, IsAnnouncement: c.Group.IsDefaultSubGroup}
	}
	return nil // a sibling link says nothing about this group's community
}

func groupEvent(ctx context.Context, bot wa.JID, e *events.GroupInfo, lids pairLookup) *wa.GroupEvent {
	out := &wa.GroupEvent{
		Bot: bot, Group: jid(e.JID), Sender: jidPtr(e.Sender), SenderPN: jidPtr(e.SenderPN),
		Timestamp: e.Timestamp, JoinReason: e.JoinReason, Deleted: e.Delete != nil,
		Join: jids(e.Join), Leave: jids(e.Leave), Promote: jids(e.Promote), Demote: jids(e.Demote),
	}
	if e.Name != nil {
		out.Name = &e.Name.Name
	}
	if e.Topic != nil {
		out.Topic = &e.Topic.Topic
	}
	if e.Announce != nil {
		out.Announce = &e.Announce.IsAnnounce
	}
	if e.Locked != nil {
		out.Locked = &e.Locked.IsLocked
	}
	if e.MembershipApprovalMode != nil {
		out.JoinApproval = &e.MembershipApprovalMode.IsJoinApprovalRequired
	}
	out.Link, out.Unlink = communityLink(out.Group, e.Link), communityLink(out.Group, e.Unlink)
	if lids == nil {
		return out
	}
	// whatsmeow's participant-list parser stores the LID↔PN pairs the server sent; read them back
	// so the identity layer learns them (DESIGN.md §7.7, participant-list events).
	seen := map[types.JID]bool{}
	for _, list := range [][]types.JID{e.Join, e.Leave, e.Promote, e.Demote} {
		for _, j := range list {
			j = j.ToNonAD()
			if seen[j] {
				continue
			}
			seen[j] = true
			switch j.Server {
			case types.HiddenUserServer:
				if pn, err := lids.GetPNForLID(ctx, j); err == nil && !pn.IsEmpty() {
					out.Pairs = append(out.Pairs, wa.JIDPair{LID: jid(j), PN: jid(pn)})
				}
			case types.DefaultUserServer:
				if lid, err := lids.GetLIDForPN(ctx, j); err == nil && !lid.IsEmpty() {
					out.Pairs = append(out.Pairs, wa.JIDPair{LID: jid(lid), PN: jid(j)})
				}
			}
		}
	}
	return out
}
