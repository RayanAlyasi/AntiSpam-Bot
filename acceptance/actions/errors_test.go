package actions_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/actions"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa/fake"
)

// retrySlack is what may pass between a failure and the retry beyond the backoff itself: the
// pause after a write and one poll.
func retrySlack(cfg actions.Config) time.Duration {
	return cfg.Spacing + cfg.Jitter + cfg.PollInterval + eps
}

func TestTransientErrorBackoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := actions.DefaultConfig()
		h := newHarness(t, cfg)
		h.wa.FailNext(fake.MethodRevoke, wa.ErrTimeout, wa.ErrTimeout, wa.ErrTimeout)
		id := h.add(revoke(group(1), "m", actions.P1, time.Minute))
		h.start()
		h.waitFinal(5*time.Minute, "three short backoffs")

		calls := h.rec.to(fake.MethodRevoke)
		if len(calls) != 4 {
			t.Fatalf("%d revoke calls, want 4: three timeouts are retried, then it succeeds (DESIGN §10.5)", len(calls))
		}
		for k := 1; k < len(calls); k++ {
			backoff := min(cfg.BackoffBase<<(k-1), cfg.BackoffMax)
			if gap := calls[k].Start.Sub(calls[k-1].End); gap > backoff+retrySlack(cfg) {
				t.Errorf("retry %d came %s after the failure, want at most %s backoff (full jitter, min(2^n·2 s, 5 min), DESIGN §10.5) plus pacing", k, gap, backoff)
			}
		}
		a := h.get(id)
		if a.Status != actions.StatusDone || a.Attempts != 4 {
			t.Fatalf("action is %s after %d attempts, want done after 4", a.Status, a.Attempts)
		}
	})
}

func TestTransientErrorGivesUp(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := actions.DefaultConfig()
		h := newHarness(t, cfg)
		h.wa.FailAlways(fake.MethodRevoke, wa.ErrTimeout)
		id := h.add(revoke(group(1), "m", actions.P1, time.Minute))
		h.start()
		h.advance(30 * time.Minute)

		calls := h.rec.to(fake.MethodRevoke)
		if len(calls) != cfg.MaxAttempts {
			t.Fatalf("%d revoke calls, want exactly %d: max 6 attempts (DESIGN §10.5)", len(calls), cfg.MaxAttempts)
		}
		var maxSpan time.Duration
		for k := 1; k < cfg.MaxAttempts; k++ {
			maxSpan += min(cfg.BackoffBase<<(k-1), cfg.BackoffMax) + retrySlack(cfg)
		}
		if span := calls[len(calls)-1].Start.Sub(calls[0].End); span > maxSpan {
			t.Errorf("retries spread over %s, want at most %s with min(2^n·2 s, 5 min) backoff (DESIGN §10.5)", span, maxSpan)
		}
		if a := h.get(id); a.Status != actions.StatusFailed || a.LastError == "" {
			t.Fatalf("action is %s with error %q, want failed with the error recorded", a.Status, a.LastError)
		}
	})
}

func TestRateLimitPausesThenSlowsDown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := actions.DefaultConfig()
		h := newHarness(t, cfg)
		h.wa.FailNext(fake.MethodRevoke, wa.ErrRateLimited)
		for i := range 60 {
			h.add(revoke(group(i%20), fmt.Sprintf("m%d", i), actions.P1, time.Duration(i)*time.Second))
		}
		h.start()
		h.advance(5 * time.Second)
		first := h.rec.all()
		if len(first) == 0 || !errors.Is(first[0].Err, wa.ErrRateLimited) {
			t.Fatalf("first call %+v, want the injected 429", first)
		}
		hitAt := first[0].End
		h.advance(cfg.RateLimitPause / 2)
		if s := h.state(); s.PausedUntil.Before(hitAt.Add(cfg.RateLimitPause)) {
			t.Errorf("PausedUntil %v during the pause, want at least 429 time + %s (DESIGN §10.5)", s.PausedUntil, cfg.RateLimitPause)
		}
		h.advance(cfg.RateLimitPause + 10*time.Minute)

		var after []call
		for _, c := range h.rec.all()[1:] {
			if c.Start.Before(hitAt.Add(cfg.RateLimitPause)) {
				t.Fatalf("%s call at %s after the 429, want no WhatsApp call for %s (DESIGN §10.5)", c.Method, c.Start.Sub(hitAt), cfg.RateLimitPause)
			}
			if c.Start.Before(hitAt.Add(cfg.RateLimitPause + 10*time.Minute)) {
				after = append(after, c)
			}
		}
		if len(after) == 0 {
			t.Fatalf("no writes in the 10 minutes after the pause, want the queue to resume")
		}
		checkBucket(t, "writes after the 429 pause", starts(after), cfg.Burst, float64(cfg.CeilingPerMin)/4,
			"after a 429 the rate drops to a quarter, DESIGN §10.3/§10.5")
		h.waitFinal(40*time.Minute, "the backlog drains slowly")
		for _, a := range h.store.All() {
			if a.Status != actions.StatusDone {
				t.Fatalf("action #%d is %s (%s), want done: a 429 delays work but never drops it (DESIGN §10.1)", a.ID, a.Status, a.LastError)
			}
		}
	})
}

func TestPermanentErrorsFailFast(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		othersFail bool // the group's other admin-only actions fail without a call
	}{
		{"not admin", wa.ErrNotAdmin, true},
		{"message gone", wa.ErrNotFound, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h := newHarness(t, actions.DefaultConfig())
				g, other := group(1), group(2)
				h.setGroup(g, 5)
				h.wa.FailNext(fake.MethodRevoke, tc.err)
				firstID := h.add(revoke(g, "first", actions.P0, time.Minute))
				secondID := h.add(revoke(g, "second", actions.P1, time.Minute))
				kickID := h.add(kick(g, "ban", member(1)))
				otherID := h.add(revoke(other, "elsewhere", actions.P1, time.Minute))
				dmID := h.add(send(dm(1), "dm", "your review"))
				h.start()
				h.waitFinal(5*time.Minute, "a handful of actions")

				if a := h.get(firstID); a.Status != actions.StatusFailed || a.Attempts != 1 {
					t.Errorf("action hitting %v is %s after %d attempts, want failed after 1: permanent errors fail fast (DESIGN §10.5)", tc.err, a.Status, a.Attempts)
				}
				if n := slices.Index(revokedIDs(h.rec.all()), "first"); n >= 0 {
					t.Errorf("the failed revoke shows as a successful call")
				}
				if n := len(h.rec.to(fake.MethodRevoke)) - len(revokedIDs(h.rec.all())); n != 1 {
					t.Errorf("%d failed revoke calls, want exactly 1 (no retry of a permanent error, DESIGN §10.5)", n)
				}
				for _, id := range []int64{otherID, dmID} {
					if got := h.get(id).Status; got != actions.StatusDone {
						t.Errorf("unrelated action #%d is %s, want done", id, got)
					}
				}
				notAdmin := h.groups.notAdminCalls()
				gotKick := len(h.rec.to(fake.MethodUpdateParticipants))
				if tc.othersFail {
					if !slices.Contains(notAdmin, g) {
						t.Errorf("Groups.NotAdmin calls %v, want %s recorded so the dashboard shows it (DESIGN §10.5)", notAdmin, g)
					}
					for _, id := range []int64{secondID, kickID} {
						if got := h.get(id).Status; got != actions.StatusFailed {
							t.Errorf("admin-only action #%d in the same group is %s, want failed without calling WhatsApp (DESIGN §10.5)", id, got)
						}
					}
					if slices.Contains(revokedIDs(h.rec.all()), "second") || gotKick != 0 {
						t.Errorf("WhatsApp was called for admin-only actions in a group where the bot isn't admin, want none")
					}
				} else {
					if len(notAdmin) != 0 {
						t.Errorf("Groups.NotAdmin called %v for a missing message, want no admin change", notAdmin)
					}
					for _, id := range []int64{secondID, kickID} {
						if got := h.get(id).Status; got != actions.StatusDone {
							t.Errorf("action #%d in the group is %s, want done: a gone message affects only its own delete", id, got)
						}
					}
				}
			})
		})
	}
}

func TestBreakerOpens(t *testing.T) {
	unusual := errors.New("unexpected stanza from server")
	cases := []struct {
		name string
		open func(h *harness)
	}{
		{"temporary ban", func(h *harness) {
			h.wa.FailNext(fake.MethodRevoke, wa.ErrTemporaryBan)
			h.add(revoke(group(50), "trigger", actions.P1, time.Minute))
		}},
		{"temporary ban with details", func(h *harness) {
			h.wa.FailNext(fake.MethodRevoke, &wa.TemporaryBanError{Code: 101, Expire: 24 * time.Hour})
			h.add(revoke(group(50), "trigger", actions.P1, time.Minute))
		}},
		{"connector reports a logout", func(h *harness) {
			if err := h.exec.OpenBreaker(context.Background(), "logged out"); err != nil {
				h.t.Fatalf("OpenBreaker: %v", err)
			}
		}},
		{"three warning signs in ten minutes", func(h *harness) {
			h.wa.FailNext(fake.MethodRevoke, unusual, unusual, unusual)
			h.add(revoke(group(50), "trigger", actions.P1, time.Minute))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx := context.Background()
				h := newHarness(t, actions.DefaultConfig())
				h.start()
				h.advance(time.Second)
				tc.open(h)
				for i := 0; !h.state().BreakerOpen; i++ {
					if i > 120 {
						t.Fatalf("breaker still closed 2 minutes after %s, want open (DESIGN §10.5)", tc.name)
					}
					h.advance(time.Second)
				}
				if n := h.alerts.count(actions.AlertBreakerOpen); n == 0 {
					t.Errorf("no breaker_open alert, want the operator alerted (DESIGN §10.5)")
				}
				revokesBefore := len(h.rec.to(fake.MethodRevoke))

				lock, g2 := group(1), group(2)
				h.setGroup(lock, 5)
				h.setGroup(g2, 5)
				lockIDs := []int64{
					h.add(lockdown(lock, actions.KindAnnounce, "raid")),
					h.add(lockdown(lock, actions.KindJoinApproval, "raid")),
					h.add(lockdown(lock, actions.KindResetInvite, "raid")),
				}
				p0send := send(dm(2), "p0-notice", "urgent")
				p0send.Priority = actions.P0
				heldIDs := []int64{
					h.add(revoke(g2, "p0-delete", actions.P0, time.Minute)),
					h.add(revoke(g2, "p1-delete", actions.P1, time.Minute)),
					h.add(kick(g2, "ban", member(1))),
					h.add(send(dm(1), "review", "review item")),
					h.add(p0send),
				}
				h.advance(5 * time.Minute)

				for _, m := range []string{fake.MethodSetAnnounce, fake.MethodSetJoinApproval, fake.MethodResetInviteLink} {
					if n := len(h.rec.to(m)); n != 1 {
						t.Errorf("%d %s calls while the breaker is open, want 1: P0 lockdowns still run (DESIGN §10.5)", n, m)
					}
				}
				for _, id := range lockIDs {
					if got := h.get(id).Status; got != actions.StatusDone {
						t.Errorf("lockdown #%d is %s, want done", id, got)
					}
				}
				if n := len(h.rec.to(fake.MethodRevoke)) - revokesBefore; n != 0 {
					t.Errorf("%d revokes while the breaker is open, want 0: all writes stop except P0 lockdowns (DESIGN §10.5)", n)
				}
				if n := len(h.rec.to(fake.MethodUpdateParticipants)) + len(h.rec.to(fake.MethodSend)); n != 0 {
					t.Errorf("%d kicks or sends while the breaker is open, want 0 (DESIGN §10.5)", n)
				}
				for _, id := range heldIDs {
					if got := h.get(id).Status; got != actions.StatusPending {
						t.Errorf("held action #%d is %s, want still pending: work waits for the breaker, it isn't dropped (DESIGN §10.1/§10.5)", id, got)
					}
				}
				if !h.state().BreakerOpen {
					t.Fatalf("breaker closed by itself, want it open until the operator closes it (DESIGN §10.5)")
				}
				called := false
				if err := h.exec.Read(ctx, func(context.Context) error { called = true; return nil }); !errors.Is(err, actions.ErrBreakerOpen) || called {
					t.Errorf("Read with the breaker open = %v (fn called: %v), want ErrBreakerOpen without calling WhatsApp", err, called)
				}

				if err := h.op.CloseBreaker(ctx, botA, "operator"); err != nil {
					t.Fatalf("CloseBreaker: %v", err)
				}
				h.waitFinal(30*time.Minute, "held work resumes after the operator closes the breaker")
				for _, id := range heldIDs {
					if got := h.get(id).Status; got != actions.StatusDone {
						t.Errorf("held action #%d is %s after the breaker closed, want done", id, got)
					}
				}
				if h.state().BreakerOpen {
					t.Errorf("State().BreakerOpen still true after CloseBreaker")
				}
			})
		})
	}
}

func TestKillSwitch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		cfg := actions.DefaultConfig()
		store := actions.NewMemStore(clock.Real{})
		hs := []*harness{newHarnessOn(t, cfg, store, botA, 1), newHarnessOn(t, cfg, store, botB, 2)}
		for _, h := range hs {
			for i := range 40 {
				h.add(withBot(revoke(group(i), fmt.Sprintf("%s-%d", h.bot, i), actions.P1, time.Minute), h.bot))
			}
			h.start()
		}
		hs[0].advance(5 * time.Second)
		pausedAt := time.Now()
		if err := hs[0].op.Pause(ctx, "operator", "incident"); err != nil {
			t.Fatalf("Pause: %v", err)
		}
		hs[0].advance(2 * time.Minute)
		for _, h := range hs {
			h.setGroup(group(99), 3)
			h.add(withBot(lockdown(group(99), actions.KindAnnounce, string(h.bot)), h.bot))
		}
		hs[0].advance(2 * time.Minute)

		for _, h := range hs {
			for _, c := range h.rec.all() {
				if c.Start.After(pausedAt.Add(cfg.PollInterval + 10*time.Millisecond)) {
					t.Fatalf("bot %s made a %s call %s after the global pause, want none after %s, not even P0 (kill switch, DESIGN §10.5)",
						h.bot, c.Method, c.Start.Sub(pausedAt), cfg.PollInterval)
				}
			}
			if !h.state().Paused {
				t.Errorf("bot %s: State().Paused false during the global pause", h.bot)
			}
			called := false
			if err := h.exec.Read(ctx, func(context.Context) error { called = true; return nil }); !errors.Is(err, actions.ErrPaused) || called {
				t.Errorf("bot %s: Read while paused = %v (fn called: %v), want ErrPaused without calling WhatsApp", h.bot, err, called)
			}
		}
		if !hs[0].audit.has("actions.pause") {
			t.Errorf("no actions.pause audit entry, want the kill switch audited")
		}

		if err := hs[0].op.Resume(ctx, "operator"); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		hs[0].waitFinal(15*time.Minute, "both bots resume after the kill switch is off")
		for _, a := range store.All() {
			if a.Status != actions.StatusDone {
				t.Errorf("action #%d of %s is %s after Resume, want done: pausing delays work, it doesn't drop it", a.ID, a.Bot, a.Status)
			}
		}
		if !hs[0].audit.has("actions.resume") {
			t.Errorf("no actions.resume audit entry, want the kill switch audited")
		}
	})
}
