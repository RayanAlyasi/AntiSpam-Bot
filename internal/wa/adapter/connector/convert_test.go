package connector

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

const bot = wa.JID("966500000000@s.whatsapp.net")

var t0 = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

type fakePairs struct {
	lidToPN map[types.JID]types.JID
	pnToLID map[types.JID]types.JID
}

func (f fakePairs) GetPNForLID(_ context.Context, lid types.JID) (types.JID, error) {
	return f.lidToPN[lid], nil
}

func (f fakePairs) GetLIDForPN(_ context.Context, pn types.JID) (types.JID, error) {
	return f.pnToLID[pn], nil
}

func lidJ(u string) types.JID { return types.NewJID(u, types.HiddenUserServer) }
func pnJ(u string) types.JID  { return types.NewJID(u, types.DefaultUserServer) }

func TestJIDNormalizes(t *testing.T) {
	ad := types.JID{User: "123", Server: types.HiddenUserServer, Device: 7}
	hosted := types.JID{User: "9665", Server: types.HostedServer, Device: 99}
	tests := []struct {
		in   types.JID
		want wa.JID
	}{
		{ad, "123@lid"},
		{hosted, "9665@s.whatsapp.net"},
		{types.JID{User: "1", Server: types.HostedLIDServer}, "1@lid"},
		{types.NewJID("1203", types.GroupServer), "1203@g.us"},
		{types.EmptyJID, ""},
	}
	for _, tt := range tests {
		if got := jid(tt.in); got != tt.want {
			t.Errorf("jid(%v) = %q, want %q (one person, one JID per identifier)", tt.in, got, tt.want)
		}
	}
}

func TestConvertMessageKeepsRawProtoAndInfo(t *testing.T) {
	raw := &waE2E.Message{EphemeralMessage: &waE2E.FutureProofMessage{Message: &waE2E.Message{Conversation: proto.String("hi")}}}
	info := types.MessageInfo{
		MessageSource: types.MessageSource{
			Chat:   types.NewJID("1203", types.GroupServer),
			Sender: types.JID{User: "1", Server: types.HiddenUserServer, Device: 3}, SenderAlt: pnJ("9661"), IsGroup: true,
			AddressingMode: types.AddressingModeLID,
		},
		ID: "ABC", PushName: "Ali", Timestamp: t0, Edit: types.EditAttributeMessageEdit,
	}
	evt, ok, err := convert(t.Context(), bot, &events.Message{Info: info, RawMessage: raw, Message: raw.EphemeralMessage.Message}, nil)
	if err != nil || !ok {
		t.Fatalf("convert: %v %v", ok, err)
	}
	m := evt.(*wa.MessageEvent)
	want := wa.MessageInfo{
		Chat: "1203@g.us", Sender: "1@lid", SenderAlt: "9661@s.whatsapp.net", IsGroup: true,
		AddressingMode: "lid", ID: "ABC", PushName: "Ali", Timestamp: t0, Edit: "1",
	}
	if !reflect.DeepEqual(m.Info, want) || m.Bot != bot {
		t.Errorf("info = %+v, want %+v", m.Info, want)
	}
	var back waE2E.Message
	if err := proto.Unmarshal(m.Raw, &back); err != nil || back.GetEphemeralMessage() == nil {
		t.Errorf("raw proto must be the still-wrapped RawMessage, for exact replay: %v %v", err, &back)
	}
	var infoBack types.MessageInfo
	if err := json.Unmarshal(m.InfoJSON, &infoBack); err != nil || infoBack.ID != "ABC" {
		t.Errorf("InfoJSON round trip: %v %+v", err, infoBack)
	}
}

func TestConvertEvents(t *testing.T) {
	name := types.GroupName{Name: "Cars"}
	appr := types.GroupMembershipApprovalMode{IsJoinApprovalRequired: true}
	sender := lidJ("5")
	senderPN := pnJ("9665")
	pairs := fakePairs{
		lidToPN: map[types.JID]types.JID{lidJ("1"): pnJ("9661")},
		pnToLID: map[types.JID]types.JID{pnJ("9662"): lidJ("2")},
	}
	tests := []struct {
		name string
		in   any
		want wa.Event
	}{
		{
			"group info", &events.GroupInfo{
				JID: types.NewJID("1203", types.GroupServer), Sender: &sender, SenderPN: &senderPN,
				Timestamp: t0, Name: &name, MembershipApprovalMode: &appr, JoinReason: "invite",
				Join: []types.JID{{User: "1", Server: types.HiddenUserServer, Device: 2}, pnJ("9662"), lidJ("3")}, Leave: []types.JID{lidJ("1")},
			},
			&wa.GroupEvent{
				Bot: bot, Group: "1203@g.us", Sender: "5@lid", SenderPN: "9665@s.whatsapp.net", Timestamp: t0,
				Name: ptr("Cars"), JoinApproval: ptr(true), JoinReason: "invite",
				Join: []wa.JID{"1@lid", "9662@s.whatsapp.net", "3@lid"}, Leave: []wa.JID{"1@lid"},
				Pairs: []wa.JIDPair{{LID: "1@lid", PN: "9661@s.whatsapp.net"}, {LID: "2@lid", PN: "9662@s.whatsapp.net"}},
			},
		},
		{
			"group deleted", &events.GroupInfo{JID: types.NewJID("1203", types.GroupServer), Delete: &types.GroupDelete{}},
			&wa.GroupEvent{Bot: bot, Group: "1203@g.us", Deleted: true},
		},
		{
			"joined group", &events.JoinedGroup{Reason: "invite", Sender: &sender, GroupInfo: types.GroupInfo{
				JID: types.NewJID("1203", types.GroupServer), GroupName: name, AddressingMode: types.AddressingModeLID,
				Participants: []types.GroupParticipant{{JID: lidJ("1"), PhoneNumber: pnJ("9661"), LID: lidJ("1"), IsSuperAdmin: true}},
			}},
			&wa.JoinedGroupEvent{Bot: bot, Reason: "invite", Sender: "5@lid", Info: wa.GroupInfo{
				JID: "1203@g.us", Name: "Cars",
				AddressingMode: "lid", Size: 1, Participants: []wa.Participant{{JID: "1@lid", PhoneNumber: "9661@s.whatsapp.net", LID: "1@lid", IsSuperAdmin: true}},
			}},
		},
		{
			"picture", &events.Picture{JID: lidJ("1"), Timestamp: t0, PictureID: "p1"},
			&wa.ProfileEvent{Bot: bot, Kind: wa.ProfileKindPicture, JID: "1@lid", Timestamp: t0, PictureID: "p1"},
		},
		{
			"about", &events.UserAbout{JID: lidJ("1"), Status: "hello"},
			&wa.ProfileEvent{Bot: bot, Kind: wa.ProfileKindAbout, JID: "1@lid", About: "hello"},
		},
		{
			"push name", &events.PushName{JID: lidJ("1"), JIDAlt: pnJ("9661"), NewPushName: "Ali"},
			&wa.ProfileEvent{Bot: bot, Kind: wa.ProfileKindPushName, JID: "1@lid", JIDAlt: "9661@s.whatsapp.net", PushName: "Ali"},
		},
		{"connected", &events.Connected{}, &wa.ConnectionEvent{Bot: bot, State: wa.Connected}},
		{"disconnected", &events.Disconnected{}, &wa.ConnectionEvent{Bot: bot, State: wa.Disconnected}},
		{"keepalive", &events.KeepAliveTimeout{}, &wa.ConnectionEvent{Bot: bot, State: wa.KeepAliveTimeout}},
		{
			"temp ban", &events.TemporaryBan{Code: events.TempBanSentToTooManyPeople, Expire: time.Hour},
			&wa.AccountEvent{Bot: bot, Problem: wa.ProblemTemporaryBan, Code: 101, Expire: time.Hour},
		},
		{
			"logged out", &events.LoggedOut{Reason: events.ConnectFailureLoggedOut},
			&wa.AccountEvent{Bot: bot, Problem: wa.ProblemLoggedOut, Code: int(events.ConnectFailureLoggedOut)},
		},
		{"stream replaced", &events.StreamReplaced{}, &wa.AccountEvent{Bot: bot, Problem: wa.ProblemStreamReplaced}},
		{
			"undecryptable view once", &events.UndecryptableMessage{Info: types.MessageInfo{ID: "V1"}, IsUnavailable: true, UnavailableType: events.UnavailableTypeViewOnce},
			nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok, err := convert(t.Context(), bot, tt.in, pairs)
			if err != nil || !ok {
				t.Fatalf("convert: %v %v", ok, err)
			}
			if u, isU := got.(*wa.UndecryptableEvent); isU {
				if u.Info.ID != "V1" || !u.IsUnavailable || u.UnavailableType != "view_once" || len(u.InfoJSON) == 0 {
					t.Errorf("undecryptable = %+v: want ID, view_once and info JSON (we can still delete it)", u)
				}
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got  %#v\nwant %#v", got, tt.want)
			}
		})
	}
	if _, ok, _ := convert(t.Context(), bot, &events.Receipt{}, pairs); ok {
		t.Error("receipts are not used by the bot; want ok=false")
	}
}

func ptr[T any](v T) *T { return &v }

type fakeUsync struct {
	calls int
	lid   types.JID
	err   error
}

func (f *fakeUsync) GetUserInfo(_ context.Context, js []types.JID) (map[types.JID]types.UserInfo, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return map[types.JID]types.UserInfo{js[0]: {LID: f.lid}}, nil
}

func TestLIDResolver(t *testing.T) {
	us := &fakeUsync{lid: lidJ("7")}
	r := NewLIDResolver(fakePairs{
		lidToPN: map[types.JID]types.JID{lidJ("1"): pnJ("9661")},
		pnToLID: map[types.JID]types.JID{pnJ("9661"): lidJ("1")},
	}, us)
	ctx := t.Context()
	if pn, _ := r.PNForLID(ctx, "1@lid"); pn != "9661@s.whatsapp.net" {
		t.Errorf("PNForLID = %q", pn)
	}
	if pn, _ := r.PNForLID(ctx, "9661@s.whatsapp.net"); pn != "" {
		t.Errorf("PNForLID of a PN = %q, want empty", pn)
	}
	if lid, _ := r.LIDForPN(ctx, "9661@s.whatsapp.net"); lid != "1@lid" || us.calls != 0 {
		t.Errorf("LIDForPN known locally = %q with %d usync calls, want 1@lid and none", lid, us.calls)
	}
	if lid, _ := r.LIDForPN(ctx, "9667@s.whatsapp.net"); lid != "7@lid" || us.calls != 1 {
		t.Errorf("LIDForPN unknown = %q with %d usync calls, want 7@lid via one usync", lid, us.calls)
	}
	us.err = errors.New("429")
	if _, err := r.LIDForPN(ctx, "9668@s.whatsapp.net"); err == nil {
		t.Error("usync failure: nil error")
	}
	for range 10 {
		_, _ = r.LIDForPN(ctx, "9669@s.whatsapp.net")
	}
	if us.calls > 6 {
		t.Errorf("usync calls = %d after a burst, want the rate cap (≤ 5 burst + 1) to hold", us.calls)
	}
}

func TestSetupProfile(t *testing.T) {
	f := &fakeProfile{}
	if err := setupProfile(t.Context(), f, ""); err != nil {
		t.Fatal(err)
	}
	if f.about != DefaultAbout || f.setting != types.PrivacySettingTypeGroupAdd || f.value != types.PrivacySettingAll {
		t.Errorf("profile = %+v: want the appeal line and group-add = all (§8.5, §13.6)", f)
	}
	f.err = errors.New("boom")
	if err := setupProfile(t.Context(), f, "x"); err == nil {
		t.Error("setupProfile error: nil")
	}
}

type fakeProfile struct {
	about   string
	setting types.PrivacySettingType
	value   types.PrivacySetting
	err     error
}

func (f *fakeProfile) SetStatusMessage(_ context.Context, s types.SetStatusInput) error {
	f.about = *s.Text
	return f.err
}

func (f *fakeProfile) SetPrivacySetting(_ context.Context, n types.PrivacySettingType, v types.PrivacySetting) (types.PrivacySettings, error) {
	f.setting, f.value = n, v
	return types.PrivacySettings{}, f.err
}
