package chat_test

import (
	"context"
	"strings"
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review"
)

// TestStrangersGetNothing: in a review group or any other group the bot is in, someone who
// administers no protected group can't change settings or make the bot post.
func TestStrangersGetNothing(t *testing.T) {
	r := newRig(t)
	r.groups.Serve(admins, cars)
	for _, cmd := range []string{".review timeout 7d dismiss", ".review dashboard", ".reviewgroup off", ".help", ".use cars", ".review ZZZZ ban", ".reviewgroup here"} {
		r.in(t, admins, member, cmd)
		r.in(t, "120363000000000055@g.us", member, cmd)
	}
	if got := r.acts.All(); len(got) != 0 {
		t.Errorf("non-admin commands produced %+v, want nothing (DESIGN §13.7)", got)
	}
	if ch := r.settings.Changes(); len(ch) != 0 {
		t.Errorf("non-admin commands changed settings: %+v", ch)
	}
	r.in(t, admins, omar, ".review timeout 7d dismiss")
	if r.value(cars, "review.timeout") != "6h" {
		t.Error("an admin of another group changed this group's review timeout, want ignored")
	}
}

// TestReviewGroupOnlyByReviewGroupHere: .set can't point reviews at an arbitrary chat, such as
// another protected group (DESIGN §9.5, §9.7).
func TestReviewGroupOnlyByReviewGroupHere(t *testing.T) {
	r := newRig(t)
	r.dm(t, sara, ".set review.review_group "+string(shop))
	if r.value(cars, "review.review_group") != "" {
		t.Fatal(".set review.review_group: want refused")
	}
	if got := r.lastReply(t).Message.Text; !strings.Contains(got, "reviewgroup here") {
		t.Errorf("reply %q, want a pointer to .reviewgroup here", got)
	}
}

// TestLateCodeVoteInProtectedGroupIsSilentThere: a late .review CODE vote sent in a protected
// group is answered by DM, never in the group.
func TestLateCodeVoteInProtectedGroupIsSilentThere(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	it, err := r.reviews.Open(ctx, review.NewItem{Group: cars, Kind: review.KindBanDecision, Subject: review.Subject{Person: member}})
	if err != nil {
		t.Fatal(err)
	}
	r.in(t, cars, sara, ".review "+it.Code+" ban")
	r.admins.Set(cars, omar, true)
	r.in(t, shop, omar, ".review "+it.Code+" ok")
	for _, a := range r.acts.All() {
		switch a := a.(type) {
		case review.Send:
			if a.Chat == cars || a.Chat == shop {
				t.Errorf("posted %q into a protected group (DESIGN §9.7)", a.Message.Text)
			}
		case review.React:
			if a.Chat == cars || a.Chat == shop {
				t.Error("reacted in a protected group (DESIGN §9.7)")
			}
		}
	}
	got := r.lastReply(t)
	if got.Chat != omar || !strings.Contains(got.Message.Text, "Already handled") {
		t.Errorf("late vote reply = %q to %s, want an 'already handled' DM to the voter", got.Message.Text, got.Chat)
	}
}
