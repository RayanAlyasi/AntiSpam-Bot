package actions_test

import (
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/actions"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa/fake"
)

const ghost = 900 // a member number that isn't in the group and has no join request

func TestKicksCoalesce(t *testing.T) {
	seq := func(from, to int) [][]int {
		var out [][]int
		for i := from; i <= to; i++ {
			out = append(out, []int{i})
		}
		return out
	}
	cases := []struct {
		name    string
		kind    actions.Kind
		actions [][]int // member numbers per action
		dupes   []int   // members kicked a second time for another cause
	}{
		{"3 kicks, one call", actions.KindKick, seq(1, 3), nil},
		{"10 kicks with one non-member, two calls", actions.KindKick, append(seq(1, 9), []int{ghost}), nil},
		{"17 JIDs, multi-JID action and a duplicate", actions.KindKick, append(append([][]int{{1, 2}}, seq(3, 16)...), []int{ghost}), []int{16}},
		{"10 join rejections, two calls", actions.KindRejectJoin, append(seq(1, 9), []int{ghost}), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cfg := actions.DefaultConfig()
				h := newHarness(t, cfg)
				g, other := group(1), group(2)
				h.setGroup(g, 20)
				h.setGroup(other, 3)
				var reqs []wa.JoinRequest
				for i := 1; i <= 20; i++ {
					reqs = append(reqs, wa.JoinRequest{JID: member(i), RequestedAt: time.Now()})
				}
				h.wa.SetJoinRequests(g, reqs)
				h.wa.SetJoinRequests(other, reqs[:3])

				build, method := kick, fake.MethodUpdateParticipants
				if tc.kind == actions.KindRejectJoin {
					build, method = rejectJoin, fake.MethodUpdateJoinRequests
				}
				want := map[wa.JID]bool{}
				ids := map[int64][]wa.JID{}
				for _, ms := range tc.actions {
					var jids []wa.JID
					for _, m := range ms {
						jids = append(jids, member(m))
						want[member(m)] = true
					}
					ids[h.add(build(g, "ban1", jids...))] = jids
				}
				for _, m := range tc.dupes {
					ids[h.add(build(g, "ban2", member(m)))] = []wa.JID{member(m)}
				}
				otherID := h.add(build(other, "ban1", member(1)))
				h.start()
				h.waitFinal(5*time.Minute, "a few batched calls")

				var seen []wa.JID
				var inG int
				for _, c := range h.rec.to(method) {
					jids := c.Args[1].([]wa.JID)
					if c.Chat == other {
						if !slices.Equal(jids, []wa.JID{member(1)}) {
							t.Errorf("call to the other group carried %v, want only its own kick: batches never mix groups (DESIGN §10.4)", jids)
						}
						continue
					}
					inG++
					if len(jids) > cfg.MaxKickBatch {
						t.Errorf("one call carried %d JIDs, want at most max_kick_batch %d (DESIGN §10.4)", len(jids), cfg.MaxKickBatch)
					}
					if h.off(c.Start) < cfg.KickWindow {
						t.Errorf("batch went out at +%s, want after the %s coalescing window (DESIGN §10.4)", h.off(c.Start), cfg.KickWindow)
					}
					switch a := c.Args[2].(type) {
					case wa.ParticipantAction:
						if a != wa.ParticipantRemove {
							t.Errorf("participant action %v, want remove", a)
						}
					case wa.JoinRequestAction:
						if a != wa.JoinRequestReject {
							t.Errorf("join request action %v, want reject", a)
						}
					}
					for _, j := range jids {
						if !want[j] {
							t.Errorf("batch included %s, which was never enqueued: batching never lowers the bar, each JID is individually eligible (DESIGN §10.4)", j)
						}
						if slices.Contains(seen, j) {
							t.Errorf("%s appeared in two batch slots, want each JID once", j)
						}
						seen = append(seen, j)
					}
				}
				if wantCalls := (len(want) + cfg.MaxKickBatch - 1) / cfg.MaxKickBatch; inG != wantCalls {
					t.Errorf("%d calls for %d JIDs, want %d: coalesced into as few calls as max_kick_batch allows (DESIGN §10.4)", inG, len(want), wantCalls)
				}
				if len(seen) != len(want) {
					t.Errorf("batches carried %d JIDs, want all %d enqueued", len(seen), len(want))
				}
				for id, jids := range ids {
					a := h.get(id)
					if a.Status != actions.StatusDone {
						t.Errorf("action #%d %v is %s (%s), want done, also for a JID no longer in the group", id, jids, a.Status, a.LastError)
					}
					var got []wa.JID
					for _, p := range a.Result.Participants {
						got = append(got, p.JID)
						if p.JID == member(ghost) && p.Error == "" {
							t.Errorf("outcome for a non-member has no error, want the per-JID failure recorded")
						}
					}
					slices.Sort(got)
					sorted := slices.Sorted(slices.Values(jids))
					if !slices.Equal(got, sorted) {
						t.Errorf("action #%d Result.Participants for %v, want one outcome per own JID %v", id, got, sorted)
					}
				}
				if got := h.get(otherID).Status; got != actions.StatusDone {
					t.Errorf("other group's action is %s, want done", got)
				}
			})
		})
	}
}

func TestKicksWithinWindowMerge(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := actions.DefaultConfig()
		h := newHarness(t, cfg)
		g := group(1)
		h.setGroup(g, 10)
		h.start()
		h.advance(time.Second)
		first := time.Now()
		h.add(kick(g, "ban", member(1)))
		h.advance(time.Second)
		h.add(kick(g, "ban", member(2)))
		h.advance(time.Second)
		h.add(kick(g, "ban", member(3)))
		h.advance(30 * time.Second)

		calls := h.rec.to(fake.MethodUpdateParticipants)
		if len(calls) != 1 {
			t.Fatalf("%d UpdateParticipants calls, want 1: kicks within the 2–3 s window merge into one call (DESIGN §10.4)", len(calls))
		}
		got := slices.Sorted(slices.Values(calls[0].Args[1].([]wa.JID)))
		if want := []wa.JID{member(1), member(2), member(3)}; !slices.Equal(got, want) {
			t.Fatalf("batch %v, want %v", got, want)
		}
		if d := calls[0].Start.Sub(first); d < cfg.KickWindow {
			t.Fatalf("batch went out %s after the first kick, want at least the %s window (DESIGN §10.4)", d, cfg.KickWindow)
		}
	})
}
