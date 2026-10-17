package wpk_test

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review/reviewtest"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// TestF120_OnlyCurrentAdminsDecide: a vote from someone who isn't a current admin of the
// item's protected group has no effect but is stored as not_admin (DESIGN §9.5).
func TestF120_OnlyCurrentAdminsDecide(t *testing.T) {
	e := newReviewEnv(t, groupDest(review.ModeAuto))
	reviewer := wa.JID("300000000000001@lid")
	e.admins.Set(revGrp, reviewer, true) // admin of the review group only
	it, msgID := e.open(t, spamItem(review.KindBanDecision, true, plainSubject()))
	if msgID == "" {
		t.Fatalf("no review Send with ReviewID %d to the review group, want one (group destination, DESIGN §9.5)", it.ID)
	}
	before := len(e.acts.All())

	for _, voter := range []wa.JID{member2, reviewer} {
		res, ok, err := e.svc.HandleReaction(e.ctx, revGrp, voter, msgID, "✅")
		if err != nil || !ok {
			t.Fatalf("HandleReaction() = ok %v, err %v; want ok (the target is a review message)", ok, err)
		}
		if res.Outcome != review.OutcomeNotAdmin {
			t.Errorf("voter %s: got outcome %d, want OutcomeNotAdmin (only current admins of the protected group decide, DESIGN §9.5)", voter, res.Outcome)
		}
	}
	if got := e.get(t, it.ID); got.Status != review.StatusPending {
		t.Errorf("got status %q, want pending (non-admin votes have no effect, DESIGN §9.5)", got.Status)
	}
	if got := e.acts.All()[before:]; len(got) != 0 {
		t.Errorf("got actions %+v after non-admin votes, want none (they are ignored, DESIGN §9.5)", got)
	}
	votes, err := e.store.Votes(e.ctx, it.ID)
	if err != nil {
		t.Fatalf("Votes() error = %v", err)
	}
	if len(votes) != 2 {
		t.Fatalf("got %d stored votes, want 2 (every vote is stored, DESIGN §9.5)", len(votes))
	}
	for _, v := range votes {
		if v.Valid || v.InvalidReason != review.InvalidNotAdmin {
			t.Errorf("got vote {Valid:%v InvalidReason:%q}, want {false %q} (DESIGN §14 review_votes)", v.Valid, v.InvalidReason, review.InvalidNotAdmin)
		}
	}
}

// TestF120_FirstVoteWinsUnderConcurrency: many admins voting at once produce exactly one
// decision and exactly one execution of it (DESIGN §9.5 first-wins).
func TestF120_FirstVoteWinsUnderConcurrency(t *testing.T) {
	e := newReviewEnv(t, groupDest(review.ModeAuto))
	const voters = 16
	var jids []wa.JID
	for i := range voters {
		j := wa.JID(fmt.Sprintf("4000000000000%02d@lid", i))
		e.admins.Set(grp, j, true)
		jids = append(jids, j)
	}
	it, _ := e.open(t, spamItem(review.KindBanDecision, true, plainSubject()))

	results := make([]review.VoteResult, voters)
	var wg sync.WaitGroup
	for i, j := range jids {
		d := review.DecisionBan
		if i%2 == 1 {
			d = review.DecisionNotSpam
		}
		wg.Go(func() {
			r, err := e.svc.Vote(e.ctx, review.Ballot{ReviewID: it.ID, Voter: j, Decision: d, Method: review.MethodCommand, Raw: string(d), Chat: revGrp})
			if err != nil {
				t.Errorf("Vote() error = %v", err)
			}
			results[i] = r
		})
	}
	wg.Wait()

	decided, late := 0, 0
	for _, r := range results {
		switch r.Outcome {
		case review.OutcomeDecided:
			decided++
		case review.OutcomeAlreadyHandled:
			late++
		}
	}
	if decided != 1 || late != voters-1 {
		t.Fatalf("got %d deciding and %d late votes, want 1 and %d (the first valid vote wins atomically, DESIGN §9.5)", decided, late, voters-1)
	}
	final := e.get(t, it.ID)
	bans := len(bansOf(e.acts, member))
	restores := standingKinds(e.standing, member)["restore_trust"]
	switch final.Decision {
	case review.DecisionBan:
		if bans != 1 || restores != 0 {
			t.Errorf("decision ban executed as %d bans and %d trust restores, want 1 and 0 (a decision executes once)", bans, restores)
		}
	case review.DecisionNotSpam:
		if bans != 0 || restores != 1 {
			t.Errorf("decision not_spam executed as %d bans and %d trust restores, want 0 and 1 (a decision executes once)", bans, restores)
		}
	default:
		t.Errorf("got decision %q, want ban or not_spam", final.Decision)
	}
	votes, _ := e.store.Votes(e.ctx, it.ID)
	if len(votes) != voters {
		t.Errorf("got %d stored votes, want %d (all votes, including late ones, are stored, DESIGN §9.5)", len(votes), voters)
	}
}

// TestF120_DecisionExecutesThroughActions: the winning decision runs through the action queue:
// ban → Ban (reason spam, via review) plus a Delete if the message is still visible, and
// learning; not spam → overturn, no ban (DESIGN §9.5, §7.5).
func TestF120_DecisionExecutesThroughActions(t *testing.T) {
	cases := []struct {
		name       string
		decision   review.Decision
		deleted    bool
		byCode     bool
		wantBan    bool
		wantDelete bool
	}{
		{"ban, message already deleted", review.DecisionBan, true, false, true, false},
		{"ban, message still visible", review.DecisionBan, false, true, true, true},
		{"not spam", review.DecisionNotSpam, true, false, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newReviewEnv(t, groupDest(review.ModeAuto))
			it, _ := e.open(t, spamItem(review.KindBanDecision, tc.deleted, plainSubject()))
			b := review.Ballot{ReviewID: it.ID, Voter: adminA, VoterName: "Sara", Decision: tc.decision, Method: review.MethodCommand, Chat: revGrp}
			if tc.byCode {
				b = review.Ballot{Code: it.Code, Voter: adminA, VoterName: "Sara", Decision: tc.decision, Method: review.MethodCommand, Chat: revGrp}
			}
			res, err := e.svc.Vote(e.ctx, b)
			if err != nil || res.Outcome != review.OutcomeDecided {
				t.Fatalf("Vote() = %d, %v; want OutcomeDecided", res.Outcome, err)
			}
			if got := e.get(t, it.ID); got.Status != review.StatusDecided || got.Decision != tc.decision || got.DecidedBy != adminA {
				t.Errorf("got item {Status:%s Decision:%s By:%s}, want {decided %s %s} (who decided is recorded, DESIGN §9.5)", got.Status, got.Decision, got.DecidedBy, tc.decision, adminA)
			}
			bans := bansOf(e.acts, member)
			if tc.wantBan {
				if len(bans) != 1 || bans[0].Group != grp || bans[0].Reason != review.ReasonSpam || bans[0].Via != "review" || bans[0].ReviewID != it.ID {
					t.Errorf("got bans %+v, want one Ban{Group:%s Reason:spam Via:review ReviewID:%d} (DESIGN §9.5)", bans, grp, it.ID)
				}
				if len(activeFor(e.corpus, "spam-1")) != 1 {
					t.Errorf("no corpus entry after a ban decision, want the confirmed spam learned (DESIGN §7.5)")
				}
			} else if len(bans) != 0 {
				t.Errorf("got %d bans, want 0 for decision %s", len(bans), tc.decision)
			}
			if got := len(deletesOf(e.acts, "spam-1")) > 0; got != tc.wantDelete {
				t.Errorf("Delete of the reviewed message = %v, want %v (delete only if still visible, DESIGN §9.5)", got, tc.wantDelete)
			}
			if tc.decision == review.DecisionNotSpam {
				assertAllowListed(t, e.corpus.Allowed(), "spam-1")
				if standingKinds(e.standing, member)["restore_trust"] == 0 {
					t.Errorf("trust not restored after not spam, want restored (DESIGN §7.5)")
				}
			}
			assertSilent(t, e.acts, grp)
		})
	}
}

// TestF120_ReactionAndReplyKeywords: reactions and reply keywords on the review message are
// votes; "kick"/"طرد" mean ban (DESIGN §9.5).
func TestF120_ReactionAndReplyKeywords(t *testing.T) {
	cases := []struct {
		reaction, reply string
		want            review.Decision // empty: no decision
	}{
		{reaction: "✅", want: review.DecisionBan},
		{reaction: "👎", want: review.DecisionNotSpam},
		{reaction: "😂"},
		{reply: "kick", want: review.DecisionBan},
		{reply: "طرد", want: review.DecisionBan},
		{reply: "حظر", want: review.DecisionBan},
		{reply: "ليس سبام", want: review.DecisionNotSpam},
		{reply: "ok", want: review.DecisionNotSpam},
		{reply: "delete only", want: review.DecisionDeleteOnly},
	}
	for _, tc := range cases {
		t.Run(tc.reaction+tc.reply, func(t *testing.T) {
			e := newReviewEnv(t, groupDest(review.ModeAuto))
			it, msgID := e.open(t, spamItem(review.KindBanDecision, true, plainSubject()))
			var ok bool
			var err error
			if tc.reaction != "" {
				_, ok, err = e.svc.HandleReaction(e.ctx, revGrp, adminA, msgID, tc.reaction)
			} else {
				_, ok, err = e.svc.HandleReply(e.ctx, revGrp, adminA, wa.MessageRef{Sender: adminA, ID: "r-1"}, msgID, tc.reply)
			}
			if err != nil || !ok {
				t.Fatalf("got ok %v, err %v; want ok (the target is a posted review message, DESIGN §9.5)", ok, err)
			}
			got := e.get(t, it.ID)
			if tc.want == "" {
				if got.Status != review.StatusPending {
					t.Errorf("got status %s, want pending (an unmapped emoji is not a vote)", got.Status)
				}
				return
			}
			if got.Status != review.StatusDecided || got.Decision != tc.want {
				t.Errorf("got {%s %s}, want {decided %s} (DESIGN §9.5 reaction and reply keywords)", got.Status, got.Decision, tc.want)
			}
		})
	}
	t.Run("not a review message", func(t *testing.T) {
		e := newReviewEnv(t, groupDest(review.ModeAuto))
		if _, ok, err := e.svc.HandleReaction(e.ctx, revGrp, adminA, "some-other-msg", "✅"); ok || err != nil {
			t.Errorf("got ok %v, err %v; want ok=false, nil (only reactions on our review messages are votes)", ok, err)
		}
	})
}

// TestF120_LateVotersGetOneAck: after a decision, a late voter gets at most one 👌 and one
// "already handled by ~Sara" per review; the late votes are stored (DESIGN §9.5).
func TestF120_LateVotersGetOneAck(t *testing.T) {
	e := newReviewEnv(t, groupDest(review.ModeAuto))
	it, msgID := e.open(t, spamItem(review.KindBanDecision, true, plainSubject()))
	if _, err := e.svc.Vote(e.ctx, review.Ballot{ReviewID: it.ID, Voter: adminA, VoterName: "Sara", Decision: review.DecisionBan, Method: review.MethodCommand, Chat: revGrp}); err != nil {
		t.Fatalf("Vote() error = %v", err)
	}
	afterDecision := len(e.acts.All())

	first := wa.MessageRef{Sender: adminB, ID: "late-1"}
	res, ok, err := e.svc.HandleReply(e.ctx, revGrp, adminB, first, msgID, "ok")
	if err != nil || !ok || res.Outcome != review.OutcomeAlreadyHandled {
		t.Fatalf("late reply = %d, ok %v, err %v; want OutcomeAlreadyHandled", res.Outcome, ok, err)
	}
	if got := e.get(t, it.ID); got.Decision != review.DecisionBan {
		t.Errorf("late vote changed the decision to %s, want ban (first decision is final, DESIGN §9.5)", got.Decision)
	}
	newActs := e.acts.All()[afterDecision:]
	var acks int
	var named bool
	for _, a := range newActs {
		switch a := a.(type) {
		case review.React:
			if a.Emoji == "👌" && a.Target == first {
				acks++
			}
		case review.Send:
			named = named || strings.Contains(a.Message.Text, "Sara")
		case review.Ban, review.Unban, review.Delete:
			t.Errorf("late vote caused %T, want no enforcement (DESIGN §9.5)", a)
		}
	}
	if acks != 1 {
		t.Errorf("got %d 👌 reactions on the late reply, want 1 (DESIGN §9.5)", acks)
	}
	if !named {
		t.Errorf("no \"already handled by ~Sara\" message, want one naming who decided (DESIGN §9.5)")
	}

	mark := len(e.acts.All())
	if _, _, err := e.svc.HandleReply(e.ctx, revGrp, adminB, wa.MessageRef{Sender: adminB, ID: "late-2"}, msgID, "ban"); err != nil {
		t.Fatalf("second late reply error = %v", err)
	}
	if _, _, err := e.svc.HandleReaction(e.ctx, revGrp, adminB, msgID, "❌"); err != nil {
		t.Fatalf("late reaction error = %v", err)
	}
	if extra := e.acts.All()[mark:]; len(extra) != 0 {
		t.Errorf("got %d more actions %+v for the same late voter, want none (at most one 👌 and one note per voter, DESIGN §9.5)", len(extra), extra)
	}
	votes, _ := e.store.Votes(e.ctx, it.ID)
	var lateVotes int
	for _, v := range votes {
		if v.Voter == adminB && !v.Valid && v.InvalidReason == review.InvalidLate {
			lateVotes++
		}
	}
	if lateVotes != 3 {
		t.Errorf("got %d stored late votes for %s, want 3 (all votes are stored, DESIGN §9.5)", lateVotes, adminB)
	}
}

// TestF120_WithdrawAfterDecisionDoesNothing: removing a reaction after the decision changes
// nothing (DESIGN §9.5: withdrawal only counts before the decision is final).
func TestF120_WithdrawAfterDecisionDoesNothing(t *testing.T) {
	e := newReviewEnv(t, groupDest(review.ModeAuto))
	it, msgID := e.open(t, spamItem(review.KindBanDecision, true, plainSubject()))
	if _, _, err := e.svc.HandleReaction(e.ctx, revGrp, adminA, msgID, "✅"); err != nil {
		t.Fatalf("HandleReaction() error = %v", err)
	}
	bans := len(reviewtest.Of[review.Ban](e.acts))
	if _, _, err := e.svc.HandleReaction(e.ctx, revGrp, adminA, msgID, ""); err != nil {
		t.Fatalf("withdraw error = %v", err)
	}
	if got := e.get(t, it.ID); got.Status != review.StatusDecided || got.Decision != review.DecisionBan {
		t.Errorf("got {%s %s} after withdrawal, want {decided ban} (decisions are final)", got.Status, got.Decision)
	}
	if n := len(reviewtest.Of[review.Ban](e.acts)); n != bans || len(reviewtest.Of[review.Unban](e.acts)) != 0 {
		t.Errorf("withdrawal changed enforcement (bans %d→%d), want nothing", bans, n)
	}
}
