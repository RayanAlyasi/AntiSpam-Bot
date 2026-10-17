package queue_test

import (
	"context"
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/actions"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

func count(r *rig, kind actions.Kind) int {
	n := 0
	for _, a := range r.store.All() {
		if a.Kind == kind {
			n++
		}
	}
	return n
}

// TestRepeatedJoinRequestsAreRejectedAgain: §9.8 rejects every join request from a listed
// person, not just the first.
func TestRepeatedJoinRequestsAreRejectedAgain(t *testing.T) {
	ctx := context.Background()
	r := newRig()
	for range 2 {
		if err := r.q.Enqueue(ctx, review.RejectJoin{Group: group, Targets: []wa.JID{spam}, Reason: "blacklist"}); err != nil {
			t.Fatal(err)
		}
	}
	if n := count(r, actions.KindRejectJoin); n != 2 {
		t.Errorf("two join requests made %d rejections, want 2", n)
	}
}

// TestRebanOfReturningMemberKicksAgain: a listed person who is already banned but back in the
// group (rejoined, or still posting) is kicked again.
func TestRebanOfReturningMemberKicksAgain(t *testing.T) {
	ctx := context.Background()
	r := newRig()
	ban := review.Ban{Group: group, Target: spam, Reason: review.ReasonOther, Via: "blacklist"}
	for range 2 {
		if err := r.q.Enqueue(ctx, ban); err != nil {
			t.Fatal(err)
		}
	}
	if n := count(r, actions.KindKick); n != 2 {
		t.Errorf("ban, then ban again on return: %d kicks, want 2", n)
	}
	if b, ok, _ := r.bans.ActiveBan(ctx, group, 7); !ok || !b.RejoinBlock {
		t.Error("blacklist ban without a rejoin block, want it always blocked (DESIGN §9.8)")
	}
}

// TestStaleMarkNeverLiftsAnAdminsBan: once the blacklist's ban is gone some other way, a later
// admin ban isn't the blacklist's to lift.
func TestStaleMarkNeverLiftsAnAdminsBan(t *testing.T) {
	ctx := context.Background()
	r := newRig()
	if err := r.q.Enqueue(ctx, review.Ban{Group: group, Target: spam, Reason: review.ReasonOther, Via: "blacklist"}); err != nil {
		t.Fatal(err)
	}
	// An admin re-adds them, which lifts the ban outside the adapter (policy.Bans.OnJoin).
	if _, err := r.bans.LiftBan(ctx, group, 7, "admin_readd", start); err != nil {
		t.Fatal(err)
	}
	if err := r.q.Enqueue(ctx, review.Ban{Group: group, Target: spam, Reason: review.ReasonSpam, Actor: admin, Via: "command"}); err != nil {
		t.Fatal(err)
	}
	if err := r.q.Enqueue(ctx, review.Unban{Group: group, Target: spam, Via: "blacklist"}); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := r.bans.ActiveBan(ctx, group, 7); !ok {
		t.Error("a blacklist removal lifted an admin's later ban through a stale mark (DESIGN §9.8 Undo)")
	}
}

// TestDeadActionIsRetried: a delete that failed earlier is queued again rather than treated as
// done.
func TestDeadActionIsRetried(t *testing.T) {
	ctx := context.Background()
	r := newRig()
	d := review.Delete{Group: group, Message: wa.MessageRef{Sender: spam, ID: "M9"}, Priority: review.P1}
	if err := r.q.Enqueue(ctx, d); err != nil {
		t.Fatal(err)
	}
	a := r.store.All()[0]
	if _, ok, err := r.store.Claim(ctx, a.ID); !ok || err != nil {
		t.Fatal("claim", err)
	}
	if err := r.store.Finish(ctx, a.ID, actions.StatusFailed, "boom", actions.Result{}); err != nil {
		t.Fatal(err)
	}
	if err := r.q.Enqueue(ctx, d); err != nil {
		t.Fatal(err)
	}
	if n := count(r, actions.KindRevoke); n != 2 {
		t.Errorf("delete after a failed one: %d revokes, want a retry", n)
	}
}
