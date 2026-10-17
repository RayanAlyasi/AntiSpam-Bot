package wpk_test

import (
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// TestF119_SpamReplyDeletesBansAndLearns: replying `.spam` deletes the message at P1, bans the
// sender with reason spam and records confirmed spam (DESIGN §13.7, §7.5).
func TestF119_SpamReplyDeletesBansAndLearns(t *testing.T) {
	e := newChatEnv(t)
	e.openThread(t, adminA)
	cmd := e.replyTo(adminA, ".spam", "q-1", member, spamText)
	e.handle(t, cmd)

	dels := deletesOf(e.acts, "q-1")
	if len(dels) == 0 {
		t.Fatalf("spam message not deleted, want a Delete of q-1 (.spam deletes it, DESIGN §13.7)")
	}
	if d := dels[0]; d.Group != grp || d.Message.Sender != member || d.Priority != review.P1 {
		t.Errorf("got Delete{Group:%s Sender:%s Priority:%d}, want Delete{Group:%s Sender:%s Priority:P1} (spam deletions run at P1, DESIGN §10.2)",
			d.Group, d.Message.Sender, d.Priority, grp, member)
	}
	bans := bansOf(e.acts, member)
	if len(bans) != 1 || bans[0].Group != grp || bans[0].Reason != review.ReasonSpam {
		t.Errorf("got bans %+v, want one Ban{Group:%s Reason:spam} (.spam bans the sender, even an established member, DESIGN §13.7)", bans, grp)
	}
	act := activeFor(e.corpus, "q-1")
	if len(act) != 1 || act[0].Admin != adminA {
		t.Errorf("got corpus entries %+v, want one confirmed entry for q-1 by %s (DESIGN §7.5 learning event)", act, adminA)
	}
	if len(deletesOf(e.acts, cmd.ID)) == 0 {
		t.Errorf("command message not deleted, want it removed (DESIGN §9.7)")
	}
	assertSilent(t, e.acts, grp)
}

// TestF119_NotSpamOverturnsConfirmation: `.notspam` after `.spam` retracts the corpus entry,
// allow-lists the fingerprint, restores trust and records the false positive, keeping no text
// (DESIGN §13.7, §7.5).
func TestF119_NotSpamOverturnsConfirmation(t *testing.T) {
	for _, word := range []string{".notspam", ".ok"} {
		t.Run(word, func(t *testing.T) {
			e := newChatEnv(t)
			e.openThread(t, adminA)
			e.handle(t, e.replyTo(adminA, ".spam", "q-1", member, spamText))
			if len(activeFor(e.corpus, "q-1")) != 1 {
				t.Fatalf("precondition: .spam created no corpus entry")
			}
			e.handle(t, e.replyTo(adminB, word, "q-1", member, spamText))

			if got := activeFor(e.corpus, "q-1"); len(got) != 0 {
				t.Errorf("got %d active corpus entries for q-1, want 0 (an overturn retracts what was learned, DESIGN §7.5)", len(got))
			}
			assertAllowListed(t, e.corpus.Allowed(), "q-1")
			k := standingKinds(e.standing, member)
			if k["restore_trust"] == 0 || k["false_positive"] == 0 {
				t.Errorf("got standing events %v, want restore_trust and false_positive for %s (DESIGN §13.7 .notspam)", k, member)
			}
			assertNoTextLeft(t, e.ctx, e.evidence, e.corpus, grp, "q-1", spamText)
			assertSilent(t, e.acts, grp)
		})
	}
}

// TestF119_NotSpamOverturnsBotDeletion: `.notspam` on a message the bot deleted (never
// confirmed) deletes the evidence text and restores trust (DESIGN §7.5, F-118).
func TestF119_NotSpamOverturnsBotDeletion(t *testing.T) {
	e := newChatEnv(t)
	e.openThread(t, adminA)
	ref := wa.MessageRef{Sender: member, ID: "q-2"}
	if err := e.learning.Snapshot(e.ctx, review.Snapshot{Group: grp, Message: ref, Text: spamText, Reason: "spam", At: e.clk.Now()}); err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	e.handle(t, e.replyTo(adminA, ".notspam", "q-2", member, spamText))

	assertAllowListed(t, e.corpus.Allowed(), "q-2")
	k := standingKinds(e.standing, member)
	if k["remove_strike"] == 0 || k["restore_trust"] == 0 || k["false_positive"] == 0 {
		t.Errorf("got standing events %v, want remove_strike, restore_trust and false_positive (DESIGN §7.5 overturns)", k)
	}
	if n := len(e.corpus.Entries()); n != 0 {
		t.Errorf("got %d corpus entries, want 0 (.notspam never adds to the spam corpus)", n)
	}
	if n := len(bansOf(e.acts, member)); n != 0 {
		t.Errorf("got %d Bans, want 0 (.notspam never bans)", n)
	}
	assertNoTextLeft(t, e.ctx, e.evidence, e.corpus, grp, "q-2", spamText)
	assertSilent(t, e.acts, grp)
}

// TestF119_NonAdminSpamReplyIgnored: `.spam`/`.notspam` from a non-admin do nothing (DESIGN §13.7).
func TestF119_NonAdminSpamReplyIgnored(t *testing.T) {
	e := newChatEnv(t)
	e.handle(t, e.replyTo(member2, ".spam", "q-1", member, spamText))
	e.handle(t, e.replyTo(member2, ".notspam", "q-1", member, spamText))
	if got := e.acts.All(); len(got) != 0 {
		t.Errorf("got actions %+v, want none (commands from non-admins are ignored, DESIGN §13.7)", got)
	}
	if len(e.corpus.Entries()) != 0 || len(e.corpus.Allowed()) != 0 || len(e.standing.Events()) != 0 {
		t.Errorf("non-admin reply changed learning or standing, want nothing (DESIGN §13.7)")
	}
}

func assertAllowListed(t *testing.T, allowed []review.FingerprintAllow, id wa.MessageID) {
	t.Helper()
	for _, a := range allowed {
		if a.Message.ID == id && a.Group == grp {
			return
		}
	}
	t.Errorf("message %s not on the fingerprint allow-list (got %+v), want an entry (DESIGN §7.5 overturns)", id, allowed)
}
