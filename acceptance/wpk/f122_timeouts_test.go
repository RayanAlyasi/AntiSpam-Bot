package wpk_test

import (
	"errors"
	"testing"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review/reviewtest"
)

// TestF122_EffectiveTimeout: defaults are 6 h / keep_deleted_no_ban, and 15 min / delete_no_ban
// in ask_before_action; auto_ban never survives for ask modes, established members, possibly
// hacked accounts or admins (DESIGN §9.5 Timeouts, §8.3).
func TestF122_EffectiveTimeout(t *testing.T) {
	plain := plainSubject()
	established, hacked, admin := plain, plain, plain
	established.Established = true
	hacked.Hacked = true
	admin.Admin = true
	cases := []struct {
		name     string
		cfg      review.Config
		subj     review.Subject
		wantDur  time.Duration
		wantFall review.OnTimeout
	}{
		{"auto defaults", review.Config{Mode: review.ModeAuto}, plain, 6 * time.Hour, review.TimeoutKeepDeletedNoBan},
		{"ask_delete_first defaults", review.Config{Mode: review.ModeAskDeleteFirst}, plain, 6 * time.Hour, review.TimeoutKeepDeletedNoBan},
		{"ask_before_action defaults", review.Config{Mode: review.ModeAskBeforeAction}, plain, 15 * time.Minute, review.TimeoutDeleteNoBan},
		{"configured auto_ban, new member", review.Config{Mode: review.ModeAuto, Timeout: 2 * time.Hour, OnTimeout: review.TimeoutAutoBan}, plain, 2 * time.Hour, review.TimeoutAutoBan},
		{"configured dismiss", review.Config{Mode: review.ModeAuto, OnTimeout: review.TimeoutDismiss}, plain, 6 * time.Hour, review.TimeoutDismiss},
		{"auto_ban, established member", review.Config{Mode: review.ModeAuto, Timeout: 2 * time.Hour, OnTimeout: review.TimeoutAutoBan}, established, 2 * time.Hour, review.TimeoutKeepDeletedNoBan},
		{"auto_ban, possibly hacked", review.Config{Mode: review.ModeAuto, OnTimeout: review.TimeoutAutoBan}, hacked, 6 * time.Hour, review.TimeoutKeepDeletedNoBan},
		{"auto_ban, admin", review.Config{Mode: review.ModeAuto, OnTimeout: review.TimeoutAutoBan}, admin, 6 * time.Hour, review.TimeoutKeepDeletedNoBan},
		{"auto_ban in ask_delete_first", review.Config{Mode: review.ModeAskDeleteFirst, OnTimeout: review.TimeoutAutoBan}, plain, 6 * time.Hour, review.TimeoutKeepDeletedNoBan},
		{"auto_ban in ask_before_action", review.Config{Mode: review.ModeAskBeforeAction, OnTimeout: review.TimeoutAutoBan}, plain, 15 * time.Minute, review.TimeoutDeleteNoBan},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, f := review.EffectiveTimeout(tc.cfg, tc.subj)
			if d != tc.wantDur || f != tc.wantFall {
				t.Errorf("got (%v, %s), want (%v, %s) (a timeout must never become a ban the bot isn't allowed to make, DESIGN §9.5)", d, f, tc.wantDur, tc.wantFall)
			}
		})
	}
}

// TestF122_CheckOnTimeout: auto_ban can't be configured in ask modes; unknown fallbacks are
// rejected (DESIGN §9.5, §8.3).
func TestF122_CheckOnTimeout(t *testing.T) {
	all := []review.OnTimeout{review.TimeoutKeepDeletedNoBan, review.TimeoutDeleteNoBan, review.TimeoutAutoBan, review.TimeoutDismiss}
	for _, m := range []review.EnforcementMode{review.ModeAuto, review.ModeAskDeleteFirst, review.ModeAskBeforeAction} {
		for _, f := range all {
			err := review.CheckOnTimeout(m, f)
			wantRefused := f == review.TimeoutAutoBan && m.AskMode()
			if wantRefused && !errors.Is(err, review.ErrAutoBanInAskMode) {
				t.Errorf("CheckOnTimeout(%s, %s) = %v, want ErrAutoBanInAskMode (ask-admins mode never bans on its own, DESIGN §8.3)", m, f, err)
			}
			if !wantRefused && err != nil {
				t.Errorf("CheckOnTimeout(%s, %s) = %v, want nil", m, f, err)
			}
		}
		if err := review.CheckOnTimeout(m, "forever"); !errors.Is(err, review.ErrUnknownFallback) {
			t.Errorf("CheckOnTimeout(%s, forever) = %v, want ErrUnknownFallback", m, err)
		}
	}
}

// TestF122_ExpireAppliesFallbackOnce: a pending item past its deadline gets its fallback exactly
// once, with method timeout; the "No decision" note goes only to a group destination (DESIGN §9.5).
func TestF122_ExpireAppliesFallbackOnce(t *testing.T) {
	established := plainSubject()
	established.Established = true
	cases := []struct {
		name       string
		cfg        review.Config
		kind       review.Kind
		deleted    bool
		subj       review.Subject
		wantWait   time.Duration
		wantBan    bool
		wantDelete bool
	}{
		{"keep deleted, no ban", groupDest(review.ModeAuto), review.KindBanDecision, true, plainSubject(), 6 * time.Hour, false, false},
		{"ask_before_action deletes, no ban", groupDest(review.ModeAskBeforeAction), review.KindAskBefore, false, plainSubject(), 15 * time.Minute, false, true},
		{"auto_ban for a new member", withFallback(groupDest(review.ModeAuto), review.TimeoutAutoBan), review.KindBanDecision, true, plainSubject(), 6 * time.Hour, true, false},
		{"auto_ban never for an established member", withFallback(groupDest(review.ModeAuto), review.TimeoutAutoBan), review.KindBanDecision, true, established, 6 * time.Hour, false, false},
		{"auto_ban never in ask_delete_first", withFallback(groupDest(review.ModeAskDeleteFirst), review.TimeoutAutoBan), review.KindBanDecision, true, plainSubject(), 6 * time.Hour, false, false},
		{"dismiss leaves the message", withFallback(groupDest(review.ModeAuto), review.TimeoutDismiss), review.KindUncertain, false, plainSubject(), 6 * time.Hour, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newReviewEnv(t, tc.cfg)
			it, _ := e.open(t, spamItem(tc.kind, tc.deleted, tc.subj))
			if want := e.clk.Now().Add(tc.wantWait); !it.Deadline.Equal(want) {
				t.Errorf("got deadline %v, want %v (DESIGN §9.5 default timeouts)", it.Deadline, want)
			}
			e.clk.Advance(tc.wantWait - time.Second)
			if n, err := e.svc.Expire(e.ctx); err != nil || n != 0 {
				t.Fatalf("Expire() before the deadline = %d, %v; want 0, nil", n, err)
			}
			before := len(e.acts.All())
			e.clk.Advance(2 * time.Second)
			if n, err := e.svc.Expire(e.ctx); err != nil || n != 1 {
				t.Fatalf("Expire() after the deadline = %d, %v; want 1, nil", n, err)
			}
			got := e.get(t, it.ID)
			if got.Status != review.StatusTimedOut || got.Method != review.MethodTimeout {
				t.Errorf("got {Status:%s Method:%s}, want {timed_out timeout} (DESIGN §9.5 records method = timeout)", got.Status, got.Method)
			}
			newActs := e.acts.All()[before:]
			var bans, dels, notes int
			for _, a := range newActs {
				switch a := a.(type) {
				case review.Ban:
					bans++
				case review.Delete:
					if a.Message.ID == "spam-1" && a.Group == grp {
						dels++
					}
				case review.Send:
					if a.Chat == revGrp {
						notes++
					}
				case review.Edit:
					if a.Chat == revGrp {
						notes++
					}
				}
			}
			if (bans > 0) != tc.wantBan {
				t.Errorf("got %d bans, want ban=%v (DESIGN §9.5 fallback %s)", bans, tc.wantBan, it.OnTimeout)
			}
			if (dels > 0) != tc.wantDelete {
				t.Errorf("got %d deletes of the message, want delete=%v (DESIGN §9.5 fallback %s)", dels, tc.wantDelete, it.OnTimeout)
			}
			if notes == 0 {
				t.Errorf("no \"No decision\" note in the review group, want one (DESIGN §9.5 Timeouts)")
			}
			assertSilent(t, e.acts, grp)

			mark := len(e.acts.All())
			e.clk.Advance(time.Hour)
			if n, err := e.svc.Expire(e.ctx); err != nil || n != 0 {
				t.Errorf("second Expire() = %d, %v; want 0, nil (a fallback applies once)", n, err)
			}
			if extra := e.acts.All()[mark:]; len(extra) != 0 {
				t.Errorf("second Expire() enqueued %+v, want nothing", extra)
			}
		})
	}
}

// TestF122_DashboardPostsNothingInWhatsApp: with the dashboard destination neither the item nor
// its timeout note is sent anywhere in WhatsApp (DESIGN §9.5 destination (c)).
func TestF122_DashboardPostsNothingInWhatsApp(t *testing.T) {
	e := newReviewEnv(t, review.Config{Destination: review.DestinationDashboard, Mode: review.ModeAuto})
	it, _ := e.open(t, spamItem(review.KindBanDecision, true, plainSubject()))
	e.clk.Advance(6*time.Hour + time.Second)
	if n, err := e.svc.Expire(e.ctx); err != nil || n != 1 {
		t.Fatalf("Expire() = %d, %v; want 1, nil", n, err)
	}
	if got := e.get(t, it.ID); got.Status != review.StatusTimedOut {
		t.Errorf("got status %s, want timed_out", got.Status)
	}
	if s, ed := reviewtest.Of[review.Send](e.acts), reviewtest.Of[review.Edit](e.acts); len(s)+len(ed) != 0 {
		t.Errorf("got sends %+v and edits %+v, want none (dashboard sends nothing in WhatsApp, DESIGN §9.5)", s, ed)
	}
	if pend, _ := e.svc.Pending(e.ctx, grp); len(pend) != 0 {
		t.Errorf("got %d pending items after the timeout, want 0", len(pend))
	}
}

// TestF122_ReviewCommands: `.review group|dashboard` and `.review timeout <d> keep|delete|ban|dismiss`
// set the dotted keys; `ban` is refused in ask modes with an explanation (DESIGN §13.7, §9.5).
func TestF122_ReviewCommands(t *testing.T) {
	t.Run("destination", func(t *testing.T) {
		e := newChatEnv(t)
		e.openThread(t, adminA)
		for _, want := range []review.Destination{review.DestinationDashboard, review.DestinationGroup} {
			e.handle(t, e.inGroup(adminA, ".review "+string(want)))
			if v, _ := e.settings.Get(e.ctx, grp, "review.destination"); v != string(want) {
				t.Errorf("review.destination = %q after .review %s, want %q (DESIGN §13.7)", v, want, want)
			}
		}
		assertSilent(t, e.acts, grp)
	})

	cases := []struct {
		mode        review.EnforcementMode
		word        string
		wantRefused bool
		wantValue   review.OnTimeout
	}{
		{review.ModeAuto, "keep", false, review.TimeoutKeepDeletedNoBan},
		{review.ModeAuto, "delete", false, review.TimeoutDeleteNoBan},
		{review.ModeAuto, "ban", false, review.TimeoutAutoBan},
		{review.ModeAuto, "dismiss", false, review.TimeoutDismiss},
		{review.ModeAskDeleteFirst, "ban", true, ""},
		{review.ModeAskBeforeAction, "ban", true, ""},
		{review.ModeAskBeforeAction, "delete", false, review.TimeoutDeleteNoBan},
	}
	for _, tc := range cases {
		t.Run(string(tc.mode)+" "+tc.word, func(t *testing.T) {
			e := newChatEnv(t)
			if _, err := e.settings.Set(e.ctx, grp, "enforcement.mode", string(tc.mode), ""); err != nil {
				t.Fatalf("setup: %v", err)
			}
			e.openThread(t, adminA)
			e.handle(t, e.inGroup(adminA, ".review timeout 2h "+tc.word))

			fall, dur := e.changesBy(adminA, "review.on_timeout"), e.changesBy(adminA, "review.timeout")
			if tc.wantRefused {
				if len(fall)+len(dur) != 0 {
					t.Errorf("got changes %+v %+v, want none (auto_ban is refused in ask modes, DESIGN §9.5)", fall, dur)
				}
				if len(sendsTo(e.acts, adminA)) == 0 {
					t.Errorf("no reply to the admin, want an explanation of the refusal (DESIGN §13.7)")
				}
			} else {
				if v, _ := e.settings.Get(e.ctx, grp, "review.on_timeout"); v != string(tc.wantValue) || len(fall) == 0 {
					t.Errorf("review.on_timeout = %q, want %q set by %s (DESIGN §13.7)", v, tc.wantValue, adminA)
				}
				v, _ := e.settings.Get(e.ctx, grp, "review.timeout")
				if d, err := time.ParseDuration(v); err != nil || d != 2*time.Hour {
					t.Errorf("review.timeout = %q, want a duration of 2h (DESIGN §13.7)", v)
				}
			}
			assertSilent(t, e.acts, grp)
		})
	}

	t.Run("non-admin ignored", func(t *testing.T) {
		e := newChatEnv(t)
		e.handle(t, e.inGroup(member, ".review timeout 1h ban"))
		e.handle(t, e.inGroup(member, ".review group"))
		if c := e.settings.Changes(); len(c) != 0 {
			t.Errorf("got settings changes %+v from a non-admin, want none (DESIGN §13.7)", c)
		}
		if a := e.acts.All(); len(a) != 0 {
			t.Errorf("got actions %+v, want none (DESIGN §13.7)", a)
		}
	})
}

func withFallback(c review.Config, f review.OnTimeout) review.Config {
	c.OnTimeout = f
	return c
}
