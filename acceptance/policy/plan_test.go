package policy_test

import (
	"testing"

	"pgregory.net/rapid"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/policy"
)

var wantPriority = map[policy.ActionKind]int{policy.ActionDelete: 1, policy.ActionBan: 2, policy.ActionReview: 3}

// genDecision draws a decision directly, so PlanFor is tested independently of Decide.
func genDecision(t *rapid.T) policy.Decision {
	v := rapid.SampledFrom([]policy.Verdict{policy.VerdictSpam, policy.VerdictPolicy, policy.VerdictClean}).Draw(t, "verdict")
	if v == policy.VerdictClean {
		return policy.Decision{Verdict: v, Route: policy.RouteNone}
	}
	d := policy.Decision{
		Verdict: v,
		Route:   rapid.SampledFrom([]policy.Route{policy.RouteAutoBan, policy.RouteAsk, policy.RouteDeleteOnly, policy.RouteAllow, policy.RoutePolicy}).Draw(t, "route"),
		Delete:  rapid.Bool().Draw(t, "delete"),
		Ban:     rapid.Bool().Draw(t, "ban"),
		Reason:  "generated",
	}
	if rapid.Bool().Draw(t, "review") {
		d.Review = &policy.ReviewRequest{
			Kind:      rapid.SampledFrom([]policy.ReviewKind{policy.ReviewBanDecision, policy.ReviewUncertain, policy.ReviewHacked, policy.ReviewAskBefore, policy.ReviewAlert}).Draw(t, "review.kind"),
			OnTimeout: policy.TimeoutKeepDeletedNoBan,
		}
	}
	return d
}

// checkActions asserts that list holds exactly the actions d calls for, at §10.2 priorities.
func checkActions(t *rapid.T, label string, d policy.Decision, list []policy.Action) {
	want := map[policy.ActionKind]bool{policy.ActionDelete: d.Delete, policy.ActionBan: d.Ban, policy.ActionReview: d.Review != nil}
	for k, on := range want {
		n := count(list, k)
		if (on && n != 1) || (!on && n != 0) {
			t.Fatalf("%s: %s: got %d %s actions in %+v, want %d (one action per decided step, DESIGN §8.4, §10)", describeDecision(d), label, n, k, list, map[bool]int{true: 1, false: 0}[on])
		}
	}
	if len(list) != count(list, policy.ActionDelete)+count(list, policy.ActionBan)+count(list, policy.ActionReview) {
		t.Fatalf("%s: %s = %+v, want only delete/ban/review actions", describeDecision(d), label, list)
	}
	for _, a := range list {
		if a.Priority != wantPriority[a.Kind] {
			t.Fatalf("%s: %s action %s has priority %d, want %d (delete P1, ban P2, review P3, DESIGN §10.2)", describeDecision(d), label, a.Kind, a.Priority, wantPriority[a.Kind])
		}
		if a.Kind == policy.ActionReview && (a.Review == nil || a.Review.Kind != d.Review.Kind) {
			t.Fatalf("%s: %s review action carries %+v, want the decision's review request", describeDecision(d), label, a.Review)
		}
	}
}

// Shadow mode runs everything and records what would have happened, but enqueues nothing
// (DESIGN §8.4, §14 detections.mode, §18).
func TestShadowModeEnqueuesNothingButRecords(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		s := policy.Defaults()
		s.Mode = policy.ModeShadow
		d := genDecision(rt)
		p := policy.PlanFor(s, d)
		if len(p.Enqueue) != 0 {
			rt.Fatalf("%s: shadow Enqueue = %+v, want empty (shadow mode acts on nothing, DESIGN §8.4)", describeDecision(d), p.Enqueue)
		}
		if d.Verdict == policy.VerdictClean {
			return
		}
		if !p.Record || p.Mode != policy.DetectionShadow {
			rt.Fatalf("%s: shadow Record=%v Mode=%q, want Record=true Mode=shadow (every would-be action is recorded for review, DESIGN §8.4, §14)", describeDecision(d), p.Record, p.Mode)
		}
		checkActions(rt, "shadow Would", d, p.Would)
	})
}

// Paused does nothing at all: nothing recorded, nothing planned, nothing enqueued (DESIGN §12.1).
func TestPausedModeDoesNothing(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		s := policy.Defaults()
		s.Mode = policy.ModePaused
		d := genDecision(rt)
		p := policy.PlanFor(s, d)
		if p.Record || len(p.Enqueue) != 0 || len(p.Would) != 0 {
			rt.Fatalf("%s: paused plan = %+v, want Record=false and no actions (paused does nothing, DESIGN §12.1)", describeDecision(d), p)
		}
	})
}

// Enforce records every non-clean verdict and enqueues exactly what the decision calls for.
func TestEnforceModeEnqueuesDecision(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		s := policy.Defaults()
		d := genDecision(rt)
		p := policy.PlanFor(s, d)
		if d.Verdict == policy.VerdictClean {
			if p.Record || len(p.Enqueue) != 0 {
				rt.Fatalf("clean decision: plan = %+v, want nothing recorded or enqueued (only non-clean verdicts are stored, DESIGN §14)", p)
			}
			return
		}
		if !p.Record || p.Mode != policy.DetectionEnforce {
			rt.Fatalf("%s: enforce Record=%v Mode=%q, want Record=true Mode=enforce (DESIGN §14 detections)", describeDecision(d), p.Record, p.Mode)
		}
		checkActions(rt, "enforce Enqueue", d, p.Enqueue)
		checkActions(rt, "enforce Would", d, p.Would)
	})
}

// End to end: known spam in a shadow-mode group is decided as usual but only recorded.
func TestShadowModeRecordsWouldBeBan(t *testing.T) {
	s := policy.Defaults()
	s.Mode = policy.ModeShadow
	d := policy.Decide(s, spam(policy.BandCertain, policy.SourceKnown, policy.TypeText), newMember())
	p := policy.PlanFor(s, d)
	if len(p.Enqueue) != 0 || count(p.Would, policy.ActionDelete) != 1 || count(p.Would, policy.ActionBan) != 1 || p.Mode != policy.DetectionShadow || !p.Record {
		t.Fatalf("shadow, known text spam, new sender: got %s and plan %+v, want Would = delete + ban, Enqueue empty, recorded as shadow (\"we would have deleted 42 messages and banned 3 people\", DESIGN §8.4)", describeDecision(d), p)
	}
}
