package actions_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/actions"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa/fake"
)

// TestBacklogBeyondWindowDoesNotStarve: a raided group's backlog far larger than the in-memory
// window must not hide another group's delete, an OTP or an urgent delete (DESIGN.md §10.3, §10.4).
func TestBacklogBeyondWindowDoesNotStarve(t *testing.T) {
	within(t, func(t *testing.T) {
		cfg := actions.DefaultConfig()
		r := newRig(t, cfg)
		for i := range 4 * cfg.Window {
			r.enqueue(revoke(group(1), fmt.Sprint("raid", i), time.Now(), actions.P1))
		}
		quiet := r.enqueue(revoke(group(2), "quiet", time.Now(), actions.P1)).ID
		urgent := r.enqueue(revoke(group(3), "urgent", time.Now().Add(-47*time.Hour-30*time.Minute), actions.P1)).ID
		otp := r.enqueue(send("701@s.whatsapp.net", "otp", "Your code is 1234", actions.PurposeOTP)).ID
		r.start()
		time.Sleep(time.Minute)
		for name, id := range map[string]int64{"other group's delete": quiet, "urgent delete": urgent, "OTP": otp} {
			if got := r.status(id).Status; got != actions.StatusDone {
				t.Errorf("%s behind a %d-delete backlog is %s after a minute, want done", name, 4*cfg.Window, got)
			}
		}
	})
}

// TestTempBanOnAllowedLockdownBacksOff: a lockdown still allowed by the open breaker must back
// off and give up on repeated temporary bans, not retry in a tight loop.
func TestTempBanOnAllowedLockdownBacksOff(t *testing.T) {
	within(t, func(t *testing.T) {
		cfg := actions.DefaultConfig()
		r := newRig(t, cfg)
		g := group(1)
		r.client.SetGroup(wa.GroupInfo{JID: g})
		r.client.FailAlways(fake.MethodSetAnnounce, &wa.TemporaryBanError{Code: 101, Expire: time.Hour})
		id := r.enqueue(actions.Action{Chat: g, Kind: actions.KindAnnounce, Priority: actions.P0, DedupeKey: "lock", Payload: actions.Payload{On: true}}).ID
		r.start()
		time.Sleep(time.Hour)
		if n := len(r.client.CallsTo(fake.MethodSetAnnounce)); n > cfg.MaxAttempts {
			t.Errorf("%d lockdown calls during a temporary ban, want at most %d (backoff, then give up)", n, cfg.MaxAttempts)
		}
		if got := r.status(id).Status; got != actions.StatusFailed {
			t.Errorf("lockdown after repeated temporary bans is %s, want failed", got)
		}
		if st := r.ex.State(); !st.BreakerOpen || st.Rate >= float64(st.Ceiling) {
			t.Errorf("state %+v, want the breaker open and the rate cut", st)
		}
	})
}

// TestKickBatchesPackAcrossActions: JIDs of several kick actions pack into the fewest calls.
func TestKickBatchesPackAcrossActions(t *testing.T) {
	cases := []struct {
		name      string
		sizes     []int
		wantCalls int
	}{
		{"three kicks of five", []int{5, 5, 5}, 2},
		{"a big kick and a small one", []int{10, 1}, 2},
		{"one kick of seventeen", []int{17}, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			within(t, func(t *testing.T) {
				r := newRig(t, actions.DefaultConfig())
				g := group(1)
				info := wa.GroupInfo{JID: g}
				n := 0
				for i, size := range tc.sizes {
					var jids []wa.JID
					for range size {
						n++
						jids = append(jids, member(n))
						info.Participants = append(info.Participants, wa.Participant{JID: member(n)})
					}
					r.enqueue(actions.Action{
						Chat: g, Kind: actions.KindKick, Priority: actions.P2,
						Payload: actions.Payload{JIDs: jids}, DedupeKey: fmt.Sprint("kick", i),
					})
				}
				r.client.SetGroup(info)
				r.start()
				time.Sleep(time.Minute)
				calls := r.client.writes()
				if len(calls) != tc.wantCalls {
					t.Errorf("%d kick calls for %d members, want %d (max_kick_batch 8)", len(calls), n, tc.wantCalls)
				}
				for _, a := range r.store.All() {
					if a.Status != actions.StatusDone {
						t.Errorf("kick %d is %s (%s), want done", a.ID, a.Status, a.LastError)
					}
				}
			})
		})
	}
}

// TestKillSwitchStopsABatchMidway: pausing stops the remaining calls of a batch.
func TestKillSwitchStopsABatchMidway(t *testing.T) {
	within(t, func(t *testing.T) {
		r := newRig(t, actions.DefaultConfig())
		op := actions.Operator{Store: r.store, Audit: &audits{}}
		g := group(1)
		info := wa.GroupInfo{JID: g}
		var jids []wa.JID
		for i := range 30 {
			jids = append(jids, member(i))
			info.Participants = append(info.Participants, wa.Participant{JID: member(i)})
		}
		r.client.SetGroup(info)
		r.enqueue(actions.Action{Chat: g, Kind: actions.KindKick, Priority: actions.P0, Payload: actions.Payload{JIDs: jids}, DedupeKey: "raid"})
		r.start()
		time.Sleep(2600 * time.Millisecond) // the first call has gone out
		if err := op.Pause(context.Background(), "operator", "stop"); err != nil {
			t.Fatal(err)
		}
		paused := time.Now()
		time.Sleep(time.Hour)
		for _, c := range r.client.writes() {
			if c.at.After(paused) {
				t.Errorf("a kick call started %s after the kill switch, want none", c.at.Sub(paused))
			}
		}
	})
}
