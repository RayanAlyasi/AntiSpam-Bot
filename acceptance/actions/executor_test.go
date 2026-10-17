package actions_test

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/actions"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa/fake"
)

func TestNewExecutorRejectsBadSetup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, actions.DefaultConfig())
		zeroCeiling := actions.DefaultConfig()
		zeroCeiling.CeilingPerMin = 0
		if _, err := actions.NewExecutor(zeroCeiling, h.deps(1)); err == nil {
			t.Errorf("NewExecutor with CeilingPerMin 0 returned nil error, want the config rejected (pacing must be safe, DESIGN §10.3)")
		}
		noGroups := h.deps(1)
		noGroups.Groups = nil
		if _, err := actions.NewExecutor(actions.DefaultConfig(), noGroups); err == nil {
			t.Errorf("NewExecutor without Groups returned nil error, want an error (silent mode can't be enforced without it, DESIGN §9.7)")
		}
	})
}

func TestOneExecutorPerBot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, actions.DefaultConfig())
		h.start()
		h.advance(time.Second)

		second, err := actions.NewExecutor(actions.DefaultConfig(), h.deps(2))
		if err != nil {
			t.Fatalf("NewExecutor: %v", err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := second.Run(ctx); !errors.Is(err, actions.ErrExecutorRunning) {
			t.Fatalf("second Run for the same bot = %v, want ErrExecutorRunning (exactly one serial executor per bot, DESIGN §10.1)", err)
		}

		// Another bot on the same store is independent.
		other := newHarnessOn(t, actions.DefaultConfig(), h.store, botB, 3)
		other.start()
		id := other.add(withBot(revoke(group(1), "b1", actions.P1, time.Minute), botB))
		other.advance(10 * time.Second)
		if got := other.get(id).Status; got != actions.StatusDone {
			t.Fatalf("bot B's revoke is %s, want done (each bot has its own executor, DESIGN §10.1)", got)
		}
		if n := len(h.rec.to(fake.MethodRevoke)); n != 0 {
			t.Fatalf("bot A's client got %d revokes of bot B's action, want 0 (an action runs on its own bot)", n)
		}
	})
}

func TestExecutorIsStrictlySerial(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, actions.DefaultConfig())
		h.rec.setLatency("*", 700*time.Millisecond)
		h.setGroup(group(9), 10)
		for i := range 12 {
			h.add(revoke(group(i%4), "s"+string(rune('a'+i)), actions.P1, time.Duration(i)*time.Minute))
			h.add(send(dm(i), "s"+string(rune('a'+i)), "reply "+string(rune('a'+i))))
		}
		for i := 1; i <= 6; i++ {
			h.add(kick(group(9), "ban", member(i)))
		}
		h.add(lockdown(group(9), actions.KindAnnounce, "raid"))
		h.start()
		h.waitFinal(10*time.Minute, "30 writes fit in a few minutes at 20/min")
		if got := h.rec.maxInFlight(); got != 1 {
			t.Fatalf("%d WhatsApp calls were in flight at once, want 1 (the executor is strictly serial per bot, DESIGN §10.1)", got)
		}
		for _, a := range h.store.All() {
			if a.Status != actions.StatusDone {
				t.Errorf("action #%d %s is %s (%s), want done", a.ID, a.Kind, a.Status, a.LastError)
			}
		}
	})
}

func TestCrashRecoveryRerunsRunningActions(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		s := actions.NewMemStore(clock.Real{})
		res, err := s.Enqueue(ctx, revoke(group(1), "crashed", actions.P1, time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if _, ok, err := s.Claim(ctx, res.ID); !ok || err != nil { // a previous process died mid-action
			t.Fatalf("Claim = %v, %v", ok, err)
		}
		h := newHarnessOn(t, actions.DefaultConfig(), s, botA, 1)
		h.start()
		h.advance(10 * time.Second)
		if got := h.get(res.ID).Status; got != actions.StatusDone {
			t.Fatalf("action left running by a crash is %s after restart, want done (the table is the source of truth; at-least-once, DESIGN §10.1)", got)
		}
		if got := revokedIDs(h.rec.to(fake.MethodRevoke)); len(got) != 1 || got[0] != wa.MessageID("crashed") {
			t.Fatalf("revokes after restart = %v, want exactly [crashed]", got)
		}
	})
}
