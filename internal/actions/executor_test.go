package actions_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/actions"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa/fake"
)

// TestCeilingPropertyRandomWorkloads checks the pacing invariant over random workloads: in any
// interval of length W the bot makes at most Burst + Ceiling·W writes, each chat at most
// GroupPerMin per minute, and calls never overlap (DESIGN.md §10.1, §10.3).
func TestCeilingPropertyRandomWorkloads(t *testing.T) {
	for seed := range uint64(12) {
		t.Run(fmt.Sprint("seed", seed), func(t *testing.T) {
			within(t, func(t *testing.T) {
				cfg := actions.DefaultConfig()
				rnd := rand.New(rand.NewPCG(seed, 99))
				cfg.CeilingPerMin = 5 + rnd.IntN(30)
				cfg.Burst = 1 + rnd.IntN(15)
				cfg.Spacing = time.Duration(rnd.IntN(1500)) * time.Millisecond
				cfg.Jitter = time.Duration(rnd.IntN(1500)) * time.Millisecond
				cfg.MinRatePerMin = 1
				r := newRig(t, cfg)
				r.groups.open[group(1)] = true
				r.start()
				n := 0
				// Waves of mixed work arrive over 20 minutes.
				for wave := range 6 {
					for range 20 + rnd.IntN(60) {
						n++
						chat := group(rnd.IntN(5))
						switch rnd.IntN(4) {
						case 0:
							r.enqueue(kick(chat, member(n)))
						case 1:
							r.enqueue(send(group(1), fmt.Sprint("s", n), fmt.Sprint("note ", n), actions.PurposeNotice))
						default:
							r.enqueue(revoke(chat, fmt.Sprint("m", n), time.Now(), actions.Priority(rnd.IntN(5))))
						}
					}
					time.Sleep(time.Duration(wave+1) * time.Minute)
				}
				time.Sleep(2 * time.Hour)
				r.stop()

				calls := r.client.writes()
				if r.client.overlap.Load() {
					t.Fatal("two WhatsApp calls were in flight at once, want strictly serial (DESIGN.md §10.1)")
				}
				for _, span := range []time.Duration{10 * time.Second, time.Minute, 5 * time.Minute, 30 * time.Minute} {
					limit := cfg.Burst + int(float64(cfg.CeilingPerMin)*span.Minutes())
					if got := maxInWindow(calls, span); got > limit {
						t.Errorf("%d writes within %s, want at most burst %d + ceiling %d/min = %d (DESIGN.md §10.3)",
							got, span, cfg.Burst, cfg.CeilingPerMin, limit)
					}
				}
				perChat := map[wa.JID][]call{}
				for _, c := range calls {
					perChat[c.chat] = append(perChat[c.chat], c)
				}
				for chat, cs := range perChat {
					if got := maxInWindow(cs, time.Minute); got > cfg.GroupPerMin {
						t.Errorf("%d writes to %s within a minute, want at most %d (per-group bucket)", got, chat, cfg.GroupPerMin)
					}
				}
				for _, a := range r.store.All() {
					if !a.Status.Final() {
						t.Errorf("action %d (%s) still %s after two calm hours, want every action run", a.ID, a.Kind, a.Status)
					}
				}
			})
		})
	}
}

func TestSpacingBetweenWrites(t *testing.T) {
	within(t, func(t *testing.T) {
		cfg := actions.DefaultConfig()
		r := newRig(t, cfg)
		r.start()
		for i := range 8 {
			r.enqueue(revoke(group(i), fmt.Sprint("m", i), time.Now(), actions.P1))
		}
		time.Sleep(time.Minute)
		calls := r.client.writes()
		if len(calls) != 8 {
			t.Fatalf("got %d writes, want 8 (all within the burst)", len(calls))
		}
		for i := 1; i < len(calls); i++ {
			gap := calls[i].at.Sub(calls[i-1].at)
			if gap < cfg.Spacing || gap > cfg.Spacing+cfg.Jitter {
				t.Errorf("gap %d = %s, want between %s and %s (base spacing plus jitter)", i, gap, cfg.Spacing, cfg.Spacing+cfg.Jitter)
			}
		}
	})
}

func TestPriorityAndNewestFirst(t *testing.T) {
	within(t, func(t *testing.T) {
		r := newRig(t, actions.DefaultConfig())
		base := time.Now()
		g := group(1)
		r.enqueue(revoke(g, "old", base.Add(-3*time.Hour), actions.P1))
		r.enqueue(revoke(g, "new", base.Add(-time.Minute), actions.P1))
		r.enqueue(revoke(g, "mid", base.Add(-time.Hour), actions.P1))
		r.enqueue(revoke(g, "bg", base, actions.P4))
		r.enqueue(revoke(g, "scam", base.Add(-2*time.Hour), actions.P0))
		urgent := revoke(g, "urgent", base.Add(-47*time.Hour-30*time.Minute), actions.P1)
		r.enqueue(urgent)
		r.start()
		time.Sleep(time.Minute)
		var got []string
		for _, c := range r.client.writes() {
			got = append(got, c.text)
		}
		want := []string{"scam", "urgent", "new", "mid", "old", "bg"}
		if !slices.Equal(got, want) {
			t.Errorf("delete order %v, want %v (P0 first; then near-deadline; then newest first, DESIGN.md §10.4)", got, want)
		}
	})
}

func TestRoundRobinAcrossGroups(t *testing.T) {
	within(t, func(t *testing.T) {
		r := newRig(t, actions.DefaultConfig())
		for i := range 40 {
			r.enqueue(revoke(group(1), fmt.Sprint("raid", i), time.Now(), actions.P1))
		}
		r.enqueue(revoke(group(2), "quiet", time.Now(), actions.P1))
		r.start()
		time.Sleep(10 * time.Second)
		for i, c := range r.client.writes() {
			if c.chat == group(2) {
				if i > 1 {
					t.Errorf("the quiet group's delete ran as write %d, want within the first two (round robin)", i+1)
				}
				return
			}
		}
		t.Error("the quiet group's delete never ran while another group was raided, want round robin (DESIGN.md §10.3)")
	})
}

func TestDedupeExpiryAndCancel(t *testing.T) {
	within(t, func(t *testing.T) {
		r := newRig(t, actions.DefaultConfig())
		g := group(1)
		first := r.enqueue(revoke(g, "a", time.Now(), actions.P1))
		again := r.enqueue(revoke(g, "a", time.Now(), actions.P1))
		if again.Inserted || again.ID != first.ID {
			t.Errorf("second enqueue = %+v, want not inserted with ID %d (dedupe key)", again, first.ID)
		}
		late := revoke(g, "late", time.Now().Add(-49*time.Hour), actions.P1)
		if res := r.enqueue(late); res.Dropped == "" || res.Inserted {
			t.Errorf("enqueue of a 49 h old delete = %+v, want dropped (past the delete window)", res)
		}
		soon := revoke(g, "soon", time.Now(), actions.P4)
		soon.Deadline = time.Now().Add(2 * time.Second)
		soon.NotBefore = time.Now().Add(5 * time.Second)
		if _, err := r.store.Enqueue(context.Background(), soon); !errors.Is(err, actions.ErrInvalid) {
			t.Errorf("deadline before not_before: err = %v, want ErrInvalid", err)
		}
		soon.NotBefore = time.Time{}
		soon.Priority = actions.P4
		exp := r.enqueue(soon)
		gone := r.enqueue(revoke(g, "self", time.Now(), actions.P1))
		if ok, err := r.store.Cancel(context.Background(), actions.RevokeKey(g, "self"), "sender deleted it"); !ok || err != nil {
			t.Fatalf("Cancel = %v, %v; want true", ok, err)
		}
		// Keep the bot busy so the P4 delete can't run before its deadline.
		for i := range 20 {
			r.enqueue(revoke(group(2+i%3), fmt.Sprint("busy", i), time.Now(), actions.P1))
		}
		r.start()
		time.Sleep(5 * time.Minute)
		if got := r.status(gone.ID).Status; got != actions.StatusCancelled {
			t.Errorf("cancelled delete is %s, want cancelled", got)
		}
		if got := r.status(exp.ID).Status; got != actions.StatusExpired {
			t.Errorf("delete past its deadline is %s, want expired (DESIGN.md §10.4)", got)
		}
		for _, c := range r.client.writes() {
			if c.text == "self" || c.text == "soon" {
				t.Errorf("WhatsApp got a revoke of %q, want none (cancelled or expired)", c.text)
			}
		}
	})
}

func TestKickCoalescing(t *testing.T) {
	within(t, func(t *testing.T) {
		r := newRig(t, actions.DefaultConfig())
		g := group(1)
		info := wa.GroupInfo{JID: g}
		for i := range 11 {
			info.Participants = append(info.Participants, wa.Participant{JID: member(i)})
		}
		r.client.SetGroup(info)
		var ids []int64
		for i := range 10 {
			ids = append(ids, r.enqueue(kick(g, member(i))).ID)
		}
		// The same person banned twice in the same group is sent once.
		dup := kick(g, member(3))
		dup.DedupeKey = actions.KickKey(g, member(3), "ban2")
		ids = append(ids, r.enqueue(dup).ID)
		// Someone who already left.
		ids = append(ids, r.enqueue(kick(g, member(99))).ID)
		r.start()
		time.Sleep(30 * time.Second)
		calls := r.client.writes()
		if len(calls) != 2 {
			t.Fatalf("got %d kick calls, want 2 (11 distinct members, max_kick_batch 8)", len(calls))
		}
		seen := map[wa.JID]int{}
		for _, c := range calls {
			if len(c.jids) > 8 {
				t.Errorf("a call removed %d members, want at most 8", len(c.jids))
			}
			for _, j := range c.jids {
				seen[j]++
			}
		}
		for j, n := range seen {
			if n != 1 {
				t.Errorf("%s sent %d times, want once", j, n)
			}
		}
		if len(seen) != 11 {
			t.Errorf("%d distinct members kicked, want 11 (exactly the enqueued ones)", len(seen))
		}
		for _, id := range ids {
			if a := r.status(id); a.Status != actions.StatusDone {
				t.Errorf("kick %d is %s (%s), want done (someone who left counts as removed)", id, a.Status, a.LastError)
			}
		}
	})
}

func TestKickWaitsForWindow(t *testing.T) {
	within(t, func(t *testing.T) {
		r := newRig(t, actions.DefaultConfig())
		g := group(1)
		r.client.SetGroup(wa.GroupInfo{JID: g, Participants: []wa.Participant{{JID: member(1)}, {JID: member(2)}}})
		r.start()
		r.enqueue(kick(g, member(1)))
		time.Sleep(time.Second)
		r.enqueue(kick(g, member(2)))
		time.Sleep(20 * time.Second)
		if calls := r.client.writes(); len(calls) != 1 || len(calls[0].jids) != 2 {
			t.Errorf("calls = %+v, want one call with both members (2.5 s coalescing window)", calls)
		}
	})
}

func TestErrorHandling(t *testing.T) {
	cases := []struct {
		name        string
		errs        []error
		wantStatus  actions.Status
		wantCalls   int
		wantPaused  bool
		wantBreaker bool
		slowed      bool
	}{
		{"timeout then success", []error{wa.ErrTimeout, wa.ErrTimeout}, actions.StatusDone, 3, false, false, false},
		{"always timing out gives up after 6", repeat(wa.ErrTimeout, 6), actions.StatusFailed, 6, false, false, false},
		{"not found fails fast", []error{wa.ErrNotFound}, actions.StatusFailed, 1, false, false, false},
		{"not admin fails fast", []error{wa.ErrNotAdmin}, actions.StatusFailed, 1, false, false, false},
		{"429 pauses and slows", []error{wa.ErrRateLimited}, actions.StatusDone, 2, true, false, true},
		{"unusual error slows", []error{errors.New("stream error 500")}, actions.StatusDone, 2, false, false, true},
		{"temporary ban opens the breaker", []error{&wa.TemporaryBanError{Code: 101, Expire: time.Hour}}, actions.StatusPending, 1, false, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			within(t, func(t *testing.T) {
				r := newRig(t, actions.DefaultConfig())
				r.client.FailNext(fake.MethodRevoke, tc.errs...)
				id := r.enqueue(revoke(group(1), "m", time.Now(), actions.P1)).ID
				var other int64
				if tc.name == "not admin fails fast" {
					other = r.enqueue(revoke(group(1), "m2", time.Now().Add(-time.Hour), actions.P1)).ID
				}
				r.start()
				time.Sleep(2 * time.Second)
				if tc.wantPaused {
					if n := len(r.client.writes()); n != 1 {
						t.Errorf("%d calls within 2 s of a 429, want 1 (60 s pause)", n)
					}
					time.Sleep(50 * time.Second)
					if n := len(r.client.writes()); n != 1 {
						t.Errorf("%d calls within 52 s of a 429, want 1 (60 s pause)", n)
					}
				}
				time.Sleep(time.Hour)
				a := r.status(id)
				if a.Status != tc.wantStatus {
					t.Errorf("status %s (%s), want %s", a.Status, a.LastError, tc.wantStatus)
				}
				var revokes int
				for _, c := range r.client.writes() {
					if c.text == "m" {
						revokes++
					}
				}
				if revokes != tc.wantCalls {
					t.Errorf("%d revoke calls, want %d", revokes, tc.wantCalls)
				}
				st := r.ex.State()
				if st.BreakerOpen != tc.wantBreaker {
					t.Errorf("breaker open = %v, want %v", st.BreakerOpen, tc.wantBreaker)
				}
				if tc.slowed && st.Rate >= float64(st.Ceiling) {
					// An hour of calm recovers 4 writes/min from the cut to 5.
					t.Errorf("rate %g after a warning sign and an hour of calm, want below the ceiling %d", st.Rate, st.Ceiling)
				}
				if tc.name == "not admin fails fast" {
					if got := r.status(other).Status; got != actions.StatusFailed {
						t.Errorf("other pending delete in the group is %s, want failed without a call (bot not admin)", got)
					}
					if len(r.groups.notAdmin) != 1 {
						t.Errorf("Groups.NotAdmin called %d times, want once", len(r.groups.notAdmin))
					}
				}
				if tc.wantBreaker && !slices.Contains(r.alerts.kinds(), actions.AlertBreakerOpen) {
					t.Errorf("alerts %v, want breaker_open", r.alerts.kinds())
				}
			})
		})
	}
}

func repeat(err error, n int) []error {
	out := make([]error, n)
	for i := range out {
		out[i] = err
	}
	return out
}

func TestSlowDownOnlyAndRecovery(t *testing.T) {
	within(t, func(t *testing.T) {
		cfg := actions.DefaultConfig()
		cfg.BreakerWarnings = 100
		r := newRig(t, cfg)
		r.start()
		ctx := context.Background()
		r.ex.Warn(ctx, actions.WarnSlowAck)
		if got := r.ex.State().Rate; got != 5 {
			t.Fatalf("rate after one warning = %g, want 5 (a quarter of 20)", got)
		}
		r.ex.Warn(ctx, actions.WarnSlowAck)
		r.ex.Warn(ctx, actions.WarnSlowAck)
		if got := r.ex.State().Rate; got != 2 {
			t.Fatalf("rate after three warnings = %g, want 2 (the minimum)", got)
		}
		time.Sleep(14 * time.Minute)
		if got := r.ex.State().Rate; got != 2 {
			t.Errorf("rate after 14 calm minutes = %g, want still 2", got)
		}
		time.Sleep(time.Minute)
		if got := r.ex.State().Rate; got != 3 {
			t.Errorf("rate after 15 calm minutes = %g, want 3 (+1/min per 15 min)", got)
		}
		time.Sleep(24 * time.Hour)
		if got := r.ex.State().Rate; got != 20 {
			t.Errorf("rate after a calm day = %g, want exactly the ceiling 20, never above", got)
		}
		// The disconnect only counts right after a write.
		r.ex.Warn(ctx, actions.WarnDisconnect)
		if got := r.ex.State().Rate; got != 20 {
			t.Errorf("rate after a disconnect with no recent write = %g, want 20", got)
		}
		r.enqueue(revoke(group(1), "m", time.Now(), actions.P1))
		time.Sleep(5 * time.Second)
		r.ex.Warn(ctx, actions.WarnDisconnect)
		if got := r.ex.State().Rate; got != 5 {
			t.Errorf("rate after a disconnect right after a write = %g, want 5", got)
		}
	})
}

func TestCeilingRaiseIsAuditedAndSlow(t *testing.T) {
	within(t, func(t *testing.T) {
		r := newRig(t, actions.DefaultConfig())
		au := &audits{}
		op := actions.Operator{Store: r.store, Audit: au}
		r.start()
		time.Sleep(time.Second)
		if err := op.SetCeiling(context.Background(), bot, "operator", 30); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Second)
		if st := r.ex.State(); st.Ceiling != 30 || st.Rate != 20 {
			t.Errorf("after a raise to 30: %+v, want ceiling 30 and rate still 20 (no jump)", st)
		}
		time.Sleep(15 * time.Minute)
		if got := r.ex.State().Rate; got != 21 {
			t.Errorf("rate 15 min after the raise = %g, want 21", got)
		}
		if len(au.got) != 1 || au.got[0].Action != "actions.ceiling" {
			t.Errorf("audit entries %+v, want one actions.ceiling", au.got)
		}
		if err := (actions.Operator{Store: r.store}).SetCeiling(context.Background(), bot, "x", 99); err == nil {
			t.Error("SetCeiling without an auditor succeeded, want an error (raises must be audited)")
		}
	})
}

func TestBreakerAndKillSwitch(t *testing.T) {
	within(t, func(t *testing.T) {
		r := newRig(t, actions.DefaultConfig())
		au := &audits{}
		op := actions.Operator{Store: r.store, Audit: au}
		g := group(1)
		r.client.SetGroup(wa.GroupInfo{JID: g})
		r.start()
		ctx := context.Background()
		if err := r.ex.OpenBreaker(ctx, "logged out"); err != nil {
			t.Fatal(err)
		}
		del := r.enqueue(revoke(g, "m", time.Now(), actions.P0)).ID
		lock := r.enqueue(actions.Action{Chat: g, Kind: actions.KindAnnounce, Priority: actions.P0, DedupeKey: "lock1", Payload: actions.Payload{On: true}}).ID
		time.Sleep(time.Minute)
		if got := r.status(lock).Status; got != actions.StatusDone {
			t.Errorf("P0 lockdown with the breaker open is %s, want done (lockdowns still run)", got)
		}
		if got := r.status(del).Status; got != actions.StatusPending {
			t.Errorf("delete with the breaker open is %s, want pending (kept for later cleanup)", got)
		}
		if err := op.CloseBreaker(ctx, bot, "operator"); err != nil {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Second)
		if got := r.status(del).Status; got != actions.StatusDone {
			t.Errorf("delete after the breaker closed is %s, want done", got)
		}
		if err := op.Pause(ctx, "operator", "investigating"); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Second)
		lock2 := r.enqueue(actions.Action{Chat: g, Kind: actions.KindAnnounce, Priority: actions.P0, DedupeKey: "lock2", Payload: actions.Payload{On: false}}).ID
		time.Sleep(10 * time.Minute)
		if got := r.status(lock2).Status; got != actions.StatusPending {
			t.Errorf("P0 action under the kill switch is %s, want pending (pause stops everything)", got)
		}
		if err := r.ex.Read(ctx, func(context.Context) error { return nil }); !errors.Is(err, actions.ErrPaused) {
			t.Errorf("Read under the kill switch = %v, want ErrPaused", err)
		}
		if err := op.Resume(ctx, "operator"); err != nil {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Second)
		if got := r.status(lock2).Status; got != actions.StatusDone {
			t.Errorf("P0 action after resume is %s, want done", got)
		}
		if len(au.got) != 3 {
			t.Errorf("%d audit entries, want 3 (close, pause, resume)", len(au.got))
		}
	})
}

func TestRepeatedWarningsOpenBreaker(t *testing.T) {
	within(t, func(t *testing.T) {
		r := newRig(t, actions.DefaultConfig())
		r.start()
		for range 3 {
			r.ex.Warn(context.Background(), actions.WarnUnusual)
			time.Sleep(time.Minute)
		}
		if !r.ex.State().BreakerOpen {
			t.Error("breaker closed after 3 warning signs in 3 minutes, want open (DESIGN.md §10.5)")
		}
		if !slices.Contains(r.alerts.kinds(), actions.AlertBreakerOpen) {
			t.Errorf("alerts %v, want breaker_open", r.alerts.kinds())
		}
	})
}

func TestSilentModeRefusesPosts(t *testing.T) {
	within(t, func(t *testing.T) {
		r := newRig(t, actions.DefaultConfig())
		silent, loud := group(1), group(2)
		r.groups.silent[silent] = true
		var ids []int64
		for i, k := range actions.Kinds {
			if !k.Posts() {
				continue
			}
			ids = append(ids, r.enqueue(actions.Action{
				Chat: silent, Kind: k, Priority: actions.P3, DedupeKey: fmt.Sprint("post", i),
				Payload: actions.Payload{Text: "hi", Target: wa.MessageRef{ID: "x", Sender: member(1)}, Emoji: "👍"},
			}).ID)
		}
		for i := range 3 {
			r.enqueue(send(loud, fmt.Sprint("loud", i), fmt.Sprint("warning ", i), actions.PurposeWarning))
		}
		r.enqueue(send("777@s.whatsapp.net", "dm", "hello", actions.PurposeDM))
		r.start()
		time.Sleep(5 * time.Minute)
		for _, id := range ids {
			if got := r.status(id).Status; got != actions.StatusRefused {
				t.Errorf("post %d in a silent protected group is %s, want refused (DESIGN.md §9.7)", id, got)
			}
		}
		var loudAt []time.Time
		for _, c := range r.client.writes() {
			if c.chat == silent {
				t.Errorf("WhatsApp got %s in the silent group, want nothing posted there", c.method)
			}
			if c.chat == loud {
				loudAt = append(loudAt, c.at)
			}
		}
		if len(loudAt) != 3 {
			t.Fatalf("%d posts in the non-silent group, want 3", len(loudAt))
		}
		for i := 1; i < len(loudAt); i++ {
			if gap := loudAt[i].Sub(loudAt[i-1]); gap < time.Minute {
				t.Errorf("posts in a non-silent protected group %s apart, want at least a minute", gap)
			}
		}
	})
}

func TestSilentWhenGroupStateFails(t *testing.T) {
	within(t, func(t *testing.T) {
		r := newRig(t, actions.DefaultConfig())
		r.groups.postingFn = func(wa.JID) (actions.Posting, error) { return actions.Posting{}, errors.New("db down") }
		id := r.enqueue(send(group(1), "s", "hi", actions.PurposeNotice)).ID
		r.start()
		time.Sleep(time.Minute)
		if got := r.status(id).Status; got != actions.StatusRefused {
			t.Errorf("post with unknown group state is %s, want refused (never risk posting in a silent group)", got)
		}
		if n := len(r.client.writes()); n != 0 {
			t.Errorf("%d WhatsApp calls, want 0", n)
		}
	})
}

func TestSendVariantsNeverRepeatAcrossChats(t *testing.T) {
	within(t, func(t *testing.T) {
		r := newRig(t, actions.DefaultConfig())
		for i := range 6 {
			r.enqueue(send(wa.JID(fmt.Sprintf("%d@s.whatsapp.net", 700+i)), fmt.Sprint("otp", i), "Your code", actions.PurposeOTP, "Code for you", "Here is your code"))
		}
		single := r.enqueue(send("800@s.whatsapp.net", "single", "Your code", actions.PurposeDM)).ID
		r.start()
		time.Sleep(10 * time.Minute)
		// If the single-text send is held behind an identical text, any other text frees it.
		r.enqueue(send("801@s.whatsapp.net", "other", "Welcome", actions.PurposeDM))
		time.Sleep(10 * time.Minute)
		calls := r.client.writes()
		for i := 1; i < len(calls); i++ {
			if calls[i].text == calls[i-1].text && calls[i].chat != calls[i-1].chat {
				t.Errorf("sends %d and %d both %q to different chats, want varied text (DESIGN.md §10.3)", i-1, i, calls[i].text)
			}
		}
		if a := r.status(single); a.Status != actions.StatusDone || a.Result.SentText != "Your code" {
			t.Errorf("single-text send: %s %q, want done once another text went out first", a.Status, a.Result.SentText)
		}
	})
}

func TestReservedSubBudgetsDuringRaid(t *testing.T) {
	within(t, func(t *testing.T) {
		r := newRig(t, actions.DefaultConfig())
		for i := range 200 {
			r.enqueue(revoke(group(i%4), fmt.Sprint("raid", i), time.Now(), actions.P1))
		}
		otp := r.enqueue(send("701@s.whatsapp.net", "otp", "Your code is 1234", actions.PurposeOTP)).ID
		review := r.enqueue(send("702@s.whatsapp.net", "rev", "Review: spam?", actions.PurposeReview)).ID
		notice := r.enqueue(send("703@s.whatsapp.net", "note", "FYI", actions.PurposeNotice)).ID
		r.start()
		time.Sleep(2 * time.Minute)
		for name, id := range map[string]int64{"otp": otp, "review": review} {
			if got := r.status(id).Status; got != actions.StatusDone {
				t.Errorf("%s during a raid is %s after 2 min, want done (reserved P3 sub-budget)", name, got)
			}
		}
		if got := r.status(notice).Status; got != actions.StatusPending {
			t.Errorf("plain notice during a raid is %s, want pending behind P1 deletes", got)
		}
	})
}

func TestReadsHaveTheirOwnBudget(t *testing.T) {
	within(t, func(t *testing.T) {
		cfg := actions.DefaultConfig()
		r := newRig(t, cfg)
		r.start()
		for i := range 30 {
			r.enqueue(revoke(group(i%5), fmt.Sprint("m", i), time.Now(), actions.P1))
		}
		var reads atomic.Int32
		rctx, stopReads := context.WithCancel(context.Background())
		go func() {
			for rctx.Err() == nil {
				if err := r.ex.Read(rctx, func(context.Context) error { return nil }); err == nil {
					reads.Add(1)
				}
			}
		}()
		time.Sleep(time.Minute)
		stopReads()
		synctest.Wait()
		if got := int(reads.Load()); got > cfg.ReadBurst+cfg.ReadsPerMin || got < cfg.ReadsPerMin {
			t.Errorf("%d reads in a minute, want about %d (their own bucket)", got, cfg.ReadBurst+cfg.ReadsPerMin)
		}
		if n := len(r.client.writes()); n < cfg.Burst+cfg.CeilingPerMin-5 {
			t.Errorf("%d writes in a minute alongside reads, want about %d (reads don't use write budget)", n, cfg.Burst+cfg.CeilingPerMin)
		}
		err := r.ex.Read(context.Background(), func(context.Context) error { return wa.ErrRateLimited })
		if !errors.Is(err, wa.ErrRateLimited) {
			t.Fatalf("Read = %v, want the 429", err)
		}
		if st := r.ex.State(); st.PausedUntil.IsZero() || st.Rate >= float64(st.Ceiling) {
			t.Errorf("after a 429 on a read: %+v, want paused and slowed down", st)
		}
	})
}

func TestOneExecutorPerBotAndRecovery(t *testing.T) {
	within(t, func(t *testing.T) {
		r := newRig(t, actions.DefaultConfig())
		ctx := context.Background()
		id := r.enqueue(revoke(group(1), "m", time.Now(), actions.P1)).ID
		// A crash left the action running.
		if _, ok, err := r.store.Claim(ctx, id); !ok || err != nil {
			t.Fatal("claim", ok, err)
		}
		r.start()
		time.Sleep(time.Second)
		second, err := actions.NewExecutor(actions.DefaultConfig(), actions.Deps{Bot: bot, Client: r.client, Store: r.store, Groups: r.groups})
		if err != nil {
			t.Fatal(err)
		}
		if err := second.Run(ctx); !errors.Is(err, actions.ErrExecutorRunning) {
			t.Errorf("second executor Run = %v, want ErrExecutorRunning", err)
		}
		time.Sleep(time.Minute)
		if got := r.status(id).Status; got != actions.StatusDone {
			t.Errorf("action interrupted by a crash is %s, want done after restart", got)
		}
	})
}

func TestDailyWritesAlert(t *testing.T) {
	within(t, func(t *testing.T) {
		cfg := actions.DefaultConfig()
		cfg.DailyAlert = 5
		r := newRig(t, cfg)
		r.start()
		for i := range 8 {
			r.enqueue(revoke(group(i), fmt.Sprint("m", i), time.Now(), actions.P1))
		}
		time.Sleep(time.Minute)
		var n int
		for _, k := range r.alerts.kinds() {
			if k == actions.AlertDailyWrites {
				n++
			}
		}
		if n != 1 {
			t.Errorf("%d daily-writes alerts after 8 writes with a limit of 5, want exactly 1", n)
		}
		if n := len(r.client.writes()); n != 8 {
			t.Errorf("%d writes, want 8 (the alert doesn't slow anything down)", n)
		}
	})
}
