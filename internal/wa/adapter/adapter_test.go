package adapter

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// stub records what the adapter sends and returns err from every call.
type stub struct {
	err  error
	sent []sent

	participants []types.GroupParticipant
	group        *types.GroupInfo
	users        map[types.JID]types.UserInfo
	picture      *types.ProfilePictureInfo
	newsletter   *types.NewsletterMetadata
	gotRemove    whatsmeow.ParticipantChange
	gotTargets   []types.JID
}

type sent struct {
	to  types.JID
	msg *waE2E.Message
}

func (s *stub) SendMessage(_ context.Context, to types.JID, m *waE2E.Message, _ ...whatsmeow.SendRequestExtra) (whatsmeow.SendResponse, error) {
	s.sent = append(s.sent, sent{to, m})
	return whatsmeow.SendResponse{ID: "SENT1", Timestamp: time.Unix(100, 0)}, s.err
}

func (s *stub) Upload(context.Context, []byte, whatsmeow.MediaType) (whatsmeow.UploadResponse, error) {
	return whatsmeow.UploadResponse{URL: "https://mmg/x", DirectPath: "/x", FileSHA256: []byte{1}, FileLength: 3}, s.err
}

// The Build* stubs mark their output so a test can tell which builder made a message.
func (s *stub) BuildRevoke(chat, sender types.JID, id types.MessageID) *waE2E.Message {
	return &waE2E.Message{Conversation: proto.String("revoke " + chat.String() + " " + sender.String() + " " + id)}
}

func (s *stub) BuildEdit(chat types.JID, id types.MessageID, c *waE2E.Message) *waE2E.Message {
	return &waE2E.Message{Conversation: proto.String("edit " + chat.String() + " " + id + " " + c.GetConversation())}
}

func (s *stub) BuildReaction(chat, sender types.JID, id types.MessageID, r string) *waE2E.Message {
	return &waE2E.Message{Conversation: proto.String("react " + sender.String() + " " + id + " " + r)}
}

func (s *stub) UpdateGroupParticipants(_ context.Context, _ types.JID, t []types.JID, a whatsmeow.ParticipantChange) ([]types.GroupParticipant, error) {
	s.gotRemove, s.gotTargets = a, t
	return s.participants, s.err
}

func (s *stub) GetGroupRequestParticipants(context.Context, types.JID) ([]types.GroupParticipantRequest, error) {
	return []types.GroupParticipantRequest{{JID: types.NewJID("1", types.HiddenUserServer), RequestedAt: time.Unix(5, 0)}}, s.err
}

func (s *stub) UpdateGroupRequestParticipants(_ context.Context, _ types.JID, t []types.JID, _ whatsmeow.ParticipantRequestChange) ([]types.GroupParticipant, error) {
	s.gotTargets = t
	return s.participants, s.err
}

func (s *stub) SetGroupAnnounce(context.Context, types.JID, bool) error         { return s.err }
func (s *stub) SetGroupLocked(context.Context, types.JID, bool) error           { return s.err }
func (s *stub) SetGroupJoinApprovalMode(context.Context, types.JID, bool) error { return s.err }
func (s *stub) GetGroupInviteLink(context.Context, types.JID, bool) (string, error) {
	return "https://chat.whatsapp.com/NEW", s.err
}

func (s *stub) GetGroupInfo(context.Context, types.JID) (*types.GroupInfo, error) {
	return s.group, s.err
}

func (s *stub) GetJoinedGroups(context.Context) ([]*types.GroupInfo, error) {
	return []*types.GroupInfo{s.group, nil}, s.err
}

func (s *stub) GetGroupInfoFromLink(context.Context, string) (*types.GroupInfo, error) {
	return s.group, s.err
}

func (s *stub) GetUserInfo(context.Context, []types.JID) (map[types.JID]types.UserInfo, error) {
	return s.users, s.err
}

func (s *stub) GetProfilePictureInfo(context.Context, types.JID, *whatsmeow.GetProfilePictureParams) (*types.ProfilePictureInfo, error) {
	return s.picture, s.err
}

func (s *stub) GetNewsletterInfo(context.Context, types.JID) (*types.NewsletterMetadata, error) {
	return s.newsletter, s.err
}

func (s *stub) GetNewsletterInfoWithInvite(context.Context, string) (*types.NewsletterMetadata, error) {
	return s.newsletter, s.err
}

func newTest(s *stub) (*Client, *clock.Fake) {
	clk := clock.NewFake(time.Unix(1000, 0))
	return &Client{cli: s, clock: clk}, clk
}

const (
	group  wa.JID = "1203@g.us"
	member wa.JID = "555@lid"
)

func TestErrorMapping(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want error // nil: no wa value
	}{
		{"rate limit", whatsmeow.ErrIQRateOverLimit, wa.ErrRateLimited},
		{"send 429", fmt.Errorf("%w %d", whatsmeow.ErrServerReturnedError, 429), wa.ErrRateLimited},
		{"iq timeout", whatsmeow.ErrIQTimedOut, wa.ErrTimeout},
		{"send timeout", whatsmeow.ErrMessageTimedOut, wa.ErrTimeout},
		{"deadline", context.DeadlineExceeded, wa.ErrTimeout},
		{"disconnected", whatsmeow.ErrIQDisconnected, wa.ErrTimeout},
		{"not connected", whatsmeow.ErrNotConnected, wa.ErrTimeout},
		{"forbidden", whatsmeow.ErrIQForbidden, wa.ErrNotAdmin},
		{"wrapped forbidden", fmt.Errorf("x: %w", whatsmeow.ErrIQForbidden), wa.ErrNotAdmin},
		{"send 403", fmt.Errorf("%w %d", whatsmeow.ErrServerReturnedError, 403), wa.ErrNotAdmin},
		{"no permission", whatsmeow.ErrNoPermissionInGroup, wa.ErrNotAdmin},
		{"not in group", whatsmeow.ErrNotInGroup, wa.ErrNotAdmin},
		{"iq 404", whatsmeow.ErrIQNotFound, wa.ErrNotFound},
		{"group gone", whatsmeow.ErrGroupNotFound, wa.ErrNotFound},
		{"invite revoked", whatsmeow.ErrInviteLinkRevoked, wa.ErrNotFound},
		{"invite invalid", whatsmeow.ErrInviteLinkInvalid, wa.ErrNotFound},
		{"send 404", fmt.Errorf("%w %d", whatsmeow.ErrServerReturnedError, 404), wa.ErrNotFound},
		{"send 500", fmt.Errorf("%w %d", whatsmeow.ErrServerReturnedError, 500), wa.ErrTimeout},
		{"iq 503", whatsmeow.ErrIQServiceUnavailable, wa.ErrTimeout},
		{"iq 429 with other text", &whatsmeow.IQError{Code: 429, Text: "too-many"}, wa.ErrRateLimited},
		{"send 400", fmt.Errorf("%w %d", whatsmeow.ErrServerReturnedError, 400), nil},
		{"bad request", whatsmeow.ErrIQBadRequest, nil},
		{"other", errors.New("boom"), nil},
	}
	all := []error{wa.ErrRateLimited, wa.ErrTimeout, wa.ErrNotAdmin, wa.ErrNotFound, wa.ErrTemporaryBan}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := newTest(&stub{err: tt.err})
			err := c.SetAnnounce(context.Background(), group, true)
			if !errors.Is(err, tt.err) {
				t.Errorf("error %v lost the whatsmeow error %v (logs need it)", err, tt.err)
			}
			for _, v := range all {
				if got, want := errors.Is(err, v), errors.Is(tt.want, v); got != want {
					t.Errorf("errors.Is(%v, %v) = %v, want %v (the executor routes on it)", err, v, got, want)
				}
			}
		})
	}
}

func TestTemporaryBan(t *testing.T) {
	s := &stub{}
	c, clk := newTest(s)
	if err := c.Revoke(context.Background(), group, wa.MessageRef{Sender: member, ID: "A"}); err != nil {
		t.Fatalf("Revoke before ban = %v, want nil", err)
	}
	c.HandleEvent(&events.TemporaryBan{Code: events.TempBanSentToTooManyPeople, Expire: time.Hour})
	clk.Advance(10 * time.Minute)
	err := c.Revoke(context.Background(), group, wa.MessageRef{Sender: member, ID: "B"})
	var tb *wa.TemporaryBanError
	if !errors.Is(err, wa.ErrTemporaryBan) || !errors.As(err, &tb) {
		t.Fatalf("Revoke during ban = %v, want a *wa.TemporaryBanError (the breaker must open)", err)
	}
	if tb.Code != 101 || tb.Expire != 50*time.Minute {
		t.Errorf("ban = code %d expire %s, want 101 and 50m left", tb.Code, tb.Expire)
	}
	if len(s.sent) != 1 {
		t.Errorf("sent %d messages, want 1: nothing may be sent during a ban", len(s.sent))
	}
	clk.Advance(time.Hour)
	if err := c.Revoke(context.Background(), group, wa.MessageRef{Sender: member, ID: "C"}); err != nil {
		t.Errorf("Revoke after the ban lifted = %v, want nil", err)
	}
	c.HandleEvent(&events.TemporaryBan{Code: events.TempBanSentToTooManyPeople, Expire: time.Hour})
	c.HandleEvent(&events.Connected{})
	if err := c.Revoke(context.Background(), group, wa.MessageRef{Sender: member, ID: "D"}); err != nil {
		t.Errorf("Revoke after reconnecting = %v, want nil (a reconnect ends the ban)", err)
	}
}

func TestWrites(t *testing.T) {
	ctx := context.Background()
	s := &stub{}
	c, _ := newTest(s)

	if err := c.Revoke(ctx, group, wa.MessageRef{Sender: "555:3@lid", ID: "M1"}); err != nil {
		t.Fatal(err)
	}
	if err := c.Edit(ctx, group, "M2", "new text"); err != nil {
		t.Fatal(err)
	}
	if err := c.React(ctx, group, wa.MessageRef{Sender: member, ID: "M3"}, "👍"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"revoke 1203@g.us 555:3@lid M1", // BuildRevoke + SendMessage, never RevokeMessage
		"edit 1203@g.us M2 new text",
		"react 555@lid M3 👍",
	}
	if len(s.sent) != len(want) {
		t.Fatalf("sent %d messages, want %d", len(s.sent), len(want))
	}
	for i, w := range want {
		if got := s.sent[i].msg.GetConversation(); got != w || s.sent[i].to.String() != string(group) {
			t.Errorf("message %d = %q to %s, want %q to %s", i, got, s.sent[i].to, w, group)
		}
	}
}

func TestSend(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name  string
		msg   wa.OutgoingMessage
		check func(*testing.T, *waE2E.Message)
	}{
		{"plain", wa.OutgoingMessage{Text: "hi"}, func(t *testing.T, m *waE2E.Message) {
			if m.GetConversation() != "hi" {
				t.Errorf("got %v, want a plain conversation", m)
			}
		}},
		{"reply", wa.OutgoingMessage{Text: "hi", ReplyTo: &wa.MessageRef{Sender: member, ID: "Q"}}, func(t *testing.T, m *waE2E.Message) {
			ci := m.GetExtendedTextMessage().GetContextInfo()
			if m.GetExtendedTextMessage().GetText() != "hi" || ci.GetStanzaID() != "Q" || ci.GetParticipant() != string(member) {
				t.Errorf("got %v, want an extended text quoting Q from %s", m, member)
			}
		}},
		{"image", wa.OutgoingMessage{Text: "cap", Image: &wa.Image{Data: []byte("img"), Mimetype: "image/jpeg"}}, func(t *testing.T, m *waE2E.Message) {
			im := m.GetImageMessage()
			if im.GetCaption() != "cap" || im.GetURL() != "https://mmg/x" || im.GetMimetype() != "image/jpeg" || im.GetFileLength() != 3 {
				t.Errorf("got %v, want an uploaded image with its caption", m)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &stub{}
			c, _ := newTest(s)
			res, err := c.Send(ctx, group, tt.msg)
			if err != nil || res.ID != "SENT1" || !res.Timestamp.Equal(time.Unix(100, 0)) {
				t.Fatalf("Send = %+v, %v; want the server's ID and timestamp", res, err)
			}
			tt.check(t, s.sent[0].msg)
		})
	}
}

func TestParticipantResults(t *testing.T) {
	s := &stub{participants: []types.GroupParticipant{
		{JID: types.NewJID("1", types.HiddenUserServer)},
		{JID: types.NewJID("2", types.HiddenUserServer), Error: 404},
		{JID: types.NewJID("3", types.HiddenUserServer), Error: 403},
		{JID: types.NewJID("4", types.HiddenUserServer), Error: 500},
	}}
	c, _ := newTest(s)
	res, err := c.UpdateParticipants(context.Background(), group, []wa.JID{"1@lid", "2@lid", "3@lid", "4@lid"}, wa.ParticipantRemove)
	if err != nil {
		t.Fatal(err)
	}
	if s.gotRemove != whatsmeow.ParticipantChangeRemove || len(s.gotTargets) != 4 {
		t.Errorf("called with %q and %d targets, want one batched remove of 4", s.gotRemove, len(s.gotTargets))
	}
	wantErr := []error{nil, wa.ErrNotFound, wa.ErrNotAdmin, nil}
	for i, r := range res {
		if r.JID != wa.JID(fmt.Sprintf("%d@lid", i+1)) {
			t.Errorf("result %d JID = %s", i, r.JID)
		}
		switch {
		case wantErr[i] != nil && !errors.Is(r.Err, wantErr[i]):
			t.Errorf("result %d err = %v, want %v", i, r.Err, wantErr[i])
		case i == 0 && r.Err != nil:
			t.Errorf("result 0 err = %v, want nil (success)", r.Err)
		case i == 3 && r.Err == nil:
			t.Errorf("result 3 err = nil, want an error for code 500")
		}
	}
	if _, err := c.UpdateParticipants(context.Background(), group, []wa.JID{"1@lid"}, 0); err == nil {
		t.Error("an unknown participant action must fail, not silently remove")
	}
	if _, err := c.UpdateJoinRequests(context.Background(), group, []wa.JID{"1@lid"}, wa.JoinRequestApprove); err != nil {
		t.Errorf("UpdateJoinRequests = %v", err)
	}
	reqs, err := c.JoinRequests(context.Background(), group)
	if err != nil || len(reqs) != 1 || reqs[0].JID != "1@lid" {
		t.Errorf("JoinRequests = %v, %v", reqs, err)
	}
}

func TestReads(t *testing.T) {
	ctx := context.Background()
	gi := &types.GroupInfo{
		JID:      types.NewJID("1203", types.GroupServer),
		OwnerJID: types.NewADJID("9", 0, 4),
		Participants: []types.GroupParticipant{
			{JID: types.NewJID("1", types.HiddenUserServer), LID: types.NewJID("1", types.HiddenUserServer), PhoneNumber: types.NewJID("966", types.DefaultUserServer), IsAdmin: true},
		},
	}
	gi.Name, gi.Topic, gi.IsAnnounce, gi.IsJoinApprovalRequired = "Group", "Topic", true, true
	s := &stub{
		group:   gi,
		users:   map[types.JID]types.UserInfo{types.NewJID("1", types.HiddenUserServer): {Status: "about", Devices: []types.JID{{}, {}}}},
		picture: &types.ProfilePictureInfo{ID: "P", URL: "https://pps/x"},
		newsletter: &types.NewsletterMetadata{
			ID: types.NewJID("77", types.NewsletterServer),
			ThreadMeta: types.NewsletterThreadMetadata{
				Name: types.NewsletterText{Text: "Chan"}, SubscriberCount: 12,
				VerificationState: types.NewsletterVerificationStateVerified,
			},
		},
	}
	c, _ := newTest(s)

	info, err := c.GroupInfo(ctx, group)
	if err != nil {
		t.Fatal(err)
	}
	if info.JID != group || info.Name != "Group" || info.Topic != "Topic" || !info.Announce || !info.JoinApproval ||
		info.Owner != "9@s.whatsapp.net" || info.Size != 1 || len(info.Participants) != 1 ||
		info.Participants[0].PhoneNumber != "966@s.whatsapp.net" || !info.Participants[0].IsAdmin {
		t.Errorf("GroupInfo = %+v (the owner's device part must be stripped)", info)
	}
	if groups, err := c.JoinedGroups(ctx); err != nil || len(groups) != 1 {
		t.Errorf("JoinedGroups = %v, %v; want 1 group (nil entries skipped)", groups, err)
	}
	users, err := c.UserInfo(ctx, []wa.JID{"1@lid"})
	if err != nil || users["1@lid"].Status != "about" || users["1@lid"].Devices != 2 {
		t.Errorf("UserInfo = %v, %v", users, err)
	}
	pic, err := c.ProfilePicture(ctx, member, wa.ProfilePictureParams{CommonGroup: group})
	if err != nil || pic == nil || pic.ID != "P" {
		t.Errorf("ProfilePicture = %v, %v", pic, err)
	}
	s.picture = nil
	if pic, err := c.ProfilePicture(ctx, member, wa.ProfilePictureParams{ExistingID: "P"}); pic != nil || err != nil {
		t.Errorf("ProfilePicture unchanged = %v, %v; want nil, nil", pic, err)
	}
	nl, err := c.NewsletterInfoFromInvite(ctx, "code")
	if err != nil || nl.JID != "77@newsletter" || nl.Name != "Chan" || !nl.Verified || nl.Subscribers != 12 {
		t.Errorf("NewsletterInfoFromInvite = %+v, %v", nl, err)
	}
	if link, err := c.ResetInviteLink(ctx, group); err != nil || link != "https://chat.whatsapp.com/NEW" {
		t.Errorf("ResetInviteLink = %q, %v", link, err)
	}
	s.newsletter, s.group = nil, nil
	if _, err := c.NewsletterInfoFromInvite(ctx, "code"); !errors.Is(err, wa.ErrNotFound) {
		t.Errorf("NewsletterInfoFromInvite with no result = %v, want wa.ErrNotFound, not a panic", err)
	}
	if _, err := c.GroupInfoFromLink(ctx, "code"); !errors.Is(err, wa.ErrNotFound) {
		t.Errorf("GroupInfoFromLink with no result = %v, want wa.ErrNotFound", err)
	}
	if _, err := c.GroupInfo(ctx, ""); err == nil {
		t.Error("an empty group JID must fail, not act on nothing")
	}
}

func TestJIDConversion(t *testing.T) {
	tests := []struct {
		in   types.JID
		want wa.JID
	}{
		{types.EmptyJID, ""},
		{types.NewADJID("555", 0, 7), "555@s.whatsapp.net"},
		{types.NewJID("1203", types.GroupServer), "1203@g.us"},
		{types.StatusBroadcastJID, "status@broadcast"},
	}
	for _, tt := range tests {
		if got := FromWM(tt.in); got != tt.want {
			t.Errorf("FromWM(%v) = %q, want %q (one person, one JID: no device part)", tt.in, got, tt.want)
		}
		back, err := ToWM(tt.want)
		if err != nil || FromWM(back) != tt.want {
			t.Errorf("ToWM(%q) = %v, %v; want a round trip", tt.want, back, err)
		}
	}
}
