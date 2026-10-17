package wpk_test

import (
	"testing"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/api/chat"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review/reviewtest"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

const spamText = "💰 earn 3000 SAR a day, join t.me/quickcash now"

// mention builds an in-group command that @mentions target.
func (e *chatEnv) mention(from wa.JID, text string, target wa.JID) chat.Message {
	m := e.inGroup(from, text)
	m.Mentions = []wa.JID{target}
	return m
}

// TestF083_BanRequiresReason: `.ban` takes spam|other; only spam feeds learning; with no reason
// the ban still happens at once as `other` and the admin is asked by DM (DESIGN §13.7, §7.5).
func TestF083_BanRequiresReason(t *testing.T) {
	cases := []struct {
		name       string
		text       string
		wantReason review.BanReason
		wantLearn  bool
		wantAsk    bool
	}{
		{"spam", ".ban spam", review.ReasonSpam, true, false},
		{"other", ".ban other", review.ReasonOther, false, false},
		{"no reason", ".ban", review.ReasonOther, false, true},
		{"unknown reason", ".ban rude", review.ReasonOther, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newChatEnv(t)
			e.openThread(t, adminA)
			cmd := e.replyTo(adminA, tc.text, "q-1", member, spamText)
			e.handle(t, cmd)

			bans := bansOf(e.acts, member)
			if len(bans) != 1 {
				t.Fatalf("got %d bans of the member, want 1 (an admin's .ban is acted on at once, DESIGN §13.7)", len(bans))
			}
			if b := bans[0]; b.Group != grp || b.Reason != tc.wantReason || b.Actor != adminA {
				t.Errorf("got Ban{Group:%s Reason:%q Actor:%s}, want Ban{Group:%s Reason:%q Actor:%s} (no or unknown reason counts as other, DESIGN §13.7)",
					b.Group, b.Reason, b.Actor, grp, tc.wantReason, adminA)
			}
			if got := len(reviewtest.Of[review.Kick](e.acts)); got != 0 {
				t.Errorf("got %d Kicks, want 0 (.ban is a ban with a rejoin block, not a kick)", got)
			}
			if learned := len(activeFor(e.corpus, "q-1")) > 0; learned != tc.wantLearn {
				t.Errorf("confirmed-spam corpus entry present = %v, want %v (only reason spam feeds learning, DESIGN §7.5)", learned, tc.wantLearn)
			}
			if tc.wantReason == review.ReasonSpam && len(deletesOf(e.acts, "q-1")) == 0 {
				t.Errorf("replied spam message not deleted, want a Delete of q-1 (.ban spam on a reply removes the spam, DESIGN §13.7)")
			}
			if len(deletesOf(e.acts, cmd.ID)) == 0 {
				t.Errorf("command message not deleted, want a Delete of %s (commands in a protected group are deleted, DESIGN §9.7)", cmd.ID)
			}
			if asked := len(sendsTo(e.acts, adminA)) > 0; tc.wantAsk && !asked {
				t.Errorf("no DM to the admin, want the question \"Reason? reply spam or other\" (reason is required, DESIGN §13.7)")
			}
			assertSilent(t, e.acts, grp)
		})
	}
}

// TestF083_BanReasonUpgradedWithinWindow: answering "spam" to the reason question within 10
// minutes upgrades the ban's reason; a late answer leaves it as other (DESIGN §13.7).
func TestF083_BanReasonUpgradedWithinWindow(t *testing.T) {
	cases := []struct {
		name     string
		wait     time.Duration
		wantSpam bool
	}{
		{"answered after 2 minutes", 2 * time.Minute, true},
		{"answered after 11 minutes", 11 * time.Minute, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newChatEnv(t)
			e.openThread(t, adminA)
			e.handle(t, e.mention(adminA, ".ban @200000000000001", member))
			if bans := bansOf(e.acts, member); len(bans) != 1 || bans[0].Reason != review.ReasonOther {
				t.Fatalf("got bans %+v, want one Ban{Reason:other} at once (no answer is treated as other, DESIGN §13.7)", bans)
			}
			e.clk.Advance(tc.wait)
			e.handle(t, e.dm(adminA, "spam"))

			var spam int
			for _, b := range bansOf(e.acts, member) {
				if b.Reason == review.ReasonSpam && b.Group == grp {
					spam++
				}
			}
			if got := spam > 0; got != tc.wantSpam {
				t.Errorf("reason upgraded to spam = %v, want %v (the answer counts only within 10 minutes, DESIGN §13.7)", got, tc.wantSpam)
			}
			assertSilent(t, e.acts, grp)
		})
	}
}

// TestF083_KickIsNeverABan: `.kick` removes without a rejoin block (DESIGN §13.7).
func TestF083_KickIsNeverABan(t *testing.T) {
	for _, text := range []string{".kick @200000000000001", ".kick @200000000000001 spam"} {
		t.Run(text, func(t *testing.T) {
			e := newChatEnv(t)
			e.openThread(t, adminA)
			e.handle(t, e.mention(adminA, text, member))

			kicks := reviewtest.Of[review.Kick](e.acts)
			if len(kicks) != 1 || kicks[0].Target != member || kicks[0].Group != grp {
				t.Errorf("got kicks %+v, want one Kick of %s in %s (DESIGN §13.7)", kicks, member, grp)
			}
			if bans := reviewtest.Of[review.Ban](e.acts); len(bans) != 0 {
				t.Errorf("got %d Bans, want 0 (.kick has no rejoin block, DESIGN §13.7)", len(bans))
			}
			assertSilent(t, e.acts, grp)
		})
	}
}

// TestF083_UnbanAllowUnallow: `.unban <number>` lifts a local ban; `.allow`/`.unallow` manage
// the member allow-list (DESIGN §13.7).
func TestF083_UnbanAllowUnallow(t *testing.T) {
	e := newChatEnv(t)
	e.openThread(t, adminA)

	e.handle(t, e.inGroup(adminA, ".unban +966 55 123 4567"))
	unbans := reviewtest.Of[review.Unban](e.acts)
	if len(unbans) != 1 || unbans[0].Group != grp || (unbans[0].Target != member && unbans[0].Target != memberPhone) {
		t.Errorf("got unbans %+v, want one Unban of %s (or %s) in %s (DESIGN §13.7 .unban)", unbans, member, memberPhone, grp)
	}

	e.handle(t, e.mention(adminA, ".allow @200000000000001", member))
	if !e.members.Allowed(grp, member) {
		t.Errorf("member not allow-listed after .allow, want allow-listed (DESIGN §13.7)")
	}
	e.handle(t, e.mention(adminA, ".unallow @200000000000001", member))
	if e.members.Allowed(grp, member) {
		t.Errorf("member still allow-listed after .unallow, want removed (DESIGN §13.7)")
	}
	if n := len(reviewtest.Of[review.Ban](e.acts)) + len(reviewtest.Of[review.Kick](e.acts)); n != 0 {
		t.Errorf("got %d bans/kicks, want 0 (.unban/.allow never remove anyone)", n)
	}
	assertSilent(t, e.acts, grp)
}

// TestF083_UndoReversesLastAction: `.undo` reverses the bot's last reversible action once
// (DESIGN §13.7).
func TestF083_UndoReversesLastAction(t *testing.T) {
	for _, kind := range []string{"ban", "kick"} {
		t.Run(kind, func(t *testing.T) {
			e := newChatEnv(t)
			e.openThread(t, adminA)
			e.history.Add(grp, chat.Reversible{ID: 7, Kind: kind, Target: member, At: e.clk.Now()})

			e.handle(t, e.inGroup(adminA, ".undo"))
			unbans := reviewtest.Of[review.Unban](e.acts)
			if len(unbans) != 1 || unbans[0].Target != member || unbans[0].Group != grp {
				t.Fatalf("got unbans %+v, want one Unban of %s (undoing a %s lifts it, DESIGN §13.7)", unbans, member, kind)
			}
			if _, ok, _ := e.history.LastReversible(e.ctx, grp); ok {
				t.Errorf("action still reversible after .undo, want it marked undone (so it can't be undone twice)")
			}
			e.handle(t, e.inGroup(adminA, ".undo"))
			if got := len(reviewtest.Of[review.Unban](e.acts)); got != 1 {
				t.Errorf("got %d Unbans after a second .undo with nothing left, want 1", got)
			}
			assertSilent(t, e.acts, grp)
		})
	}
}

// TestF083_NeverActsOnAdmins: commands never ban or kick a group admin; the adder is told
// instead (DESIGN §8.6).
func TestF083_NeverActsOnAdmins(t *testing.T) {
	for _, text := range []string{".ban @100000000000002 spam", ".kick @100000000000002"} {
		t.Run(text, func(t *testing.T) {
			e := newChatEnv(t)
			e.openThread(t, adminA)
			e.handle(t, e.mention(adminA, text, adminB))

			if n := len(bansOf(e.acts, adminB)); n != 0 {
				t.Errorf("got %d Bans of an admin, want 0 (never act on admins, DESIGN §8.6)", n)
			}
			for _, k := range reviewtest.Of[review.Kick](e.acts) {
				if k.Target == adminB {
					t.Errorf("got a Kick of an admin, want none (never act on admins, DESIGN §8.6)")
				}
			}
			if len(sendsTo(e.acts, adminA)) == 0 {
				t.Errorf("admin who sent the command got no DM, want an explanation (the adder is told instead, DESIGN §8.6)")
			}
			assertSilent(t, e.acts, grp)
		})
	}
}

// TestF083_NonAdminCommandsIgnored: commands from non-admins do nothing at all (DESIGN §13.7).
func TestF083_NonAdminCommandsIgnored(t *testing.T) {
	e := newChatEnv(t)
	e.history.Add(grp, chat.Reversible{ID: 1, Kind: "ban", Target: member2, At: e.clk.Now()})
	msgs := []chat.Message{
		e.replyTo(member, ".ban spam", "q-9", member2, spamText),
		e.mention(member, ".kick @200000000000002", member2),
		e.inGroup(member, ".unban +966551234567"),
		e.mention(member, ".allow @200000000000001", member),
		e.inGroup(member, ".undo"),
	}
	for _, m := range msgs {
		e.handle(t, m)
	}
	if got := e.acts.All(); len(got) != 0 {
		t.Errorf("got %d actions %+v, want none (the bot ignores commands from non-admins, DESIGN §13.7)", len(got), got)
	}
	if e.members.Allowed(grp, member) {
		t.Errorf("non-admin allow-listed themselves, want ignored (DESIGN §13.7)")
	}
	if _, ok, _ := e.history.LastReversible(e.ctx, grp); !ok {
		t.Errorf("non-admin .undo consumed the last action, want ignored (DESIGN §13.7)")
	}
	if len(e.corpus.Entries()) != 0 {
		t.Errorf("non-admin .ban spam created a corpus entry, want none (only admin decisions are learned, DESIGN §7.5)")
	}
}
