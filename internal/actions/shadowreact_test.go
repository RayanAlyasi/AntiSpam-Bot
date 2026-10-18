package actions_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/actions"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa/fake"
)

func shadowReact(chat wa.JID, id, emoji string) actions.Action {
	return actions.Action{
		Chat: chat, Kind: actions.KindReact, Priority: actions.P4,
		Payload:   actions.Payload{Target: wa.MessageRef{Sender: member(1), ID: wa.MessageID(id)}, Emoji: emoji, Purpose: actions.PurposeShadow},
		DedupeKey: actions.ShadowReactKey(chat, wa.MessageID(id)),
	}
}

func TestShadowReactionsPostOnlyWhileOn(t *testing.T) {
	within(t, func(t *testing.T) {
		r := newRig(t, actions.DefaultConfig())
		on, off, open := group(1), group(2), group(3)
		r.groups.postingFn = func(chat wa.JID) (actions.Posting, error) {
			switch chat {
			case on:
				return actions.Posting{Protected: true, Silent: true, ShadowReactions: true}, nil
			case off:
				return actions.Posting{Protected: true, Silent: true}, nil
			default:
				return actions.Posting{Protected: true}, nil // not silent, not in shadow mode
			}
		}
		var shown []int64
		for i := range 3 {
			shown = append(shown, r.enqueue(shadowReact(on, fmt.Sprint("M", i), "❌")).ID)
		}
		plain := shadowReact(on, "P", "👍")
		plain.Payload.Purpose, plain.DedupeKey = "", "plain"
		refused := []int64{
			r.enqueue(plain).ID,
			r.enqueue(shadowReact(off, "M9", "❓")).ID,
			r.enqueue(shadowReact(open, "M8", "❓")).ID,
		}
		r.start()
		time.Sleep(5 * time.Minute)
		for _, id := range shown {
			if got := r.status(id).Status; got != actions.StatusDone {
				t.Errorf("shadow reaction %d is %s, want done (shadow.reactions on lets them through silent mode, DESIGN §8.4)", id, got)
			}
		}
		for i, id := range refused {
			if got := r.status(id).Status; got != actions.StatusRefused {
				t.Errorf("post %d is %s, want refused (only shadow reactions, only while shadow.reactions is on)", i, got)
			}
		}
		var at []time.Time
		for _, c := range r.client.writes() {
			if c.method != fake.MethodReact || c.chat != on {
				t.Errorf("WhatsApp got %s in %s, want only the shadow reactions in %s", c.method, c.chat, on)
			}
			at = append(at, c.at)
		}
		for i := 1; i < len(at); i++ {
			if gap := at[i].Sub(at[i-1]); gap < time.Minute {
				t.Errorf("shadow reactions %s apart, want at least a minute (the per-group post cap still applies)", gap)
			}
		}
	})
}

func TestShadowReactionsExpireInsteadOfGoingOutLate(t *testing.T) {
	within(t, func(t *testing.T) {
		r := newRig(t, actions.DefaultConfig())
		r.groups.postingFn = func(wa.JID) (actions.Posting, error) {
			return actions.Posting{Protected: true, Silent: true, ShadowReactions: true}, nil
		}
		start := time.Now()
		var ids []int64
		for i := range 15 {
			ids = append(ids, r.enqueue(shadowReact(group(1), fmt.Sprint("M", i), "❌")).ID)
		}
		if d := r.status(ids[0]).Deadline.Sub(start); d != actions.DefaultShadowReactWindow {
			t.Fatalf("shadow reaction deadline %s after enqueue, want %s", d, actions.DefaultShadowReactWindow)
		}
		r.start()
		time.Sleep(30 * time.Minute)
		var done, expired int
		for _, id := range ids {
			switch got := r.status(id).Status; got {
			case actions.StatusDone:
				done++
			case actions.StatusExpired:
				expired++
			default:
				t.Errorf("shadow reaction %d is %s, want done or expired", id, got)
			}
		}
		if done == 0 || expired == 0 {
			t.Errorf("%d done, %d expired; want the first ones shown and the rest dropped at the 1/min post cap", done, expired)
		}
		for _, c := range r.client.writes() {
			if c.at.Sub(start) > actions.DefaultShadowReactWindow {
				t.Errorf("a shadow reaction went out %s after it was queued, want none after %s", c.at.Sub(start), actions.DefaultShadowReactWindow)
			}
		}
	})
}
