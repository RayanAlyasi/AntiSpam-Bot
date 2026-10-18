package actions_test

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/actions"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa/fake"
)

// TestShadowReactsPacedLikeTheDeletesTheyStandFor runs the same burst once as real deletes and
// once as shadow reactions at the delete's priority: the reactions must go out at the same
// moments the deletes would have (DESIGN.md §8.4), not one per minute.
func TestShadowReactsPacedLikeTheDeletesTheyStandFor(t *testing.T) {
	const burst = 12
	offsets := func(t *testing.T, mk func(chat wa.JID, i int) actions.Action) []time.Duration {
		var out []time.Duration
		within(t, func(t *testing.T) {
			r := newRig(t, actions.DefaultConfig())
			r.groups.postingFn = func(wa.JID) (actions.Posting, error) {
				return actions.Posting{Protected: true, Silent: true, ShadowReactions: true}, nil
			}
			start := time.Now()
			for i := range burst {
				r.enqueue(mk(group(1), i))
			}
			r.start()
			time.Sleep(5 * time.Minute)
			calls := r.client.writes()
			if len(calls) != burst {
				t.Fatalf("%d writes, want all %d out within 5 minutes", len(calls), burst)
			}
			if got, limit := maxInWindow(calls, time.Minute), actions.DefaultConfig().GroupPerMin; got > limit {
				t.Errorf("%d writes to one group in a minute, want at most %d (the per-group bucket)", got, limit)
			}
			for _, c := range calls {
				out = append(out, c.at.Sub(start))
			}
		})
		return out
	}
	deletes := offsets(t, func(chat wa.JID, i int) actions.Action {
		return revoke(chat, fmt.Sprint("M", i), time.Time{}, actions.P1)
	})
	reacts := offsets(t, func(chat wa.JID, i int) actions.Action {
		a := shadowReact(chat, fmt.Sprint("M", i), "❌")
		a.Priority = actions.P1
		return a
	})
	if !slices.Equal(deletes, reacts) {
		t.Errorf("shadow reactions at %v, deletes at %v; want the same timing (a shadow reaction stands in for its delete)", reacts, deletes)
	}
	if len(reacts) > 1 && reacts[1] >= time.Minute {
		t.Errorf("second shadow reaction %s after the first burst started, want well within a minute (not the 1/min post cap)", reacts[1])
	}
}

// TestShadowReactsLeaveThePostCapAlone: shadow reactions don't use up the per-group post cap, so
// a real post in a group that allows posts isn't held back by them.
func TestShadowReactsLeaveThePostCapAlone(t *testing.T) {
	within(t, func(t *testing.T) {
		r := newRig(t, actions.DefaultConfig())
		r.groups.postingFn = func(wa.JID) (actions.Posting, error) {
			return actions.Posting{Protected: true, ShadowReactions: true}, nil // shadow mode, silent off
		}
		start := time.Now()
		for i := range 3 {
			a := shadowReact(group(1), fmt.Sprint("M", i), "❌")
			a.Priority = actions.P1
			r.enqueue(a)
		}
		post := r.enqueue(send(group(1), "notice", "hello", actions.PurposeNotice)).ID
		r.start()
		time.Sleep(time.Minute)
		if got := r.status(post).Status; got != actions.StatusDone {
			t.Fatalf("post is %s a minute in, want done (shadow reactions must not count against the post cap)", got)
		}
		var sent time.Duration
		for _, c := range r.client.writes() {
			if c.method == fake.MethodSend {
				sent = c.at.Sub(start)
			}
		}
		if sent >= time.Minute {
			t.Errorf("post went out %s after the shadow reactions were queued, want within the first minute", sent)
		}
	})
}

// TestShadowReactsNeverJumpTheQueue: a shadow reaction's 10-minute deadline must not make it
// urgent, or raided shadow groups would hold back real work elsewhere at the same priority
// (round robin, DESIGN.md §10.3) and the OTPs that must never be starved (§10.2).
func TestShadowReactsNeverJumpTheQueue(t *testing.T) {
	tests := []struct {
		name   string
		prio   actions.Priority
		groups int
		other  []actions.Action
	}{
		{"real deletes in an enforce group", actions.P1, 3, func() []actions.Action {
			var out []actions.Action
			for i := range 5 {
				out = append(out, revoke(group(9), fmt.Sprint("R", i), time.Time{}, actions.P1))
			}
			return out
		}()},
		{"an OTP while only P3 work waits", actions.P3, 3, []actions.Action{send("701@s.whatsapp.net", "otp", "code 1234", actions.PurposeOTP)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			within(t, func(t *testing.T) {
				r := newRig(t, actions.DefaultConfig())
				r.groups.postingFn = func(chat wa.JID) (actions.Posting, error) {
					return actions.Posting{Protected: true, Silent: true, ShadowReactions: chat != group(9)}, nil
				}
				for g := range tt.groups {
					for i := range 100 {
						a := shadowReact(group(g+1), fmt.Sprint("M", i), "❌")
						a.Priority = tt.prio
						r.enqueue(a)
					}
				}
				var ids []int64
				for _, a := range tt.other {
					ids = append(ids, r.enqueue(a).ID)
				}
				r.start()
				time.Sleep(2 * time.Minute)
				for _, id := range ids {
					if got := r.status(id).Status; got != actions.StatusDone {
						t.Errorf("action %d is %s two minutes in, want done (shadow reactions take turns, they don't go first)", id, got)
					}
				}
			})
		})
	}
}
