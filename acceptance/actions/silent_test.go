package actions_test

import (
	"errors"
	"fmt"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/actions"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa/fake"
)

var postMethods = []string{fake.MethodSend, fake.MethodEdit, fake.MethodReact}

// TestSilentGroupRefusesEveryPost is the §18 silent-mode check at the queue: every kind that
// posts, at every priority, is refused in a silent protected group (or when its settings can't be
// read), and WhatsApp is never asked to post there.
func TestSilentGroupRefusesEveryPost(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, actions.DefaultConfig())
		silent, unknown := group(1), group(2)
		h.groups.set(silent, actions.Posting{Protected: true, Silent: true})
		h.groups.fail(unknown, errors.New("group settings unavailable"))
		var refused []int64
		for _, kind := range actions.Kinds {
			if !kind.Posts() {
				continue
			}
			for p := actions.P0; p <= actions.P4; p++ {
				for _, chat := range []wa.JID{silent, unknown} {
					refused = append(refused, h.add(post(kind, chat, p, fmt.Sprintf("%s-%s-%s", kind, p, chat))))
				}
			}
		}
		if len(refused) < 30 {
			t.Fatalf("only %d posting actions built, want every posting kind × 5 priorities × 2 chats", len(refused))
		}
		delID := h.add(revoke(silent, "spam", actions.P1, time.Minute))
		h.start()
		h.waitFinal(10*time.Minute, "refusals are immediate")

		for _, m := range postMethods {
			if n := len(h.rec.to(m)); n != 0 {
				t.Errorf("%d %s calls, want 0: nothing is posted in a silent protected group, by any code path (DESIGN §9.7/§18)", n, m)
			}
		}
		for _, id := range refused {
			if a := h.get(id); a.Status != actions.StatusRefused {
				t.Errorf("%s %s in %s is %s, want refused (silent mode is enforced at the queue, DESIGN §9.7)", a.Priority, a.Kind, a.Chat, a.Status)
			}
		}
		if got := h.get(delID).Status; got != actions.StatusDone {
			t.Errorf("delete in the silent group is %s, want done: deleting isn't posting (DESIGN §9.7)", got)
		}
	})
}

func TestNonSilentGroupOnePostPerMinute(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := actions.DefaultConfig()
		h := newHarness(t, cfg)
		loud, plain := group(1), group(2)
		h.groups.set(loud, actions.Posting{Protected: true, Silent: false})
		for i := range 4 {
			h.add(post(actions.KindSend, loud, actions.P3, fmt.Sprintf("loud%d", i)))
			h.add(post(actions.KindSend, plain, actions.P3, fmt.Sprintf("plain%d", i)))
		}
		h.start()
		h.waitFinal(10*time.Minute, "4 posts at 1/min")

		var loudAt, plainAt []time.Time
		for _, c := range h.rec.to(fake.MethodSend) {
			switch c.Chat {
			case loud:
				loudAt = append(loudAt, c.Start)
			case plain:
				plainAt = append(plainAt, c.Start)
			}
		}
		if len(loudAt) != 4 || len(plainAt) != 4 {
			t.Fatalf("sent %d to the protected group and %d to the other, want 4 each", len(loudAt), len(plainAt))
		}
		for i := 1; i < len(loudAt); i++ {
			if d := loudAt[i].Sub(loudAt[i-1]); d < time.Minute {
				t.Errorf("posts %d and %d in a protected group with silent off are %s apart, want at least 1 min (DESIGN §9.7)", i-1, i, d)
			}
		}
		if d := h.off(plainAt[len(plainAt)-1]); d > 30*time.Second {
			t.Errorf("the non-protected group's 4th post went out at +%s, want within 30 s: only protected groups get the 1/min limit (DESIGN §10.3)", d)
		}
		for _, a := range h.store.All() {
			if a.Status != actions.StatusDone {
				t.Errorf("post #%d in %s is %s, want done", a.ID, a.Chat, a.Status)
			}
		}
	})
}

func TestSendVariants(t *testing.T) {
	// noRepeat fails if two consecutive sends to different chats carry the same text.
	noRepeat := func(t *testing.T, sends []call) {
		t.Helper()
		for i := 1; i < len(sends); i++ {
			a, b := sends[i-1], sends[i]
			if a.Chat != b.Chat && a.Args[1].(wa.OutgoingMessage).Text == b.Args[1].(wa.OutgoingMessage).Text {
				t.Errorf("sends %d and %d to different chats both said %q, want no identical text twice in a row (DESIGN §10.3)", i-1, i, b.Args[1].(wa.OutgoingMessage).Text)
			}
		}
	}

	t.Run("variants rotate", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			h := newHarness(t, actions.DefaultConfig())
			ids := map[int64]wa.JID{}
			for i := range 6 {
				ids[h.add(send(dm(i), fmt.Sprintf("v%d", i), "Please review", "Please take a look", "A review is waiting"))] = dm(i)
			}
			h.start()
			h.waitFinal(5*time.Minute, "6 DMs")
			sends := h.rec.to(fake.MethodSend)
			if len(sends) != 6 {
				t.Fatalf("%d sends, want 6", len(sends))
			}
			noRepeat(t, sends)
			for id, chat := range ids {
				a := h.get(id)
				i := slices.IndexFunc(sends, func(c call) bool { return c.Chat == chat })
				sent := sends[i].Args[1].(wa.OutgoingMessage).Text
				if a.Result.SentText != sent {
					t.Errorf("Result.SentText %q, want the text actually sent %q", a.Result.SentText, sent)
				}
				if !slices.Contains(append([]string{a.Payload.Text}, a.Payload.Variants...), sent) {
					t.Errorf("sent %q, want Text or one of its Variants", sent)
				}
			}
		})
	})

	t.Run("a repeat waits for a different text", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			h := newHarness(t, actions.DefaultConfig())
			h.add(send(dm(1), "x1", "Same text"))
			h.add(send(dm(2), "x2", "Same text"))
			h.add(send(dm(3), "y3", "Other text"))
			h.start()
			h.waitFinal(5*time.Minute, "3 DMs")
			sends := h.rec.to(fake.MethodSend)
			if len(sends) != 3 {
				t.Fatalf("%d sends, want 3", len(sends))
			}
			noRepeat(t, sends)
		})
	})

	t.Run("a repeat with nothing else to send expires", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			h := newHarness(t, actions.DefaultConfig())
			for i := 1; i <= 2; i++ {
				a := send(dm(i), fmt.Sprintf("only%d", i), "Same text")
				a.Deadline = time.Now().Add(3 * time.Minute)
				h.add(a)
			}
			h.start()
			h.waitFinal(10*time.Minute, "one sends, the other expires")
			if n := len(h.rec.to(fake.MethodSend)); n != 1 {
				t.Fatalf("%d sends, want 1: the same text never goes to two chats in a row (DESIGN §10.3)", n)
			}
			var done, expired int
			for _, a := range h.store.All() {
				switch a.Status {
				case actions.StatusDone:
					done++
				case actions.StatusExpired:
					expired++
				}
			}
			if done != 1 || expired != 1 {
				t.Fatalf("%d done and %d expired, want 1 and 1", done, expired)
			}
		})
	})
}
