package ai_test

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"pgregory.net/rapid"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai/budget"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai/cache"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai/fake"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai/gateway"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai/llm"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai/safety"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/policy"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/scoring"
)

// TestAISpamDeletesMessageAndAsksAdminsForBan follows an UNCERTAIN message through T3 and the
// decision engine under the Standard preset in auto mode.
func TestAISpamDeletesMessageAndAsksAdminsForBan(t *testing.T) {
	f := fakeAnswering(fake.Response{Verdict: spamVerdict(0.95)})
	v := newVerifier(t, verifierOpts{classifier: f})

	out := v.Verify(t.Context(), item(uncertainVerdict(), "double your usdt in 24h, dm me"))

	if f.Calls() != 1 || !out.Called {
		t.Fatalf("classifier calls = %d, Called = %v; want 1, true (the UNCERTAIN band goes to the AI, DESIGN §7.0)", f.Calls(), out.Called)
	}
	if out.Verdict.Band != scoring.BandUncertain || out.Verdict.Source != scoring.SourceAI {
		t.Fatalf("verdict = %s/%q; want uncertain/ai (AI spam ≥ 0.90 is AI-verified, DESIGN §7.0, §7.3)", out.Verdict.Band, out.Verdict.Source)
	}
	if out.Reason != gateway.ReasonVerified || out.Source != cache.SourceFresh {
		t.Fatalf("reason = %q, source = %v; want verified, fresh (a first-time answer from the provider)", out.Reason, out.Source)
	}
	if !out.Verdict.DeletesAsSpam() {
		t.Fatalf("DeletesAsSpam = false; want true (AI-verified spam reaches the delete line, DESIGN §7.3)")
	}

	for _, ct := range []policy.ContentType{policy.TypeText, policy.TypeLinks} {
		d := safety.Decide(mustPreset(t, policy.PresetStandard), finding(out.Verdict, ct), newMember, safety.Unmet{})
		if !d.Delete || !hasAction(d, policy.ActionDelete) {
			t.Errorf("%s: Delete = %v; want true (AI spam ≥ 0.90 is deleted, DESIGN §8.3)", ct, d.Delete)
		}
		if d.Ban || hasAction(d, policy.ActionBan) || d.Route == policy.RouteAutoBan {
			t.Errorf("%s: Ban = %v, route = %s; want no ban (early-AI safety sends the ban to admins, DESIGN §9.2)", ct, d.Ban, d.Route)
		}
		if d.Review == nil || !d.Review.ProposeBan {
			t.Errorf("%s: review = %+v; want a review proposing the ban (admins decide the ban, DESIGN §8.3)", ct, d.Review)
		} else if d.Review.OnTimeout == policy.TimeoutAutoBan {
			t.Errorf("%s: review on_timeout = auto_ban; want a fallback that can't ban (no AI-only ban without a human, DESIGN §9.2)", ct)
		}
	}
}

// TestAISpamConfidenceBoundary checks the inclusive ai.spam_confidence line (0.90).
func TestAISpamConfidenceBoundary(t *testing.T) {
	cases := []struct {
		conf       float64
		wantBand   scoring.Band
		wantDelete bool
	}{
		{0.90, scoring.BandUncertain, true},
		{0.89, scoring.BandStillUncertain, false},
		{1.00, scoring.BandUncertain, true},
		{0.0, scoring.BandStillUncertain, false},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprint(tc.conf), func(t *testing.T) {
			v := newVerifier(t, verifierOpts{classifier: fakeAnswering(fake.Response{Verdict: spamVerdict(tc.conf)})})
			out := v.Verify(t.Context(), item(uncertainVerdict(), "free crypto signals"))
			if out.Verdict.Band != tc.wantBand {
				t.Fatalf("spam at %.2f: band = %s; want %s (spam settles UNCERTAIN only at confidence ≥ 0.90, DESIGN §7.0)", tc.conf, out.Verdict.Band, tc.wantBand)
			}
			d := safety.Decide(mustPreset(t, policy.PresetStandard), finding(out.Verdict, policy.TypeText), newMember, safety.Unmet{})
			if d.Delete != tc.wantDelete {
				t.Fatalf("spam at %.2f: Delete = %v; want %v (below 0.90 the message stays and admins decide, DESIGN §7.0)", tc.conf, d.Delete, tc.wantDelete)
			}
			if d.Ban {
				t.Fatalf("spam at %.2f: Ban = true; want false (AI evidence never auto-bans without a track record, DESIGN §9.2)", tc.conf)
			}
			if !tc.wantDelete && d.Review == nil {
				t.Fatalf("spam at %.2f: no review; want one (a still-uncertain message goes to admins, DESIGN §7.0)", tc.conf)
			}
		})
	}

	t.Run("property", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			conf := rapid.Float64Range(0, 1).Draw(rt, "confidence")
			v := newVerifier(rt, verifierOpts{classifier: fakeAnswering(fake.Response{Verdict: spamVerdict(conf)})})
			out := v.Verify(context.Background(), item(uncertainVerdict(), "free crypto signals"))
			want := scoring.BandStillUncertain
			if conf >= 0.90 {
				want = scoring.BandUncertain
			}
			if out.Verdict.Band != want {
				rt.Fatalf("spam at %v: band = %s; want %s (the 0.90 line is inclusive and nothing else settles it, DESIGN §7.0)", conf, out.Verdict.Band, want)
			}
		})
	})
}

// TestCertainAndCleanMessagesNeverCallAI: only the UNCERTAIN band reaches the AI.
func TestCertainAndCleanMessagesNeverCallAI(t *testing.T) {
	for name, sv := range map[string]scoring.Verdict{"certain": certainVerdict(), "clean": cleanVerdict()} {
		t.Run(name, func(t *testing.T) {
			f := fake.New(ai.Caps{})
			f.SetDefault(fake.Response{Verdict: notSpamVerdict(0.99)})
			v := newVerifier(t, verifierOpts{classifier: f})
			out := v.Verify(t.Context(), item(sv, "some message"))
			if f.Calls() != 0 || out.Called {
				t.Fatalf("classifier calls = %d, Called = %v; want 0, false (%s messages never call the AI, DESIGN §7.0, §7.1)", f.Calls(), out.Called, name)
			}
			if out.Verdict.Band != sv.Band || out.Verdict.Source != sv.Source {
				t.Fatalf("verdict = %s/%q; want unchanged %s/%q (only UNCERTAIN is resolved by the AI)", out.Verdict.Band, out.Verdict.Source, sv.Band, sv.Source)
			}
		})
	}

	t.Run("property", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			n := rapid.IntRange(1, 4).Draw(rt, "signals")
			var sigs []scoring.Signal
			for i := range n {
				sigs = append(sigs, scoring.Signal{
					Code:   fmt.Sprintf("sig.%d", i),
					Weight: rapid.Float64Range(-5, 5).Draw(rt, "weight"),
					Known:  rapid.Float64Range(0, 1).Draw(rt, "known") < 0.2,
				})
			}
			sv := scored(sigs...)
			f := fake.New(ai.Caps{})
			f.SetDefault(fake.Response{Verdict: spamVerdict(0.99)})
			v := newVerifier(rt, verifierOpts{classifier: f})
			v.Verify(context.Background(), item(sv, "msg"))
			wantCalls := 0
			if sv.Band == scoring.BandUncertain {
				wantCalls = 1
			}
			if f.Calls() != wantCalls {
				rt.Fatalf("band %s: classifier calls = %d; want %d (only the UNCERTAIN band is sent to the AI, DESIGN §7.0)", sv.Band, f.Calls(), wantCalls)
			}
		})
	})
}

// garbageCompleter is a provider adapter that always answers with invalid output.
type garbageCompleter struct{ calls atomic.Int32 }

func (g *garbageCompleter) Complete(context.Context, llm.Request) (llm.Response, error) {
	g.calls.Add(1)
	return llm.Response{Text: "Sure! I think this is spam.", Model: "test-model"}, nil
}
func (g *garbageCompleter) Caps() llm.Caps { return llm.Caps{} }

// TestStillUncertainAsksAdminsNothingDeleted: every way the AI can fail to settle an UNCERTAIN
// message resolves to STILL_UNCERTAIN: admins are asked and nothing is deleted.
func TestStillUncertainAsksAdminsNothingDeleted(t *testing.T) {
	type setup struct {
		verifier *gateway.Verifier
		item     gateway.Item
		calls    func() int // provider requests made, for the cases that check them
	}
	withFake := func(t *testing.T, rs ...fake.Response) setup {
		f := fakeAnswering(rs...)
		return setup{newVerifier(t, verifierOpts{classifier: f}), item(uncertainVerdict(), "suspicious"), f.Calls}
	}
	cases := []struct {
		name       string
		build      func(t *testing.T) setup
		wantReason string
		wantCalls  int // -1: not checked
		wantCalled bool
	}{
		{"unsure", func(t *testing.T) setup {
			return withFake(t, fake.Response{Verdict: ai.Verdict{Label: ai.LabelUnsure, Confidence: 0.95, Categories: []string{}, Reason: "could be a local sale"}})
		}, gateway.ReasonUnsure, 1, true},
		{"spam below threshold", func(t *testing.T) setup {
			return withFake(t, fake.Response{Verdict: spamVerdict(0.6)})
		}, gateway.ReasonLowConfidence, 1, true},
		{"not_spam below threshold", func(t *testing.T) setup {
			return withFake(t, fake.Response{Verdict: notSpamVerdict(0.89)})
		}, gateway.ReasonLowConfidence, 1, true},
		{"provider timeout error", func(t *testing.T) setup {
			return withFake(t, fake.Response{Verdict: ai.Unsure("timeout"), Err: fmt.Errorf("classify: %w", llm.ErrTimeout)})
		}, gateway.ReasonTimeout, 1, true},
		{"provider error", func(t *testing.T) setup {
			return withFake(t, fake.Response{Verdict: ai.Unsure("error"), Err: fmt.Errorf("classify: %w", llm.ErrUnavailable)})
		}, gateway.ReasonError, 1, true},
		{"invalid output twice", func(t *testing.T) setup {
			g := &garbageCompleter{}
			c := gateway.NewClassifier(g, gateway.Options{Provider: "test", Model: "test-model", StructuredOutput: ai.OutputJSON})
			return setup{newVerifier(t, verifierOpts{classifier: c}), item(uncertainVerdict(), "suspicious"), func() int { return int(g.calls.Load()) }}
		}, gateway.ReasonInvalid, 2, true},
		{"budget exhausted", func(t *testing.T) setup {
			b, err := budget.New(budget.NewMemStore(), clock.NewFake(t0), budget.Limits{GroupPerDay: 1}, nil, nil)
			if err != nil {
				t.Fatalf("budget.New error = %v", err)
			}
			if d, err := b.Allow(t.Context(), testGroup); err != nil || !d.Allowed {
				t.Fatalf("first Allow = %+v, %v; want allowed (setup)", d, err)
			}
			f := fakeAnswering(fake.Response{Verdict: spamVerdict(0.99)})
			return setup{newVerifier(t, verifierOpts{classifier: f, budget: b}), item(uncertainVerdict(), "suspicious"), f.Calls}
		}, gateway.ReasonBudget, 0, false},
		{"AI not configured", func(t *testing.T) setup {
			return setup{newVerifier(t, verifierOpts{}), item(uncertainVerdict(), "suspicious"), func() int { return 0 }}
		}, gateway.ReasonAIOff, 0, false},
		{"AI disabled for the group", func(t *testing.T) setup {
			s := withFake(t, fake.Response{Verdict: spamVerdict(0.99)})
			s.item.AIEnabled = false
			return s
		}, gateway.ReasonAIOff, 0, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := tc.build(t)
			out := s.verifier.Verify(t.Context(), s.item)
			checkStillUncertain(t, out, tc.wantReason)
			if tc.wantCalls >= 0 && s.calls() != tc.wantCalls {
				t.Errorf("provider requests = %d; want %d (no call when AI is off or out of budget; one retry on invalid output, DESIGN §7.4)", s.calls(), tc.wantCalls)
			}
			if out.Called != tc.wantCalled {
				t.Errorf("Called = %v; want %v", out.Called, tc.wantCalled)
			}
		})
	}

	t.Run("delete_while_pending opt-in", func(t *testing.T) {
		v := newVerifier(t, verifierOpts{classifier: fakeAnswering(fake.Response{Verdict: spamVerdict(0.5)})})
		out := v.Verify(t.Context(), item(uncertainVerdict(), "suspicious"))
		s := mustPreset(t, policy.PresetStandard)
		s.Uncertain.DeleteWhilePending = true
		d := safety.Decide(s, finding(out.Verdict, policy.TypeText), newMember, safety.Unmet{})
		if !d.Delete || d.Ban || d.Review == nil {
			t.Fatalf("delete_while_pending: Delete = %v, Ban = %v, review = %v; want delete, no ban, review (the opt-in hides the message while admins decide, DESIGN §7.0)", d.Delete, d.Ban, d.Review != nil)
		}
	})
}

func checkStillUncertain(t *testing.T, out gateway.Outcome, wantReason string) {
	t.Helper()
	if out.Verdict.Band != scoring.BandStillUncertain {
		t.Errorf("band = %s; want still_uncertain (an unsettled UNCERTAIN message goes to admins, DESIGN §7.0)", out.Verdict.Band)
	}
	if out.Verdict.Source == scoring.SourceAI {
		t.Errorf("source = ai; want rules (the AI didn't verify it, DESIGN §7.3)")
	}
	if out.Verdict.DeletesAsSpam() {
		t.Errorf("DeletesAsSpam = true; want false (nothing is deleted on AI failure, DESIGN §7.4 guardrails)")
	}
	if out.Reason != wantReason {
		t.Errorf("reason = %q; want %q (the explanation and metrics name why it stayed uncertain)", out.Reason, wantReason)
	}
	for _, p := range namedPresets {
		for _, mode := range allEnforce {
			s := mustPreset(t, p)
			s.Enforcement.Mode = mode
			d := safety.Decide(s, finding(out.Verdict, policy.TypeLinks), newMember, safety.Unmet{})
			if d.Delete || d.Ban {
				t.Errorf("%s/%s: Delete = %v, Ban = %v; want neither (still uncertain stays visible by default, DESIGN §7.0, §8.3)", p, mode, d.Delete, d.Ban)
			}
			if d.Review == nil {
				t.Errorf("%s/%s: no review; want admins asked (STILL UNCERTAIN → ask admins, DESIGN §7.0)", p, mode)
			}
		}
	}
}

// TestAINotSpamHighConfidenceLoggedOnly: a confident not_spam ends it: no delete, no review.
func TestAINotSpamHighConfidenceLoggedOnly(t *testing.T) {
	for _, conf := range []float64{0.90, 0.99} {
		v := newVerifier(t, verifierOpts{classifier: fakeAnswering(fake.Response{Verdict: notSpamVerdict(conf)})})
		out := v.Verify(t.Context(), item(uncertainVerdict(), "selling my bike, 50 dinars, dm"))
		if out.Verdict.Band != scoring.BandClean || out.Verdict.Source != scoring.SourceAI {
			t.Fatalf("not_spam at %.2f: verdict = %s/%q; want clean/ai (confident not spam settles it, DESIGN §7.0)", conf, out.Verdict.Band, out.Verdict.Source)
		}
		if out.Verdict.AI == nil || out.Verdict.AI.Label != ai.LabelNotSpam {
			t.Fatalf("not_spam at %.2f: verdict.AI = %+v; want the AI answer kept for the log (DESIGN §8.3: none, logged)", conf, out.Verdict.AI)
		}
		d := safety.Decide(mustPreset(t, policy.PresetStandard), finding(out.Verdict, policy.TypeText), newMember, safety.Unmet{})
		if d.Delete || d.Ban || d.Review != nil {
			t.Fatalf("not_spam at %.2f: Delete = %v, Ban = %v, review = %v; want nothing (DESIGN §8.3: untouched, no ban)", conf, d.Delete, d.Ban, d.Review != nil)
		}
		if p := policy.PlanFor(mustPreset(t, policy.PresetStandard), d); len(p.Enqueue) != 0 {
			t.Fatalf("not_spam at %.2f: enqueued %v; want nothing on the action queue", conf, p.Enqueue)
		}
	}
}

// TestAskBeforeActionModeKeepsAISpamVisible: in ask_before_action, AI spam is only reviewed.
func TestAskBeforeActionModeKeepsAISpamVisible(t *testing.T) {
	v := newVerifier(t, verifierOpts{classifier: fakeAnswering(fake.Response{Verdict: spamVerdict(0.97)})})
	out := v.Verify(t.Context(), item(uncertainVerdict(), "investment opportunity"))
	if out.Verdict.Band != scoring.BandUncertain || out.Verdict.Source != scoring.SourceAI {
		t.Fatalf("verdict = %s/%q; want uncertain/ai (setup: AI-verified spam)", out.Verdict.Band, out.Verdict.Source)
	}
	for _, p := range namedPresets {
		s := mustPreset(t, p)
		s.Enforcement.Mode = policy.EnforceAskBeforeAction
		for _, tr := range []safety.TrackRecord{safety.Unmet{}, met{}} {
			d := safety.Decide(s, finding(out.Verdict, policy.TypeLinks), newMember, tr)
			if d.Delete || d.Ban {
				t.Errorf("%s (track record met=%v): Delete = %v, Ban = %v; want neither (ask_before_action deletes nothing until an admin decides, DESIGN §8.3)", p, tr.AIMet(), d.Delete, d.Ban)
			}
			if d.Review == nil {
				t.Errorf("%s (track record met=%v): no review; want admins asked (DESIGN §8.3 mode b)", p, tr.AIMet())
			} else if d.Review.OnTimeout == policy.TimeoutAutoBan {
				t.Errorf("%s: review on_timeout = auto_ban; want a fallback that can't ban (DESIGN §8.3)", p)
			}
		}
	}
}

// TestRedeliveredMessageClassifiedOnce: the same message verified again makes no second call.
func TestRedeliveredMessageClassifiedOnce(t *testing.T) {
	f := fake.New(ai.Caps{})
	f.SetDefault(fake.Response{Verdict: spamVerdict(0.96)})
	v := newVerifier(t, verifierOpts{classifier: f})
	it := item(uncertainVerdict(), "join my vip crypto channel")

	first := v.Verify(t.Context(), it)
	second := v.Verify(t.Context(), it)
	if f.Calls() != 1 {
		t.Fatalf("classifier calls = %d; want 1 (a redelivered message reuses the cached verdict, DESIGN §7.4 cost control 2)", f.Calls())
	}
	if !first.Called || second.Called || second.Source != cache.SourceCached {
		t.Fatalf("first Called = %v; second Called = %v, source = %v; want true, false, cached (the reuse is marked for metrics)", first.Called, second.Called, second.Source)
	}
	if second.Verdict.Band != first.Verdict.Band || second.Verdict.Source != first.Verdict.Source {
		t.Fatalf("second verdict = %s/%q; want %s/%q (a cached answer decides the same way)", second.Verdict.Band, second.Verdict.Source, first.Verdict.Band, first.Verdict.Source)
	}

	t.Run("failures are not cached", func(t *testing.T) {
		f := fake.New(ai.Caps{})
		f.SetDefault(fake.Response{Verdict: ai.Unsure("x"), Err: errors.New("boom")})
		v := newVerifier(t, verifierOpts{classifier: f})
		it := item(uncertainVerdict(), "maybe spam")
		v.Verify(t.Context(), it)
		v.Verify(t.Context(), it)
		if f.Calls() != 2 {
			t.Fatalf("classifier calls = %d; want 2 (only definitive verdicts are cached; failures retry next time, DESIGN §7.4)", f.Calls())
		}
	})
}
