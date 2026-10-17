package actions_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/actions"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa/fake"
)

// TestCeilingNeverExceeded is the §18 property test: whatever the mix and timing of the work,
// writes stay under the bucket (burst + ceiling), the per-chat cap and the spacing.
func TestCeilingNeverExceeded(t *testing.T) {
	for _, seed := range []uint64{1, 7, 42} {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cfg := actions.DefaultConfig()
				h := newHarnessOn(t, cfg, actions.NewMemStore(clock.Real{}), botA, seed)
				r := rand.New(rand.NewPCG(seed, 99))
				for g := range 12 {
					h.setGroup(group(g), 5)
				}
				h.start()
				n := 0
				for range 6 {
					for range 50 {
						n++
						key := fmt.Sprintf("w%d", n)
						switch r.IntN(10) {
						case 0:
							h.add(lockdown(group(r.IntN(12)), actions.KindAnnounce, key))
						case 1, 2, 3:
							h.add(send(dm(n), key, "reply "+key))
						default:
							h.add(revoke(group(r.IntN(12)), key, actions.Priority(r.IntN(2)), time.Duration(r.IntN(600))*time.Minute))
						}
					}
					h.advance(time.Duration(r.IntN(240)) * time.Second)
				}
				h.waitFinal(time.Hour, "300 writes fit in about 15 minutes at 20/min")

				writes := h.rec.writes()
				if len(writes) != n {
					t.Fatalf("%d writes for %d actions, want one write each", len(writes), n)
				}
				checkBucket(t, "bot writes", starts(writes), cfg.Burst, float64(cfg.CeilingPerMin),
					"the effective rate never exceeds the ceiling, DESIGN §10.3/§18")
				checkPerChat(t, writes, cfg.GroupPerMin, "per-group buckets, DESIGN §10.3")
				for i := 1; i < len(writes); i++ {
					if gap := writes[i].Start.Sub(writes[i-1].End); gap < cfg.Spacing {
						t.Fatalf("write #%d started %s after the previous one ended, want at least %s (spacing after every write, DESIGN §10.3)", i, gap, cfg.Spacing)
					}
				}
				if got := h.rec.maxInFlight(); got != 1 {
					t.Fatalf("%d calls in flight at once, want 1 (strictly serial, DESIGN §10.1)", got)
				}
				for _, a := range h.store.All() {
					if a.Status != actions.StatusDone {
						t.Fatalf("action #%d %s is %s (%s), want done: nothing is dropped for being queued (DESIGN §10.1)", a.ID, a.Kind, a.Status, a.LastError)
					}
				}
			})
		})
	}
}

func TestBurstSpacingAndJitter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := actions.DefaultConfig()
		h := newHarness(t, cfg)
		for i := range cfg.Burst {
			h.add(revoke(group(i), fmt.Sprintf("b%d", i), actions.P1, time.Minute))
		}
		h.start()
		h.advance(40 * time.Second)
		w := h.rec.writes()
		if len(w) != cfg.Burst {
			t.Fatalf("%d writes after 40 s, want all %d (a burst of 10 goes out in the first seconds of a wave, DESIGN §10.3)", len(w), cfg.Burst)
		}
		if d := h.off(w[0].Start); d > cfg.PollInterval+eps {
			t.Errorf("first write at +%s, want within %s of start", d, cfg.PollInterval)
		}
		gaps := map[time.Duration]bool{}
		for i := 1; i < len(w); i++ {
			gap := w[i].Start.Sub(w[i-1].End)
			if gap < cfg.Spacing || gap > cfg.Spacing+cfg.Jitter+eps {
				t.Errorf("gap before write #%d is %s, want %s plus 0–%s jitter (DESIGN §10.3 spacing)", i, gap, cfg.Spacing, cfg.Jitter)
			}
			gaps[gap] = true
		}
		if len(gaps) < 3 {
			t.Errorf("only %d distinct gaps among %d writes, want random jitter so the pattern isn't machine-regular (DESIGN §10.3)", len(gaps), len(w)-1)
		}
	})
}

func TestPerGroupCap(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := actions.DefaultConfig()
		h := newHarness(t, cfg)
		for i := range 40 {
			h.add(revoke(group(1), fmt.Sprintf("raid%d", i), actions.P1, time.Duration(i)*time.Second))
		}
		for i := range 3 {
			h.add(revoke(group(2), fmt.Sprintf("calm%d", i), actions.P1, time.Minute))
		}
		h.start()
		h.waitFinal(15*time.Minute, "43 deletes drain at 10/min for the raided group")
		w := h.rec.writes()
		checkPerChat(t, w, cfg.GroupPerMin, "a raided group is capped at 10 writes/min so it can't starve others, DESIGN §10.3")
		for _, c := range w {
			if c.Chat == group(2) && h.off(c.Start) > 40*time.Second {
				t.Errorf("calm group's delete went out at +%s, want within 40 s: the raided group's cap must not hold it back (DESIGN §10.3)", h.off(c.Start))
			}
		}
	})
}

func TestRoundRobinFairness(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, actions.DefaultConfig())
		for i := range 60 {
			h.add(revoke(group(1), fmt.Sprintf("raid%d", i), actions.P1, time.Duration(i)*time.Second))
		}
		for g := 2; g <= 4; g++ {
			h.add(revoke(group(g), fmt.Sprintf("single%d", g), actions.P1, time.Minute))
		}
		h.start()
		h.advance(time.Minute)
		ids := revokedIDs(h.rec.writes())
		for g := 2; g <= 4; g++ {
			i := slices.Index(ids, wa.MessageID(fmt.Sprintf("single%d", g)))
			if i < 0 || i >= 6 {
				t.Errorf("group %d's single delete was write #%d (of %d), want among the first 6: round robin within a priority (DESIGN §10.3)", g, i, len(ids))
			}
		}
	})
}

func TestReadsUseSeparateBudget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := actions.DefaultConfig()
		h := newHarness(t, cfg)
		for i := range 30 {
			h.add(revoke(group(i), fmt.Sprintf("r%d", i), actions.P1, time.Minute))
		}
		h.start()
		rctx, rcancel := context.WithCancel(context.Background())
		defer rcancel()
		readsDone := make(chan struct{})
		go func() {
			defer close(readsDone)
			for i := range 30 {
				err := h.exec.Read(rctx, func(ctx context.Context) error {
					_, err := h.rec.UserInfo(ctx, []wa.JID{member(i)})
					return err
				})
				if err != nil {
					return
				}
			}
		}()
		h.advance(100 * time.Second)
		if n := len(h.rec.writes()); n != 30 {
			t.Errorf("%d of 30 writes done after 100 s while reads ran, want all 30: reads have their own budget and never use the write budget (DESIGN §10.3)", n)
		}
		select {
		case <-readsDone:
		case <-time.After(5 * time.Minute):
			t.Fatalf("30 reads not done after 5 more minutes, want them paced at %d/min", cfg.ReadsPerMin)
		}
		reads := h.rec.to(fake.MethodUserInfo)
		if len(reads) != 30 {
			t.Fatalf("%d reads ran, want 30", len(reads))
		}
		checkBucket(t, "reads", starts(reads), cfg.ReadBurst, float64(cfg.ReadsPerMin), "reads are budgeted separately, DESIGN §10.3")
	})
}

func TestNotBeforeHonoured(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, actions.DefaultConfig())
		a := revoke(group(1), "later", actions.P0, time.Minute)
		a.NotBefore = time.Now().Add(30 * time.Second)
		id := h.add(a)
		h.start()
		h.advance(time.Minute)
		w := h.rec.writes()
		if len(w) != 1 || h.off(w[0].Start) < 30*time.Second {
			t.Fatalf("writes %v, want exactly one at or after +30s (not_before, DESIGN §10.1)", w)
		}
		if got := h.get(id).Status; got != actions.StatusDone {
			t.Fatalf("status %s, want done", got)
		}
	})
}

func TestSlowDownOnWarningSigns(t *testing.T) {
	unusual := errors.New("unexpected stanza from server")
	cases := []struct {
		name  string
		steps func(h *harness)
		want  float64
	}{
		{"429 on a write", func(h *harness) {
			h.wa.FailNext(fake.MethodRevoke, wa.ErrRateLimited)
			h.add(revoke(group(1), "m", actions.P1, time.Minute))
			h.advance(h.cfg.RateLimitPause + 10*time.Second)
		}, 5},
		{"unclassified error", func(h *harness) {
			h.wa.FailNext(fake.MethodRevoke, unusual)
			h.add(revoke(group(1), "m", actions.P1, time.Minute))
			h.advance(20 * time.Second)
		}, 5},
		{"slow ack", func(h *harness) {
			h.rec.setLatency(fake.MethodRevoke, h.cfg.SlowAck+time.Second)
			h.add(revoke(group(1), "m", actions.P1, time.Minute))
			h.advance(20 * time.Second)
		}, 5},
		{"disconnect right after a write", func(h *harness) {
			h.add(revoke(group(1), "m", actions.P1, time.Minute))
			h.advance(5 * time.Second)
			h.exec.Warn(context.Background(), actions.WarnDisconnect)
			h.advance(2 * time.Second)
		}, 5},
		{"disconnect long after the last write is not a sign", func(h *harness) {
			h.add(revoke(group(1), "m", actions.P1, time.Minute))
			h.advance(h.cfg.DisconnectWindow + 30*time.Second)
			h.exec.Warn(context.Background(), actions.WarnDisconnect)
			h.advance(2 * time.Second)
		}, 20},
		{"two signs stop at the 2/min floor", func(h *harness) {
			h.wa.FailNext(fake.MethodRevoke, unusual, unusual)
			h.add(revoke(group(1), "m", actions.P1, time.Minute))
			h.advance(30 * time.Second)
		}, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h := newHarness(t, actions.DefaultConfig())
				h.start()
				h.advance(time.Second)
				if s := h.state(); !approxEq(s.Rate, 20) || s.Ceiling != 20 {
					t.Fatalf("initial state %+v, want Rate 20 and Ceiling 20 (the rate starts at the ceiling)", s)
				}
				tc.steps(h)
				s := h.state()
				if !approxEq(s.Rate, tc.want) {
					t.Fatalf("Rate %.3f, want %.3f (a warning sign cuts the rate to a quarter, minimum 2/min, DESIGN §10.3)", s.Rate, tc.want)
				}
				if s.Rate > float64(s.Ceiling) {
					t.Fatalf("Rate %.3f above Ceiling %d, want never above (DESIGN §10.3)", s.Rate, s.Ceiling)
				}
			})
		})
	}
}

func TestRecoveryIsSlowAndCapped(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := actions.DefaultConfig()
		h := newHarness(t, cfg)
		h.wa.FailNext(fake.MethodRevoke, errors.New("unexpected stanza from server"))
		id := h.add(revoke(group(1), "m", actions.P1, time.Minute))
		h.start()
		h.advance(5 * time.Second)
		calls := h.rec.to(fake.MethodRevoke)
		if len(calls) == 0 {
			t.Fatal("no revoke within 5 s")
		}
		warnAt := calls[0].End
		sawStep := false
		for {
			h.advance(30 * time.Second)
			elapsed := time.Since(warnAt)
			rate := h.state().Rate
			bound := math.Min(float64(cfg.CeilingPerMin), 5+math.Floor(float64(elapsed)/float64(cfg.RecoverEvery)))
			if rate > bound+1e-6 {
				t.Fatalf("Rate %.3f at +%s after the warning, want at most %.0f (+1/min per 15 calm minutes, never above the ceiling, DESIGN §10.3)", rate, elapsed, bound)
			}
			if elapsed > cfg.RecoverEvery+5*time.Second && elapsed < 2*cfg.RecoverEvery && rate >= 6-1e-6 {
				sawStep = true
			}
			if elapsed > 6*time.Hour {
				break
			}
		}
		if !sawStep {
			t.Errorf("Rate didn't reach 6 between 15 and 30 calm minutes, want +1/min per RecoverEvery (DESIGN §10.3)")
		}
		if got := h.state().Rate; !approxEq(got, float64(cfg.CeilingPerMin)) {
			t.Errorf("Rate %.3f after 6 calm hours, want back at the ceiling %d and no higher (DESIGN §10.3)", got, cfg.CeilingPerMin)
		}
		if got := h.get(id).Status; got != actions.StatusDone {
			t.Errorf("the action that hit the unusual error is %s, want done after a retry (unclassified errors are transient, DESIGN §10.5)", got)
		}
	})
}

func TestSetCeiling(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		cfg := actions.DefaultConfig()
		h := newHarness(t, cfg)
		h.start()
		h.advance(2 * time.Second)

		if err := h.op.SetCeiling(ctx, botA, "operator", 30); err != nil {
			t.Fatalf("SetCeiling(30): %v", err)
		}
		if !h.audit.has("actions.ceiling") {
			t.Errorf("no actions.ceiling audit entry, want every ceiling change audited (DESIGN §10.3)")
		}
		raisedAt := time.Now()
		h.advance(2 * cfg.PollInterval)
		if s := h.state(); s.Ceiling != 30 || s.Rate > 21+1e-6 {
			t.Fatalf("after raising the ceiling to 30: %+v, want Ceiling 30 and Rate still about 20: the rate doesn't jump (DESIGN §10.3)", s)
		}
		for time.Since(raisedAt) < 3*time.Hour {
			h.advance(time.Minute)
			elapsed := time.Since(raisedAt)
			bound := math.Min(30, 21+math.Floor(float64(elapsed)/float64(cfg.RecoverEvery)))
			if rate := h.state().Rate; rate > bound+1e-6 {
				t.Fatalf("Rate %.3f at +%s after the raise, want at most %.0f: it climbs +1/min per 15 calm minutes (DESIGN §10.3)", rate, elapsed, bound)
			}
		}
		if got := h.state().Rate; !approxEq(got, 30) {
			t.Errorf("Rate %.3f 3 h after raising the ceiling to 30, want 30", got)
		}

		if err := h.op.SetCeiling(ctx, botA, "operator", 10); err != nil {
			t.Fatalf("SetCeiling(10): %v", err)
		}
		h.advance(2 * cfg.PollInterval)
		if s := h.state(); s.Ceiling != 10 || s.Rate > 10+1e-6 {
			t.Fatalf("after cutting the ceiling to 10: %+v, want Ceiling 10 and Rate at most 10 at once (DESIGN §10.3)", s)
		}
		if err := h.op.SetCeiling(ctx, botA, "operator", -1); !errors.Is(err, actions.ErrInvalid) {
			t.Errorf("SetCeiling(-1) = %v, want ErrInvalid", err)
		}
	})
}

func TestDailyWritesAlert(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := actions.DefaultConfig()
		cfg.DailyAlert = 5
		cfg.PollInterval = 10 * time.Second
		h := newHarness(t, cfg)
		h.start()
		for i := range 4 {
			h.add(revoke(group(i), fmt.Sprintf("d1-%d", i), actions.P1, time.Minute))
		}
		h.waitFinal(5*time.Minute, "4 writes")
		h.advance(time.Minute)
		if n := h.alerts.count(actions.AlertDailyWrites); n != 0 {
			t.Fatalf("%d daily alerts after 4 writes, want 0 below the threshold of 5", n)
		}
		for i := 4; i < 12; i++ {
			h.add(revoke(group(i), fmt.Sprintf("d1-%d", i), actions.P1, time.Minute))
		}
		h.waitFinal(5*time.Minute, "an alert never slows anything down (DESIGN §10.3)")
		h.advance(time.Minute)
		if n := h.alerts.count(actions.AlertDailyWrites); n != 1 {
			t.Fatalf("%d daily alerts after 12 writes in a day, want exactly 1 (alert once a day past the threshold, DESIGN §10.3)", n)
		}
		if rate := h.state().Rate; !approxEq(rate, 20) {
			t.Errorf("Rate %.3f after the daily alert, want 20: the alert doesn't slow or stop anything (DESIGN §10.3)", rate)
		}

		now := time.Now().UTC()
		midnight := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)
		h.advance(midnight.Sub(now) + time.Minute)
		for i := range 12 {
			h.add(revoke(group(i), fmt.Sprintf("d2-%d", i), actions.P1, time.Minute))
		}
		h.waitFinal(5*time.Minute, "12 writes on day 2")
		h.advance(time.Minute)
		if n := h.alerts.count(actions.AlertDailyWrites); n != 2 {
			t.Fatalf("%d daily alerts after a second busy UTC day, want 2 (one per day, DESIGN §10.3)", n)
		}
	})
}
