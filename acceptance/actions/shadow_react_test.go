package actions_test

import (
	"fmt"
	"testing"
	"testing/synctest"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/actions"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa/fake"
)

const (
	wouldAct = "❌" // ❌
	wouldAsk = "❓" // ❓
)

var (
	shadowOn  = actions.Posting{Protected: true, Silent: true, ShadowReactions: true}
	shadowOff = actions.Posting{Protected: true, Silent: true}
)

// shadowReact builds shadow mode's reaction to message id in chat, as the fast lane enqueues it.
func shadowReact(chat wa.JID, id, emoji string) actions.Action {
	mid := wa.MessageID(id)
	return actions.Action{
		Bot: botA, Chat: chat, Kind: actions.KindReact, Priority: actions.P4,
		Payload:   actions.Payload{Target: wa.MessageRef{Sender: spammer, ID: mid}, Emoji: emoji, Purpose: actions.PurposeShadow},
		DedupeKey: actions.ShadowReactKey(chat, mid),
	}
}

func TestShadowReactIsTheOnlyPostInASilentGroup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, actions.DefaultConfig())
		on, off := group(1), group(2)
		h.groups.set(on, shadowOn)
		h.groups.set(off, shadowOff)

		shadowID := h.add(shadowReact(on, "SPAM1", wouldAct))
		refused := map[int64]string{
			h.add(post(actions.KindReact, on, actions.P4, "plain")): "an ordinary react (no purpose) in a silent group, even with shadow reactions on",
			h.add(post(actions.KindSend, on, actions.P3, "notice")): "a send in a silent group, even with shadow reactions on",
			h.add(shadowReact(off, "SPAM2", wouldAsk)):              "a shadow react in a silent group whose shadow.reactions is off",
		}
		h.start()
		h.waitFinal(10*time.Minute, "one react, the rest refused at once")

		if got := h.get(shadowID).Status; got != actions.StatusDone {
			t.Errorf("shadow react in a silent shadow group with shadow.reactions on is %s, want done (the one exception to silent mode, DESIGN §8.4, §9.7)", got)
		}
		reacts := h.rec.to(fake.MethodReact)
		if len(reacts) != 1 {
			t.Fatalf("%d React calls, want exactly 1 (only the shadow react may post in a silent group, DESIGN §9.7)", len(reacts))
		}
		r := reacts[0]
		if r.Chat != on || r.Args[1].(wa.MessageRef).ID != "SPAM1" || r.Args[2].(string) != wouldAct {
			t.Errorf("React(%v), want React(%s, SPAM1, %s): the reaction goes on the caught message", r.Args, on, wouldAct)
		}
		for id, what := range refused {
			if got := h.get(id).Status; got != actions.StatusRefused {
				t.Errorf("%s is %s, want refused (DESIGN §9.7)", what, got)
			}
		}
		if n := len(h.rec.to(fake.MethodSend)); n != 0 {
			t.Errorf("%d Send calls, want 0 (nothing but a shadow reaction is posted in a silent group, DESIGN §9.7)", n)
		}
	})
}

func TestShadowReactRefusedOnceShadowReactionsTurnOff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, actions.DefaultConfig())
		g := group(1)
		h.groups.set(g, shadowOn)
		first := h.add(shadowReact(g, "A", wouldAct))
		second := h.add(shadowReact(g, "B", wouldAct))
		h.start()
		for i := 0; len(h.rec.to(fake.MethodReact)) == 0; i++ {
			if i > 30 {
				t.Fatalf("no React after 30 s, want the first shadow react sent at once")
			}
			h.advance(time.Second)
		}
		// The admins leave shadow mode (or turn shadow.reactions off) before the second runs; it
		// can't run yet because of the one-post-per-minute cap.
		h.groups.set(g, shadowOff)
		h.waitFinal(15*time.Minute, "the second react is refused when it comes up")

		if got := h.get(first).Status; got != actions.StatusDone {
			t.Errorf("first shadow react is %s, want done", got)
		}
		if got := h.get(second).Status; got != actions.StatusRefused {
			t.Errorf("second shadow react is %s, want refused (silent mode is checked when the action runs; shadow reactions were turned off, DESIGN §9.7)", got)
		}
		if n := len(h.rec.to(fake.MethodReact)); n != 1 {
			t.Errorf("%d React calls, want 1 (a shadow react queued before the group left shadow mode must not be sent, DESIGN §8.4, §9.7)", n)
		}
	})
}

func TestShadowReactsPacedAndExpireAfterTenMinutes(t *testing.T) {
	if actions.DefaultShadowReactWindow != 10*time.Minute {
		t.Errorf("DefaultShadowReactWindow = %s, want 10m (a late judgement is pointless, DESIGN §10.4)", actions.DefaultShadowReactWindow)
	}
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	if got := actions.DefaultDeadline(shadowReact(group(1), "X", wouldAct), now); !got.Equal(now.Add(10 * time.Minute)) {
		t.Errorf("DefaultDeadline(shadow react) = now+%s, want now+10m (DESIGN §10.4)", got.Sub(now))
	}

	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, actions.DefaultConfig())
		g := group(1)
		h.groups.set(g, shadowOn)
		const n = 15
		for i := range n {
			h.add(shadowReact(g, fmt.Sprintf("M%02d", i), wouldAct))
		}
		h.start()
		h.waitFinal(30*time.Minute, "every shadow react is sent or expires")

		reacts := h.rec.to(fake.MethodReact)
		for i, r := range reacts {
			if d := h.off(r.Start); d > 10*time.Minute {
				t.Errorf("React #%d started at +%s, want within 10 min of enqueue (expired shadow reacts are dropped, never sent late, DESIGN §10.4)", i, d)
			}
			if i > 0 {
				if d := r.Start.Sub(reacts[i-1].Start); d < time.Minute {
					t.Errorf("Reacts #%d and #%d in one silent group are %s apart, want at least 1 min (shadow reacts keep the per-group post cap, DESIGN §10.3)", i-1, i, d)
				}
			}
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
		if done < 2 || expired < 1 || done+expired != n || done != len(reacts) {
			t.Errorf("%d done, %d expired, %d React calls of %d; want several done (1/min), the rest expired after 10 min, and one call per done react (DESIGN §10.3, §10.4)",
				done, expired, len(reacts), n)
		}
	})
}
