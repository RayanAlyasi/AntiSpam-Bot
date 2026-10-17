package actions_test

import (
	"fmt"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/actions"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa/fake"
)

func TestPriorityOrder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, actions.DefaultConfig())
		// Enqueued lowest priority first, each in its own group, so only priority decides.
		for p := actions.P4; p >= actions.P0; p-- {
			h.add(revoke(group(int(p)), "p"+p.String(), p, time.Minute))
		}
		h.start()
		h.advance(30 * time.Second)
		got := revokedIDs(h.rec.writes())
		want := []wa.MessageID{"pP0", "pP1", "pP2", "pP3", "pP4"}
		if !slices.Equal(got, want) {
			t.Fatalf("execution order %v, want %v (P0 runs first, DESIGN §10.2)", got, want)
		}
	})
}

func TestUrgentDeadlinesFirst(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, actions.DefaultConfig())
		for i := range 4 {
			h.add(revoke(group(i), fmt.Sprintf("fresh%d", i), actions.P1, time.Minute))
		}
		// Default deadline is MessageTime + 48 h: 50 and 30 minutes left.
		h.add(revoke(group(10), "50min", actions.P1, actions.RevokeWindow-50*time.Minute))
		h.add(revoke(group(11), "30min", actions.P1, actions.RevokeWindow-30*time.Minute))
		h.start()
		h.advance(30 * time.Second)
		got := revokedIDs(h.rec.writes())
		if len(got) < 2 || got[0] != "30min" || got[1] != "50min" {
			t.Fatalf("execution order %v, want 30min then 50min first: work about to leave the 48 h delete window moves ahead, earliest deadline first (DESIGN §10.4)", got)
		}
	})
}

func TestDeletesNewestFirstInGroup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, actions.DefaultConfig())
		// Enqueued oldest first, so ID order is the opposite of the wanted order.
		for _, age := range []int{50, 40, 30, 20, 10} {
			h.add(revoke(group(1), fmt.Sprintf("age%d", age), actions.P1, time.Duration(age)*time.Minute))
		}
		h.add(revoke(group(1), "nearly-gone", actions.P1, actions.RevokeWindow-20*time.Minute))
		h.start()
		h.advance(time.Minute)
		got := revokedIDs(h.rec.writes())
		want := []wa.MessageID{"nearly-gone", "age10", "age20", "age30", "age40", "age50"}
		if !slices.Equal(got, want) {
			t.Fatalf("delete order %v, want %v: the urgent one first, then newest first because that's what members see (DESIGN §10.4)", got, want)
		}
	})
}

func TestExpiredActionsNeverRun(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, actions.DefaultConfig())
		for i := range 30 {
			h.add(revoke(group(i), fmt.Sprintf("p0-%d", i), actions.P0, time.Minute))
		}
		late := send(dm(1), "warning", "please stop")
		late.Deadline = time.Now().Add(5 * time.Second)
		lateID := h.add(late)
		h.start()
		h.waitFinal(10*time.Minute, "30 P0 deletes drain in a few minutes")
		if got := h.get(lateID).Status; got != actions.StatusExpired {
			t.Errorf("an action whose deadline passed while it waited is %s, want expired (DESIGN §10.4 expiry)", got)
		}
		if n := len(h.rec.to(fake.MethodSend)); n != 0 {
			t.Errorf("%d sends, want 0: an action past its deadline is never executed (DESIGN §10.4)", n)
		}
	})
}
