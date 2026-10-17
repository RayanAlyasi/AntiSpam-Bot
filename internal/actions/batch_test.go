package actions_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/actions"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa/fake"
)

// TestHugeKickYieldsAndKeepsProgress: a 200-member kick runs in turns of at most 4 calls, so
// another group's lockdown isn't held up, and every member is sent exactly once.
func TestHugeKickYieldsAndKeepsProgress(t *testing.T) {
	within(t, func(t *testing.T) {
		r := newRig(t, actions.DefaultConfig())
		g, other := group(1), group(2)
		info := wa.GroupInfo{JID: g}
		var jids []wa.JID
		for i := range 200 {
			jids = append(jids, member(i))
			info.Participants = append(info.Participants, wa.Participant{JID: member(i)})
		}
		r.client.SetGroup(info)
		r.client.SetGroup(wa.GroupInfo{JID: other})
		kickID := r.enqueue(actions.Action{Chat: g, Kind: actions.KindKick, Priority: actions.P0, Payload: actions.Payload{JIDs: jids}, DedupeKey: "raid"}).ID
		r.start()
		time.Sleep(3 * time.Second)
		lock := r.enqueue(actions.Action{Chat: other, Kind: actions.KindAnnounce, Priority: actions.P0, DedupeKey: "lock", Payload: actions.Payload{On: true}}).ID
		time.Sleep(20 * time.Second)
		if got := r.status(lock).Status; got != actions.StatusDone {
			t.Errorf("another group's lockdown 20 s into a 200-member kick is %s, want done (batches take turns)", got)
		}
		time.Sleep(time.Hour)
		seen := map[wa.JID]int{}
		calls := 0
		for _, c := range r.client.writes() {
			if c.method == fake.MethodUpdateParticipants {
				calls++
				for _, j := range c.jids {
					seen[j]++
				}
			}
		}
		if calls != 25 || len(seen) != 200 {
			t.Errorf("%d calls for %d members, want 25 calls for 200 (progress kept between turns)", calls, len(seen))
		}
		for j, n := range seen {
			if n != 1 {
				t.Errorf("%s sent %d times, want once", j, n)
			}
		}
		if a := r.status(kickID); a.Status != actions.StatusDone || len(a.Result.Participants) != 200 {
			t.Errorf("huge kick is %s with %d outcomes, want done with 200", a.Status, len(a.Result.Participants))
		}
	})
}

// TestUnsentBatchMembersKeepTheirAttempts: when a batch call fails, members whose JIDs weren't
// in that call don't lose an attempt.
func TestUnsentBatchMembersKeepTheirAttempts(t *testing.T) {
	within(t, func(t *testing.T) {
		r := newRig(t, actions.DefaultConfig())
		g := group(1)
		info := wa.GroupInfo{JID: g}
		for i := range 16 {
			info.Participants = append(info.Participants, wa.Participant{JID: member(i)})
		}
		r.client.SetGroup(info)
		var ids []int64
		for i := range 2 {
			var jids []wa.JID
			for k := range 8 {
				jids = append(jids, member(i*8+k))
			}
			ids = append(ids, r.enqueue(actions.Action{
				Chat: g, Kind: actions.KindKick, Priority: actions.P2,
				Payload: actions.Payload{JIDs: jids}, DedupeKey: fmt.Sprint("k", i),
			}).ID)
		}
		r.client.FailNext(fake.MethodUpdateParticipants, wa.ErrTimeout)
		r.start()
		time.Sleep(3 * time.Second) // the first call failed and its member waits for its backoff
		if a := r.status(ids[1]); a.Attempts != 0 {
			t.Errorf("member never sent has %d attempts, want 0", a.Attempts)
		}
		time.Sleep(10 * time.Minute)
		for _, id := range ids {
			if a := r.status(id); a.Status != actions.StatusDone {
				t.Errorf("kick %d is %s (%s), want done after the retry", id, a.Status, a.LastError)
			}
		}
	})
}
