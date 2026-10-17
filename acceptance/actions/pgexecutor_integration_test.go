//go:build integration

package actions_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/actions"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/config"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa/fake"
)

// ---- F-065: serial executor per bot on Postgres ----

func TestPGOneExecutorPerBotAcrossProcesses(t *testing.T) {
	ctx := context.Background()
	pools := pgPools(t, 2)
	s1 := actions.NewPGStore(pools[0], clock.Real{})
	s2 := actions.NewPGStore(pools[1], clock.Real{})

	first := newPGExec(t, pgConfig(), s1, botA, 1).start()
	id := pgEnqueue(t, s1, revoke(group(1), "p1", actions.P1, time.Minute))
	first.exec.Wake()
	waitFor(t, 10*time.Second, func() bool { return pgStatus(t, s1, id) == actions.StatusDone },
		func() string {
			return fmt.Sprintf("first executor's revoke is %s after 10 s, want done", pgStatus(t, s1, id))
		})

	second := newPGExec(t, pgConfig(), s2, botA, 2)
	rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := second.exec.Run(rctx); !errors.Is(err, actions.ErrExecutorRunning) {
		t.Fatalf("Run in a second process for the same bot = %v, want ErrExecutorRunning (exactly one executor per bot across processes, DESIGN §10.1)", err)
	}
	if unlock, err := s2.Lock(ctx, botA); !errors.Is(err, actions.ErrExecutorRunning) {
		if unlock != nil {
			unlock()
		}
		t.Fatalf("Lock(botA) from a second process = %v, want ErrExecutorRunning (the lock holds across processes)", err)
	}
	unlockB, err := s2.Lock(ctx, botB)
	if err != nil {
		t.Fatalf("Lock(botB) from a second process = %v, want nil (each bot has its own executor, DESIGN §10.1)", err)
	}
	unlockB()

	first.stop()
	third := newPGExec(t, pgConfig(), s2, botA, 3).start()
	id2 := pgEnqueue(t, s2, revoke(group(2), "p2", actions.P1, time.Minute))
	third.exec.Wake()
	waitFor(t, 10*time.Second, func() bool { third.checkRunning(); return pgStatus(t, s2, id2) == actions.StatusDone },
		func() string {
			return fmt.Sprintf("after the first executor stopped, a new one's revoke is %s after 10 s, want done (stopping releases the bot)", pgStatus(t, s2, id2))
		})
}

func TestPGCrashRecoveryRerunsRunningActions(t *testing.T) {
	ctx := context.Background()
	pools := pgPools(t, 3)
	dead := actions.NewPGStore(pools[0], clock.Real{}) // the process that "died"

	crashed := pgEnqueue(t, dead, revoke(group(1), "crashed", actions.P1, time.Minute))
	finished := pgEnqueue(t, dead, revoke(group(2), "finished", actions.P1, time.Minute))
	waiting := pgEnqueue(t, dead, revoke(group(3), "waiting", actions.P1, time.Minute))
	if _, ok, err := dead.Claim(ctx, crashed); !ok || err != nil {
		t.Fatalf("Claim(crashed) = %v, %v; want claimed", ok, err)
	}
	if _, ok, err := dead.Claim(ctx, finished); !ok || err != nil {
		t.Fatalf("Claim(finished) = %v, %v; want claimed", ok, err)
	}
	if err := dead.Finish(ctx, finished, actions.StatusDone, "", actions.Result{}); err != nil {
		t.Fatalf("Finish(finished): %v", err)
	}

	s := actions.NewPGStore(pools[1], clock.Real{})
	e := newPGExec(t, pgConfig(), s, botA, 1).start()
	waitFor(t, 10*time.Second, func() bool {
		e.checkRunning()
		return pgStatus(t, s, crashed) == actions.StatusDone && pgStatus(t, s, waiting) == actions.StatusDone
	}, func() string {
		return fmt.Sprintf("after restart: crashed is %s, waiting is %s, want both done (an action left running by a crash runs again; the table is the source of truth, DESIGN §10.1)",
			pgStatus(t, s, crashed), pgStatus(t, s, waiting))
	})
	time.Sleep(500 * time.Millisecond)
	e.stop()

	// A second restart repeats nothing.
	again := newPGExec(t, pgConfig(), actions.NewPGStore(pools[2], clock.Real{}), botA, 2).start()
	time.Sleep(time.Second)
	again.stop()

	got := append(revokedIDs(e.rec.to(fake.MethodRevoke)), revokedIDs(again.rec.to(fake.MethodRevoke))...)
	slices.Sort(got)
	if want := []wa.MessageID{"crashed", "waiting"}; !slices.Equal(got, want) {
		t.Fatalf("revokes over two restarts = %v, want %v once each (a finished action is never repeated; a crashed one runs again, DESIGN §10.1)", got, want)
	}
}

func TestPGExecutorPriorityOrderAndPacing(t *testing.T) {
	pool := pgPools(t, 1)[0]
	s := actions.NewPGStore(pool, clock.Real{})
	cfg := actions.ConfigFromPacing(config.Pacing{
		CeilingPerMin: 120, Burst: 2, SpacingBase: 20 * time.Millisecond, SpacingJitter: 20 * time.Millisecond,
		GroupCeilingPerMin: 10, SlowdownFactor: 0.25, MinRatePerMin: 2, RecoveryStepPerMin: 1,
		RecoveryInterval: 15 * time.Minute, MaxKickBatch: 8, DailyAlertWrites: 1000,
	})
	cfg.PollInterval = 100 * time.Millisecond

	prios := []actions.Priority{actions.P4, actions.P3, actions.P2, actions.P1, actions.P0, actions.P3, actions.P1, actions.P0}
	want := map[wa.MessageID]actions.Priority{}
	for i, p := range prios {
		id := wa.MessageID(fmt.Sprintf("m%d", i))
		want[id] = p
		pgEnqueue(t, s, revoke(group(i), string(id), p, time.Minute)) // one chat each: only priority orders them
	}
	e := newPGExec(t, cfg, s, botA, 1).start()
	waitFor(t, 20*time.Second, func() bool { e.checkRunning(); return len(e.rec.writes()) >= len(prios) },
		func() string {
			return fmt.Sprintf("%d of %d writes after 20 s, want all (8 writes at 120/min with a burst of 2 take about 3 s)", len(e.rec.writes()), len(prios))
		})
	time.Sleep(300 * time.Millisecond)
	writes := e.rec.writes()
	if len(writes) != len(prios) {
		t.Fatalf("%d writes, want %d (each action runs once)", len(writes), len(prios))
	}
	if got := e.exec.State().Ceiling; got != 120 {
		t.Errorf("State().Ceiling = %d, want 120 (ConfigFromPacing carries pacing.ceiling_per_min, DESIGN §10.3)", got)
	}

	var order []actions.Priority
	for _, id := range revokedIDs(writes) {
		order = append(order, want[id])
	}
	if !slices.IsSorted(order) {
		t.Errorf("priorities in call order = %v, want P0 first and non-decreasing (pulled by priority, not_before, id, DESIGN §10.1–§10.2)", order)
	}

	// Ceiling: at most Burst + rate·(b−a) writes in any interval [a, b]; 50 ms slack for timestamps.
	ts := starts(writes)
	for i := range ts {
		for j := i; j < len(ts); j++ {
			allowed := float64(cfg.Burst) + float64(cfg.CeilingPerMin)*(ts[j].Sub(ts[i])+50*time.Millisecond).Minutes()
			if n := j - i + 1; float64(n) > allowed {
				t.Fatalf("%d writes in %s, want at most %.2f (burst %d + %d/min): the executor must pace under the ceiling on Postgres too (DESIGN §10.3)",
					n, ts[j].Sub(ts[i]), allowed, cfg.Burst, cfg.CeilingPerMin)
			}
		}
	}
	if got := e.rec.maxInFlight(); got != 1 {
		t.Errorf("%d calls in flight at once, want 1 (strictly serial per bot, DESIGN §10.1)", got)
	}
}

func TestPGKicksCoalesce(t *testing.T) {
	pool := pgPools(t, 1)[0]
	s := actions.NewPGStore(pool, clock.Real{})
	g := group(7)
	e := newPGExec(t, pgConfig(), s, botA, 1)
	ps := []wa.Participant{{JID: "999999@lid", IsAdmin: true, IsSuperAdmin: true}}
	for i := 1; i <= 5; i++ {
		ps = append(ps, wa.Participant{JID: member(i)})
	}
	e.wa.SetGroup(wa.GroupInfo{JID: g, Name: "g", Participants: ps, Size: len(ps)})

	ids := map[int64]wa.JID{}
	for i := 1; i <= 3; i++ {
		ids[pgEnqueue(t, s, kick(g, "ban", member(i)))] = member(i)
	}
	e.start()
	waitFor(t, 10*time.Second, func() bool {
		e.checkRunning()
		for id := range ids {
			if pgStatus(t, s, id) != actions.StatusDone {
				return false
			}
		}
		return true
	}, func() string { return "kicks not all done after 10 s, want done (3 kicks in one group are one call)" })

	calls := e.rec.to(fake.MethodUpdateParticipants)
	if len(calls) != 1 {
		t.Fatalf("%d UpdateParticipants calls, want 1 (kicks of one group within KickWindow coalesce, DESIGN §10.4)", len(calls))
	}
	got := slices.Clone(calls[0].Args[1].([]wa.JID))
	slices.Sort(got)
	if want := []wa.JID{member(1), member(2), member(3)}; !slices.Equal(got, want) {
		t.Fatalf("coalesced kick removed %v, want %v (every enqueued JID once, nothing else)", got, want)
	}
	for id, jid := range ids {
		a, err := s.Get(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if len(a.Result.Participants) != 1 || a.Result.Participants[0].JID != jid || a.Result.Participants[0].Error != "" {
			t.Errorf("action %d result = %+v, want one successful outcome for %s (each action keeps its own outcome in the table)", id, a.Result.Participants, jid)
		}
	}
}

func TestPGKillSwitchAcrossStores(t *testing.T) {
	ctx := context.Background()
	pools := pgPools(t, 2)
	execStore := actions.NewPGStore(pools[0], clock.Real{})
	opStore := actions.NewPGStore(pools[1], clock.Real{}) // the API process
	op := actions.Operator{Store: opStore, Clock: clock.Real{}}

	cfg := pgConfig()
	cfg.Burst = 1
	cfg.Spacing = 50 * time.Millisecond
	cfg.Jitter = 0
	const n = 30
	var ids []int64
	for i := range n {
		ids = append(ids, pgEnqueue(t, opStore, revoke(group(i), fmt.Sprintf("k%d", i), actions.P1, time.Minute)))
	}
	e := newPGExec(t, cfg, execStore, botA, 1).start()
	waitFor(t, 10*time.Second, func() bool { e.checkRunning(); return len(e.rec.writes()) >= 3 },
		func() string {
			return fmt.Sprintf("%d writes after 10 s, want at least 3 before pausing", len(e.rec.writes()))
		})

	if err := op.Pause(ctx, "operator", "incident"); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	paused := time.Now()
	time.Sleep(time.Second)
	limit := paused.Add(cfg.PollInterval + 250*time.Millisecond)
	for _, c := range e.rec.writes() {
		if c.Start.After(limit) {
			t.Fatalf("a write started %s after Pause on another store, want none after PollInterval %s (the kill switch stops every bot, DESIGN §10.5)",
				c.Start.Sub(paused), cfg.PollInterval)
		}
	}
	if !e.exec.State().Paused {
		t.Errorf("State().Paused = false while the kill switch is on, want true (DESIGN §10.5)")
	}
	before := len(e.rec.writes())
	if before >= n {
		t.Fatalf("all %d actions ran despite the pause, want the rest held back", n)
	}

	if err := op.Resume(ctx, "operator"); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	waitFor(t, 15*time.Second, func() bool {
		e.checkRunning()
		for _, id := range ids {
			if pgStatus(t, execStore, id) != actions.StatusDone {
				return false
			}
		}
		return true
	}, func() string {
		return fmt.Sprintf("after Resume, %d of %d writes done after 15 s, want all (pending work survives a pause, DESIGN §10.5)", len(e.rec.writes()), n)
	})

	for _, action := range []string{"actions.pause", "actions.resume"} {
		if got := countRows(t, pools[0], "SELECT count(*) FROM app.audit_log WHERE action = $1 AND actor = 'operator'", action); got != 1 {
			t.Errorf("%d %s rows in audit_log, want 1 (every kill-switch change is audited, DESIGN §10.5, §14)", got, action)
		}
	}
}

func TestPGSetCeilingPersistsAndIsAudited(t *testing.T) {
	ctx := context.Background()
	pools := pgPools(t, 3)
	execStore := actions.NewPGStore(pools[0], clock.Real{})
	op := actions.Operator{Store: actions.NewPGStore(pools[1], clock.Real{})} // no Auditor: the store audits

	e := newPGExec(t, pgConfig(), execStore, botA, 1).start()
	if err := op.SetCeiling(ctx, botA, "operator", 5); err != nil {
		t.Fatalf("SetCeiling: %v", err)
	}
	waitFor(t, 3*time.Second, func() bool { e.checkRunning(); st := e.exec.State(); return st.Ceiling == 5 && st.Rate <= 5 },
		func() string {
			return fmt.Sprintf("executor on another store has State %+v 3 s after SetCeiling(5), want Ceiling 5 and Rate ≤ 5 (a cut applies within PollInterval and caps the rate, DESIGN §10.3)", e.exec.State())
		})

	c, err := actions.NewPGStore(pools[2], clock.Real{}).Control(ctx, string(botA))
	if err != nil {
		t.Fatalf("Control: %v", err)
	}
	if c.Ceiling != 5 {
		t.Errorf("persisted Ceiling = %d, want 5 (the ceiling override lives in the table, so every process sees it)", c.Ceiling)
	}
	if got := countRows(t, pools[0], "SELECT count(*) FROM app.audit_log WHERE action = 'actions.ceiling' AND actor = 'operator'"); got != 1 {
		t.Errorf("%d actions.ceiling rows in audit_log, want 1 (a ceiling change is audited in the same transaction, DESIGN §10.3, §14)", got)
	}
}
