package adapter

import (
	"fmt"

	"go.mau.fi/whatsmeow/types"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// FromWM converts a whatsmeow JID to a wa.JID without its device part (DESIGN.md §7.7).
// The empty JID becomes "".
func FromWM(j types.JID) wa.JID {
	if j.IsEmpty() {
		return ""
	}
	return wa.JID(j.ToNonAD().String())
}

// ToWM parses a wa.JID into whatsmeow's JID. The empty JID becomes types.EmptyJID.
func ToWM(j wa.JID) (types.JID, error) {
	if j == "" {
		return types.EmptyJID, nil
	}
	parsed, err := types.ParseJID(string(j))
	if err != nil {
		return types.EmptyJID, fmt.Errorf("parse jid %q: %w", j, err)
	}
	return parsed, nil
}

func toWM(j wa.JID) (types.JID, error) {
	if j == "" {
		return types.EmptyJID, fmt.Errorf("empty jid")
	}
	return ToWM(j)
}

func toWM2(a, b wa.JID) (types.JID, types.JID, error) {
	x, err := toWM(a)
	if err != nil {
		return x, types.EmptyJID, err
	}
	y, err := toWM(b)
	return x, y, err
}

func toWMs(jids []wa.JID) ([]types.JID, error) {
	out := make([]types.JID, len(jids))
	for i, j := range jids {
		var err error
		if out[i], err = toWM(j); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func participantResults(ps []types.GroupParticipant) []wa.ParticipantResult {
	out := make([]wa.ParticipantResult, len(ps))
	for i, p := range ps {
		out[i] = wa.ParticipantResult{JID: FromWM(p.JID), Err: participantError(p.Error)}
	}
	return out
}

func groupInfo(g *types.GroupInfo) wa.GroupInfo {
	info := wa.GroupInfo{
		JID:            FromWM(g.JID),
		Name:           g.Name,
		Topic:          g.Topic,
		Owner:          FromWM(g.OwnerJID),
		OwnerPN:        FromWM(g.OwnerPN),
		LinkedParent:   FromWM(g.LinkedParentJID),
		AddressingMode: string(g.AddressingMode),
		Announce:       g.IsAnnounce,
		Locked:         g.IsLocked,
		JoinApproval:   g.IsJoinApprovalRequired,
		Size:           g.ParticipantCount,
	}
	if info.Size == 0 {
		info.Size = len(g.Participants)
	}
	if len(g.Participants) > 0 {
		info.Participants = make([]wa.Participant, len(g.Participants))
		for i, p := range g.Participants {
			info.Participants[i] = wa.Participant{
				JID:          FromWM(p.JID),
				PhoneNumber:  FromWM(p.PhoneNumber),
				LID:          FromWM(p.LID),
				IsAdmin:      p.IsAdmin,
				IsSuperAdmin: p.IsSuperAdmin,
			}
		}
	}
	return info
}

func userInfo(u types.UserInfo) wa.UserInfo {
	info := wa.UserInfo{
		Status:    u.Status,
		PictureID: u.PictureID,
		Devices:   len(u.Devices),
		LID:       FromWM(u.LID),
	}
	if u.VerifiedName != nil && u.VerifiedName.Details != nil {
		info.VerifiedName = u.VerifiedName.Details.GetVerifiedName()
	}
	return info
}

func newsletterInfo(m *types.NewsletterMetadata) wa.NewsletterInfo {
	t := m.ThreadMeta
	info := wa.NewsletterInfo{
		JID:         FromWM(m.ID),
		Name:        t.Name.Text,
		Description: t.Description.Text,
		Subscribers: t.SubscriberCount,
		Verified:    t.VerificationState == types.NewsletterVerificationStateVerified,
		PictureURL:  t.Preview.URL,
	}
	if t.Picture != nil && t.Picture.URL != "" {
		info.PictureURL = t.Picture.URL
	}
	return info
}
