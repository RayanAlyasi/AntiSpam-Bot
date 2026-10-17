package wpk_test

import (
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/api/chat/chattest"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// TestF118_OverturnRemovesWhatWasLearned covers both overturn cases DESIGN §7.5 names: a message
// that was confirmed spam, and one the bot deleted but nobody confirmed. Either way only a
// fingerprint allow-list entry remains, the evidence text is gone, the strike is removed and
// trust is restored.
func TestF118_OverturnRemovesWhatWasLearned(t *testing.T) {
	cases := []struct {
		name      string
		confirmed bool
	}{
		{"confirmed spam", true},
		{"bot deletion only", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			e := newLearnEnv(clock.NewFake(chattest.Start))
			ref := wa.MessageRef{Sender: member, ID: "m-1"}
			if err := e.learning.Snapshot(ctx, review.Snapshot{
				Group: grp, Message: ref, Text: spamText,
				Thumbnail: &wa.Image{Data: []byte{1, 2, 3}, Mimetype: "image/jpeg"}, Reason: "spam", At: e.clk.Now(),
			}); err != nil {
				t.Fatalf("Snapshot() error = %v", err)
			}
			if tc.confirmed {
				if err := e.learning.ConfirmSpam(ctx, review.Confirmation{Group: grp, Message: ref, Text: spamText, Admin: adminA, Source: "command", At: e.clk.Now()}); err != nil {
					t.Fatalf("ConfirmSpam() error = %v", err)
				}
				if len(activeFor(e.corpus, "m-1")) != 1 {
					t.Fatalf("ConfirmSpam stored no active corpus entry, want one (confirmed spam is learned, DESIGN §7.5)")
				}
			}

			if err := e.learning.Overturn(ctx, review.Overturn{Group: grp, Message: ref, Actor: adminB, Method: review.MethodCommand}); err != nil {
				t.Fatalf("Overturn() error = %v, want nil (an overturn of an unconfirmed message must also work)", err)
			}

			if got := activeFor(e.corpus, "m-1"); len(got) != 0 {
				t.Errorf("got %d active corpus entries, want 0 (overturns retract the corpus entry at once, DESIGN §7.5)", len(got))
			}
			assertAllowListed(t, e.corpus.Allowed(), "m-1")
			k := standingKinds(e.standing, member)
			for _, want := range []string{"remove_strike", "restore_trust", "false_positive"} {
				if k[want] == 0 {
					t.Errorf("no %s event for %s (got %v), want one (the member's trust is restored and strike removed, DESIGN §7.5)", want, member, k)
				}
			}
			assertNoTextLeft(t, ctx, e.evidence, e.corpus, grp, "m-1", spamText)
		})
	}
}

// TestF118_OverturnLeavesOtherEntriesAlone: retracting one message must not touch other
// confirmed spam (DESIGN §7.5: only the overturned pattern is removed).
func TestF118_OverturnLeavesOtherEntriesAlone(t *testing.T) {
	ctx := t.Context()
	e := newLearnEnv(clock.NewFake(chattest.Start))
	other := "🎁 free iPhone, claim at bit.ly/claimnow"
	for _, c := range []struct {
		id   wa.MessageID
		text string
	}{{"m-1", spamText}, {"m-2", other}} {
		if err := e.learning.ConfirmSpam(ctx, review.Confirmation{Group: grp, Message: wa.MessageRef{Sender: member, ID: c.id}, Text: c.text, Admin: adminA, Source: "review", At: e.clk.Now()}); err != nil {
			t.Fatalf("ConfirmSpam(%s) error = %v", c.id, err)
		}
	}
	if err := e.learning.Overturn(ctx, review.Overturn{Group: grp, Message: wa.MessageRef{Sender: member, ID: "m-1"}, Actor: adminB, Method: review.MethodReaction}); err != nil {
		t.Fatalf("Overturn() error = %v", err)
	}
	kept := activeFor(e.corpus, "m-2")
	if len(kept) != 1 || kept[0].Text == "" {
		t.Errorf("got %+v for m-2, want its entry still active with its text (an overturn retracts only its own message, DESIGN §7.5)", kept)
	}
	if len(activeFor(e.corpus, "m-1")) != 0 {
		t.Errorf("m-1 still active after its overturn, want retracted (DESIGN §7.5)")
	}
}

// TestF118_OverturnIsIdempotent: a second not-spam decision on the same message (for example
// review ❌ after .notspam) succeeds and leaves no text behind.
func TestF118_OverturnIsIdempotent(t *testing.T) {
	ctx := t.Context()
	e := newLearnEnv(clock.NewFake(chattest.Start))
	ref := wa.MessageRef{Sender: member, ID: "m-1"}
	if err := e.learning.ConfirmSpam(ctx, review.Confirmation{Group: grp, Message: ref, Text: spamText, Admin: adminA, Source: "command", At: e.clk.Now()}); err != nil {
		t.Fatalf("ConfirmSpam() error = %v", err)
	}
	for i := range 2 {
		if err := e.learning.Overturn(ctx, review.Overturn{Group: grp, Message: ref, Actor: adminB, Method: review.MethodCommand}); err != nil {
			t.Fatalf("Overturn() #%d error = %v, want nil (repeated overturns are harmless)", i+1, err)
		}
	}
	if len(activeFor(e.corpus, "m-1")) != 0 {
		t.Errorf("entry active after overturns, want retracted (DESIGN §7.5)")
	}
	assertNoTextLeft(t, ctx, e.evidence, e.corpus, grp, "m-1", spamText)
}
