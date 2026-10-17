package fake

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

const (
	group wa.JID = "120363000000000001@g.us"
	alice wa.JID = "111@lid"
	bob   wa.JID = "222@lid"
	carol wa.JID = "333@lid"
)

func TestRecordsEveryCallInOrder(t *testing.T) {
	ctx := context.Background()
	c := New()
	start := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	c.Clock = clock.NewFake(start)

	res, err := c.Send(ctx, alice, wa.OutgoingMessage{Text: "hi"})
	if err != nil || res.ID == "" || !res.Timestamp.Equal(start) {
		t.Fatalf("Send() = %+v, %v; want an ID stamped %v", res, err, start)
	}
	res2, _ := c.Send(ctx, alice, wa.OutgoingMessage{Text: "again"})
	if res2.ID == res.ID {
		t.Fatalf("two sends got the same ID %q; IDs must be unique so reviews can be matched", res.ID)
	}
	target := wa.MessageRef{Sender: bob, ID: "M1"}
	if err := c.Revoke(ctx, group, target); err != nil {
		t.Fatalf("Revoke() = %v", err)
	}
	if err := c.React(ctx, group, target, "👌"); err != nil {
		t.Fatalf("React() = %v", err)
	}

	calls := c.Calls()
	var methods []string
	for _, call := range calls {
		methods = append(methods, call.Method)
	}
	want := []string{MethodSend, MethodSend, MethodRevoke, MethodReact}
	if !slices.Equal(methods, want) {
		t.Fatalf("recorded %v, want %v", methods, want)
	}
	revokes := c.CallsTo(MethodRevoke)
	if len(revokes) != 1 || revokes[0].Args[0] != group || revokes[0].Args[1] != target {
		t.Fatalf("CallsTo(Revoke) = %+v, want one call with (%s, %+v)", revokes, group, target)
	}
}

func TestInjectedErrors(t *testing.T) {
	ctx := context.Background()
	ban := &wa.TemporaryBanError{Code: 101, Expire: time.Hour}
	tests := []struct {
		name string
		err  error
		is   error
	}{
		{"rate limit 429", wa.ErrRateLimited, wa.ErrRateLimited},
		{"temporary ban", ban, wa.ErrTemporaryBan},
		{"not admin", wa.ErrNotAdmin, wa.ErrNotAdmin},
		{"timeout", wa.ErrTimeout, wa.ErrTimeout},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := New()
			c.FailNext(MethodRevoke, tt.err)
			err := c.Revoke(ctx, group, wa.MessageRef{Sender: bob, ID: "M1"})
			if !errors.Is(err, tt.is) {
				t.Fatalf("Revoke() = %v, want %v", err, tt.is)
			}
			if calls := c.CallsTo(MethodRevoke); len(calls) != 1 || !errors.Is(calls[0].Err, tt.is) {
				t.Fatalf("failed call not recorded with its error: %+v", calls)
			}
			if err := c.Revoke(ctx, group, wa.MessageRef{Sender: bob, ID: "M2"}); err != nil {
				t.Fatalf("FailNext must fail only one call; second Revoke() = %v", err)
			}
		})
	}
}

func TestFailNextQueueThenFailAlways(t *testing.T) {
	ctx := context.Background()
	c := New()
	c.SetGroup(wa.GroupInfo{JID: group})
	c.FailNext(MethodSetAnnounce, wa.ErrTimeout, wa.ErrRateLimited)
	c.FailAlways(MethodSetAnnounce, wa.ErrNotAdmin)
	want := []error{wa.ErrTimeout, wa.ErrRateLimited, wa.ErrNotAdmin, wa.ErrNotAdmin}
	for i, w := range want {
		if err := c.SetAnnounce(ctx, group, true); !errors.Is(err, w) {
			t.Fatalf("call %d: got %v, want %v", i+1, err, w)
		}
	}
	c.FailAlways(MethodSetAnnounce, nil)
	if err := c.SetAnnounce(ctx, group, true); err != nil {
		t.Fatalf("after FailAlways(nil): got %v, want nil", err)
	}
	if err := c.SetLocked(ctx, group, true); err != nil {
		t.Fatalf("errors for one method leaked into another: %v", err)
	}
}

func TestCancelledContextFailsAndIsRecorded(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := New()
	if _, err := c.Send(ctx, alice, wa.OutgoingMessage{Text: "x"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Send() with a cancelled context = %v, want context.Canceled", err)
	}
	if len(c.Calls()) != 1 {
		t.Fatalf("cancelled call not recorded: %+v", c.Calls())
	}
}

func TestUpdateParticipantsRemovesMembers(t *testing.T) {
	ctx := context.Background()
	c := New()
	c.SetGroup(wa.GroupInfo{JID: group, Size: 2, Participants: []wa.Participant{{JID: alice}, {JID: bob}}})

	results, err := c.UpdateParticipants(ctx, group, []wa.JID{alice, carol}, wa.ParticipantRemove)
	if err != nil {
		t.Fatalf("UpdateParticipants() = %v", err)
	}
	if len(results) != 2 || results[0].Err != nil || !errors.Is(results[1].Err, wa.ErrNotFound) {
		t.Fatalf("results = %+v, want alice removed and carol not found", results)
	}
	info, err := c.GroupInfo(ctx, group)
	if err != nil {
		t.Fatalf("GroupInfo() = %v", err)
	}
	if len(info.Participants) != 1 || info.Participants[0].JID != bob || info.Size != 1 {
		t.Fatalf("group after removal = %+v, want only bob", info)
	}
}

func TestUnknownGroupAndSuperadmin(t *testing.T) {
	ctx := context.Background()
	c := New()
	if _, err := c.UpdateParticipants(ctx, group, []wa.JID{alice}, wa.ParticipantRemove); !errors.Is(err, wa.ErrNotFound) {
		t.Fatalf("UpdateParticipants on an unknown group = %v, want ErrNotFound (the real server errors)", err)
	}
	if err := c.SetAnnounce(ctx, group, true); !errors.Is(err, wa.ErrNotFound) {
		t.Fatalf("SetAnnounce on an unknown group = %v, want ErrNotFound", err)
	}
	if calls := c.CallsTo(MethodSetAnnounce); len(calls) != 1 || !errors.Is(calls[0].Err, wa.ErrNotFound) {
		t.Fatalf("failed SetAnnounce not recorded with its error: %+v", calls)
	}

	c.SetGroup(wa.GroupInfo{JID: group, Participants: []wa.Participant{{JID: alice, IsSuperAdmin: true}, {JID: bob}}})
	results, err := c.UpdateParticipants(ctx, group, []wa.JID{alice, bob}, wa.ParticipantRemove)
	if err != nil {
		t.Fatal(err)
	}
	if !errors.Is(results[0].Err, ErrSuperAdmin) || results[1].Err != nil {
		t.Fatalf("results = %+v, want the superadmin refused and bob removed (§2.3)", results)
	}
	info, _ := c.GroupInfo(ctx, group)
	if len(info.Participants) != 1 || info.Participants[0].JID != alice {
		t.Fatalf("group = %+v, want only the superadmin left", info)
	}
}

func TestGroupStateChanges(t *testing.T) {
	ctx := context.Background()
	c := New()
	c.SetGroup(wa.GroupInfo{JID: group})
	if err := c.SetAnnounce(ctx, group, true); err != nil {
		t.Fatal(err)
	}
	if err := c.SetJoinApproval(ctx, group, true); err != nil {
		t.Fatal(err)
	}
	info, _ := c.GroupInfo(ctx, group)
	if !info.Announce || !info.JoinApproval || info.Locked {
		t.Fatalf("group = %+v, want announce and join approval on, locked off", info)
	}
	l1, _ := c.ResetInviteLink(ctx, group)
	l2, _ := c.ResetInviteLink(ctx, group)
	if l1 == "" || l1 == l2 {
		t.Fatalf("ResetInviteLink gave %q then %q, want two different links", l1, l2)
	}
}

func TestJoinRequests(t *testing.T) {
	ctx := context.Background()
	c := New()
	c.SetJoinRequests(group, []wa.JoinRequest{{JID: alice}, {JID: bob}})
	results, err := c.UpdateJoinRequests(ctx, group, []wa.JID{bob, carol}, wa.JoinRequestReject)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Err != nil || !errors.Is(results[1].Err, wa.ErrNotFound) {
		t.Fatalf("results = %+v, want bob rejected and carol not found", results)
	}
	left, _ := c.JoinRequests(ctx, group)
	if len(left) != 1 || left[0].JID != alice {
		t.Fatalf("pending after reject = %+v, want only alice", left)
	}
}

func TestLookups(t *testing.T) {
	ctx := context.Background()
	c := New()
	c.SetUser(alice, wa.UserInfo{Status: "hello", LID: alice})
	c.SetProfilePicture(alice, wa.ProfilePicture{ID: "P1", URL: "https://pps.example/p1"})
	channel := wa.NewsletterInfo{JID: "1@newsletter", Name: "Deals", Subscribers: 10}
	c.SetNewsletter(channel)
	c.SetNewsletterInvite("INV", channel)
	c.SetInviteLink("CODE", wa.GroupInfo{JID: group, Name: "Free crypto"})

	users, err := c.UserInfo(ctx, []wa.JID{alice, bob})
	if err != nil || len(users) != 1 || users[alice].Status != "hello" {
		t.Fatalf("UserInfo() = %+v, %v; want only alice", users, err)
	}
	pic, err := c.ProfilePicture(ctx, alice, wa.ProfilePictureParams{})
	if err != nil || pic == nil || pic.ID != "P1" {
		t.Fatalf("ProfilePicture() = %+v, %v", pic, err)
	}
	pic, err = c.ProfilePicture(ctx, alice, wa.ProfilePictureParams{ExistingID: "P1"})
	if err != nil || pic != nil {
		t.Fatalf("ProfilePicture(ExistingID unchanged) = %+v, %v; want nil, nil", pic, err)
	}
	if _, err := c.ProfilePicture(ctx, bob, wa.ProfilePictureParams{}); !errors.Is(err, wa.ErrNotFound) {
		t.Fatalf("ProfilePicture(unknown) = %v, want ErrNotFound", err)
	}
	if got, err := c.NewsletterInfo(ctx, channel.JID); err != nil || got.Name != "Deals" {
		t.Fatalf("NewsletterInfo() = %+v, %v", got, err)
	}
	if got, err := c.NewsletterInfoFromInvite(ctx, "INV"); err != nil || got.JID != channel.JID {
		t.Fatalf("NewsletterInfoFromInvite() = %+v, %v", got, err)
	}
	if got, err := c.GroupInfoFromLink(ctx, "CODE"); err != nil || got.Name != "Free crypto" {
		t.Fatalf("GroupInfoFromLink() = %+v, %v", got, err)
	}
	if _, err := c.GroupInfoFromLink(ctx, "NOPE"); !errors.Is(err, wa.ErrNotFound) {
		t.Fatalf("GroupInfoFromLink(unknown) = %v, want ErrNotFound", err)
	}
	if calls := c.CallsTo(MethodGroupInfoFromLink); !errors.Is(calls[1].Err, wa.ErrNotFound) {
		t.Fatalf("a not-found lookup must be recorded with its error, got %+v", calls[1])
	}
}

func TestJoinedGroupsReturnsCopies(t *testing.T) {
	ctx := context.Background()
	c := New()
	c.SetGroup(wa.GroupInfo{JID: "2@g.us", Participants: []wa.Participant{{JID: alice}}})
	c.SetGroup(wa.GroupInfo{JID: "1@g.us"})
	groups, err := c.JoinedGroups(ctx)
	if err != nil || len(groups) != 2 || groups[0].JID != "1@g.us" {
		t.Fatalf("JoinedGroups() = %+v, %v; want both, sorted by JID", groups, err)
	}
	groups[1].Participants[0].IsAdmin = true
	again, _ := c.GroupInfo(ctx, "2@g.us")
	if again.Participants[0].IsAdmin {
		t.Fatal("mutating a returned group changed the fake's state")
	}
}
