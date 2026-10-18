package wpk_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/api/chat"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/api/chat/chattest"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/policy"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review/blacklist"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review/reviewtest"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// Maintainer decision 2026-10-18: in ask_before_action the bot never deletes or bans unless an
// admin says so; a timed-out case leaves the message (DESIGN §8.3 (b), §9.5 Timeouts, §12.1).

const askDecision = "maintainer decision 2026-10-18: ask_before_action never deletes or bans unless an admin says so"

// TestAskBeforeKeep_ConfigFrom: in ask_before_action the fallback comes from
// enforcement.ask_before.on_timeout: delete_no_ban and dismiss map across, anything else is leave;
// review.on_timeout is ignored there (DESIGN §9.5, §12.1).
func TestAskBeforeKeep_ConfigFrom(t *testing.T) {
	cases := []struct {
		askOn    policy.TimeoutAction
		reviewOn policy.TimeoutAction
		want     review.OnTimeout
	}{
		{policy.TimeoutLeave, policy.TimeoutKeepDeletedNoBan, review.TimeoutLeave},
		{policy.TimeoutLeave, policy.TimeoutAutoBan, review.TimeoutLeave},
		{policy.TimeoutLeave, policy.TimeoutDeleteNoBan, review.TimeoutLeave},
		{policy.TimeoutDeleteNoBan, policy.TimeoutKeepDeletedNoBan, review.TimeoutDeleteNoBan},
		{policy.TimeoutDismiss, policy.TimeoutAutoBan, review.TimeoutDismiss},
		{"", policy.TimeoutKeepDeletedNoBan, review.TimeoutLeave},
	}
	for _, tc := range cases {
		s := policy.Defaults()
		s.Enforcement.Mode = policy.EnforceAskBeforeAction
		s.Enforcement.AskBefore.OnTimeout = tc.askOn
		s.Enforcement.AskBefore.Timeout = policy.Duration(20 * time.Minute)
		s.Review.OnTimeout = tc.reviewOn
		cfg := review.ConfigFrom(s)
		if cfg.OnTimeout != tc.want {
			t.Errorf("ConfigFrom(ask_before.on_timeout=%q, review.on_timeout=%q).OnTimeout = %q, want %q (%s; review.on_timeout doesn't apply in ask_before_action, DESIGN §9.5)", tc.askOn, tc.reviewOn, cfg.OnTimeout, tc.want, askDecision)
		}
		if cfg.Timeout != 20*time.Minute {
			t.Errorf("ConfigFrom(ask_before_action).Timeout = %v, want 20m from enforcement.ask_before.timeout (DESIGN §9.5)", cfg.Timeout)
		}
	}

	s := policy.Defaults()
	s.Enforcement.Mode = policy.EnforceAskBeforeAction
	if got := review.ConfigFrom(s).OnTimeout; got != review.TimeoutLeave {
		t.Errorf("ConfigFrom(Defaults() in ask_before_action).OnTimeout = %q, want leave (%s)", got, askDecision)
	}
}

// TestAskBeforeKeep_EffectiveTimeout: ask_before_action defaults to 15 min / leave, keeps the
// configured delete_no_ban, dismiss and leave, and turns auto_ban into leave (DESIGN §9.5).
func TestAskBeforeKeep_EffectiveTimeout(t *testing.T) {
	established := plainSubject()
	established.Established = true
	cases := []struct {
		name string
		on   review.OnTimeout
		subj review.Subject
		want review.OnTimeout
	}{
		{"unset", "", plainSubject(), review.TimeoutLeave},
		{"leave", review.TimeoutLeave, plainSubject(), review.TimeoutLeave},
		{"delete_no_ban", review.TimeoutDeleteNoBan, plainSubject(), review.TimeoutDeleteNoBan},
		{"dismiss", review.TimeoutDismiss, plainSubject(), review.TimeoutDismiss},
		{"auto_ban", review.TimeoutAutoBan, plainSubject(), review.TimeoutLeave},
		{"auto_ban, established", review.TimeoutAutoBan, established, review.TimeoutLeave},
		{"unknown", "forever", plainSubject(), review.TimeoutLeave},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, f := review.EffectiveTimeout(review.Config{Mode: review.ModeAskBeforeAction, OnTimeout: tc.on}, tc.subj)
			if d != 15*time.Minute || f != tc.want {
				t.Errorf("EffectiveTimeout(ask_before_action, %q) = (%v, %s), want (15m0s, %s) (%s, DESIGN §9.5)", tc.on, d, f, tc.want, askDecision)
			}
		})
	}
	if err := review.CheckOnTimeout(review.ModeAskBeforeAction, review.TimeoutLeave); err != nil {
		t.Errorf("CheckOnTimeout(ask_before_action, leave) = %v, want nil (leave is the default fallback there)", err)
	}
	if err := review.CheckOnTimeout(review.ModeAskBeforeAction, review.TimeoutAutoBan); !errors.Is(err, review.ErrAutoBanInAskMode) {
		t.Errorf("CheckOnTimeout(ask_before_action, auto_ban) = %v, want ErrAutoBanInAskMode (DESIGN §8.3)", err)
	}
}

// newKeepReviewEnv is newReviewEnv with Standing wired in, so a trust restore is observable.
func newKeepReviewEnv(t *testing.T, cfg review.Config) *reviewEnv {
	t.Helper()
	e := newReviewEnv(t, cfg)
	e.svc = review.NewService(review.Deps{
		Store: e.store, Settings: e.settings, Admins: e.admins, Actions: e.acts,
		Learner: e.learning, Standing: e.standing, Clock: e.clk,
	})
	return e
}

func timeoutNotes(acts *reviewtest.Actions) []string {
	var out []string
	for _, s := range sendsTo(acts, revGrp) {
		if s.ReviewID == 0 {
			out = append(out, s.Message.Text)
		}
	}
	for _, ed := range reviewtest.Of[review.Edit](acts) {
		if ed.Chat == revGrp {
			out = append(out, ed.Text)
		}
	}
	return out
}

// TestAskBeforeKeep_ExpireFallbacks: an ask_before case that nobody decides is closed once as
// timed out; leave does nothing to the message or the member, delete_no_ban deletes without a
// ban, dismiss restores trust; the note goes to the review group (DESIGN §9.5 Timeouts).
func TestAskBeforeKeep_ExpireFallbacks(t *testing.T) {
	cases := []struct {
		name         string
		on           review.OnTimeout
		wantFallback review.OnTimeout
		wantDelete   bool
		wantRestore  bool
		wantDecision review.Decision // empty: not pinned here
		wantNote     string
	}{
		{"default is leave", "", review.TimeoutLeave, false, false, review.DecisionLeave, "leave the message, no ban"},
		{"leave", review.TimeoutLeave, review.TimeoutLeave, false, false, review.DecisionLeave, "leave the message, no ban"},
		{"delete_no_ban", review.TimeoutDeleteNoBan, review.TimeoutDeleteNoBan, true, false, "", ""},
		{"dismiss", review.TimeoutDismiss, review.TimeoutDismiss, false, true, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newKeepReviewEnv(t, withFallback(groupDest(review.ModeAskBeforeAction), tc.on))
			it, _ := e.open(t, spamItem(review.KindAskBefore, false, plainSubject()))
			if it.OnTimeout != tc.wantFallback {
				t.Errorf("opened item OnTimeout = %q, want %q (%s, DESIGN §9.5)", it.OnTimeout, tc.wantFallback, askDecision)
			}
			if want := e.clk.Now().Add(15 * time.Minute); !it.Deadline.Equal(want) {
				t.Errorf("deadline = %v, want %v (15 min default in ask_before_action, DESIGN §9.5)", it.Deadline, want)
			}
			before := len(e.acts.All())
			e.clk.Advance(15*time.Minute + time.Second)
			if n, err := e.svc.Expire(e.ctx); err != nil || n != 1 {
				t.Fatalf("Expire() after the deadline = %d, %v; want 1, nil", n, err)
			}
			got := e.get(t, it.ID)
			if got.Status != review.StatusTimedOut || got.Method != review.MethodTimeout {
				t.Errorf("got {Status:%s Method:%s}, want {timed_out timeout} (DESIGN §9.5)", got.Status, got.Method)
			}
			if tc.wantDecision != "" && got.Decision != tc.wantDecision {
				t.Errorf("recorded decision = %q, want %q (a leave timeout records decision leave)", got.Decision, tc.wantDecision)
			}
			newActs := &reviewtest.Actions{}
			for _, a := range e.acts.All()[before:] {
				_ = newActs.Enqueue(e.ctx, a)
			}
			if b := bansOf(newActs, member); len(b) != 0 {
				t.Errorf("got bans %+v, want none (an ask_before timeout never bans, DESIGN §8.3 (b))", b)
			}
			if dels := deletesOf(newActs, "spam-1"); (len(dels) > 0) != tc.wantDelete {
				t.Errorf("got %d deletes of the message, want delete=%v (fallback %s; %s)", len(dels), tc.wantDelete, tc.wantFallback, askDecision)
			}
			kinds := standingKinds(e.standing, member)
			if (kinds["restore_trust"] > 0) != tc.wantRestore {
				t.Errorf("got standing events %v, want restore_trust=%v (only dismiss restores trust; leave changes nothing about the member, DESIGN §9.5)", kinds, tc.wantRestore)
			}
			if tc.on == review.TimeoutLeave || tc.on == "" {
				if len(kinds) != 0 {
					t.Errorf("got standing events %v, want none (leave: no strike, no trust change)", kinds)
				}
				if a, en := e.corpus.Allowed(), e.corpus.Entries(); len(a)+len(en) != 0 {
					t.Errorf("got allow-list %+v and corpus %+v, want none (leave learns and overturns nothing)", a, en)
				}
			}
			notes := timeoutNotes(newActs)
			if len(notes) == 0 {
				t.Fatalf("no timeout note in the review group, want one (DESIGN §9.5 Timeouts)")
			}
			if tc.wantNote != "" && !strings.Contains(strings.Join(notes, "\n"), tc.wantNote) {
				t.Errorf("timeout notes %q, want one containing %q (the note says what the fallback did)", notes, tc.wantNote)
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

// TestAskBeforeKeep_TimeoutNoteArabic: the Arabic note names the leave fallback in Arabic, not as
// a raw key or in English (DESIGN §9.5, §13 bilingual replies).
func TestAskBeforeKeep_TimeoutNoteArabic(t *testing.T) {
	cfg := groupDest(review.ModeAskBeforeAction)
	cfg.Language = "ar"
	e := newKeepReviewEnv(t, cfg)
	e.open(t, spamItem(review.KindAskBefore, false, plainSubject()))
	before := len(e.acts.All())
	e.clk.Advance(15*time.Minute + time.Second)
	if n, err := e.svc.Expire(e.ctx); err != nil || n != 1 {
		t.Fatalf("Expire() = %d, %v; want 1, nil", n, err)
	}
	newActs := &reviewtest.Actions{}
	for _, a := range e.acts.All()[before:] {
		_ = newActs.Enqueue(e.ctx, a)
	}
	notes := timeoutNotes(newActs)
	if len(notes) == 0 {
		t.Fatalf("no timeout note in the review group, want one (DESIGN §9.5)")
	}
	for _, n := range notes {
		if strings.Contains(n, "review.fallback") || strings.Contains(n, "leave the message") || strings.Contains(n, "leave") {
			t.Errorf("Arabic timeout note %q, want the leave fallback in Arabic (no raw key, no English)", n)
		}
	}
	if fb := review.Texts.T("ar", "review.fallback.leave", nil); fb == "" || fb == "review.fallback.leave" || fb == review.Texts.T("en", "review.fallback.leave", nil) {
		t.Errorf("Arabic review.fallback.leave = %q, want an Arabic text", fb)
	}
}

// TestAskBeforeKeep_AutoBanDowngradeLeaves: an item whose fallback is auto_ban, in a group now in
// ask_before_action, times out as leave: no delete, no ban (DESIGN §9.5, §8.3 (b)).
func TestAskBeforeKeep_AutoBanDowngradeLeaves(t *testing.T) {
	e := newKeepReviewEnv(t, withFallback(groupDest(review.ModeAuto), review.TimeoutAutoBan))
	it, _ := e.open(t, spamItem(review.KindUncertain, false, plainSubject()))
	if it.OnTimeout != review.TimeoutAutoBan {
		t.Fatalf("setup: opened item OnTimeout = %q, want auto_ban (auto mode, new member)", it.OnTimeout)
	}
	e.settings.Set(grp, groupDest(review.ModeAskBeforeAction))
	before := len(e.acts.All())
	e.clk.Advance(6*time.Hour + time.Second)
	if n, err := e.svc.Expire(e.ctx); err != nil || n != 1 {
		t.Fatalf("Expire() = %d, %v; want 1, nil", n, err)
	}
	newActs := &reviewtest.Actions{}
	for _, a := range e.acts.All()[before:] {
		_ = newActs.Enqueue(e.ctx, a)
	}
	if b := bansOf(newActs, member); len(b) != 0 {
		t.Errorf("got bans %+v, want none (auto_ban never survives ask_before_action, DESIGN §8.3)", b)
	}
	if d := deletesOf(newActs, "spam-1"); len(d) != 0 {
		t.Errorf("got deletes %+v, want none (a downgraded auto_ban in ask_before_action becomes leave, not delete_no_ban; %s)", d, askDecision)
	}
	if got := e.get(t, it.ID); got.Status != review.StatusTimedOut || got.Decision != review.DecisionLeave {
		t.Errorf("got {Status:%s Decision:%s}, want {timed_out leave}", got.Status, got.Decision)
	}
}

// newAskChatEnv is newChatEnv whose settings also know the enforcement.ask_before keys.
func newAskChatEnv(t *testing.T, mode review.EnforcementMode, askOn string) *chatEnv {
	t.Helper()
	e := newChatEnv(t)
	e.settings = chattest.NewSettings(map[string]string{
		"enforcement.mode":                  string(mode),
		"review.destination":                string(review.DestinationDashboard),
		"review.timeout":                    "6h",
		"review.on_timeout":                 string(review.TimeoutKeepDeletedNoBan),
		"enforcement.ask_before.timeout":    "15m",
		"enforcement.ask_before.on_timeout": askOn,
	})
	rs := &reviewtest.Settings{Default: review.Config{Destination: review.DestinationDashboard, Mode: mode}}
	svc := review.NewService(review.Deps{Store: review.NewMemoryStore(), Settings: rs, Admins: e.admins, Actions: e.acts, Learner: e.learning, Clock: e.clk})
	e.h = chat.New(chat.Deps{
		Groups: e.groups, Admins: e.admins, Actions: e.acts, Reviews: svc,
		Learning: e.learning, Evidence: e.learning, Blacklist: e.bl, Normalize: blacklist.SimpleNormalizer{},
		Settings: e.settings, Lists: &chattest.Lists{}, Recent: &chattest.Recent{}, Members: e.members,
		History: e.history, Identity: e.identity, Track: chattest.Track{}, Stats: chattest.Stats{},
		Threads: chat.NewMemoryThreads(), Audit: e.audit, Clock: e.clk, Language: "en",
		Operator: func(j wa.JID) bool { return j == operator },
	})
	return e
}

// TestAskBeforeKeep_ReviewTimeoutCommand: in ask_before_action `.review timeout` writes
// enforcement.ask_before.* (keep→leave, delete→delete_no_ban, dismiss→dismiss) and leaves
// review.* alone; other modes still write review.* (DESIGN §13.7, §9.5).
func TestAskBeforeKeep_ReviewTimeoutCommand(t *testing.T) {
	cases := []struct {
		mode        review.EnforcementMode
		word        string
		wantRefused bool
		wantAsk     string           // enforcement.ask_before.on_timeout; "" = must not change
		wantReview  review.OnTimeout // review.on_timeout; "" = must not change
	}{
		{review.ModeAskBeforeAction, "keep", false, "leave", ""},
		{review.ModeAskBeforeAction, "delete", false, "delete_no_ban", ""},
		{review.ModeAskBeforeAction, "dismiss", false, "dismiss", ""},
		{review.ModeAskBeforeAction, "ban", true, "", ""},
		{review.ModeAuto, "keep", false, "", review.TimeoutKeepDeletedNoBan},
		{review.ModeAskDeleteFirst, "delete", false, "", review.TimeoutDeleteNoBan},
	}
	for _, tc := range cases {
		t.Run(string(tc.mode)+" "+tc.word, func(t *testing.T) {
			e := newAskChatEnv(t, tc.mode, "leave")
			e.openThread(t, adminA)
			e.handle(t, e.inGroup(adminA, ".review timeout 30m "+tc.word))

			askOn, askDur := e.changesBy(adminA, "enforcement.ask_before.on_timeout"), e.changesBy(adminA, "enforcement.ask_before.timeout")
			revOn, revDur := e.changesBy(adminA, "review.on_timeout"), e.changesBy(adminA, "review.timeout")
			switch {
			case tc.wantRefused:
				if n := len(askOn) + len(askDur) + len(revOn) + len(revDur); n != 0 {
					t.Errorf("got %d settings changes, want none (ban is refused in ask modes, DESIGN §9.5)", n)
				}
				if len(sendsTo(e.acts, adminA)) == 0 {
					t.Errorf("no reply to the admin, want an explanation of the refusal (DESIGN §13.7)")
				}
			case tc.wantAsk != "":
				if v, _ := e.settings.Get(e.ctx, grp, "enforcement.ask_before.on_timeout"); v != tc.wantAsk || len(askOn) == 0 {
					t.Errorf("enforcement.ask_before.on_timeout = %q (%d changes), want %q set by the admin (ask_before_action reads its fallback from enforcement.ask_before, DESIGN §9.5)", v, len(askOn), tc.wantAsk)
				}
				v, _ := e.settings.Get(e.ctx, grp, "enforcement.ask_before.timeout")
				if d, err := time.ParseDuration(v); err != nil || d != 30*time.Minute || len(askDur) == 0 {
					t.Errorf("enforcement.ask_before.timeout = %q, want 30m set by the admin (DESIGN §9.5)", v)
				}
				if len(revOn)+len(revDur) != 0 {
					t.Errorf("got review.timeout/on_timeout changes %+v %+v, want none (ask_before_action ignores review.*, so writing there would do nothing)", revDur, revOn)
				}
			default:
				if v, _ := e.settings.Get(e.ctx, grp, "review.on_timeout"); v != string(tc.wantReview) || len(revOn) == 0 {
					t.Errorf("review.on_timeout = %q, want %q set by the admin (outside ask_before_action the command is unchanged, DESIGN §13.7)", v, tc.wantReview)
				}
				if len(askOn)+len(askDur) != 0 {
					t.Errorf("got enforcement.ask_before changes %+v %+v in mode %s, want none", askDur, askOn, tc.mode)
				}
			}
			assertSilent(t, e.acts, grp)
		})
	}
}

func lastSendTo(t *testing.T, e *chatEnv) string {
	t.Helper()
	s := sendsTo(e.acts, adminA)
	if len(s) == 0 {
		t.Fatalf("no DM to the admin, want one")
	}
	return s[len(s)-1].Message.Text
}

// TestAskBeforeKeep_Wording: .status and the promotion DM say the message is left for leave and
// dismiss, and mention deleting only for delete_no_ban (DESIGN §8.3 (b), §13.6).
func TestAskBeforeKeep_Wording(t *testing.T) {
	for _, on := range []string{"leave", "dismiss", "delete_no_ban"} {
		t.Run(on, func(t *testing.T) {
			deletes := on == "delete_no_ban"

			e := newAskChatEnv(t, review.ModeAskBeforeAction, on)
			e.openThread(t, adminA)
			e.handle(t, e.inGroup(adminA, ".status"))
			status := lastSendTo(t, e)
			if strings.Contains(status, "then delete") != deletes {
				t.Errorf(".status with ask_before.on_timeout=%s:\n%s\nwant \"then delete\" present=%v (the asking line names the real fallback)", on, status, deletes)
			}
			if !deletes && !strings.Contains(status, "leave") {
				t.Errorf(".status with ask_before.on_timeout=%s:\n%s\nwant it to say the message is left (\"leave\")", on, status)
			}

			e = newAskChatEnv(t, review.ModeAskBeforeAction, on)
			e.openThread(t, adminA)
			if err := e.h.OnPromoted(e.ctx, grp, adminA); err != nil {
				t.Fatalf("OnPromoted() error = %v", err)
			}
			promo := lastSendTo(t, e)
			if strings.Contains(promo, "I delete the message") != deletes {
				t.Errorf("promotion DM with ask_before.on_timeout=%s:\n%s\nwant \"I delete the message\" present=%v", on, promo, deletes)
			}
			if !deletes && !strings.Contains(promo, "leave the message") {
				t.Errorf("promotion DM with ask_before.on_timeout=%s:\n%s\nwant it to say \"leave the message\"", on, promo)
			}
		})
	}
}

// TestAskBeforeKeep_HelpTexts: the help and usage texts no longer promise to delete the message
// after 15 minutes in ask_before_action (EN and AR).
func TestAskBeforeKeep_HelpTexts(t *testing.T) {
	stale := map[string]string{"en": "I delete it", "ar": "أحذفها بلا حظر"}
	for _, key := range []string{"cmd.help.reviews", "cmd.help.settings", "cmd.usage.askadmins"} {
		for lang, phrase := range stale {
			txt := review.Texts.T(lang, key, map[string]string{"prefix": "."})
			if txt == "" || txt == key {
				t.Errorf("%s [%s] is missing, want the help text", key, lang)
				continue
			}
			if strings.Contains(txt, phrase) {
				t.Errorf("%s [%s] still says %q, want the leave default described (%s)", key, lang, phrase, askDecision)
			}
		}
	}
}
