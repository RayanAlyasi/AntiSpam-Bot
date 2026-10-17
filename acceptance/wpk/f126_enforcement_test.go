package wpk_test

import (
	"slices"
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review/blacklist"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review/reviewtest"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// TestF126_AddBansListedMemberSilently: blacklisting a current member bans them through the
// queue with no message anywhere (DESIGN §9.8 step 2).
func TestF126_AddBansListedMemberSilently(t *testing.T) {
	e := newBlEnv(t)
	res := e.add(t, blacklist.AddRequest{Group: grp, Numbers: []string{memberE164}, Reason: "scam", By: adminA})

	bans := bansOf(e.acts, member)
	if len(bans) != 1 || bans[0].Group != grp || bans[0].Via != "blacklist" {
		t.Fatalf("got bans %+v, want one Ban{Group:%s Target:%s Via:blacklist} (a listed member is banned, DESIGN §9.8)", bans, grp, member)
	}
	if !slices.Contains(res.Banned, member) {
		t.Errorf("AddResult.Banned = %v, want it to include %s", res.Banned, member)
	}
	if k := reviewtest.Of[review.Kick](e.acts); len(k) != 0 {
		t.Errorf("got kicks %+v, want a ban (kick + rejoin block), not a plain kick (DESIGN §9.8)", k)
	}
	assertNoSends(t, e.acts)
}

// TestF126_NeverActsOnAdmins: a blacklisted admin is never acted on; the adder is told instead
// (DESIGN §9.8 step 2, §8.6).
func TestF126_NeverActsOnAdmins(t *testing.T) {
	e := newBlEnv(t)
	res := e.add(t, blacklist.AddRequest{Group: grp, Numbers: []string{adminBE164}, By: adminA})

	for _, a := range e.acts.All() {
		switch a := a.(type) {
		case review.Ban:
			if a.Target == adminB {
				t.Errorf("got a Ban of an admin, want none (never act on admins, DESIGN §8.6)")
			}
		case review.Kick:
			if a.Target == adminB {
				t.Errorf("got a Kick of an admin, want none (never act on admins, DESIGN §8.6)")
			}
		}
	}
	if !slices.Contains(res.Skipped, adminB) {
		t.Errorf("AddResult.Skipped = %v, want %s (the listed admin was skipped)", res.Skipped, adminB)
	}
	var told bool
	for _, n := range e.notifier.All() {
		told = told || n.To == adminA
	}
	if !told {
		t.Errorf("adder %s got no notice, want one (the adder is told instead, DESIGN §9.8)", adminA)
	}
	if _, err := e.svc.OnMessage(e.ctx, grp, wa.MessageRef{Sender: adminB, ID: "a-1"}, blacklist.Person{JID: adminB, LID: adminB}); err != nil {
		t.Fatalf("OnMessage() error = %v", err)
	}
	if n := len(deletesOf(e.acts, "a-1")) + len(bansOf(e.acts, adminB)); n != 0 {
		t.Errorf("got %d deletes/bans for a listed admin's message, want none (never act on admins, DESIGN §8.6)", n)
	}
	assertNoSends(t, e.acts)
}

// TestF126_JoinsRequestsAndMessages: listed people are kicked (banned) on join, their join
// requests rejected in one batch, and their messages deleted at P1 plus a ban while they're
// still members (DESIGN §9.8 step 3).
func TestF126_JoinsRequestsAndMessages(t *testing.T) {
	stranger := wa.JID("200000000000777@lid")
	cases := []struct {
		name   string
		person blacklist.Person
	}{
		{"known by LID", blacklist.Person{JID: member, LID: member}},
		{"known by phone", blacklist.Person{JID: memberPhone, Phone: memberPhone}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newBlEnv(t)
			e.groups.SetMembers(grp, adminRow) // the listed person isn't a member yet
			e.add(t, blacklist.AddRequest{Group: grp, Numbers: []string{memberE164}, By: adminA})
			e.acts.Reset()

			if ok, err := e.svc.OnJoin(e.ctx, grp, blacklist.Person{JID: stranger, LID: stranger}); err != nil || ok {
				t.Errorf("OnJoin(stranger) = %v, %v; want false, nil", ok, err)
			}
			if ok, err := e.svc.OnJoin(e.ctx, grp, tc.person); err != nil || !ok {
				t.Fatalf("OnJoin(listed) = %v, %v; want true (auto-kick on join, DESIGN §9.8)", ok, err)
			}
			if bans := reviewtest.Of[review.Ban](e.acts); len(bans) != 1 || bans[0].Group != grp || bans[0].Target != tc.person.JID {
				t.Errorf("got bans %+v after the join, want one Ban of %s in %s (DESIGN §9.8)", bans, tc.person.JID, grp)
			}

			e.acts.Reset()
			rejected, err := e.svc.OnJoinRequests(e.ctx, grp, []blacklist.Person{{JID: stranger, LID: stranger}, tc.person})
			if err != nil || !slices.Equal(rejected, []wa.JID{tc.person.JID}) {
				t.Errorf("OnJoinRequests() = %v, %v; want [%s]", rejected, err, tc.person.JID)
			}
			rj := reviewtest.Of[review.RejectJoin](e.acts)
			if len(rj) != 1 || rj[0].Group != grp || !slices.Equal(rj[0].Targets, []wa.JID{tc.person.JID}) {
				t.Errorf("got rejections %+v, want one RejectJoin of [%s] (DESIGN §9.8)", rj, tc.person.JID)
			}

			e.acts.Reset()
			ref := wa.MessageRef{Sender: tc.person.JID, ID: "bl-1"}
			if ok, err := e.svc.OnMessage(e.ctx, grp, ref, tc.person); err != nil || !ok {
				t.Fatalf("OnMessage(listed) = %v, %v; want true", ok, err)
			}
			dels := deletesOf(e.acts, "bl-1")
			if len(dels) != 1 || dels[0].Priority != review.P1 || dels[0].Group != grp {
				t.Errorf("got deletes %+v, want one P1 Delete in %s (blacklist deletions run at P1, DESIGN §9.8, §10.2)", dels, grp)
			}
			if b := reviewtest.Of[review.Ban](e.acts); len(b) != 0 {
				t.Errorf("got bans %+v for a non-member's message, want only the delete", b)
			}

			e.acts.Reset()
			e.groups.SetMembers(grp, adminRow, memberRow)
			if _, err := e.svc.OnMessage(e.ctx, grp, wa.MessageRef{Sender: tc.person.JID, ID: "bl-2"}, tc.person); err != nil {
				t.Fatalf("OnMessage() error = %v", err)
			}
			if len(deletesOf(e.acts, "bl-2")) != 1 || len(reviewtest.Of[review.Ban](e.acts)) != 1 {
				t.Errorf("got %+v, want a Delete and a Ban (still a member, DESIGN §9.8 step 3)", e.acts.All())
			}
			assertNoSends(t, e.acts)
		})
	}
}

// TestF126_ScopeAndAskMode: group entries apply only to their group; instance entries apply to
// every group that uses them; in ask-admins groups instance entries open a review item instead
// of banning, while the group's own entries still apply (DESIGN §9.8 Scope, §8.3).
func TestF126_ScopeAndAskMode(t *testing.T) {
	t.Run("group entry stays in its group", func(t *testing.T) {
		e := newBlEnv(t)
		e.groups.SetMembers(grp2, memberRow)
		e.add(t, blacklist.AddRequest{Group: grp, Numbers: []string{memberE164}, By: adminA})
		for _, b := range bansOf(e.acts, member) {
			if b.Group == grp2 {
				t.Errorf("group entry banned in another group %s, want only %s (per-group scope, DESIGN §9.8)", grp2, grp)
			}
		}
	})

	t.Run("instance entry across groups", func(t *testing.T) {
		e := newBlEnv(t)
		e.groups.SetMembers(grp2, memberRow)
		const grp3 wa.JID = "120363000000000003@g.us"
		e.groups.SetMembers(grp3, memberRow)
		e.settings.Set(grp2, blacklist.GroupSettings{UseInstance: true, Mode: review.ModeAskDeleteFirst, DefaultCountry: "SA"})
		e.settings.Set(grp3, blacklist.GroupSettings{UseInstance: false, Mode: review.ModeAuto, DefaultCountry: "SA"})
		e.add(t, blacklist.AddRequest{Numbers: []string{memberE164}, By: operator, Operator: true, Instance: true})

		in := map[wa.JID]int{}
		for _, b := range bansOf(e.acts, member) {
			in[b.Group]++
		}
		if in[grp] != 1 || in[grp2] != 0 || in[grp3] != 0 {
			t.Errorf("got bans per group %v, want only %s (ask group %s reviews, opted-out group %s ignores, DESIGN §9.8)", in, grp, grp2, grp3)
		}
		items := e.reviews.All()
		if len(items) != 1 || items[0].Group != grp2 || items[0].Kind != review.KindNetworkIncoming || items[0].Subject.Person != member {
			t.Errorf("got review items %+v, want one network_incoming item for %s in %s (DESIGN §9.8)", items, member, grp2)
		}

		e.acts.Reset()
		if ok, _ := e.svc.OnMessage(e.ctx, grp3, wa.MessageRef{Sender: member, ID: "x-1"}, blacklist.Person{JID: member, LID: member}); ok || len(e.acts.All()) != 0 {
			t.Errorf("use_instance=false group acted on an instance entry (ok=%v, %+v), want ignored (DESIGN §9.8)", ok, e.acts.All())
		}
		if ok, _ := e.svc.OnJoin(e.ctx, grp2, blacklist.Person{JID: member, LID: member}); len(bansOf(e.acts, member)) != 0 {
			t.Errorf("ask-admins group banned on join for an instance entry (ok=%v), want a review item instead (DESIGN §8.3)", ok)
		}
		assertNoSends(t, e.acts)
	})

	t.Run("own entry applies directly in an ask group", func(t *testing.T) {
		e := newBlEnv(t)
		e.settings.Set(grp, blacklist.GroupSettings{UseInstance: true, Mode: review.ModeAskBeforeAction, DefaultCountry: "SA"})
		e.add(t, blacklist.AddRequest{Group: grp, Numbers: []string{memberE164}, By: adminA})
		if bans := bansOf(e.acts, member); len(bans) != 1 {
			t.Errorf("got %d bans, want 1 (the group's own entries are its admins' decisions, DESIGN §9.8)", len(bans))
		}
		if n := len(e.reviews.All()); n != 0 {
			t.Errorf("got %d review items, want 0", n)
		}
	})
}
