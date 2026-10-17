package review_test

import (
	"context"
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review/reviewtest"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// TestNeverPostsIntoAProtectedGroup: even if settings name a protected group as the review
// group, nothing is posted or reacted there (DESIGN §9.7).
func TestNeverPostsIntoAProtectedGroup(t *testing.T) {
	ctx := context.Background()
	const other wa.JID = "120363000000000002@g.us"
	r := newRig(review.Config{Destination: review.DestinationGroup, ReviewGroup: other})
	svc := review.NewService(review.Deps{
		Store: r.store, Settings: r.settings, Admins: r.admins, Actions: r.acts, Clock: r.clock,
		Protected: func(_ context.Context, c wa.JID) (bool, error) { return c == other || c == group, nil },
	})
	it, err := svc.Open(ctx, spamItem())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Vote(ctx, review.Ballot{ReviewID: it.ID, Voter: sara, Decision: review.DecisionBan}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Vote(ctx, review.Ballot{ReviewID: it.ID, Voter: omar, Decision: review.DecisionBan, Chat: other, Message: &wa.MessageRef{ID: "X"}}); err != nil {
		t.Fatal(err)
	}
	for _, a := range r.acts.All() {
		switch a := a.(type) {
		case review.Send:
			t.Errorf("sent %q to %s, want nothing posted into protected groups", a.Message.Text, a.Chat)
		case review.React:
			t.Errorf("reacted in %s, a protected group", a.Chat)
		}
	}
}

// TestBanRechecksAdmin: someone promoted to admin while their review waited is never banned (§8.6).
func TestBanRechecksAdmin(t *testing.T) {
	ctx := context.Background()
	r := newRig(review.Config{})
	it, err := r.svc.Open(ctx, spamItem())
	if err != nil {
		t.Fatal(err)
	}
	r.admins.Set(group, ahmed, true)
	if _, err := r.svc.Vote(ctx, review.Ballot{ReviewID: it.ID, Voter: sara, Decision: review.DecisionBan}); err != nil {
		t.Fatal(err)
	}
	if bans := reviewtest.Of[review.Ban](r.acts); len(bans) != 0 {
		t.Errorf("banned a current admin: %+v (DESIGN §8.6)", bans)
	}
}
